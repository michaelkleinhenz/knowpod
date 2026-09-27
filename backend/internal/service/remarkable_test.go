package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

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
	f := &remarkableFixture{cloud: cloud, recs: memory.NewRecordings(), objects: memstore.New(), acc: &Account{ID: "u1"}}
	f.svc = NewRemarkableService(memory.NewTabletLinks(), f.recs, f.objects, remarkable.NewClient(cloud.URL, cloud.URL),
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

func TestRemarkablePullsOnlyTheFolder(t *testing.T) {
	f := newRemarkableFixture(t)
	f.pair(t)
	f.cloud.Set(notebook("outside", "Diary", ""))
	if v := f.pull(t); v.LastResult == nil || v.LastResult.FolderFound || v.LastResult.Imported != 0 {
		t.Fatalf("without folder: %+v", v.LastResult)
	}

	f.cloud.Set(rt.Item{ID: "rm", Name: "reMarkable", Folder: true})
	f.cloud.Set(rt.Item{ID: "sub", Name: "Work", Parent: "rm", Folder: true})
	f.cloud.Set(rt.Item{ID: "other", Name: "Other", Folder: true})
	f.cloud.Set(notebook("n1", "Ideas", "rm", scribble))
	f.cloud.Set(notebook("n2", "Meeting", "sub"))
	f.cloud.Set(notebook("n3", "Elsewhere", "other"))
	f.cloud.Set(notebook("n4", "Trashed", "trash"))
	f.cloud.Set(rt.Item{ID: "n5", Name: "Gone", Parent: "rm", Deleted: true})
	v := f.pull(t)
	if r := v.LastResult; !r.FolderFound || r.Documents != 2 || r.Imported != 2 || r.Updated != 0 || v.LastError != "" {
		t.Fatalf("result %+v %q", r, v.LastError)
	}
	if f.queued != 1 {
		t.Errorf("worker woken %d times", f.queued)
	}
	rec := f.note(t, "n1")
	if rec.Type != recording.TypeDocument || rec.Source != recording.SourceRemarkable || rec.Status != recording.StatusRemote ||
		rec.Title != "Ideas" || rec.OwnerID != "u1" || rec.RecordedAt == nil || rec.RecordedAt.UnixMilli() != 1700000000000 {
		t.Errorf("note %+v", rec)
	}
	f.note(t, "n2")
	for _, id := range []string{"outside", "n3", "n4", "n5"} {
		if _, err := f.recs.GetByClientID(context.Background(), recording.RemarkableDeviceID("u1"), id); err == nil {
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
	ai.answer = `{"title":"Ideas","summary":"- Call Bob"}`
	if err := s.Summarize(ctx, rec); err != nil {
		t.Fatal(err)
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

func TestFolderDocumentsIgnoresCycles(t *testing.T) {
	f := newRemarkableFixture(t)
	f.pair(t)
	f.cloud.Set(rt.Item{ID: "rm", Name: "remarkable ", Folder: true}) // other spelling is accepted
	f.cloud.Set(rt.Item{ID: "a", Name: "A", Parent: "b", Folder: true})
	f.cloud.Set(rt.Item{ID: "b", Name: "B", Parent: "a", Folder: true})
	f.cloud.Set(notebook("n1", "Loop", "a"))
	f.cloud.Set(notebook("n2", "In", "rm"))
	if v := f.pull(t); !v.LastResult.FolderFound || v.LastResult.Documents != 1 {
		t.Errorf("result %+v", v.LastResult)
	}
}
