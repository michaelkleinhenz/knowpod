package remarkable

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	rt "github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable/remarkabletest"
)

func TestNoteEPUB(t *testing.T) {
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	md := "## Plan\n\n- [x] done\n- [ ] open\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nSee #12 & <b>raw</b>\n\n---\n"
	data, err := NoteEPUB("n1", "Q3 <plan> & more", md, "de", at)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := NoteEPUB("n1", "Q3 <plan> & more", md, "de", at)
	if !bytes.Equal(data, again) {
		t.Error("the same note gives different bytes")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if f := zr.File[0]; f.Name != "mimetype" || f.Method != zip.Store {
		t.Fatalf("first file: %s %d", f.Name, f.Method)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	if files["mimetype"] != "application/epub+zip" || !strings.Contains(files["META-INF/container.xml"], "OEBPS/content.opf") {
		t.Errorf("container: %q", files["mimetype"])
	}
	page := files["OEBPS/note.xhtml"]
	for _, want := range []string{"<h1>Q3 &lt;plan&gt; &amp; more</h1>", "☑ done", "☐ open", "<table>", "See #12 &amp;", `xml:lang="de"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<b>raw</b>") || strings.Contains(page, "<input") {
		t.Errorf("raw HTML or checkboxes kept:\n%s", page)
	}
	// Every XML file must be well-formed.
	for name, text := range files {
		if !strings.HasSuffix(name, ".xhtml") && !strings.HasSuffix(name, ".opf") && !strings.HasSuffix(name, ".ncx") && !strings.HasSuffix(name, ".xml") {
			continue
		}
		d := xml.NewDecoder(strings.NewReader(text))
		d.Strict = true
		d.Entity = xml.HTMLEntity
		for {
			if _, err := d.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed: %v\n%s", name, err, text)
			}
		}
	}
}

func TestWriteDocuments(t *testing.T) {
	cloud := rt.New()
	defer cloud.Close()
	cloud.AddCode("abcdefgh", "device-1")
	cloud.Set(rt.Item{ID: "f1", Name: "Work", Folder: true})
	cloud.Set(rt.Item{ID: "d1", Name: "Ideas"})
	c := NewClient(cloud.URL, cloud.URL)
	ctx := context.Background()
	dev, _ := c.Pair(ctx, "abcdefgh")
	s, err := c.Open(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	// A new document; the write is redone when another device changed the account.
	cloud.RaceRoot = func() { cloud.Set(rt.Item{ID: "d2", Name: "Other device"}) }
	ids, err := s.WriteDocuments(ctx, []DocumentWrite{{Name: "Plan", Parent: "f1", EPUB: []byte("epub-1")}}, now)
	if err != nil || len(ids) != 1 || ids[0] == "" {
		t.Fatalf("create: %v %v", ids, err)
	}
	id := ids[0]
	meta, files, ok := cloud.Document(id)
	if !ok || meta["visibleName"] != "Plan" || meta["parent"] != "f1" || meta["type"] != "DocumentType" ||
		string(files[id+".epub"]) != "epub-1" || !strings.Contains(string(files[id+".content"]), `"fileType":"epub"`) {
		t.Fatalf("created: %v %v", meta, files)
	}
	if got := cloud.IDs(); len(got) != 4 {
		t.Fatalf("the other device's document was lost: %v", got)
	}

	// Something written on the tablet stays when the file changes.
	// What the tablet made from the old file goes, so it is made again from the new one.
	cloud.AddFile(id, id+"/p1.rm", []byte("strokes"))
	cloud.AddFile(id, id+".pdf", []byte("old pdf"))
	cloud.AddFile(id, id+".epubindex", []byte("old index"))
	cloud.AddFile(id, id+".thumbnails/p1.png", []byte("old thumbnail"))
	ids, err = s.WriteDocuments(ctx, []DocumentWrite{{ID: id, Name: "Plan v2", Parent: "", EPUB: []byte("epub-2")}}, now)
	if err != nil || ids[0] != id {
		t.Fatalf("update: %v %v", ids, err)
	}
	meta, files, _ = cloud.Document(id)
	if meta["visibleName"] != "Plan v2" || meta["parent"] != "" || meta["createdTime"] == nil || string(files[id+".epub"]) != "epub-2" || files[id+".content"] == nil ||
		string(files[id+"/p1.rm"]) != "strokes" {
		t.Fatalf("updated: %v %v", meta, files)
	}
	for _, name := range []string{id + ".pdf", id + ".epubindex", id + ".thumbnails/p1.png"} {
		if _, ok := files[name]; ok {
			t.Fatalf("%s was kept", name)
		}
	}

	// Only the folder: the file stays.
	if _, err := s.WriteDocuments(ctx, []DocumentWrite{{ID: id, Name: "Plan v2", Parent: TrashParent}}, now); err != nil {
		t.Fatal(err)
	}
	meta, files, _ = cloud.Document(id)
	if meta["parent"] != TrashParent || string(files[id+".epub"]) != "epub-2" {
		t.Fatalf("trashed: %v", meta)
	}

	// A document that is gone is made again, or skipped without a file.
	cloud.Remove(id)
	ids, err = s.WriteDocuments(ctx, []DocumentWrite{{ID: id, Name: "Gone", Parent: TrashParent}, {ID: id, Name: "Back", EPUB: []byte("epub-3")}}, now)
	if err != nil || ids[0] != "" || ids[1] == "" || ids[1] == id {
		t.Fatalf("gone: %v %v", ids, err)
	}
	if _, files, _ := cloud.Document(ids[1]); string(files[ids[1]+".epub"]) != "epub-3" {
		t.Fatal("not made again")
	}

	// The written account reads back like any other.
	root, _ := s.Root(ctx)
	entries, err := s.Index(ctx, root.Hash, "root.docSchema")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == ids[1] {
			it, err := s.ReadItem(ctx, e)
			if err != nil || it.Metadata.VisibleName != "Back" || it.Metadata.Type != TypeDocument {
				t.Fatalf("read back: %+v %v", it, err)
			}
		}
	}
}
