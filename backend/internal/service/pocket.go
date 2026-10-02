package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// ErrDuplicateDeviceFile is returned for a recorder's file that is already a note.
var ErrDuplicateDeviceFile = errors.New("this recording is already a note")

// PocketFolder is the name of the knowpod folder that imported recordings are put into.
const PocketFolder = "Pocket AI"

// PocketAPI is the part of the Pocket API client the service needs.
type PocketAPI interface {
	AudioURL(ctx context.Context, apiKey, recordingID string) (string, error)
	Download(ctx context.Context, link, dst string, maxBytes int64) (*pocket.Download, error)
}

// PocketService connects users' Pocket accounts: it keeps each user's webhook secret and API
// key, turns their webhooks into recordings (in the folder "Pocket AI"), and fetches the audio.
type PocketService struct {
	recs    ports.RecordingRepository
	users   ports.UserRepository
	folders ports.FolderRepository
	api     PocketAPI
	spool   *Spool
	maxSize int64
	log     *slog.Logger
	clock   func() time.Time
	// OnQueued is called when a new recording waits to be fetched, e.g. to wake the worker.
	OnQueued func()
	// Uploads stores the files copied from a Pocket recorder (see ImportDeviceFile).
	Uploads *ManualUploadService
}

// NewPocketService builds the service.
func NewPocketService(recs ports.RecordingRepository, users ports.UserRepository, folders ports.FolderRepository, api PocketAPI, spool *Spool, maxSize int64, log *slog.Logger) *PocketService {
	return &PocketService{recs: recs, users: users, folders: folders, api: api, spool: spool, maxSize: maxSize, log: log, clock: time.Now}
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
	var recordedAt *time.Time
	if t, err := time.Parse(time.RFC3339, ev.Recording.RecordingAt); err == nil {
		t = t.UTC()
		recordedAt = &t
		// Copied from the recorder already (desktop app)?
		idx, err := s.deviceIndex(ctx, owner.ID)
		if err != nil {
			return "", err
		}
		if near(idx.copied, t) {
			return WebhookDuplicate, nil
		}
	}

	folderID, err := s.folder(ctx, owner)
	if err != nil {
		return "", err
	}
	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: newID(), OwnerID: owner.ID, DeviceID: deviceID, ClientID: ev.Recording.ID,
		Source: recording.SourcePocket, Title: ev.Recording.Title, Status: recording.StatusRemote,
		FolderID: folderID, NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	// A shared Pocket AI folder shares the note, too.
	members, err := folderMembers(ctx, s.folders, owner.ID, folderID)
	if err != nil {
		return "", err
	}
	rec.Members = recording.ComputeMembers(members, nil, nil)
	rec.RecordedAt = recordedAt
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

// folder returns the ID of the user's knowpod folder for imported recordings, creating it at
// the top level when it's missing. The folder is remembered on the user, so it can be renamed
// or moved; when it's deleted, a new one is made for the next new recording.
func (s *PocketService) folder(ctx context.Context, u *user.User) (string, error) {
	if id := u.Pocket.FolderID; id != "" {
		f, err := s.folders.Get(ctx, id)
		if err == nil && f.OwnerID == u.ID {
			return id, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
	}
	list, err := s.folders.List(ctx, u.ID)
	if err != nil {
		return "", err
	}
	id := ""
	for _, f := range list {
		if f.ParentID == "" && strings.EqualFold(f.Name, PocketFolder) {
			id = f.ID
			break
		}
	}
	if id == "" {
		now := s.clock().UTC()
		f := &folder.Folder{ID: newID(), OwnerID: u.ID, Name: PocketFolder, CreatedAt: now, UpdatedAt: now}
		if err := s.folders.Create(ctx, f); err != nil {
			return "", err
		}
		id = f.ID
	}
	fresh, err := s.users.Get(ctx, u.ID)
	if err != nil {
		return "", err
	}
	fresh.Pocket.FolderID = id
	u.Pocket.FolderID = id
	return id, s.users.Update(ctx, fresh)
}

// --- files copied from the Pocket recorder (desktop app) ---

// A Pocket recorder plugged in by USB is a drive with its recordings as MP3 files in
// RECORD/<date>/, named by when they started in UTC ("20261002090356.mp3"). The desktop app
// asks which of them are new (NewDeviceFiles) and uploads those (ImportDeviceFile). They
// become the user's Pocket recordings like the ones announced by webhook, in the same
// folder. A file is known by its name (ClientID "file:<name>"), or when a recording from
// the webhook started at about the same time: the device and the Pocket cloud have the same
// recording then.

// deviceFileLayout is the time in a recorder's file names.
const deviceFileLayout = "20060102150405"

// deviceMatchWindow is how far apart the start of a file and of a webhook recording may be
// to be the same recording.
const deviceMatchWindow = 30 * time.Second

// DeviceFileTime returns when a recorder's file started, from its name.
func DeviceFileTime(name string) (time.Time, error) {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	t, err := time.Parse(deviceFileLayout, base)
	if !strings.EqualFold(ext, ".mp3") || len(base) != len(deviceFileLayout) || strings.Trim(base, "0123456789") != "" || err != nil {
		return time.Time{}, invalid("%q is not a Pocket recording (YYYYMMDDhhmmss.mp3)", name)
	}
	return t.UTC(), nil
}

// deviceTitle is the title of a copied recording until the summary gives it one.
const deviceTitle = "Pocket recording"

// deviceClientID is the ClientID of a recording copied from the file that started at t.
func deviceClientID(t time.Time) string { return "file:" + t.Format(deviceFileLayout) }

// deviceIndex is what is known of a user's Pocket recordings: the files copied, and when the
// webhook ones started.
type deviceIndex struct {
	files  map[string]bool
	starts []time.Time // of the webhook recordings
	copied []time.Time // of the copied files
}

func (s *PocketService) deviceIndex(ctx context.Context, ownerID string) (*deviceIndex, error) {
	// Notes in the trash count, too: a deleted note isn't copied again.
	list, err := s.recs.List(ctx, recording.ListFilter{OwnerID: ownerID, DeviceID: recording.PocketDeviceID(ownerID), Trash: recording.TrashAny, Brief: true})
	if err != nil {
		return nil, err
	}
	idx := &deviceIndex{files: map[string]bool{}}
	for _, r := range list {
		switch {
		case strings.HasPrefix(r.ClientID, "file:"):
			idx.files[r.ClientID] = true
			if r.RecordedAt != nil {
				idx.copied = append(idx.copied, *r.RecordedAt)
			}
		case r.RecordedAt != nil:
			idx.starts = append(idx.starts, *r.RecordedAt)
		}
	}
	return idx, nil
}

// has says whether the file that started at t is a note.
func (idx *deviceIndex) has(t time.Time) bool {
	return idx.files[deviceClientID(t)] || near(idx.starts, t)
}

// near says whether one of the times is within deviceMatchWindow of t.
func near(times []time.Time, t time.Time) bool {
	for _, start := range times {
		if d := start.Sub(t); d <= deviceMatchWindow && d >= -deviceMatchWindow {
			return true
		}
	}
	return false
}

// NewDeviceFiles returns the names of the recorder's files that aren't the account's notes
// yet, in the order given. Names that aren't recordings are left out.
func (s *PocketService) NewDeviceFiles(ctx context.Context, acc *Account, names []string) ([]string, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("Pocket recordings belong to a user; sign in"))
	}
	idx, err := s.deviceIndex(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	fresh := []string{}
	for _, name := range names {
		if t, err := DeviceFileTime(name); err == nil && !idx.has(t) {
			fresh = append(fresh, name)
		}
	}
	return fresh, nil
}

// ImportDeviceFile stores a recorder's MP3 file as a note in the Pocket AI folder, unless it
// is already one: then it returns the error ErrDuplicateDeviceFile.
func (s *PocketService) ImportDeviceFile(ctx context.Context, acc *Account, name string, body io.Reader) (*recording.Recording, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("Pocket recordings belong to a user; sign in"))
	}
	if s.Uploads == nil {
		return nil, errors.New("uploads are not set up")
	}
	started, err := DeviceFileTime(name)
	if err != nil {
		return nil, err
	}
	idx, err := s.deviceIndex(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	if idx.has(started) {
		return nil, ErrDuplicateDeviceFile
	}
	owner, err := s.users.Get(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	folderID, err := s.folder(ctx, owner)
	if err != nil {
		return nil, err
	}
	// A shared Pocket AI folder shares the note, too.
	members, err := folderMembers(ctx, s.folders, owner.ID, folderID)
	if err != nil {
		return nil, err
	}
	rec, err := s.Uploads.Upload(ctx, acc, ManualUpload{
		Filename: name, RecordedAt: &started, Body: body, AudioOnly: true,
		Prepare: func(rec *recording.Recording) {
			rec.DeviceID, rec.ClientID = recording.PocketDeviceID(owner.ID), deviceClientID(started)
			rec.Source, rec.Title, rec.FolderID = recording.SourcePocket, deviceTitle, folderID
			rec.Members = recording.ComputeMembers(members, nil, nil)
		},
	})
	if errors.Is(err, errDuplicate) {
		return nil, ErrDuplicateDeviceFile // copied at the same time by another request
	}
	if err != nil {
		return nil, err
	}
	s.log.Info("pocket file imported", "id", rec.ID, "owner", owner.ID, "file", name, "bytes", rec.Size)
	return rec, nil
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
