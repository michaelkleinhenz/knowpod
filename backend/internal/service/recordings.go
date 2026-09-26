package service

import (
	"context"
	"errors"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// ErrNotReady is returned for actions that need a processing step that hasn't happened.
var ErrNotReady = errors.New("recording is not ready for this")

// RecordingService implements the user actions on recordings: delete, re-transcribe and
// re-summarize.
type RecordingService struct {
	recs    ports.RecordingRepository
	objects ports.ObjectStore
	spool   *Spool
	clock   func() time.Time
	// OnRequeued is called when a recording was sent back into processing. Optional.
	OnRequeued func()
}

// NewRecordingService builds the service.
func NewRecordingService(recs ports.RecordingRepository, objects ports.ObjectStore, spool *Spool) *RecordingService {
	return &RecordingService{recs: recs, objects: objects, spool: spool, clock: time.Now}
}

// Get returns a recording the account may see.
func (s *RecordingService) Get(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, err := s.recs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(rec.OwnerID) {
		return nil, ErrNotFound
	}
	return rec, nil
}

// List returns the account's recordings (everyone's for ADMIN_TOKEN).
func (s *RecordingService) List(ctx context.Context, acc *Account, f recording.ListFilter) ([]*recording.Recording, error) {
	f.OwnerID = acc.OwnerFilter()
	return s.recs.List(ctx, f)
}

// Delete removes a recording, its archived audio and any spooled files.
func (s *RecordingService) Delete(ctx context.Context, acc *Account, id string) error {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return err
	}
	return s.delete(ctx, rec)
}

func (s *RecordingService) delete(ctx context.Context, rec *recording.Recording) error {
	for _, obj := range []*recording.Object{rec.Audio, rec.Original} {
		if obj != nil {
			if err := s.objects.Delete(ctx, obj.Key); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
	if err := s.spool.Remove(rec.ID); err != nil {
		return err
	}
	return s.recs.Delete(ctx, rec.ID)
}

// Retranscribe discards the transcript and summary and queues the recording for
// transcription again.
func (s *RecordingService) Retranscribe(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if rec.Audio == nil {
		return nil, errors.Join(ErrNotReady, errors.New("the audio has not been archived yet"))
	}
	rec.Transcript, rec.Summary = nil, nil
	return s.requeue(ctx, rec, recording.StatusStored)
}

// Resummarize discards the summary and queues the recording for summarizing again.
func (s *RecordingService) Resummarize(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if rec.Transcript == nil {
		return nil, errors.Join(ErrNotReady, errors.New("the recording has no transcript yet"))
	}
	rec.Summary = nil
	return s.requeue(ctx, rec, recording.StatusTranscribed)
}

func (s *RecordingService) requeue(ctx context.Context, rec *recording.Recording, status recording.Status) (*recording.Recording, error) {
	now := s.clock().UTC()
	rec.Status, rec.Attempts, rec.LastError, rec.NotBefore, rec.UpdatedAt = status, 0, "", now, now
	if err := s.recs.Update(ctx, rec); err != nil {
		return nil, err
	}
	if s.OnRequeued != nil {
		s.OnRequeued()
	}
	return rec, nil
}
