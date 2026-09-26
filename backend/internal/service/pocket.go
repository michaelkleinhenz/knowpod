package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// PocketAPI is the part of the Pocket API client the service needs.
type PocketAPI interface {
	AudioURL(ctx context.Context, apiKey, recordingID string) (string, error)
	Download(ctx context.Context, link, dst string, maxBytes int64) (*pocket.Download, error)
}

// PocketService connects users' Pocket accounts: it keeps each user's webhook secret and API
// key, turns their webhooks into recordings, and fetches the audio.
type PocketService struct {
	recs    ports.RecordingRepository
	users   ports.UserRepository
	api     PocketAPI
	spool   *Spool
	maxSize int64
	log     *slog.Logger
	clock   func() time.Time
	// OnQueued is called when a new recording waits to be fetched, e.g. to wake the worker.
	OnQueued func()
}

// NewPocketService builds the service.
func NewPocketService(recs ports.RecordingRepository, users ports.UserRepository, api PocketAPI, spool *Spool, maxSize int64, log *slog.Logger) *PocketService {
	return &PocketService{recs: recs, users: users, api: api, spool: spool, maxSize: maxSize, log: log, clock: time.Now}
}

// --- per-user settings ---

// PocketView is a user's Pocket integration as shown in the UI (no secrets).
type PocketView struct {
	WebhookPath             string `json:"webhookPath"`
	WebhookSecretConfigured bool   `json:"webhookSecretConfigured"`
	APIKeyConfigured        bool   `json:"apiKeyConfigured"`
	APIKeyHint              string `json:"apiKeyHint,omitempty"`
}

// PocketUpdate changes a user's Pocket settings. Nil fields stay; empty strings remove.
type PocketUpdate struct {
	WebhookSecret *string `json:"webhookSecret,omitempty"`
	APIKey        *string `json:"apiKey,omitempty"`
}

// WebhookPath is the path of a user's Pocket webhook.
func WebhookPath(webhookID string) string { return "/api/v1/webhooks/pocket/" + webhookID }

// Settings returns the account's Pocket settings, creating its webhook ID on first use.
func (s *PocketService) Settings(ctx context.Context, acc *Account) (*PocketView, error) {
	u, err := s.withWebhookID(ctx, acc)
	if err != nil {
		return nil, err
	}
	return pocketView(u), nil
}

// UpdateSettings changes the account's Pocket settings.
func (s *PocketService) UpdateSettings(ctx context.Context, acc *Account, in PocketUpdate) (*PocketView, error) {
	u, err := s.withWebhookID(ctx, acc)
	if err != nil {
		return nil, err
	}
	if in.WebhookSecret != nil {
		u.Pocket.WebhookSecret = strings.TrimSpace(*in.WebhookSecret)
	}
	if in.APIKey != nil {
		u.Pocket.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return pocketView(u), nil
}

func (s *PocketService) withWebhookID(ctx context.Context, acc *Account) (*user.User, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("Pocket settings belong to a user; sign in"))
	}
	u, err := s.users.Get(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	if u.Pocket.WebhookID == "" {
		u.Pocket.WebhookID = newWebhookID()
		if err := s.users.Update(ctx, u); err != nil {
			return nil, err
		}
	}
	return u, nil
}

func pocketView(u *user.User) *PocketView {
	v := &PocketView{
		WebhookPath:             WebhookPath(u.Pocket.WebhookID),
		WebhookSecretConfigured: u.Pocket.WebhookSecret != "",
		APIKeyConfigured:        u.Pocket.APIKey != "",
	}
	if k := u.Pocket.APIKey; len(k) >= 8 {
		v.APIKeyHint = "…" + k[len(k)-4:]
	}
	return v
}

// --- webhook ---

// WebhookUser finds the user a webhook ID belongs to and returns their signing secret.
func (s *PocketService) WebhookUser(ctx context.Context, webhookID string) (*user.User, error) {
	return s.users.GetByPocketWebhookID(ctx, webhookID)
}

// WebhookResult says what a webhook delivery did.
type WebhookResult string

const (
	WebhookQueued    WebhookResult = "queued"    // new recording; its audio will be fetched
	WebhookDuplicate WebhookResult = "duplicate" // recording already known (redelivery or a later event)
	WebhookIgnored   WebhookResult = "ignored"   // event without a recording, or a deletion
)

// HandleWebhook records the recording named in a (verified) webhook event for its user.
// Every event that carries a recording counts, not only recording.created, so a recording
// is picked up even if its first event was missed. Deliveries are at-least-once; the Pocket
// recording ID makes them idempotent.
func (s *PocketService) HandleWebhook(ctx context.Context, owner *user.User, ev *pocket.Event) (WebhookResult, error) {
	if ev.Recording.ID == "" || ev.Event == "recording.deleted" {
		return WebhookIgnored, nil
	}
	deviceID := recording.PocketDeviceID(owner.ID)
	if _, err := s.recs.GetByClientID(ctx, deviceID, ev.Recording.ID); err == nil {
		return WebhookDuplicate, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}

	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: newID(), OwnerID: owner.ID, DeviceID: deviceID, ClientID: ev.Recording.ID,
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
	s.log.Info("pocket recording queued", "id", rec.ID, "owner", owner.ID, "pocketId", ev.Recording.ID, "event", ev.Event)
	if s.OnQueued != nil {
		s.OnQueued()
	}
	return WebhookQueued, nil
}

// Fetch is the processing stage remote → received: it downloads the recording's audio from
// Pocket into the spool, using the owner's API key.
func (s *PocketService) Fetch(ctx context.Context, rec *recording.Recording) error {
	owner, err := s.users.Get(ctx, rec.OwnerID)
	if err != nil {
		return fmt.Errorf("owner of recording: %w", err)
	}
	if owner.Pocket.APIKey == "" {
		return errors.New("the owner has no Pocket API key set (Account page)")
	}
	link, err := s.api.AudioURL(ctx, owner.Pocket.APIKey, rec.ClientID)
	if err != nil {
		return err
	}
	path := s.spool.DownloadPath(rec.ID)
	d, err := s.api.Download(ctx, link, path, s.maxSize)
	if err != nil {
		return err
	}
	s.received(rec, path, d.Size, d.SHA256, d.ContentType)
	s.log.Info("pocket audio fetched", "id", rec.ID, "pocketId", rec.ClientID, "contentType", d.ContentType, "bytes", d.Size)
	return nil
}

// received records a complete file in the spool on rec.
func (s *PocketService) received(rec *recording.Recording, path string, size int64, sha, contentType string) {
	now := s.clock().UTC()
	rec.Size, rec.SHA256, rec.SourceContentType = size, sha, contentType
	rec.ReceivedAt = &now
	if info, err := wavInfoAt(path); err == nil {
		rec.Format = info.Format()
	}
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
