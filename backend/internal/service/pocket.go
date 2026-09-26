package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// PocketAPI is the part of the Pocket API client the service needs.
type PocketAPI interface {
	AudioURL(ctx context.Context, recordingID string) (string, error)
	Download(ctx context.Context, link, dst string, maxBytes int64) (*pocket.Download, error)
}

// PocketService turns Pocket webhooks into recordings and fetches their audio.
type PocketService struct {
	recs    ports.RecordingRepository
	api     PocketAPI
	spool   *Spool
	maxSize int64
	log     *slog.Logger
	clock   func() time.Time
	// OnQueued is called when a new recording waits to be fetched, e.g. to wake the worker.
	OnQueued func()
}

// NewPocketService builds the service.
func NewPocketService(recs ports.RecordingRepository, api PocketAPI, spool *Spool, maxSize int64, log *slog.Logger) *PocketService {
	return &PocketService{recs: recs, api: api, spool: spool, maxSize: maxSize, log: log, clock: time.Now}
}

// WebhookResult says what a webhook delivery did.
type WebhookResult string

const (
	WebhookQueued    WebhookResult = "queued"    // new recording; its audio will be fetched
	WebhookDuplicate WebhookResult = "duplicate" // recording already known (redelivery or a later event)
	WebhookIgnored   WebhookResult = "ignored"   // event without a recording, or a deletion
)

// HandleWebhook records the recording named in a (verified) webhook event. Every event that
// carries a recording counts, not only recording.created, so a recording is picked up even
// if its first event was missed. Deliveries are at-least-once; the Pocket recording ID makes
// them idempotent.
func (s *PocketService) HandleWebhook(ctx context.Context, ev *pocket.Event) (WebhookResult, error) {
	if ev.Recording.ID == "" || ev.Event == "recording.deleted" {
		return WebhookIgnored, nil
	}
	if _, err := s.recs.GetByClientID(ctx, recording.PocketDeviceID, ev.Recording.ID); err == nil {
		return WebhookDuplicate, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}

	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: newID(), DeviceID: recording.PocketDeviceID, ClientID: ev.Recording.ID,
		Source: recording.SourcePocket, Title: ev.Recording.Title, Status: recording.StatusRemote,
		NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	if t, err := time.Parse(time.RFC3339, ev.Recording.RecordingAt); err == nil {
		t = t.UTC()
		rec.RecordedAt = &t
	}
	if err := s.recs.Create(ctx, rec); err != nil {
		if errors.Is(err, errDuplicate) {
			return WebhookDuplicate, nil // concurrent delivery of the same recording
		}
		return "", err
	}
	s.log.Info("pocket recording queued", "id", rec.ID, "pocketId", ev.Recording.ID, "event", ev.Event)
	if s.OnQueued != nil {
		s.OnQueued()
	}
	return WebhookQueued, nil
}

// Fetch is the processing stage remote → received: it downloads the recording's audio from
// Pocket into the spool.
func (s *PocketService) Fetch(ctx context.Context, rec *recording.Recording) error {
	link, err := s.api.AudioURL(ctx, rec.ClientID)
	if err != nil {
		return err
	}
	path := s.spool.DownloadPath(rec.ID)
	d, err := s.api.Download(ctx, link, path, s.maxSize)
	if err != nil {
		return err
	}

	now := s.clock().UTC()
	rec.Size, rec.SHA256, rec.SourceContentType = d.Size, d.SHA256, d.ContentType
	rec.ReceivedAt = &now
	if info, err := wavInfoAt(path); err == nil {
		rec.Format = info.Format()
	}
	s.log.Info("pocket audio fetched", "id", rec.ID, "pocketId", rec.ClientID, "contentType", d.ContentType, "bytes", d.Size)
	return nil
}

func wavInfoAt(path string) (*audio.WAVInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	info, err := audio.ReadWAVInfo(f, st.Size())
	if err != nil {
		return nil, fmt.Errorf("not WAV: %w", err)
	}
	return info, nil
}
