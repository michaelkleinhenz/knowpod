package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

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
	return NewPocketService(recs, users, nil, spool, 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil))), recs, users
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
	s, recs, _ := newPocket(t)
	woken := 0
	s.OnQueued = func() { woken++ }
	alice, bob := &user.User{ID: "alice"}, &user.User{ID: "bob"}

	if res, err := s.HandleWebhook(ctx, alice, pocketEvent("recording.created", "rec_1")); err != nil || res != WebhookQueued {
		t.Fatalf("first: %v, %v", res, err)
	}
	for _, e := range []string{"recording.created", "summary.completed"} {
		if res, err := s.HandleWebhook(ctx, alice, pocketEvent(e, "rec_1")); err != nil || res != WebhookDuplicate {
			t.Fatalf("%s: %v, %v", e, res, err)
		}
	}
	// The same Pocket recording for another user is that user's own copy.
	if res, _ := s.HandleWebhook(ctx, bob, pocketEvent("recording.created", "rec_1")); res != WebhookQueued {
		t.Fatalf("bob: %v", res)
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
