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

// Archiver is the processing stage that moves received audio to object storage. WAV is
// transcoded to FLAC (the original WAV is kept too if configured); other formats, e.g. MP3
// or M4A fetched from Pocket, are stored unchanged.
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
	srcPath := a.spool.SourcePath(rec)
	if rec.Source != recording.SourceDevice && !isWAV(srcPath) {
		return a.storeAsIs(ctx, rec, srcPath)
	}

	wavPath, flacPath := srcPath, a.spool.FLACPath(rec.ID)
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

// storeAsIs uploads a fetched file in its original format.
func (a *Archiver) storeAsIs(ctx context.Context, rec *recording.Recording, path string) error {
	ctype := rec.SourceContentType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	obj, err := a.put(ctx, path, ObjectKey(rec, audio.ExtensionFor(ctype)), ctype)
	if err != nil {
		return err
	}
	now := a.clock().UTC()
	rec.Audio, rec.StoredAt = obj, &now
	a.log.Info("recording archived", "id", rec.ID, "source", string(rec.Source), "contentType", ctype, "bytes", obj.Size)
	return nil
}

// isWAV reports whether the file at path is a WAV file this service can transcode.
func isWAV(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	_, err = audio.ReadWAVInfo(f, st.Size())
	return err == nil
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
