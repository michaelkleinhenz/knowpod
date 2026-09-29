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

	"go.mongodb.org/mongo-driver/bson"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

type personalFixture struct {
	s       *PersonalBackupService
	recs    *memory.Recordings
	folders *memory.Folders
	labels  *memory.Labels
	times   *memory.TimeEntries
	objects *memstore.Store
}

func newPersonalFixture(t *testing.T) *personalFixture {
	t.Helper()
	f := &personalFixture{recs: memory.NewRecordings(), folders: memory.NewFolders(), labels: memory.NewLabels(),
		times: memory.NewTimeEntries(), objects: memstore.New()}
	spool, err := NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	themes := memory.NewThemes()
	filters := memory.NewFilters()
	actions := NewRecordingService(f.recs, f.objects, spool, NewThemeService(themes))
	actions.TimeEntries = f.times
	f.s = NewPersonalBackupService(f.recs, f.folders, f.labels, themes, filters, f.times, f.objects, actions)
	f.s.TempDir = t.TempDir()
	return f
}

func (f *personalFixture) put(t *testing.T, key, body string) {
	t.Helper()
	if err := f.objects.Put(context.Background(), key, strings.NewReader(body), int64(len(body)), "audio/flac"); err != nil {
		t.Fatal(err)
	}
}

