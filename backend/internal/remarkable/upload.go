package remarkable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Writing follows rmapi as well: every file is stored under the SHA-256 of its content, a
// document's index lists its files, the root index lists the documents, and the root is
// moved to the new root index only if no other device changed it meanwhile (its generation
// is still the one read).

// ErrRootChanged is returned when another device changed the account while a change was
// being written; reading the root again and redoing the change is safe.
var ErrRootChanged = errors.New("the reMarkable cloud changed meanwhile")

// Root index entry types.
const (
	entryDocument = "80000000"
	entryFile     = "0"
)

// hashOf returns the name a file is stored under.
func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// indexHashOf returns the name an index file is stored under: not the hash of its text, but
// the SHA-256 of its entries' hashes (as bytes) in the index's order, which the cloud checks.
func indexHashOf(entries []Entry) (string, error) {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	h := sha256.New()
	for _, e := range sorted {
		raw, err := hex.DecodeString(e.Hash)
		if err != nil {
			return "", fmt.Errorf("remarkable index entry %s: invalid hash %q", e.ID, e.Hash)
		}
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PutBlob stores a file under its hash; name is its name in its index.
func (s *Session) PutBlob(ctx context.Context, name string, data []byte) (string, error) {
	return s.putBlobAs(ctx, name, hashOf(data), data)
}

// putBlobAs stores a file under the given name in the cloud (its hash, or for an index the
// hash of its entries).
func (s *Session) putBlobAs(ctx context.Context, name, hash string, data []byte) (string, error) {
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.Checksum(data, castagnoli))
	res, raw, err := s.c.do(ctx, http.MethodPut, s.c.syncURL+"/sync/v3/files/"+hash, s.token, bytes.NewReader(data), map[string]string{
		"rm-filename":  name,
		"Content-Type": "application/octet-stream",
		"x-goog-hash":  "crc32c=" + base64.StdEncoding.EncodeToString(crc[:]),
	}, 64<<10)
	if err != nil {
		return "", fmt.Errorf("remarkable upload %s: %w", name, err)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return "", ErrUnauthorized
	case res.StatusCode < 200 || res.StatusCode > 299:
		return "", fmt.Errorf("remarkable upload %s: HTTP %d: %s", name, res.StatusCode, snippet(raw))
	}
	return hash, nil
}

// PutRoot moves the account's root to hash, if its generation is still gen, and tells the
// account's devices to sync. It returns the new root.
func (s *Session) PutRoot(ctx context.Context, hash string, gen int64) (Root, error) {
	body, _ := json.Marshal(map[string]any{"hash": hash, "generation": gen, "broadcast": true})
	res, raw, err := s.c.do(ctx, http.MethodPut, s.c.syncURL+"/sync/v3/root", s.token, bytes.NewReader(body), map[string]string{"rm-filename": "roothash"}, 64<<10)
	if err != nil {
		return Root{}, fmt.Errorf("remarkable root update: %w", err)
	}
	switch {
	case res.StatusCode == http.StatusPreconditionFailed || res.StatusCode == http.StatusConflict:
		return Root{}, ErrRootChanged
	case res.StatusCode == http.StatusUnauthorized:
		return Root{}, ErrUnauthorized
	case res.StatusCode < 200 || res.StatusCode > 299:
		return Root{}, fmt.Errorf("remarkable root update: HTTP %d: %s", res.StatusCode, snippet(raw))
	}
	var r Root
	if err := json.Unmarshal(raw, &r); err != nil || r.Hash == "" {
		r = Root{Hash: hash, Generation: gen + 1}
	}
	return r, nil
}

// FormatIndex writes an index file in schema "3" or "4" (see ParseIndex), its entries
// sorted by ID.
func FormatIndex(schema string, entries []Entry) []byte {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	var b bytes.Buffer
	b.WriteString(schema + "\n")
	if schema == "4" {
		var size int64
		for _, e := range sorted {
			size += e.Size
		}
		fmt.Fprintf(&b, "0:.:%d:%d\n", len(sorted), size)
	}
	for _, e := range sorted {
		fmt.Fprintf(&b, "%s:%s:%s:%d:%d\n", e.Hash, e.Type, e.ID, e.Subfiles, e.Size)
	}
	return b.Bytes()
}

// DocumentWrite is a document to create or change: an EPUB of the given name in the cloud
// folder Parent ("" for the top level, TrashParent for the trash).
type DocumentWrite struct {
	// ID is the document to change. When it is empty, or the document is gone from the
	// cloud, a new document is made (if there is an EPUB).
	ID     string
	Name   string
	Parent string
	// EPUB is the document's new file; nil keeps the file and changes only the name and
	// folder.
	EPUB []byte
}

// maxWriteAttempts bounds how often a write is redone when another device changed the
// account meanwhile.
const maxWriteAttempts = 4

// WriteDocuments creates and changes the documents in one change of the account. It returns
// each document's ID (a new one for a new document; "" when a document is gone and there was
// nothing to make it from). A changed document keeps its other files (e.g. what was written
// on it) and metadata.
func (s *Session) WriteDocuments(ctx context.Context, docs []DocumentWrite, now time.Time) ([]string, error) {
	uploaded := map[string]bool{}
	put := func(name string, data []byte) (Entry, error) {
		h := hashOf(data)
		if !uploaded[h] {
			if _, err := s.PutBlob(ctx, name, data); err != nil {
				return Entry{}, err
			}
			uploaded[h] = true
		}
		return Entry{Hash: h, Type: entryFile, ID: name, Size: int64(len(data))}, nil
	}
	putIndex := func(name string, entries []Entry, data []byte) (Entry, error) {
		h, err := indexHashOf(entries)
		if err != nil {
			return Entry{}, err
		}
		if !uploaded[h] {
			if _, err := s.putBlobAs(ctx, name, h, data); err != nil {
				return Entry{}, err
			}
			uploaded[h] = true
		}
		return Entry{Hash: h, Type: entryFile, ID: name, Size: int64(len(data))}, nil
	}
	newIDs := make([]string, len(docs))
	for attempt := 1; ; attempt++ {
		ids, err := s.writeDocuments(ctx, docs, newIDs, now, put, putIndex)
		if errors.Is(err, ErrRootChanged) && attempt < maxWriteAttempts {
			continue
		}
		return ids, err
	}
}

func (s *Session) writeDocuments(ctx context.Context, docs []DocumentWrite, newIDs []string, now time.Time, put func(string, []byte) (Entry, error), putIndex func(string, []Entry, []byte) (Entry, error)) ([]string, error) {
	root, err := s.Root(ctx)
	if err != nil {
		return nil, err
	}
	schema := "3"
	if root.SchemaVersion == 4 {
		schema = "4"
	}
	var entries []Entry
	if root.Hash != "" {
		data, err := s.Blob(ctx, root.Hash, "root.docSchema", 16<<20)
		if err != nil {
			return nil, err
		}
		if entries, err = ParseIndex(data); err != nil {
			return nil, fmt.Errorf("remarkable root index: %w", err)
		}
		schema = strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	}
	at := make(map[string]int, len(entries))
	for i, e := range entries {
		at[e.ID] = i
	}

	ids := make([]string, len(docs))
	for i, d := range docs {
		var files []Entry
		meta := map[string]any{}
		id := d.ID
		if j, ok := at[id]; ok && id != "" {
			if files, err = s.Index(ctx, entries[j].Hash, id+".docSchema"); err != nil {
				return nil, err
			}
			for _, f := range files {
				if f.ID == id+".metadata" {
					raw, err := s.Blob(ctx, f.Hash, f.ID, 1<<20)
					if err != nil {
						return nil, err
					}
					_ = json.Unmarshal(raw, &meta)
				}
			}
		} else {
			if d.EPUB == nil {
				continue // gone, and nothing to make it from
			}
			// A new document; its ID is kept when the write is redone.
			if newIDs[i] == "" {
				newIDs[i] = newUUID()
			}
			id = newIDs[i]
			ms := strconv.FormatInt(now.UnixMilli(), 10)
			meta = map[string]any{
				"createdTime": ms, "lastOpened": "0", "lastOpenedPage": 0, "pinned": false, "deleted": false,
				"metadatamodified": false, "modified": false, "synced": true, "version": 0, "type": TypeDocument,
			}
			content, _ := json.Marshal(map[string]any{
				"coverPageNumber": 0, "documentMetadata": map[string]any{}, "extraMetadata": map[string]any{},
				"fileType": "epub", "fontName": "", "lastOpenedPage": 0, "lineHeight": -1, "margins": 100,
				"orientation": "portrait", "pageCount": 0, "pages": []string{}, "textAlignment": "left", "textScale": 1,
			})
			e, err := put(id+".content", content)
			if err != nil {
				return nil, err
			}
			files = []Entry{e}
		}
		meta["visibleName"], meta["parent"], meta["lastModified"] = d.Name, d.Parent, strconv.FormatInt(now.UnixMilli(), 10)
		metaJSON, _ := json.Marshal(meta)
		changed := map[string][]byte{id + ".metadata": metaJSON}
		if d.EPUB != nil {
			changed[id+".epub"] = d.EPUB
		}
		for name, data := range changed {
			e, err := put(name, data)
			if err != nil {
				return nil, err
			}
			files = replaceEntry(files, e)
		}
		idx := FormatIndex("3", files)
		h, err := putIndex(id+".docSchema", files, idx)
		if err != nil {
			return nil, err
		}
		var size int64
		for _, f := range files {
			size += f.Size
		}
		doc := Entry{Hash: h.Hash, Type: entryDocument, ID: id, Subfiles: len(files), Size: size}
		if j, ok := at[id]; ok {
			entries[j] = doc
		} else {
			at[id] = len(entries)
			entries = append(entries, doc)
		}
		ids[i] = id
	}

	// A schema 4 root lists its items as type "0" and is named by the SHA-256 of its text; a
	// schema 3 root lists them as documents and is named like any other index (as rmapi does).
	rootEntries := append([]Entry(nil), entries...)
	for i := range rootEntries {
		rootEntries[i].Type = entryDocument
		if schema == "4" {
			rootEntries[i].Type = entryFile
		}
	}
	rootData := FormatIndex(schema, rootEntries)
	var rootIdx Entry
	if schema == "4" {
		h, err := s.putBlobAs(ctx, "root.docSchema", hashOf(rootData), rootData)
		if err != nil {
			return nil, err
		}
		rootIdx = Entry{Hash: h}
	} else if rootIdx, err = putIndex("root.docSchema", rootEntries, rootData); err != nil {
		return nil, err
	}
	if _, err := s.PutRoot(ctx, rootIdx.Hash, root.Generation); err != nil {
		return nil, err
	}
	return ids, nil
}

// replaceEntry puts e into the entries in place of the entry of the same name.
func replaceEntry(entries []Entry, e Entry) []Entry {
	for i := range entries {
		if entries[i].ID == e.ID {
			entries[i] = e
			return entries
		}
	}
	return append(entries, e)
}
