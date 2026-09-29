package remarkable

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Download writes all files of a document into a zip file at dst, each under its name in
// the document's index (e.g. "<uuid>.content", "<uuid>/<page>.rm", "<uuid>.pdf"). The
// files together may be at most maxBytes. It returns the total size of the files. On error,
// dst is removed.
func (s *Session) Download(ctx context.Context, it *Item, dst string, maxBytes int64) (total int64, err error) {
	f, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	zw := zip.NewWriter(f)
	for _, e := range it.Files {
		if !wanted(e.ID) {
			continue
		}
		w, err := zw.Create(e.ID)
		if err != nil {
			return total, err
		}
		n, err := s.CopyBlob(ctx, w, e.Hash, e.ID, maxBytes-total)
		total += n
		if err != nil {
			return total, err
		}
	}
	if err := zw.Close(); err != nil {
		return total, err
	}
	return total, f.Sync()
}

// wanted reports whether a document file is needed to show or read the document. Thumbnails
// and the per-page highlight and layer files are left out.
func wanted(name string) bool {
	switch {
	case strings.HasSuffix(name, ".metadata"), strings.HasSuffix(name, ".content"),
		strings.HasSuffix(name, ".pdf"), strings.HasSuffix(name, ".epub"):
		return !strings.Contains(name, "/")
	case strings.HasSuffix(name, ".rm"):
		return true
	}
	return false
}

// Archive is a downloaded document.
type Archive struct {
	zr       *zip.ReadCloser
	files    map[string]*zip.File
	ID       string
	Metadata Metadata
	Content  Content
}

// OpenArchive opens a zip written by Download.
func OpenArchive(path string) (*Archive, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	a := &Archive{zr: zr, files: map[string]*zip.File{}}
	for _, f := range zr.File {
		a.files[f.Name] = f
		if id, ok := strings.CutSuffix(f.Name, ".content"); ok && !strings.Contains(id, "/") {
			a.ID = id
		}
	}
	if a.ID == "" {
		zr.Close()
		return nil, errors.New("not a reMarkable document: no .content file")
	}
	if err := a.readJSON(a.ID+".content", &a.Content); err != nil {
		zr.Close()
		return nil, err
	}
	if _, ok := a.files[a.ID+".metadata"]; ok {
		if err := a.readJSON(a.ID+".metadata", &a.Metadata); err != nil {
			zr.Close()
			return nil, err
		}
	}
	return a, nil
}

// Close releases the archive.
func (a *Archive) Close() error { return a.zr.Close() }

// Kind returns what the document holds.
func (a *Archive) Kind() Kind { return a.Content.Kind() }

// Original opens the document's PDF or EPUB file (not for notebooks).
func (a *Archive) Original() (io.ReadCloser, int64, error) {
	f, ok := a.files[a.ID+"."+string(a.Kind())]
	if !ok || a.Kind() == KindNotebook {
		return nil, 0, fmt.Errorf("the document has no %s file", a.Kind())
	}
	rc, err := f.Open()
	return rc, int64(f.UncompressedSize64), err
}

// Pages returns the strokes of the document's pages in order. Pages without a drawing (no
// .rm file) are empty.
func (a *Archive) Pages() ([][]Stroke, error) {
	pages := make([][]Stroke, 0)
	err := a.eachPage(func(data []byte) error {
		var strokes []Stroke
		if data != nil {
			var err error
			if strokes, err = ParseLines(data); err != nil {
				return err
			}
		}
		pages = append(pages, strokes)
		return nil
	})
	return pages, err
}

// Ink returns the strokes of the pages that have any, in page order: the pages the
// document lists, then page files it doesn't list (sorted by name). A reflowable document (an
// EPUB) has a page file for each page something was written on. The typed text of a page is
// not part of it.
func (a *Archive) Ink() ([][]Stroke, error) {
	ids := a.Content.PageIDs()
	listed := make(map[string]bool, len(ids))
	for _, id := range ids {
		listed[id] = true
	}
	var extra []string
	for name := range a.files {
		if rest, ok := strings.CutPrefix(name, a.ID+"/"); ok && !strings.Contains(rest, "/") {
			if id, ok := strings.CutSuffix(rest, ".rm"); ok && !listed[id] {
				extra = append(extra, id)
			}
		}
	}
	sort.Strings(extra)
	pages := make([][]Stroke, 0)
	for _, id := range append(ids, extra...) {
		f, ok := a.files[a.ID+"/"+id+".rm"]
		if !ok {
			continue
		}
		data, err := readAll(f, 64<<20)
		if err != nil {
			return nil, err
		}
		strokes, err := ParseLines(data)
		if err != nil {
			return nil, fmt.Errorf("page %s: %w", id, err)
		}
		if len(strokes) > 0 {
			pages = append(pages, strokes)
		}
	}
	return pages, nil
}

// Texts returns the typed text of the document's pages in order, as Markdown; it is empty
// for pages without typed text.
func (a *Archive) Texts() ([]string, error) {
	texts := make([]string, 0)
	err := a.eachPage(func(data []byte) error {
		var text string
		if data != nil {
			var err error
			if text, err = ParseText(data); err != nil {
				return err
			}
		}
		texts = append(texts, text)
		return nil
	})
	return texts, err
}

// eachPage calls fn with the page file of each page in order, or nil for a page without one.
func (a *Archive) eachPage(fn func(data []byte) error) error {
	ids := a.Content.PageIDs()
	if len(ids) == 0 {
		// Very old documents name their page files by number.
		for i := 0; ; i++ {
			if _, ok := a.files[a.ID+"/"+strconv.Itoa(i)+".rm"]; !ok {
				break
			}
			ids = append(ids, strconv.Itoa(i))
		}
	}
	for i, id := range ids {
		var data []byte
		if f, ok := a.files[a.ID+"/"+id+".rm"]; ok {
			var err error
			if data, err = readAll(f, 64<<20); err != nil {
				return err
			}
		}
		if err := fn(data); err != nil {
			return fmt.Errorf("page %d: %w", i+1, err)
		}
	}
	return nil
}

func (a *Archive) readJSON(name string, v any) error {
	f, ok := a.files[name]
	if !ok {
		return fmt.Errorf("missing %s", name)
	}
	data, err := readAll(f, 16<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func readAll(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: %w", f.Name, ErrTooLarge)
	}
	return data, nil
}
