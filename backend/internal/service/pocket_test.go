package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
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

func TestPocketWebhookQueuesOnce(t *testing.T) {
	ctx := context.Background()
	recs := memory.NewRecordings()
	spool, _ := NewSpool(t.TempDir())
	s := NewPocketService(recs, nil, spool, 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	woken := 0
	s.OnQueued = func() { woken++ }

	if res, err := s.HandleWebhook(ctx, pocketEvent("recording.created", "rec_1")); err != nil || res != WebhookQueued {
		t.Fatalf("first: %v, %v", res, err)
	}
	// Redelivery and later events for the same recording don't queue it again.
	for _, e := range []string{"recording.created", "summary.completed"} {
		if res, err := s.HandleWebhook(ctx, pocketEvent(e, "rec_1")); err != nil || res != WebhookDuplicate {
			t.Fatalf("%s: %v, %v", e, res, err)
		}
	}
	for _, ev := range []*pocket.Event{pocketEvent("recording.deleted", "rec_2"), pocketEvent("summary.completed", "")} {
		if res, _ := s.HandleWebhook(ctx, ev); res != WebhookIgnored {
			t.Fatalf("%s/%q: %v", ev.Event, ev.Recording.ID, res)
		}
	}

	rec, err := recs.GetByClientID(ctx, recording.PocketDeviceID, "rec_1")
	if err != nil || rec.Status != recording.StatusRemote || rec.Source != recording.SourcePocket ||
		rec.Title != "Team Standup" || rec.RecordedAt == nil || woken != 1 {
		t.Fatalf("recording = %+v, %v, woken=%d", rec, err, woken)
	}
}