func (f *personalFixture) note(t *testing.T, owner, id string, edit func(*recording.Recording)) *recording.Recording {
	t.Helper()
	r := &recording.Recording{ID: id, OwnerID: owner, DeviceID: "dev-" + owner, ClientID: id, Status: recording.StatusSummarized,
		Summary: &recording.Summary{Title: "note " + id}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if edit != nil {
		edit(r)
	}
	if err := f.recs.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *personalFixture) backup(t *testing.T, owner string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := f.s.Backup(context.Background(), &Account{ID: owner}, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (f *personalFixture) noteIDs(t *testing.T, owner string) []string {
	t.Helper()
	list, err := f.recs.List(context.Background(), recording.ListFilter{OwnerID: owner, Trash: recording.TrashAny})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestPersonalBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newPersonalFixture(t)
	acc := &Account{ID: "bob"}

	f.put(t, "recordings/bob/n1.flac", "audio-1")
	f.put(t, "recordings/bob/n1/attachments/a1.pdf", "attachment")
	f.put(t, "recordings/eve/e1.flac", "eves-audio")
	_ = f.labels.Create(ctx, &label.Label{ID: "l1", OwnerID: "bob", Name: "Idea"})
	deleted := time.Now()
	f.note(t, "bob", "n1", func(r *recording.Recording) {
		r.Number, r.Labels = 7, []string{"l1", "task", "gone"}
		r.Audio = &recording.Object{Key: "recordings/bob/n1.flac", ContentType: "audio/flac", Size: 7}
		r.Attachments = []recording.Attachment{{ID: "a1", Name: "a.pdf", Key: "recordings/bob/n1/attachments/a1.pdf", Size: 10}}
		r.Shares = []recording.Share{{UserID: "eve", Role: recording.RoleViewer}}
		r.Members = []recording.Member{{UserID: "eve"}}
		r.CreatedBy, r.AssigneeID = "eve", "eve"
		r.FolderID, r.ParentID = "no-such-folder", "no-such-note"
	})
	f.note(t, "bob", "n2", func(r *recording.Recording) { r.Number, r.DeletedAt = 8, &deleted })
	f.note(t, "bob", "n3", func(r *recording.Recording) { r.Status = recording.StatusReceived })
	f.note(t, "eve", "e1", func(r *recording.Recording) {
		r.Audio = &recording.Object{Key: "recordings/eve/e1.flac", Size: 10}
	})
	start := time.Now().Add(-time.Hour)
	end := start.Add(time.Minute)
	_ = f.times.Create(ctx, &timelog.Entry{ID: "t1", OwnerID: "bob", NoteID: "n1", Start: start, End: &end})
	_ = f.times.Create(ctx, &timelog.Entry{ID: "t2", OwnerID: "bob", NoteID: "n1", Start: start, Open: true})

	data := f.backup(t, "bob")
	if bytes.Contains(data, []byte("eves-audio")) {
		t.Fatal("the backup holds someone else's file")
	}

	// Everything of bob's is lost; eve's stays.
	if err := f.s.clear(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.objects.Get(ctx, "recordings/bob/n1.flac", 0, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob's audio after clear: %v", err)
	}

	sum, err := f.s.Restore(ctx, acc, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Collections["recordings"] != 2 || sum.Objects != 2 || sum.Collections["timeEntries"] != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if got := f.noteIDs(t, "bob"); len(got) != 2 {
		t.Fatalf("notes: %v (the note that was still uploading is not part of a backup)", got)
	}
	n1, err := f.recs.Get(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if n1.Number != 7 || n1.Shares != nil || n1.Members != nil || n1.CreatedBy != "" || n1.AssigneeID != "" ||
		n1.FolderID != "" || n1.ParentID != "" || strings.Join(n1.Labels, ",") != "l1,task" {
		t.Fatalf("restored note: %+v", n1)
	}
	body, err := f.objects.Get(ctx, "recordings/bob/n1/attachments/a1.pdf", 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(body); string(b) != "attachment" {
		t.Fatalf("attachment: %q", b)
	}
	if _, err := f.recs.Get(ctx, "e1"); err != nil {
		t.Fatalf("eve's note: %v", err)
	}
	if _, err := f.objects.Get(ctx, "recordings/eve/e1.flac", 0, -1); err != nil {
		t.Fatalf("eve's audio: %v", err)
	}
	if _, err := f.times.Get(ctx, "t2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the running timer came back: %v", err)
	}
	// The next note gets a number after the restored ones.
	next := &recording.Recording{ID: "n4", OwnerID: "bob", DeviceID: "d", ClientID: "n4"}
	if err := f.recs.Create(ctx, next); err != nil || next.Number != 9 {
		t.Fatalf("next number %d (%v)", next.Number, err)
	}
}

// A backup made for one account can be restored into another (say, a recreated one): the
// notes become the new account's.
func TestPersonalRestoreIntoAnotherAccount(t *testing.T) {
	ctx := context.Background()
	f := newPersonalFixture(t)
	f.note(t, "old", "n1", func(r *recording.Recording) { r.DeviceID = recording.TextDeviceID("old") })
	data := f.backup(t, "old")
	if err := f.s.clear(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Restore(ctx, &Account{ID: "new"}, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	n, err := f.recs.Get(ctx, "n1")
	if err != nil || n.OwnerID != "new" || n.DeviceID != recording.TextDeviceID("new") {
		t.Fatalf("note: %+v (%v)", n, err)
	}
}

// craft builds a personal backup by hand.
func craft(t *testing.T, notes []*recording.Recording, objects map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("db/recordings.bson")
	for _, n := range notes {
		raw, err := bson.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(raw)
	}
	m := BackupManifest{Format: personalFormat, Version: personalVersion, CreatedAt: time.Now(), Owner: "bob",
		Collections: map[string]int{"recordings": len(notes)}, Objects: []BackupObject{}}
	for key, body := range objects {
		ow, _ := zw.Create("objects/" + key)
		_, _ = ow.Write([]byte(body))
		m.Objects = append(m.Objects, BackupObject{Key: key, Size: int64(len(body))})
	}
	mw, _ := zw.Create(manifestName)
	_ = json.NewEncoder(mw).Encode(m)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPersonalRestoreRejectsUnsafeBackups(t *testing.T) {
	ctx := context.Background()
	f := newPersonalFixture(t)
	acc := &Account{ID: "bob"}
	f.put(t, "recordings/eve/e1.flac", "eves-audio")
	f.note(t, "eve", "e1", nil)
	f.note(t, "bob", "keep", nil)

	note := func(id string, key string) *recording.Recording {
		return &recording.Recording{ID: id, OwnerID: "bob", DeviceID: "d", ClientID: id, Status: recording.StatusSummarized,
			Audio: &recording.Object{Key: key, Size: 1}}
	}
	cases := map[string][]byte{
		"a note referring to another note's file": craft(t, []*recording.Recording{note("b1", "recordings/eve/e1.flac")},
			map[string]string{"recordings/eve/e1.flac": "overwritten"}),
		"a file with no note":    craft(t, []*recording.Recording{note("b1", "recordings/bob/b1.flac")}, map[string]string{"recordings/bob/other.flac": "x"}),
		"a path out of its key":  craft(t, []*recording.Recording{note("b1", "recordings/bob/b1/../../eve/e1.flac")}, map[string]string{"recordings/bob/b1/../../eve/e1.flac": "x"}),
		"a note of someone else": craft(t, []*recording.Recording{{ID: "e1", OwnerID: "bob", DeviceID: "d", ClientID: "e1", Status: recording.StatusSummarized}}, nil),
		"a repeated note ID": craft(t, []*recording.Recording{
			{ID: "b1", DeviceID: "d", ClientID: "1"}, {ID: "b1", DeviceID: "d", ClientID: "2"}}, nil),
		"a note still uploading": craft(t, []*recording.Recording{{ID: "b1", DeviceID: "d", ClientID: "1", Status: recording.StatusUploading}}, nil),
	}
	for name, data := range cases {
		if _, err := f.s.Restore(ctx, acc, bytes.NewReader(data)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Nothing changed.
	body, err := f.objects.Get(ctx, "recordings/eve/e1.flac", 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(body); string(b) != "eves-audio" {
		t.Fatalf("eve's audio: %q", b)
	}
	if got := f.noteIDs(t, "bob"); len(got) != 1 || got[0] != "keep" {
		t.Fatalf("bob's notes after rejected restores: %v", got)
	}
}

func TestPersonalBackupNeedsAnAccountAndRunsAlone(t *testing.T) {
	ctx := context.Background()
	f := newPersonalFixture(t)
	if _, err := f.s.Backup(ctx, &Account{All: true}, io.Discard); !errors.Is(err, ErrForbidden) {
		t.Fatalf("backup without a user: %v", err)
	}
	end, ok := f.s.begin("bob", true)
	if !ok {
		t.Fatal("begin")
	}
	if _, err := f.s.Backup(ctx, &Account{ID: "bob"}, io.Discard); !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("backup during a restore: %v", err)
	}
	if _, err := f.s.Backup(ctx, &Account{ID: "eve"}, io.Discard); err != nil {
		t.Fatalf("another user's backup: %v", err)
	}
	end()
	if _, err := f.s.Backup(ctx, &Account{ID: "bob"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}
