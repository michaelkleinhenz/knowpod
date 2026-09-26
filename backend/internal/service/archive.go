package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// Archiver is the first processing stage: it transcodes a received WAV to FLAC and stores
// it (and optionally the original WAV) in object storage.
type Archiver struct {
	spool        *Spool
	store        ports.ObjectStore
	keepOriginal bool
	log          *slog.Logger
	clock        func() time.Time
}

// NewArchiver builds the stage.
func NewArchiver(spool *Spool, store ports.ObjectStore, keepOriginal bool, log *slog.Logger) *Archiver {
	return &Archiver{spool: spool, store: store, keepOriginal: keepOriginal, log: log, clock: time.Now}
}

// ObjectKey returns the storage key for a recording's file with the given extension.
func ObjectKey(rec *recording.Recording, ext string) string {
	return fmt.Sprintf("recordings/%s/%s.%s", rec.DeviceID, rec.ID, ext)
}

// Run archives the recording and records the stored objects on rec. The spooled files are
// left in place; Cleanup removes them once the new state is persisted.
func (a *Archiver) Run(ctx context.Context, rec *recording.Recording) error {
	wavPath, flacPath := a.spool.WAVPath(rec.ID), a.spool.FLACPath(rec.ID)
	start := a.clock()
	if _, err := audio.EncodeFLAC(ctx, wavPath, flacPath); err != nil {
		return fmt.Errorf("transcode to FLAC: %w", err)
	}

	flacObj, err := a.put(ctx, flacPath, ObjectKey(rec, "flac"), "audio/flac")
	if err != nil {
		return err
	}
	var wavObj *recording.Object
	if a.keepOriginal {
		if wavObj, err = a.put(ctx, wavPath, ObjectKey(rec, "wav"), "audio/wav"); err != nil {
			return err
		}
	}

	now := a.clock().UTC()
	rec.Audio, rec.Original, rec.StoredAt = flacObj, wavObj, &now
	a.log.Info("recording archived", "id", rec.ID, "wavBytes", rec.Size, "flacBytes", flacObj.Size,
		"took", now.Sub(start).String())
	return nil
}

// Cleanup removes the spooled files after the archived state has been persisted.
func (a *Archiver) Cleanup(rec *recording.Recording) {
	if err := a.spool.Remove(rec.ID); err != nil {
		a.log.Warn("removing spooled files failed", "id", rec.ID, "err", err)
	}
}

func (a *Archiver) put(ctx context.Context, path, key, contentType string) (*recording.Object, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := a.store.Put(ctx, key, f, st.Size(), contentType); err != nil {
		return nil, fmt.Errorf("store %s: %w", key, err)
	}
	return &recording.Object{Key: key, ContentType: contentType, Size: st.Size()}, nil
}
