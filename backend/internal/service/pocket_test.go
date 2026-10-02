package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func pocketEvent(event, id string) *pocket.Event {
	ev := &pocket.Event{Event: event}
	ev.Recording.ID = id
	ev.Recording.Title = "Team Standup"
	ev.Recording.RecordingAt = "2026-02-17T15:02:00.000Z"
	return ev
}

func newPocket(t *testing.T) (*PocketService, *memory.Recordings, *memory.Users) {
	t.Helper()
	recs, users := memory.NewRecordings(), memory.NewUsers()
	spool, _ := NewSpool(t.TempDir())
	return NewPocketService(recs, users, memory.NewFolders(), nil, spool, 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil))), recs, users
}

func TestPocketSettingsPerUser(t *testing.T) {
	ctx := context.Background()
	s, _, users := newPocket(t)
	_ = users.Create(ctx, &user.User{ID: "alice", Email: "alice@example.com"})
	_ = users.Create(ctx, &user.User{ID: "bob", Email: "bob@example.com"})
	alice, bob := &Account{ID: "alice"}, &Account{ID: "bob"}

	va, err := s.Settings(ctx, alice)
	if err != nil || va.WebhookSecretConfigured || va.APIKeyConfigured || len(va.WebhookPath) < 40 {
		t.Fatalf("alice: %+v, %v", va, err)
	}
	if again, _ := s.Settings(ctx, alice); again.WebhookPath != va.WebhookPath {
		t.Fatal("webhook ID changed between calls")
	}
	vb, _ := s.Settings(ctx, bob)
	if vb.WebhookPath == va.WebhookPath {
		t.Fatal("users share a webhook URL")
	}

	v, err := s.UpdateSettings(ctx, alice, PocketUpdate{WebhookSecret: ptr("whsec"), APIKey: ptr("pk_alice_12345678")})
	if err != nil || !v.WebhookSecretConfigured || !v.APIKeyConfigured || v.APIKeyHint != "…5678" {
		t.Fatalf("update: %+v, %v", v, err)
	}
	u, err := s.WebhookUser(ctx, va.WebhookPath[len("/api/v1/webhooks/pocket/"):])
	if err != nil || u.ID != "alice" || u.Pocket.WebhookSecret != "whsec" {
		t.Fatalf("webhook user: %+v, %v", u, err)
	}
	if _, err := s.Settings(ctx, &Account{All: true}); err == nil {
		t.Fatal("ADMIN_TOKEN without a user has no Pocket settings")
	}
}

func TestPocketWebhookQueuesOncePerUser(t *testing.T) {
	ctx := context.Background()
	s, recs, users := newPocket(t)
	woken := 0
	s.OnQueued = func() { woken++ }
	alice, bob := &user.User{ID: "alice"}, &user.User{ID: "bob"}
	_ = users.Create(ctx, &user.User{ID: "alice", Email: "alice@example.com"})
	_ = users.Create(ctx, &user.User{ID: "bob", Email: "bob@example.com"})

	if res, err := s.HandleWebhook(ctx, alice, pocketEvent("recording.created", "rec_1")); err != nil || res != WebhookQueued {
		t.Fatalf("first: %v, %v", res, err)
	}
	for _, e := range []string{"recording.created", "summary.completed"} {
		if res, err := s.HandleWebhook(ctx, alice, pocketEvent(e, "rec_1")); err != nil || res != WebhookDuplicate {
			t.Fatalf("%s: %v, %v", e, res, err)
		}
	}
	// The same Pocket recording for another user is that user's own copy.
	if res, err := s.HandleWebhook(ctx, bob, pocketEvent("recording.created", "rec_1")); err != nil || res != WebhookQueued {
		t.Fatalf("bob: %v, %v", res, err)
	}
	for _, ev := range []*pocket.Event{pocketEvent("recording.deleted", "rec_2"), pocketEvent("summary.completed", "")} {
		if res, _ := s.HandleWebhook(ctx, alice, ev); res != WebhookIgnored {
			t.Fatalf("%s/%q: %v", ev.Event, ev.Recording.ID, res)
		}
	}

	rec, err := recs.GetByClientID(ctx, recording.PocketDeviceID("alice"), "rec_1")
	if err != nil || rec.OwnerID != "alice" || rec.Status != recording.StatusRemote || rec.Source != recording.SourcePocket ||
		rec.Title != "Team Standup" || rec.RecordedAt == nil || woken != 2 {
		t.Fatalf("recording = %+v, %v, woken=%d", rec, err, woken)
	}
}

