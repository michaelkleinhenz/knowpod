package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

var (
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// minWAVSize is the size of the smallest possible WAV header.
const minWAVSize = 44

// UploadService implements the resumable upload protocol. A device creates an upload with
// its own recording ID, the total size and the SHA-256 of the WAV file, then appends chunks
// at the current offset. When the last byte arrives the checksum and WAV header are verified
// and the recording is handed to background processing.
type UploadService struct {
	recs    ports.RecordingRepository
	spool   *Spool
	maxSize int64
	clock   func() time.Time
	// OnReceived is called after a recording has been completely received, e.g. to wake the
	// background worker. Optional.
	OnReceived func()
	// Striped locks serialise appends per recording without an unbounded lock map.
	locks [64]sync.Mutex
}

// NewUploadService builds the service. maxSize limits the declared size of an upload.
func NewUploadService(recs ports.RecordingRepository, spool *Spool, maxSize int64) *UploadService {
	return &UploadService{recs: recs, spool: spool, maxSize: maxSize, clock: time.Now}
}

// Upload is the state of an upload as seen by the device.
type Upload struct {
	Recording *recording.Recording
	// Offset is the number of bytes received; the next chunk must start here.
	Offset int64
}

// CreateUploadInput describes a new upload.
type CreateUploadInput struct {
	RecordingID string     // device-assigned, unique per device
	Size        int64      // total WAV size in bytes
	SHA256      string     // hex SHA-256 of the complete WAV file
	RecordedAt  *time.Time // optional
	// Highlights marked while recording; optional. Sent again with a repeated create, they
	// replace the stored ones.
	Highlights []HighlightInput
}

// Create starts an upload, or returns the existing one when the device already created an
// upload with the same recording ID (so a retried create is harmless). created reports
// whether a new upload was started.
func (s *UploadService) Create(ctx context.Context, dev *device.Device, in CreateUploadInput) (up *Upload, created bool, err error) {
	in.SHA256 = strings.ToLower(strings.TrimSpace(in.SHA256))
	switch {
	case !clientIDPattern.MatchString(in.RecordingID):
		return nil, false, invalid("recordingId must be 1-128 characters of [A-Za-z0-9._-]")
	case in.Size < minWAVSize:
		return nil, false, invalid("size must be at least %d bytes", minWAVSize)
	case in.Size > s.maxSize:
		return nil, false, fmt.Errorf("%w: maximum is %d bytes", ErrTooLarge, s.maxSize)
	case !sha256Pattern.MatchString(in.SHA256):
		return nil, false, invalid("sha256 must be 64 hex characters")
	}
	highlights, err := normalizeHighlights(in.Highlights, in.RecordedAt)
	if err != nil {
		return nil, false, err
	}

	if existing, err := s.recs.GetByClientID(ctx, dev.ID, in.RecordingID); err == nil {
		up, err := s.existing(existing, in)
		if err == nil && len(in.Highlights) > 0 {
			existing.Highlights = highlights
			existing.UpdatedAt = s.clock().UTC()
			err = ports.SaveProcessed(ctx, s.recs, existing)
		}
		return up, false, err
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: newID(), OwnerID: dev.OwnerID, DeviceID: dev.ID, ClientID: in.RecordingID, Status: recording.StatusUploading,
		Size: in.Size, SHA256: in.SHA256, RecordedAt: in.RecordedAt, Highlights: highlights,
		NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.recs.Create(ctx, rec); err != nil {
		if errors.Is(err, errDuplicate) {
			// Lost a race against a concurrent create of the same recording.
			existing, gerr := s.recs.GetByClientID(ctx, dev.ID, in.RecordingID)
			if gerr != nil {
				return nil, false, gerr
			}
			up, err := s.existing(existing, in)
			return up, false, err
		}
		return nil, false, err
	}
	return &Upload{Recording: rec}, true, nil
}

func (s *UploadService) existing(rec *recording.Recording, in CreateUploadInput) (*Upload, error) {
	if rec.Size != in.Size || rec.SHA256 != in.SHA256 {
		return nil, ErrConflict
	}
	return s.state(rec)
}

// SetHighlights replaces the highlights of one of the device's recordings, e.g. when the
// device sends them after the upload. It can be called in any state; a summary that
// already exists is not regenerated automatically.
func (s *UploadService) SetHighlights(ctx context.Context, dev *device.Device, id string, in []HighlightInput) (*recording.Recording, error) {
	rec, err := s.load(ctx, dev, id)
	if err != nil {
		return nil, err
	}
	highlights, err := normalizeHighlights(in, rec.RecordedAt)
	if err != nil {
		return nil, err
	}
	rec.Highlights = highlights
	rec.UpdatedAt = s.clock().UTC()
	if err := ports.SaveProcessed(ctx, s.recs, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// Get returns the state of one of the device's uploads.
func (s *UploadService) Get(ctx context.Context, dev *device.Device, id string) (*Upload, error) {
	rec, err := s.load(ctx, dev, id)
	if err != nil {
		return nil, err
	}
	return s.state(rec)
}

// Append writes a chunk that starts at offset. Uploads that are already complete accept the
// call as a no-op, so a device that retries the final chunk gets the final state.
func (s *UploadService) Append(ctx context.Context, dev *device.Device, id string, offset int64, body io.Reader) (*Upload, error) {
	lock := s.lock(id)
	lock.Lock()
	defer lock.Unlock()

	rec, err := s.load(ctx, dev, id)
	if err != nil {
		return nil, err
	}
	if rec.Status != recording.StatusUploading {
		return s.state(rec)
	}
	cur, err := s.spool.Size(rec.ID)
	if err != nil {
		return nil, err
	}
	if offset != cur {
		return nil, &OffsetMismatchError{Current: cur}
	}

	n, werr := s.spool.Append(rec.ID, body, rec.Size-cur)
	rec.UpdatedAt = s.clock().UTC()
	if werr != nil {
		// Keep what arrived; the device resumes from the new offset.
		_ = ports.SaveProcessed(ctx, s.recs, rec)
		return nil, fmt.Errorf("receiving chunk: %w", werr)
	}
	if cur+n < rec.Size {
		if err := ports.SaveProcessed(ctx, s.recs, rec); err != nil {
			return nil, err
		}
		return &Upload{Recording: rec, Offset: cur + n}, nil
	}
	return s.finish(ctx, rec)
}

// finish verifies a completely received file and hands it to background processing.
func (s *UploadService) finish(ctx context.Context, rec *recording.Recording) (*Upload, error) {
	sum, err := s.spool.SHA256(rec.ID)
	if err != nil {
		return nil, err
	}
	if sum != rec.SHA256 {
		// Corrupted in transit: discard and let the device resend.
		if err := s.spool.Remove(rec.ID); err != nil {
			return nil, err
		}
		if err := ports.SaveProcessed(ctx, s.recs, rec); err != nil {
			return nil, err
		}
		return nil, ErrChecksumMismatch
	}

	info, err := wavInfoAt(s.spool.WAVPath(rec.ID))
	now := s.clock().UTC()
	rec.UpdatedAt = now
	if err != nil {
		// Intact but unusable: resending the same bytes won't help, so reject for good.
		rec.Status = recording.StatusFailed
		rec.LastError = err.Error()
		if rerr := s.spool.Remove(rec.ID); rerr != nil {
			return nil, rerr
		}
		if uerr := ports.SaveProcessed(ctx, s.recs, rec); uerr != nil {
			return nil, uerr
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidAudio, err)
	}

	rec.Status = recording.StatusReceived
	rec.Format = info.Format()
	rec.ReceivedAt = &now
	rec.NotBefore = now
	rec.Attempts = 0
	if err := ports.SaveProcessed(ctx, s.recs, rec); err != nil {
		return nil, err
	}
	if s.OnReceived != nil {
		s.OnReceived()
	}
	return &Upload{Recording: rec, Offset: rec.Size}, nil
}

// PurgeStale deletes uploads that have not progressed within ttl, including their spooled
// bytes. It returns the number of uploads removed.
func (s *UploadService) PurgeStale(ctx context.Context, ttl time.Duration) (int, error) {
	stale, err := s.recs.ListStale(ctx, recording.StatusUploading, s.clock().Add(-ttl), 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, rec := range stale {
		lock := s.lock(rec.ID)
		lock.Lock()
		err := s.spool.Remove(rec.ID)
		if err == nil {
			err = s.recs.Delete(ctx, rec.ID)
		}
		lock.Unlock()
		if err != nil && !errors.Is(err, ErrNotFound) {
			return n, err
		}
		n++
	}
	return n, nil
}

// load fetches an upload and hides other devices' uploads.
func (s *UploadService) load(ctx context.Context, dev *device.Device, id string) (*recording.Recording, error) {
	rec, err := s.recs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.DeviceID != dev.ID {
		return nil, ErrNotFound
	}
	return rec, nil
}

func (s *UploadService) state(rec *recording.Recording) (*Upload, error) {
	if rec.Status != recording.StatusUploading {
		return &Upload{Recording: rec, Offset: rec.Size}, nil
	}
	off, err := s.spool.Size(rec.ID)
	if err != nil {
		return nil, err
	}
	return &Upload{Recording: rec, Offset: off}, nil
}

func (s *UploadService) lock(id string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return &s.locks[h.Sum32()%uint32(len(s.locks))]
}
