package remarkable

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Entry is a line of an index file. In the root index each entry is a document or folder
// (ID is its UUID, Hash names its own index); in a document's index each entry is one of its
// files (ID is the file name, e.g. "<uuid>.metadata" or "<uuid>/<page>.rm").
type Entry struct {
	Hash     string
	Type     string
	ID       string
	Subfiles int
	Size     int64
}

// ParseIndex reads an index file. Two schemas exist: "3" (the header line, then one entry
// per line) and "4" (a second header line "0:.:<count>:<size>" follows).
func ParseIndex(data []byte) ([]Entry, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	if !sc.Scan() {
		return nil, errors.New("empty index")
	}
	switch schema := strings.TrimSpace(sc.Text()); schema {
	case "3":
	case "4":
		if !sc.Scan() {
			return nil, errors.New("schema 4 index without its summary line")
		}
	default:
		return nil, fmt.Errorf("unknown index schema %q", schema)
	}
	var out []Entry
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) != 5 {
			return nil, fmt.Errorf("malformed index line %q", line)
		}
		subfiles, err1 := strconv.Atoi(f[3])
		size, err2 := strconv.ParseInt(f[4], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("malformed index line %q", line)
		}
		out = append(out, Entry{Hash: f[0], Type: f[1], ID: f[2], Subfiles: subfiles, Size: size})
	}
	return out, sc.Err()
}

// Item types in the metadata.
const (
	TypeDocument   = "DocumentType"
	TypeCollection = "CollectionType" // a folder
)

// Special parents: items in the trash have the parent "trash"; top-level items have none.
const TrashParent = "trash"

// Metadata is a document's or folder's .metadata file.
type Metadata struct {
	VisibleName  string    `json:"visibleName"`
	Type         string    `json:"type"`
	Parent       string    `json:"parent"`
	Deleted      bool      `json:"deleted"`
	LastModified Timestamp `json:"lastModified"`
	CreatedTime  Timestamp `json:"createdTime"`
}

// Timestamp is a time in Unix milliseconds, given as a string or a number.
type Timestamp int64

// UnmarshalJSON accepts "1690000000000" and 1690000000000; anything else is zero.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		*t = 0
		return nil
	}
	*t = Timestamp(n)
	return nil
}

// Time returns the time, or nil when unknown.
func (t Timestamp) Time() *time.Time {
	if t <= 0 {
		return nil
	}
	v := time.UnixMilli(int64(t)).UTC()
	return &v
}

// Item is a document or folder of the account.
type Item struct {
	ID       string
	Hash     string // changes whenever any of its files changes
	Metadata Metadata
	Files    []Entry
}

// ContentHash identifies the item's content: it changes when a file other than the metadata
// changes, but not when the item is renamed or moved.
func (it *Item) ContentHash() string {
	files := make([]string, 0, len(it.Files))
	for _, f := range it.Files {
		if !strings.HasSuffix(f.ID, ".metadata") {
			files = append(files, f.ID+":"+f.Hash)
		}
	}
	sort.Strings(files)
	h := sha256.Sum256([]byte(strings.Join(files, "\n")))
	return hex.EncodeToString(h[:])
}

// ReadItem reads the index and metadata of a root index entry.
func (s *Session) ReadItem(ctx context.Context, e Entry) (*Item, error) {
	files, err := s.Index(ctx, e.Hash, e.ID+".docSchema")
	if err != nil {
		return nil, err
	}
	it := &Item{ID: e.ID, Hash: e.Hash, Files: files}
	for _, f := range files {
		if f.ID == e.ID+".metadata" {
			raw, err := s.Blob(ctx, f.Hash, f.ID, 1<<20)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &it.Metadata); err != nil {
				return nil, fmt.Errorf("remarkable metadata of %s: %w", e.ID, err)
			}
			return it, nil
		}
	}
	return nil, fmt.Errorf("remarkable item %s has no metadata", e.ID)
}

// Content is the part of a document's .content file used here: its kind and page order.
type Content struct {
	// FileType is "pdf", "epub", or "notebook" (or empty) for handwritten notebooks.
	FileType string   `json:"fileType"`
	Pages    []string `json:"pages"` // page IDs, before firmware 3.0
	CPages   *struct {
		Pages []struct {
			ID  string `json:"id"`
			Idx struct {
				Value string `json:"value"`
			} `json:"idx"`
			Deleted *struct {
				Value int `json:"value"`
			} `json:"deleted"`
		} `json:"pages"`
	} `json:"cPages"`
	PageCount int `json:"pageCount"`
}

// PageIDs returns the IDs of the document's pages in order.
func (c *Content) PageIDs() []string {
	if c.CPages != nil && len(c.CPages.Pages) > 0 {
		pages := c.CPages.Pages
		sort.SliceStable(pages, func(i, j int) bool { return pages[i].Idx.Value < pages[j].Idx.Value })
		ids := make([]string, 0, len(pages))
		for _, p := range pages {
			if p.Deleted == nil || p.Deleted.Value == 0 {
				ids = append(ids, p.ID)
			}
		}
		return ids
	}
	return c.Pages
}

// Kind is what a document holds.
type Kind string

const (
	KindNotebook Kind = "notebook"
	KindPDF      Kind = "pdf"
	KindEPUB     Kind = "epub"
)

// Kind returns the document's kind.
func (c *Content) Kind() Kind {
	switch strings.ToLower(c.FileType) {
	case "pdf":
		return KindPDF
	case "epub":
		return KindEPUB
	}
	return KindNotebook
}
