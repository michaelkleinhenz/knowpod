package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
	rt "github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable/remarkabletest"
)

// textNote stores a text note of u1 in the folder.
func (f *remarkableFixture) textNote(t *testing.T, id, title, markdown, folderID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := f.recs.Create(context.Background(), &recording.Recording{
		ID: id, OwnerID: "u1", DeviceID: recording.TextDeviceID("u1"), ClientID: id, Type: recording.TypeText,
		Status: recording.StatusSummarized, FolderID: folderID, Revision: 1, CreatedAt: now, UpdatedAt: now,
		Summary: &recording.Summary{Title: title, Markdown: markdown},
	}); err != nil {
		t.Fatal(err)
	}
}

// changeNote changes a stored note.
func (f *remarkableFixture) changeNote(t *testing.T, id string, fn func(r *recording.Recording)) {
	t.Helper()
	r, err := f.recs.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	fn(r)
	if err := f.recs.Update(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func (f *remarkableFixture) push(t *testing.T) {
	t.Helper()
	if err := f.svc.PushUser(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
}

// copyOf returns the note's tablet copy and the cloud document's metadata and EPUB text.
func (f *remarkableFixture) copyOf(t *testing.T, id string) (*recording.TabletCopy, map[string]any, string) {
	t.Helper()
	r, _ := f.recs.Get(context.Background(), id)
	if r.Tablet == nil {
		return nil, nil, ""
	}
	meta, files, ok := f.cloud.Document(r.Tablet.DocumentID)
	if !ok {
		t.Fatalf("the document of %s is not in the cloud", id)
	}
	epub := files[r.Tablet.DocumentID+".epub"]
	zr, err := zip.NewReader(bytes.NewReader(epub), int64(len(epub)))
	if err != nil {
		t.Fatalf("epub of %s: %v", id, err)
	}
	for _, zf := range zr.File {
		if zf.Name == "OEBPS/note.xhtml" {
			rc, _ := zf.Open()
			text, _ := io.ReadAll(rc)
			rc.Close()
			return r.Tablet, meta, string(text)
		}
	}
	t.Fatalf("epub of %s has no page", id)
	return nil, nil, ""
}

func TestRemarkableSendsTextNotes(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.cloud.Set(rt.Item{ID: "c1", Name: "Work", Folder: true})
	f.cloud.Set(notebook("d1", "Ideas", "c1"))
	f.pair(t)
	// Nothing is sent before the first pull made the folder.
	f.textNote(t, "early", "Early", "x", "")
	f.push(t)
	f.pull(t)
	tree := f.tree(t)
	root, work := tree["reMarkable"].ID, tree["reMarkable/Work"].ID

	f.textNote(t, "a", "Plan", "## Steps\n\n- [ ] call", root)
	f.textNote(t, "b", "Elsewhere", "stays here", "")
	f.textNote(t, "c", "Work notes", "in a folder", work)
	f.textNote(t, "sub", "Sub-note", "under a", "")
	f.changeNote(t, "sub", func(r *recording.Recording) { r.ParentID = "a" })
	_ = f.cloud.Requests()
	f.push(t)

	ca, meta, text := f.copyOf(t, "a")
	if ca == nil || ca.Removed || ca.Error != "" || ca.SentAt == nil || meta["visibleName"] != "Plan" || meta["parent"] != "" ||
		!strings.Contains(text, "<h2>Steps</h2>") || !strings.Contains(text, "☐ call") {
		t.Fatalf("a: %+v %v\n%s", ca, meta, text)
	}
	if cc, meta, _ := f.copyOf(t, "c"); cc == nil || meta["parent"] != "c1" {
		t.Fatalf("c goes into the cloud folder: %+v %v", cc, meta)
	}
	for _, id := range []string{"b", "sub", "early"} {
		if c, _, _ := f.copyOf(t, id); c != nil {
			t.Errorf("%s was sent: %+v", id, c)
		}
	}
	// One change of the account for all notes.
	puts := 0
	for _, r := range f.cloud.Requests() {
		if strings.HasPrefix(r, "PUT /sync/v3/root") {
			puts++
		}
	}
	if puts != 1 {
		t.Errorf("root updates: %d", puts)
	}

	// Nothing changed: the cloud isn't asked.
	f.push(t)
	if r := f.cloud.Requests(); len(r) != 0 {
		t.Errorf("unchanged notes cost requests: %v", r)
	}

	// Pulls leave the copies out.
	v := f.pull(t)
	if v.LastResult.Documents != 1 || v.LastResult.Imported != 0 {
		t.Errorf("pull: %+v", v.LastResult)
	}
	if list, _ := f.recs.List(ctx, recording.ListFilter{DeviceID: recording.RemarkableDeviceID("u1")}); len(list) != 1 {
		t.Errorf("imported notes: %d", len(list))
	}

	// Changes follow the note, into the same document.
	f.changeNote(t, "a", func(r *recording.Recording) {
		r.Summary.Title, r.Summary.Markdown = "Plan v2", "done"
		r.Revision++
	})
	f.push(t)
	if c, meta, text := f.copyOf(t, "a"); c.DocumentID != ca.DocumentID || meta["visibleName"] != "Plan v2" || !strings.Contains(text, "<p>done</p>") {
		t.Fatalf("changed: %+v %v\n%s", c, meta, text)
	}

	// Out of the folder: into the tablet's trash; back in: back on the tablet.
	f.changeNote(t, "a", func(r *recording.Recording) { r.FolderID = "" })
	f.push(t)
	if c, meta, _ := f.copyOf(t, "a"); !c.Removed || meta["parent"] != remarkable.TrashParent {
		t.Fatalf("moved out: %+v %v", c, meta)
	}
	f.changeNote(t, "a", func(r *recording.Recording) { r.FolderID = work })
	f.push(t)
	if c, meta, _ := f.copyOf(t, "a"); c.Removed || c.DocumentID != ca.DocumentID || meta["parent"] != "c1" {
		t.Fatalf("moved back: %+v %v", c, meta)
	}
	// Into the knowpod trash: the same.
	now := time.Now()
	f.changeNote(t, "c", func(r *recording.Recording) { r.DeletedAt = &now })
	f.push(t)
	if c, meta, _ := f.copyOf(t, "c"); !c.Removed || meta["parent"] != remarkable.TrashParent {
		t.Fatalf("trashed: %+v %v", c, meta)
	}

	// A document deleted on the tablet is made again with the next change.
	f.cloud.Remove(ca.DocumentID)
	f.changeNote(t, "a", func(r *recording.Recording) { r.Summary.Markdown = "again"; r.Revision++ })
	f.push(t)
	if c, _, text := f.copyOf(t, "a"); c.DocumentID == ca.DocumentID || !strings.Contains(text, "again") {
		t.Fatalf("made again: %+v", c)
	}

	// A failed send is recorded on the note and tried again.
	f.cloud.Revoke("device-u1")
	f.changeNote(t, "a", func(r *recording.Recording) { r.Summary.Markdown = "later"; r.Revision++ })
	if err := f.svc.PushUser(ctx, "u1"); !errors.Is(err, ErrPairingRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	if r, _ := f.recs.Get(ctx, "a"); r.Tablet.Error == "" {
		t.Fatal("no error on the note")
	}
}

func TestRemarkableNudgeWaitsForQuiet(t *testing.T) {
	f := newRemarkableFixture(t)
	f.svc.pushDelay = 20 * time.Millisecond
	f.pair(t)
	f.pull(t)
	f.textNote(t, "a", "Plan", "x", f.tree(t)["reMarkable"].ID)
	for i := 0; i < 3; i++ {
		f.svc.Nudge("u1")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if r, _ := f.recs.Get(context.Background(), "a"); r.Tablet != nil && r.Tablet.DocumentID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the nudge sent nothing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.svc.StopNudges()
	f.svc.Nudge("u1") // ignored after stopping
}

func TestRemarkableFolderCannotBeDeletedWhilePaired(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.cloud.Set(rt.Item{ID: "c1", Name: "Work", Folder: true})
	f.pair(t)
	f.pull(t)
	tree := f.tree(t)
	folders := NewFolderService(f.folders, f.recs)
	folders.Tablets = f.links
	list, _ := folders.List(ctx, f.acc)
	for _, fo := range list {
		if fo.Remarkable != (fo.ID == tree["reMarkable"].ID) {
			t.Errorf("remarkable flag of %s: %v", fo.Name, fo.Remarkable)
		}
	}
	if err := folders.Delete(ctx, f.acc, tree["reMarkable"].ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete: %v", err)
	}
	if err := folders.Delete(ctx, f.acc, tree["reMarkable/Work"].ID); err != nil {
		t.Fatalf("delete a folder inside: %v", err)
	}
	if err := f.svc.Unpair(ctx, f.acc); err != nil {
		t.Fatal(err)
	}
	if err := folders.Delete(ctx, f.acc, tree["reMarkable"].ID); err != nil {
		t.Fatalf("delete after unpairing: %v", err)
	}
}

// textScaleOf returns the text size the document's .content asks the reader for.
func (f *remarkableFixture) textScaleOf(t *testing.T, noteID string) float64 {
	t.Helper()
	r, _ := f.recs.Get(context.Background(), noteID)
	_, files, ok := f.cloud.Document(r.Tablet.DocumentID)
	if !ok {
		t.Fatalf("the document of %s is not in the cloud", noteID)
	}
	var c struct {
		TextScale float64 `json:"textScale"`
	}
	if err := json.Unmarshal(files[r.Tablet.DocumentID+".content"], &c); err != nil {
		t.Fatal(err)
	}
	return c.TextScale
}

// setCopyContent replaces the document's .content, as the tablet does when the text size is
// changed there, and forgets that the copy was scaled (as a copy sent before that existed).
func (f *remarkableFixture) setCopyContent(t *testing.T, noteID, content string) {
	t.Helper()
	r, _ := f.recs.Get(context.Background(), noteID)
	id := r.Tablet.DocumentID
	meta, files, _ := f.cloud.Document(id)
	f.cloud.Set(rt.Item{ID: id, Name: meta["visibleName"].(string), Content: content,
		Files: map[string][]byte{".epub": files[id+".epub"]}})
	f.changeNote(t, noteID, func(r *recording.Recording) { r.Tablet.Scaled = false })
}

func TestRemarkableSendsSmallerText(t *testing.T) {
	f := newRemarkableFixture(t)
	f.pair(t)
	f.pull(t)
	root := f.tree(t)["reMarkable"].ID
	f.textNote(t, "a", "New", "text", root)
	f.textNote(t, "b", "Default size", "text", root)
	f.textNote(t, "c", "Own size", "text", root)
	f.push(t)
	for _, id := range []string{"a", "b", "c"} {
		if got := f.textScaleOf(t, id); got != remarkable.TextScale {
			t.Errorf("new copy of %s: text scale %v", id, got)
		}
	}

	// Copies sent before are sent again once: at the default size they get the new size, but a
	// size chosen on the tablet stays.
	f.setCopyContent(t, "b", `{"fileType":"epub","textScale":1,"lastOpenedPage":4}`)
	f.setCopyContent(t, "c", `{"fileType":"epub","textScale":1.3}`)
	f.push(t)
	if got := f.textScaleOf(t, "b"); got != remarkable.TextScale {
		t.Errorf("default size copy: %v", got)
	}
	if got := f.textScaleOf(t, "c"); got != 1.3 {
		t.Errorf("chosen size copy: %v", got)
	}
	r, _ := f.recs.Get(context.Background(), "b")
	_, files, _ := f.cloud.Document(r.Tablet.DocumentID)
	if !strings.Contains(string(files[r.Tablet.DocumentID+".content"]), `"lastOpenedPage":4`) {
		t.Errorf("the rest of .content changed: %s", files[r.Tablet.DocumentID+".content"])
	}

	// From then on the size is left alone, also when it was changed to the default.
	f.setCopyContent(t, "c", `{"fileType":"epub","textScale":1}`)
	f.changeNote(t, "c", func(r *recording.Recording) { r.Tablet.Scaled = true })
	f.changeNote(t, "c", func(r *recording.Recording) { r.Summary.Markdown, r.Revision = "more text", r.Revision+1 })
	f.push(t)
	if got := f.textScaleOf(t, "c"); got != 1 {
		t.Errorf("size after a later change: %v", got)
	}
}

func TestRemarkablePullsWhatWasWrittenOnACopy(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.pair(t)
	f.pull(t)
	root := f.tree(t)["reMarkable"].ID
	f.textNote(t, "a", "Plan", "text", root)
	f.push(t)
	r, _ := f.recs.Get(ctx, "a")
	doc := r.Tablet.DocumentID

	// Untouched: nothing to keep, and it isn't looked at again.
	f.pull(t)
	if r, _ = f.recs.Get(ctx, "a"); len(r.Attachments) != 0 || r.Tablet.InkHash == "" {
		t.Fatalf("untouched copy: %+v %+v", r.Attachments, r.Tablet)
	}

	// Something is written on it, on a page file the document doesn't list (an EPUB's).
	f.cloud.AddFile(doc, doc+"/p1.rm", rt.Page(scribble))
	f.pull(t)
	r, _ = f.recs.Get(ctx, "a")
	if len(r.Attachments) != 1 || r.Attachments[0].Name != "reMarkable scribbles.pdf" || r.Attachments[0].ContentType != "application/pdf" ||
		r.Tablet.InkAttachment != r.Attachments[0].ID {
		t.Fatalf("attachments: %+v %+v", r.Attachments, r.Tablet)
	}
	att := r.Attachments[0]
	body, err := f.objects.Get(ctx, att.Key, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	pdf, _ := io.ReadAll(body)
	if !bytes.HasPrefix(pdf, []byte("%PDF")) || int64(len(pdf)) != att.Size {
		t.Fatalf("pdf: %d bytes, size %d", len(pdf), att.Size)
	}
	if !strings.HasPrefix(att.Key, "recordings/u1/a/attachments/") {
		t.Errorf("key: %s", att.Key)
	}

	// More is written: the same attachment is replaced, not added.
	f.cloud.AddFile(doc, doc+"/p2.rm", rt.Page(scribble, scribble))
	f.pull(t)
	r, _ = f.recs.Get(ctx, "a")
	if len(r.Attachments) != 1 || r.Attachments[0].ID != att.ID || r.Attachments[0].Size == att.Size {
		t.Fatalf("after more ink: %+v", r.Attachments)
	}

	// The note's text is sent again: the handwriting stays.
	f.changeNote(t, "a", func(r *recording.Recording) { r.Summary.Markdown, r.Revision = "changed", r.Revision+1 })
	f.push(t)
	f.pull(t)
	if r, _ = f.recs.Get(ctx, "a"); len(r.Attachments) != 1 {
		t.Fatalf("after a text change: %+v", r.Attachments)
	}

	// Everything is erased on the tablet: the attachment goes.
	meta, files, _ := f.cloud.Document(doc)
	f.cloud.Set(rt.Item{ID: doc, Name: meta["visibleName"].(string), Content: string(files[doc+".content"]),
		Files: map[string][]byte{".epub": files[doc+".epub"]}})
	f.pull(t)
	r, _ = f.recs.Get(ctx, "a")
	if len(r.Attachments) != 0 || r.Tablet.InkAttachment != "" {
		t.Fatalf("after erasing: %+v %+v", r.Attachments, r.Tablet)
	}
	if _, err := f.objects.Get(ctx, att.Key, 0, -1); !errors.Is(err, ErrNotFound) {
		t.Errorf("the PDF stayed in storage: %v", err)
	}
}
