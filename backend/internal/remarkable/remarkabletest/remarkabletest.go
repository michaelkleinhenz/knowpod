// Package remarkabletest provides a fake reMarkable cloud and page files for tests.
package remarkabletest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
)

// Cloud is a fake reMarkable cloud: pairing, user tokens, the root and content-addressed
// files, as the client uses them.
type Cloud struct {
	*httptest.Server

	mu    sync.Mutex
	blobs map[string][]byte
	items map[string]string // item ID → hash of its index
	root  string
	gen   int64
	// RaceRoot, when set, is called before a root update is taken, e.g. to change the
	// account as another device would.
	RaceRoot func()
	codes    map[string]string // one-time code → device token
	tokens   map[string]bool   // valid device tokens
	requests []string          // "METHOD path rm-filename"
}

// New starts a fake cloud. Close it when done.
func New() *Cloud {
	c := &Cloud{blobs: map[string][]byte{}, items: map[string]string{}, codes: map[string]string{}, tokens: map[string]bool{}, gen: 7}
	c.Server = httptest.NewServer(c)
	return c
}

// AddCode makes a one-time code valid; pairing with it returns deviceToken.
func (c *Cloud) AddCode(code, deviceToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes[code] = deviceToken
}

// Revoke makes a device token invalid, as if the user removed the device.
func (c *Cloud) Revoke(deviceToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tokens, deviceToken)
}

// Requests returns the requests so far and forgets them.
func (c *Cloud) Requests() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.requests
	c.requests = nil
	return r
}

// Put stores a file and returns its hash.
func (c *Cloud) Put(data []byte) string {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	c.mu.Lock()
	c.blobs[h] = data
	c.mu.Unlock()
	return h
}

// Item is a document or folder to put into the cloud.
type Item struct {
	ID, Name, Parent string
	Folder           bool
	Deleted          bool
	LastModified     int64             // Unix milliseconds
	Content          string            // the .content JSON; a notebook by default
	Files            map[string][]byte // further files by name below the item, e.g. "p1.rm" → "<id>/p1.rm", ".pdf" → "<id>.pdf"
}

// Set adds or replaces an item.
func (c *Cloud) Set(it Item) {
	typ := "DocumentType"
	if it.Folder {
		typ = "CollectionType"
	}
	meta, _ := json.Marshal(map[string]any{
		"visibleName": it.Name, "type": typ, "parent": it.Parent, "deleted": it.Deleted,
		"lastModified": fmt.Sprint(it.LastModified),
	})
	content := it.Content
	if content == "" {
		content = `{"fileType":"notebook"}`
	}
	files := map[string][]byte{it.ID + ".metadata": meta, it.ID + ".content": []byte(content)}
	for name, data := range it.Files {
		if strings.HasPrefix(name, ".") {
			files[it.ID+name] = data
		} else {
			files[it.ID+"/"+name] = data
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var idx strings.Builder
	idx.WriteString("3\n")
	for _, n := range names {
		fmt.Fprintf(&idx, "%s:0:%s:0:%d\n", c.Put(files[n]), n, len(files[n]))
	}
	h := c.Put([]byte(idx.String()))
	c.mu.Lock()
	c.items[it.ID] = h
	c.mu.Unlock()
	c.rebuildRoot()
}

// Remove deletes an item from the cloud.
func (c *Cloud) Remove(id string) {
	c.mu.Lock()
	delete(c.items, id)
	c.mu.Unlock()
	c.rebuildRoot()
}

func (c *Cloud) rebuildRoot() {
	c.mu.Lock()
	ids := make([]string, 0, len(c.items))
	for id := range c.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var idx strings.Builder
	fmt.Fprintf(&idx, "4\n0:.:%d:0\n", len(ids))
	for _, id := range ids {
		fmt.Fprintf(&idx, "%s:80000000:%s:3:0\n", c.items[id], id)
	}
	c.mu.Unlock()
	h := c.Put([]byte(idx.String()))
	c.mu.Lock()
	c.root = h
	c.gen++
	c.mu.Unlock()
}

// Document returns a document's metadata and its files by name (e.g. "<id>.epub"), as
// written by a client; ok is false when it isn't in the cloud.
func (c *Cloud) Document(id string) (meta map[string]any, files map[string][]byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.items[id]
	if !ok {
		return nil, nil, false
	}
	files = map[string][]byte{}
	for _, line := range strings.Split(string(c.blobs[h]), "\n")[1:] {
		f := strings.Split(line, ":")
		if len(f) == 5 {
			files[f[2]] = c.blobs[f[0]]
		}
	}
	_ = json.Unmarshal(files[id+".metadata"], &meta)
	return meta, files, true
}

// AddFile adds a file to a document, as the tablet does for what is written on it.
func (c *Cloud) AddFile(id, name string, data []byte) {
	h := c.Put(data)
	c.mu.Lock()
	idx := string(c.blobs[c.items[id]]) + fmt.Sprintf("%s:0:%s:0:%d\n", h, name, len(data))
	c.mu.Unlock()
	ih := c.Put([]byte(idx))
	c.mu.Lock()
	c.items[id] = ih
	c.mu.Unlock()
	c.rebuildRoot()
}

// IDs returns the IDs of the documents and folders in the cloud.
func (c *Cloud) IDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.items))
	for id := range c.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// putRoot takes a root update: the root index is read to learn the items.