func TestPocketWebhookPutsNotesIntoPocketFolder(t *testing.T) {
	ctx := context.Background()
	s, recs, users := newPocket(t)
	_ = users.Create(ctx, &user.User{ID: "alice", Email: "alice@example.com"})
	alice := &user.User{ID: "alice"}

	for _, id := range []string{"rec_1", "rec_2"} {
		if res, err := s.HandleWebhook(ctx, alice, pocketEvent("recording.created", id)); err != nil || res != WebhookQueued {
			t.Fatalf("%s: %v, %v", id, res, err)
		}
	}
	list, _ := s.folders.List(ctx, "alice")
	if len(list) != 1 || list[0].Name != PocketFolder || list[0].ParentID != "" {
		t.Fatalf("folders = %+v", list)
	}
	stored, _ := users.Get(ctx, "alice")
	if stored.Pocket.FolderID != list[0].ID {
		t.Fatalf("remembered folder = %q, want %q", stored.Pocket.FolderID, list[0].ID)
	}
	for _, id := range []string{"rec_1", "rec_2"} {
		rec, err := recs.GetByClientID(ctx, recording.PocketDeviceID("alice"), id)
		if err != nil || rec.FolderID != list[0].ID {
			t.Fatalf("%s: %+v, %v", id, rec, err)
		}
	}

	// A deleted folder is made again for the next recording.
	_ = s.folders.Delete(ctx, list[0].ID)
	if _, err := s.HandleWebhook(ctx, stored, pocketEvent("recording.created", "rec_3")); err != nil {
		t.Fatal(err)
	}
	list, _ = s.folders.List(ctx, "alice")
	rec, _ := recs.GetByClientID(ctx, recording.PocketDeviceID("alice"), "rec_3")
	if len(list) != 1 || list[0].Name != PocketFolder || rec.FolderID != list[0].ID {
		t.Fatalf("after delete: folders = %+v, rec folder = %q", list, rec.FolderID)
	}
}

func TestDeviceFileTime(t *testing.T) {
	got, err := DeviceFileTime("20261002090356.mp3")
	if want := time.Date(2026, 10, 2, 9, 3, 56, 0, time.UTC); err != nil || !got.Equal(want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
	if _, err := DeviceFileTime("20261002090356.MP3"); err != nil {
		t.Fatalf("upper case extension: %v", err)
	}
	for _, name := range []string{"", "20261002090356", "20261002090356.wav", "2026100209035.mp3", "2026-10-02 0903.mp3", "20261332090356.mp3", "../20261002090356.mp3"} {
		if _, err := DeviceFileTime(name); err == nil {
			t.Errorf("%q: no error", name)
		}
	}
}

func TestPocketDeviceFilesImportedOnce(t *testing.T) {
	ctx := context.Background()
	s, recs, users := newPocket(t)
	s.Uploads = NewManualUploadService(recs, s.spool, 1<<20)
	_ = users.Create(ctx, &user.User{ID: "alice", Email: "alice@example.com"})
	alice := &Account{ID: "alice"}
	mp3 := func() io.Reader { return strings.NewReader("ID3\x04\x00\x00\x00\x00\x00\x00 audio") }

	// A recording announced by the webhook (started 2026-02-17 15:02:00 UTC) is on the device, too.
	if _, err := s.HandleWebhook(ctx, &user.User{ID: "alice"}, pocketEvent("recording.created", "rec_1")); err != nil {
		t.Fatal(err)
	}
	files := []string{"20261002090356.mp3", "notes.txt", "20260217150210.mp3", "20261002100000.mp3"}
	fresh, err := s.NewDeviceFiles(ctx, alice, files)
	if err != nil || len(fresh) != 2 || fresh[0] != "20261002090356.mp3" || fresh[1] != "20261002100000.mp3" {
		t.Fatalf("new files = %v, %v", fresh, err)
	}

	rec, err := s.ImportDeviceFile(ctx, alice, "20261002090356.mp3", mp3())
	if err != nil {
		t.Fatal(err)
	}
	folders, _ := s.folders.List(ctx, "alice")
	if len(folders) != 1 || rec.FolderID != folders[0].ID || rec.Source != recording.SourcePocket ||
		rec.DeviceID != recording.PocketDeviceID("alice") || rec.SourceContentType != "audio/mpeg" ||
		rec.RecordedAt == nil || !rec.RecordedAt.Equal(time.Date(2026, 10, 2, 9, 3, 56, 0, time.UTC)) {
		t.Fatalf("imported: %+v (folders %+v)", rec, folders)
	}
	if _, err := s.ImportDeviceFile(ctx, alice, "20261002090356.mp3", mp3()); !errors.Is(err, ErrDuplicateDeviceFile) {
		t.Fatalf("second import: %v", err)
	}
	if _, err := s.ImportDeviceFile(ctx, alice, "20260217150210.mp3", mp3()); !errors.Is(err, ErrDuplicateDeviceFile) {
		t.Fatalf("webhook recording imported: %v", err)
	}
	if _, err := s.ImportDeviceFile(ctx, alice, "20261002100000.mp3", strings.NewReader("%PDF-1.4")); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("PDF imported: %v", err)
	}

	// The webhook doesn't bring a copied recording again (started 2026-10-02 09:04:10 UTC).
	ev := pocketEvent("recording.created", "rec_2")
	ev.Recording.RecordingAt = "2026-10-02T09:04:10Z"
	if res, err := s.HandleWebhook(ctx, &user.User{ID: "alice"}, ev); err != nil || res != WebhookDuplicate {
		t.Fatalf("webhook for copied file: %v, %v", res, err)
	}

	// A note in the trash isn't copied again.
	trashed, _ := recs.Get(ctx, rec.ID)
	trashed.DeletedAt = &trashed.CreatedAt
	if err := recs.Update(ctx, trashed); err != nil {
		t.Fatal(err)
	}
	fresh, _ = s.NewDeviceFiles(ctx, alice, files)
	if len(fresh) != 1 || fresh[0] != "20261002100000.mp3" {
		t.Fatalf("after import: %v", fresh)
	}
	// Another user's notes don't count.
	_ = users.Create(ctx, &user.User{ID: "bob", Email: "bob@example.com"})
	if fresh, _ := s.NewDeviceFiles(ctx, &Account{ID: "bob"}, files); len(fresh) != 3 {
		t.Fatalf("bob: %v", fresh)
	}
}
