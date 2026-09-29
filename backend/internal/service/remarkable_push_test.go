package service

import (
	"archive/zip"
	"bytes"
	"context"
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