func (c *Cloud) putRoot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hash       string `json:"hash"`
		Generation int64  `json:"generation"`
		Broadcast  bool   `json:"broadcast"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if race := c.RaceRoot; race != nil {
		c.RaceRoot = nil
		c.mu.Unlock()
		race()
		c.mu.Lock()
	}
	want := c.gen
	if c.root == "" {
		want = 0 // an empty account has no root to read the generation from
	}
	if body.Generation != want {
		http.Error(w, "generation mismatch", http.StatusPreconditionFailed)
		return
	}
	idx, ok := c.blobs[body.Hash]
	if !ok {
		http.Error(w, "unknown root index", http.StatusBadRequest)
		return
	}
	lines := strings.Split(strings.TrimSpace(string(idx)), "\n")
	if lines[0] == "4" {
		lines = lines[1:]
	}
	items := map[string]string{}
	for _, line := range lines[1:] {
		f := strings.Split(line, ":")
		if len(f) != 5 {
			http.Error(w, "bad root index line "+line, http.StatusBadRequest)
			return
		}
		if _, ok := c.blobs[f[0]]; !ok {
			http.Error(w, "unknown document index", http.StatusBadRequest)
			return
		}
		items[f[2]] = f[0]
	}
	c.items, c.root = items, body.Hash
	c.gen++
	fmt.Fprintf(w, `{"hash":%q,"generation":%d}`, c.root, c.gen)
}

// ServeHTTP answers the endpoints the client uses.
func (c *Cloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+r.Header.Get("rm-filename")))
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch {
	case r.Method == "POST" && r.URL.Path == "/token/json/2/device/new":
		var body struct{ Code, DeviceDesc, DeviceID string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		tok, ok := c.codes[body.Code]
		if !ok || body.DeviceDesc == "" || body.DeviceID == "" {
			http.Error(w, "invalid code", http.StatusBadRequest)
			return
		}
		delete(c.codes, body.Code)
		c.tokens[tok] = true
		fmt.Fprint(w, tok)
	case r.Method == "POST" && r.URL.Path == "/token/json/2/user/new":
		if !c.tokens[auth] {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, "user:"+auth)
	case !strings.HasPrefix(auth, "user:") || !c.tokens[strings.TrimPrefix(auth, "user:")]:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case r.Method == "GET" && r.URL.Path == "/sync/v4/root":
		if c.root == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"hash":%q,"generation":%d,"schemaVersion":4}`, c.root, c.gen)
	case r.Method == "PUT" && r.URL.Path == "/sync/v3/root":
		c.putRoot(w, r)
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/sync/v3/files/"):
		data, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		if strings.HasSuffix(r.Header.Get("rm-filename"), ".docSchema") {
			h = indexHash(data) // an index is named by the hash of its entries' hashes
		}
		if h != strings.TrimPrefix(r.URL.Path, "/sync/v3/files/") || r.Header.Get("x-goog-hash") == "" {
			http.Error(w, "hash mismatch", http.StatusBadRequest)
			return
		}
		c.blobs[h] = data
		w.WriteHeader(http.StatusOK)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/sync/v3/files/"):
		data, ok := c.blobs[strings.TrimPrefix(r.URL.Path, "/sync/v3/files/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

// indexHash is the SHA-256 of the entries' hashes of an index file (by entry ID), the name
// the real cloud demands for an index.
func indexHash(data []byte) string {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")[1:]
	if len(lines) > 0 && strings.HasPrefix(lines[0], "0:.:") {
		lines = lines[1:]
	}
	sort.Slice(lines, func(i, j int) bool { return strings.Split(lines[i], ":")[2] < strings.Split(lines[j], ":")[2] })
	h := sha256.New()
	for _, l := range lines {
		raw, _ := hex.DecodeString(strings.Split(l, ":")[0])
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- page files ---

// Line is a stroke for Page.
type Line struct {
	Tool, Color int
	Points      [][3]float32 // x (0 is the middle of the page), y, width
	Deleted     bool
	ARGB        uint32
}

// TextItem is an item of the typed text of a page: Text inserted by author 1 with the ID
// (of its first character) between the characters Left and Right, also of author 1 (0 for
// the start or the end). Deleted characters have Deleted set instead of Text; Format makes
// the item an inline formatting item.
type TextItem struct {
	ID, Left, Right uint64
	Text            string
	Deleted         uint32
	Format          bool
}

// Text is the typed text of a page for TextPage.
type Text struct {
	Items []TextItem
	// Styles are paragraph styles by the ID of the newline starting the paragraph (0 for the
	// first paragraph).
	Styles map[uint64]int
}

// Page encodes a version 6 page file (.rm) with the given lines.
func Page(lines ...Line) []byte { return TextPage(nil, lines...) }

// TextPage encodes a version 6 page file (.rm) with typed text (unless nil) and lines.
func TextPage(text *Text, lines ...Line) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%-43s", "reMarkable .lines file, version=6")
	block := func(typ, version uint8, body []byte) {
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(body)))
		out.Write([]byte{0, 1, version, typ})
		out.Write(body)
	}
	block(0x09, 1, []byte{1, 2, 3}) // an unrelated block (author IDs)
	if text != nil {
		block(0x07, 1, rootText(text))
	}
	for i, l := range lines {
		var b enc
		b.id(1, 0, 11)
		b.id(2, 1, uint64(100+i))
		b.id(3, 0, 0)
		b.id(4, 0, 0)
		b.tag(5, 0x4)
		if l.Deleted {
			b.u32(1)
			block(0x05, 2, b.Bytes())
			continue
		}
		b.u32(0)
		var v enc
		v.u8(0x03)
		v.tag(1, 0x4)
		v.u32(uint32(l.Tool))
		v.tag(2, 0x4)
		v.u32(uint32(l.Color))
		v.tag(3, 0x8)
		v.u64(math.Float64bits(1))
		v.tag(4, 0x4)
		v.u32(0)
		var p enc
		for _, pt := range l.Points {
			p.u32(math.Float32bits(pt[0]))
			p.u32(math.Float32bits(pt[1]))
			p.u16(0)
			p.u16(uint16(pt[2] * 4))
			p.u8(0)
			p.u8(0)
		}
		v.tag(5, 0xC)
		v.u32(uint32(p.Len()))
		v.Write(p.Bytes())
		v.id(6, 1, 5)
		if l.ARGB != 0 {
			v.tag(8, 0x4)
			v.u32(l.ARGB)
		}
		b.tag(6, 0xC)
		b.u32(uint32(v.Len()))
		b.Write(v.Bytes())
		block(0x05, 2, b.Bytes())
	}
	return out.Bytes()
}

// rootText encodes the root text block of a page.
func rootText(t *Text) []byte {
	author := func(id uint64) uint8 {
		if id == 0 {
			return 0
		}
		return 1
	}
	var seq enc
	seq.varuint(uint64(len(t.Items)))
	for _, it := range t.Items {
		var b enc
		b.id(2, 1, it.ID)
		b.id(3, author(it.Left), it.Left)
		b.id(4, author(it.Right), it.Right)
		b.tag(5, 0x4)
		b.u32(it.Deleted)
		if it.Text != "" || it.Format {
			var str enc
			str.varuint(uint64(len(it.Text)))
			str.u8(1)
			str.WriteString(it.Text)
			if it.Format {
				str.tag(2, 0x4)
				str.u32(1)
			}
			b.sub(6, &str)
		}
		seq.sub(0, &b)
	}
	var styles enc
	styles.varuint(uint64(len(t.Styles)))
	for id, style := range t.Styles {
		styles.u8(author(id))
		styles.varuint(id)
		styles.id(1, 1, 900)
		var v enc
		v.u8(17)
		v.u8(uint8(style))
		styles.sub(2, &v)
	}
	var items, formats, body, pos, b enc
	items.sub(1, &seq)
	formats.sub(1, &styles)
	body.sub(1, &items)
	body.sub(2, &formats)
	b.id(1, 0, 0)
	b.sub(2, &body)
	pos.u64(math.Float64bits(-468))
	pos.u64(math.Float64bits(234))
	b.sub(3, &pos)
	b.tag(4, 0x4)
	b.u32(math.Float32bits(936))
	return b.Bytes()
}

type enc struct{ bytes.Buffer }

func (e *enc) sub(index uint32, b *enc) {
	e.tag(index, 0xC)
	e.u32(uint32(b.Len()))
	e.Write(b.Bytes())
}

func (e *enc) u8(v uint8)   { e.WriteByte(v) }
func (e *enc) u16(v uint16) { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) u32(v uint32) { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) u64(v uint64) { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) varuint(v uint64) {
	for v >= 0x80 {
		e.u8(uint8(v) | 0x80)
		v >>= 7
	}
	e.u8(uint8(v))
}
func (e *enc) tag(index uint32, typ uint8) { e.varuint(uint64(index)<<4 | uint64(typ)) }
func (e *enc) id(index uint32, author uint8, n uint64) {
	e.tag(index, 0xF)
	e.u8(author)
	e.varuint(n)
}
