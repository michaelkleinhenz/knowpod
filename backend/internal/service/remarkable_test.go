package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
	rt "github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable/remarkabletest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

type remarkableFixture struct {
	cloud   *rt.Cloud
	svc     *RemarkableService
	recs    *memory.Recordings
	folders *memory.Folders
	objects *memstore.Store
	acc     *Account
	queued  int
}

func newRemarkableFixture(t *testing.T) *remarkableFixture {
	t.Helper()
	cloud := rt.New()
	t.Cleanup(cloud.Close)
	spool, err := NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &remarkableFixture{cloud: cloud, recs: memory.NewRecordings(), folders: memory.NewFolders(), objects: memstore.New(), acc: &Account{ID: "u1"}}
	f.svc = NewRemarkableService(memory.NewTabletLinks(), f.recs, f.folders, f.objects, remarkable.NewClient(cloud.URL, cloud.URL),
		spool, 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.svc.OnQueued = func() { f.queued++ }
	return f
}

func (f *remarkableFixture) pair(t *testing.T) {
	t.Helper()
	f.cloud.AddCode("abcdefgh", "device-u1")
	if _, err := f.svc.Pair(context.Background(), f.acc, "abcd efgh"); err != nil {
		t.Fatal(err)
	}
}

func (f *remarkableFixture) pull(t *testing.T) *RemarkableView {
	t.Helper()
	v, err := f.svc.Pull(context.Background(), f.acc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *remarkableFixture) note(t *testing.T, docID string) *recording.Recording {
	t.Helper()
	rec, err := f.recs.GetByClientID(context.Background(), recording.RemarkableDeviceID("u1"), docID)
	if err != nil {
		t.Fatalf("note of %s: %v", docID, err)
	}
	return rec
}

func notebook(id, name, parent string, strokes ...rt.Line) rt.Item {
	return rt.Item{ID: id, Name: name, Parent: parent, LastModified: 1700000000000,
		Content: `{"fileType":"notebook","cPages":{"pages":[{"id":"p1","idx":{"value":"ba"}},{"id":"p2","idx":{"value":"bb"}}]}}`,
		Files:   map[string][]byte{"p1.rm": rt.Page(strokes...)}}
}

var scribble = rt.Line{Tool: 15, Points: [][3]float32{{0, 100, 2}, {50, 120, 2}}}

func TestRemarkablePairing(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	if v, _ := f.svc.Status(ctx, f.acc); v.Paired || v.Folder != "reMarkable" || v.ConnectURL == "" {
		t.Errorf("unpaired status: %+v", v)
	}
	if _, err := f.svc.Pull(ctx, f.acc); !errors.Is(err, ErrNotPaired) {
		t.Errorf("pull unpaired: %v", err)
	}
	if _, err := f.svc.Pair(ctx, f.acc, "123"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("short code: %v", err)
	}
	if _, err := f.svc.Pair(ctx, f.acc, "zzzzzzzz"); !errors.Is(err, ErrInvalidPairingCode) {
		t.Errorf("wrong code: %v", err)
	}
	if _, err := f.svc.Pair(ctx, &Account{All: true}, "abcdefgh"); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin token: %v", err)
	}
	f.pair(t)
	if v, _ := f.svc.Status(ctx, f.acc); !v.Paired || v.PairedAt == nil {
		t.Errorf("paired status: %+v", v)
	}

	f.cloud.Revoke("device-u1")
	if _, err := f.svc.Pull(ctx, f.acc); !errors.Is(err, ErrPairingRevoked) {
		t.Errorf("revoked: %v", err)
	}
	if v, _ := f.svc.Status(ctx, f.acc); v.LastError == "" || v.LastPullAt == nil {
		t.Errorf("failed pull not recorded: %+v", v)
	}

	if err := f.svc.Unpair(ctx, f.acc); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.svc.Status(ctx, f.acc); v.Paired {
		t.Errorf("still paired: %+v", v)
	}
	if err := f.svc.Unpair(ctx, f.acc); err != nil {
		t.Errorf("unpair twice: %v", err)
	}
}

func TestRemarkablePullsAllDocumentsIntoTheFolder(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.pair(t)
	if v := f.pull(t); v.LastResult == nil || v.LastResult.Documents != 0 || v.LastResult.Imported != 0 {
		t.Fatalf("empty account: %+v", v.LastResult)
	}
	if all, _ := f.folders.List(ctx, "u1"); len(all) != 0 {
		t.Errorf("folder made without documents: %+v", all)
	}

	f.cloud.Set(rt.Item{ID: "sub", Name: "Work", Folder: true})
	f.cloud.Set(rt.Item{ID: "old", Name: "Old", Parent: "trash", Folder: true})
	f.cloud.Set(notebook("n1", "Ideas", "", scribble))
	f.cloud.Set(notebook("n2", "Meeting", "sub"))
	f.cloud.Set(notebook("n3", "Trashed", "trash"))
	f.cloud.Set(notebook("n4", "In trashed folder", "old"))
	f.cloud.Set(rt.Item{ID: "n5", Name: "Gone", Deleted: true})
	v := f.pull(t)
	if r := v.LastResult; r.Documents != 2 || r.Imported != 2 || r.Updated != 0 || v.LastError != "" {
		t.Fatalf("result %+v %q", r, v.LastError)
	}
	if f.queued != 1 {
		t.Errorf("worker woken %d times", f.queued)
	}
	tree := f.tree(t)
	if len(tree) != 2 || tree["reMarkable"] == nil || tree["reMarkable/Work"] == nil {
		t.Fatalf("folders %v", tree)
	}
	root := tree["reMarkable"]
	rec := f.note(t, "n1")
	if rec.Type != recording.TypeDocument || rec.Source != recording.SourceRemarkable || rec.Status != recording.StatusRemote ||
		rec.Title != "Ideas" || rec.OwnerID != "u1" || rec.RecordedAt == nil || rec.RecordedAt.UnixMilli() != 1700000000000 ||
		rec.FolderID != root.ID {
		t.Errorf("note %+v", rec)
	}
	if f.note(t, "n2").FolderID != tree["reMarkable/Work"].ID {
		t.Errorf("note of a subfolder not in its folder")
	}
	for _, id := range []string{"n3", "n4", "n5"} {
		if _, err := f.recs.GetByClientID(ctx, recording.RemarkableDeviceID("u1"), id); err == nil {
			t.Errorf("%s was imported", id)
		}
	}

	// Nothing changed: only the sign-in and the root are read.
	f.cloud.Requests()
	if v := f.pull(t); v.LastResult.Imported != 0 || v.LastResult.Documents != 2 {
		t.Errorf("second pull %+v", v.LastResult)
	}
	if reqs := f.cloud.Requests(); len(reqs) != 2 {
		t.Errorf("unchanged account cost %d requests: %v", len(reqs), reqs)
	}

	// A renamed folder is still used; a deleted one is made again.
	root.Name = "Tablet"
	_ = f.folders.Update(ctx, root)
	f.cloud.Set(notebook("n6", "More", ""))
	f.pull(t)
	if f.note(t, "n6").FolderID != root.ID {
		t.Errorf("renamed folder not used")
	}
	if tree := f.tree(t); len(tree) != 2 || tree["Tablet/Work"] == nil {
		t.Errorf("folders after rename %v", tree)
	}
	_ = f.folders.Delete(ctx, root.ID)
	f.cloud.Set(notebook("n7", "Even more", ""))
	f.pull(t)
	again := f.tree(t)
	if len(again) != 2 || again["reMarkable"] == nil || again["reMarkable/Work"] == nil || f.note(t, "n7").FolderID != again["reMarkable"].ID {
		t.Errorf("folder not made again: %v", again)
	}

	// A finished note is renamed with its document, summary title included.
	done := f.note(t, "n7")
	done.Status, done.Summary = recording.StatusSummarized, &recording.Summary{Title: "Even more"}
	_ = f.recs.Update(ctx, done)
	f.cloud.Set(notebook("n7", "Renamed", ""))
	f.pull(t)
	if got := f.note(t, "n7"); got.Title != "Renamed" || got.Summary.Title != "Renamed" {
		t.Errorf("note not renamed: %q %+v", got.Title, got.Summary)
	}
}

// tree returns u1's folders by path.
func (f *remarkableFixture) tree(t *testing.T) map[string]*folder.Folder {
	t.Helper()
	all, err := f.folders.List(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*folder.Folder{}
	for _, d := range all {
		byID[d.ID] = d
	}
	out := map[string]*folder.Folder{}
	for _, d := range all {
		path := d.Name
		for p := byID[d.ParentID]; p != nil; p = byID[p.ParentID] {
			path = p.Name + "/" + path
		}
		out[path] = d
	}
	return out
}

func TestRemarkableMirrorsTheFolders(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.pair(t)
	f.cloud.Set(rt.Item{ID: "work", Name: "Work", Folder: true})
	f.cloud.Set(rt.Item{ID: "meet", Name: "  Meetings ", Parent: "work", Folder: true})
	f.cloud.Set(rt.Item{ID: "empty", Name: "Empty", Folder: true})
	f.cloud.Set(rt.Item{ID: "dup1", Name: "Same", Folder: true})
	f.cloud.Set(rt.Item{ID: "dup2", Name: "same", Folder: true})
	f.cloud.Set(rt.Item{ID: "bin", Name: "Old", Parent: "trash", Folder: true})
	f.cloud.Set(notebook("n1", "Standup", "meet"))
	f.cloud.Set(notebook("n2", "Plan", "work"))
	f.cloud.Set(notebook("n3", "A", "dup1"))
	f.cloud.Set(notebook("n4", "B", "dup2"))
	f.cloud.Set(notebook("n5", "Top", ""))
	f.pull(t)

	tree := f.tree(t)
	for _, p := range []string{"reMarkable", "reMarkable/Work", "reMarkable/Work/Meetings", "reMarkable/Empty"} {
		if tree[p] == nil {
			t.Errorf("no folder %s in %v", p, tree)
		}
	}
	if len(tree) != 6 || tree["reMarkable/Old"] != nil {
		t.Errorf("folders %v", tree)
	}
	if f.note(t, "n1").FolderID != tree["reMarkable/Work/Meetings"].ID || f.note(t, "n2").FolderID != tree["reMarkable/Work"].ID ||
		f.note(t, "n5").FolderID != tree["reMarkable"].ID {
		t.Errorf("notes not in their folders")
	}
	if a, b := f.note(t, "n3").FolderID, f.note(t, "n4").FolderID; a == b || a == "" || b == "" {
		t.Errorf("folders of the same name share a folder: %q %q", a, b)
	}
	meetings := tree["reMarkable/Work/Meetings"]

	// Folders are renamed and moved with the cloud's; finished notes move with their
	// documents, notes in processing on a later pull, and notes the user moved stay.
	for _, id := range []string{"n1", "n2", "n5"} {
		rec := f.note(t, id)
		rec.Status = recording.StatusSummarized
		_ = f.recs.Update(ctx, rec)
	}
	mine := &folder.Folder{ID: "mine", OwnerID: "u1", Name: "Mine"}
	_ = f.folders.Create(ctx, mine)
	moved := f.note(t, "n5")
	moved.FolderID = "mine"
	_ = f.recs.Update(ctx, moved)
	f.cloud.Set(rt.Item{ID: "meet", Name: "Calls", Folder: true})
	f.cloud.Set(notebook("n2", "Plan", "empty"))
	f.cloud.Set(notebook("n3", "A", "work"))
	f.cloud.Set(notebook("n5", "Top", "work"))
	if v := f.pull(t); v.LastResult.Updated != 0 || v.LastResult.Imported != 0 {
		t.Errorf("moves queued notes: %+v", v.LastResult)
	}
	tree = f.tree(t)
	if tree["reMarkable/Calls"] == nil || tree["reMarkable/Calls"].ID != meetings.ID || tree["reMarkable/Work/Meetings"] != nil {
		t.Errorf("folder not renamed and moved: %v", tree)
	}
	if f.note(t, "n2").FolderID != tree["reMarkable/Empty"].ID || f.note(t, "n5").FolderID != "mine" {
		t.Errorf("finished notes not moved right")
	}
	if f.note(t, "n3").FolderID == tree["reMarkable/Work"].ID {
		t.Errorf("note in processing moved")
	}
	rec := f.note(t, "n3")
	rec.Status = recording.StatusSummarized
	_ = f.recs.Update(ctx, rec)
	f.pull(t)
	if f.note(t, "n3").FolderID != tree["reMarkable/Work"].ID {
		t.Errorf("note not moved once processed")
	}

	// Once everything is placed, an unchanged account costs two requests again.
	f.pull(t)
	f.cloud.Requests()
	f.pull(t)
	if reqs := f.cloud.Requests(); len(reqs) != 2 {
		t.Errorf("unchanged account cost %d requests: %v", len(reqs), reqs)
	}
}

func TestRemarkableMirrorsFoldersOfEarlierImports(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.pair(t)
	f.cloud.Set(rt.Item{ID: "work", Name: "Work", Folder: true})
	f.cloud.Set(notebook("n1", "Plan", "work"))
	f.pull(t)
	// As imported before folders were mirrored: all notes in the reMarkable folder.
	l, _ := f.svc.links.Get(ctx, "u1")
	l.Folders, l.MirroredHash = nil, ""
	_ = f.svc.links.Save(ctx, l)
	tree := f.tree(t)
	_ = f.folders.Delete(ctx, tree["reMarkable/Work"].ID)
	rec := f.note(t, "n1")
	rec.FolderID, rec.Status = tree["reMarkable"].ID, recording.StatusSummarized
	_ = f.recs.Update(ctx, rec)

	f.pull(t)
	tree = f.tree(t)
	if len(tree) != 2 || tree["reMarkable/Work"] == nil || f.note(t, "n1").FolderID != tree["reMarkable/Work"].ID {
		t.Errorf("earlier import not mirrored: %v", tree)
	}
}

func TestRemarkableUsesAnExistingFolder(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.pair(t)
	existing := &folder.Folder{ID: "f1", OwnerID: "u1", Name: "remarkable"}
	_ = f.folders.Create(ctx, existing)
	_ = f.folders.Create(ctx, &folder.Folder{ID: "f2", OwnerID: "u2", Name: "reMarkable"})
	f.cloud.Set(notebook("n1", "Ideas", ""))
	f.pull(t)
	if got := f.note(t, "n1").FolderID; got != "f1" {
		t.Errorf("note in folder %q", got)
	}
	if all, _ := f.folders.List(ctx, "u1"); len(all) != 1 {
		t.Errorf("folders %+v", all)
	}
}

func TestRemarkableFetchStoreAndUpdates(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.pair(t)
	f.cloud.Set(rt.Item{ID: "rm", Name: "reMarkable", Folder: true})
	f.cloud.Set(notebook("n1", "Ideas", "rm", scribble))
	pdf := []byte("%PDF-1.4 a pdf")
	f.cloud.Set(rt.Item{ID: "d1", Name: "Paper", Parent: "rm", Content: `{"fileType":"pdf","pageCount":3}`, Files: map[string][]byte{".pdf": pdf}})
	f.pull(t)

	// The notebook is rendered to a PDF; its files are kept for reading.
	rec := f.note(t, "n1")
	if err := f.svc.Fetch(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rec.Size == 0 || rec.ReceivedAt == nil || rec.SourceRevision == "" {
		t.Errorf("fetched %+v", rec)
	}
	if err := f.svc.Store(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rec.File == nil || rec.File.ContentType != "application/pdf" || rec.File.Key != "recordings/u1/"+rec.ID+".pdf" ||
		rec.Original == nil || !strings.HasSuffix(rec.Original.Key, ".rmdoc") || rec.Pages != 2 || rec.StoredAt == nil {
		t.Fatalf("stored %+v", rec)
	}
	body, _ := f.objects.Get(ctx, rec.File.Key, 0, -1)
	data, _ := io.ReadAll(body)
	if !strings.HasPrefix(string(data), "%PDF-") {
		t.Errorf("file is not a PDF: %q", data[:min(20, len(data))])
	}

	// A PDF document is stored as it is.
	doc := f.note(t, "d1")
	if err := f.svc.Fetch(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store(ctx, doc); err != nil {
		t.Fatal(err)
	}
	body, _ = f.objects.Get(ctx, doc.File.Key, 0, -1)
	data, _ = io.ReadAll(body)
	if string(data) != string(pdf) || doc.Pages != 3 {
		t.Errorf("stored PDF %q, %d pages", data, doc.Pages)
	}

	// Finished notes: a rename only changes the title; new writing imports again.
	rec.Status = recording.StatusSummarized
	_ = f.recs.Update(ctx, rec)
	f.cloud.Set(notebook("n1", "Better ideas", "rm", scribble))
	if v := f.pull(t); v.LastResult.Updated != 0 {
		t.Errorf("rename queued the note again: %+v", v.LastResult)
	}
	if got := f.note(t, "n1"); got.Title != "Better ideas" || got.Status != recording.StatusSummarized {
		t.Errorf("after rename %+v", got)
	}
	f.cloud.Set(notebook("n1", "Better ideas", "rm", scribble, scribble))
	if v := f.pull(t); v.LastResult.Updated != 1 || v.LastResult.Imported != 0 {
		t.Errorf("change not imported: %+v", v.LastResult)
	}
	if got := f.note(t, "n1"); got.Status != recording.StatusRemote || got.File == nil {
		t.Errorf("after change %+v", got)
	}
	// The PDF note is still being processed and is left alone.
	f.cloud.Set(rt.Item{ID: "d1", Name: "Paper", Parent: "rm", Content: `{"fileType":"pdf","pageCount":4}`, Files: map[string][]byte{".pdf": []byte("%PDF-1.4 v2")}})
	if v := f.pull(t); v.LastResult.Updated != 0 {
		t.Errorf("note in processing queued again: %+v", v.LastResult)
	}

	// A document removed from the cloud can't be fetched; its note stays.
	f.cloud.Remove("n1")
	if err := f.svc.Fetch(ctx, f.note(t, "n1")); err == nil || !strings.Contains(err.Error(), "no longer") {
		t.Errorf("fetch of removed document: %v", err)
	}
}

func TestReadDocument(t *testing.T) {
	f := newRemarkableFixture(t)
	ctx := context.Background()
	f.pair(t)
	f.cloud.Set(rt.Item{ID: "rm", Name: "reMarkable", Folder: true})
	f.cloud.Set(notebook("n1", "Ideas", "rm", scribble))
	f.cloud.Set(rt.Item{ID: "d1", Name: "Paper", Parent: "rm", Content: `{"fileType":"pdf"}`, Files: map[string][]byte{".pdf": []byte("%PDF-1.4 x")}})
	f.pull(t)

	ai := &fakeAI{answer: "```markdown\n# Ideas\n- [ ] call Bob\n```"}
	s, _ := newAI(t, ai)
	s.objects = f.objects
	docModel := "anthropic/claude-sonnet-5"
	if _, err := s.UpdateSettings(ctx, OpenRouterUpdate{DocumentModel: &docModel}); err != nil {
		t.Fatal(err)
	}

	rec := f.note(t, "n1")
	_ = f.svc.Fetch(ctx, rec)
	_ = f.svc.Store(ctx, rec)
	if err := s.Transcribe(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rec.Transcript == nil || rec.Transcript.Text != "# Ideas\n- [ ] call Bob" || rec.Transcript.Model != docModel {
		t.Fatalf("transcript %+v", rec.Transcript)
	}
	// One request with the prompt and one image: the empty second page is left out.
	parts := ai.requests[0].Messages[0].Content.([]any)
	if ai.requests[0].Model != docModel || len(parts) != 2 {
		t.Fatalf("request %+v", ai.requests[0])
	}
	if img, ok := parts[1].(openrouter.ImagePart); !ok || !strings.HasPrefix(img.ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("image part %+v", parts[1])
	}

	// The summary is made from the text; an edited one survives reading the document again.
	// The note keeps the document's name on the tablet, not the title the model made up.
	ai.answer = `{"title":"Calling Bob","summary":"- Call Bob"}`
	if err := s.Summarize(ctx, rec); err != nil || rec.Summary.Title != "Ideas" {
		t.Fatalf("summary title: %v %+v", err, rec.Summary)
	}
	if sys := ai.requests[1].Messages[0].Content.(string); !strings.HasPrefix(sys, "You summarize handwritten notes") {
		t.Errorf("system prompt %q", sys)
	}
	if user := ai.requests[1].Messages[1].Content.(string); !strings.Contains(user, "Document text:") {
		t.Errorf("user message %q", user)
	}
	edited := rec.Summary.CreatedAt.Add(1)
	rec.Summary.EditedAt = &edited
	ai.answer = `{"title":"New","summary":"new"}`
	if err := s.Summarize(ctx, rec); err != nil || rec.Summary.Title != "Ideas" {
		t.Errorf("edited summary replaced: %v %+v", err, rec.Summary)
	}

	// A PDF goes to the model as a file.
	doc := f.note(t, "d1")
	_ = f.svc.Fetch(ctx, doc)
	_ = f.svc.Store(ctx, doc)
	ai.answer = "Text"
	if err := s.Transcribe(ctx, doc); err != nil {
		t.Fatal(err)
	}
	last := ai.requests[len(ai.requests)-1].Messages[0].Content.([]any)
	if fp, ok := last[1].(openrouter.FilePart); !ok || !strings.HasPrefix(fp.File.FileData, "data:application/pdf;base64,") || doc.Transcript.Text != "Text" {
		t.Errorf("PDF request %+v, transcript %+v", last, doc.Transcript)
	}
}

func TestDocumentsIgnoresCycles(t *testing.T) {
	f := newRemarkableFixture(t)
	f.pair(t)
	f.cloud.Set(rt.Item{ID: "a", Name: "A", Parent: "b", Folder: true})
	f.cloud.Set(rt.Item{ID: "b", Name: "B", Parent: "a", Folder: true})
	f.cloud.Set(notebook("n1", "Loop", "a"))
	f.cloud.Set(notebook("n2", "Top", ""))
	if v := f.pull(t); v.LastResult.Documents != 2 {
		t.Errorf("result %+v", v.LastResult)
	}
	tree := f.tree(t)
	if len(tree) != 3 || (tree["reMarkable/A/B"] == nil && tree["reMarkable/B/A"] == nil) {
		t.Errorf("folders %v", tree)
	}
}

func TestRemarkableBoundsFolderNesting(t *testing.T) {
	f := newRemarkableFixture(t)
	f.pair(t)
	parent := ""
	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		f.cloud.Set(rt.Item{ID: id, Name: id, Parent: parent, Folder: true})
		parent = id
	}
	f.cloud.Set(notebook("n1", "Deep", parent))
	f.pull(t)
	deepest := f.tree(t)["reMarkable/a/b/c/d/e/f/g"]
	if len(f.tree(t)) != maxFolderDepth || deepest == nil || f.note(t, "n1").FolderID != deepest.ID {
		t.Errorf("folders %v", f.tree(t))
	}
}
