package worker_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mewkiz/flac"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/worker"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixture struct {
	recs    *memory.Recordings
	spool   *service.Spool
	objects *memstore.Store
	uploads *service.UploadService
	worker  *worker.Worker
}

func newFixture(t *testing.T, keepOriginal bool) *fixture {
	t.Helper()
	f := &fixture{recs: memory.NewRecordings(), objects: memstore.New()}
	var err error
	if f.spool, err = service.NewSpool(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	f.uploads = service.NewUploadService(f.recs, f.spool, 1<<30)
	archiver := service.NewArchiver(f.spool, f.objects, keepOriginal, quiet)
	f.worker = worker.New(f.recs, []worker.Stage{{
		Name: "archive", From: recording.StatusReceived, To: recording.StatusStored,
		Run: archiver.Run, Cleanup: archiver.Cleanup,
	}}, worker.Options{MaxAttempts: 2, BaseBackoff: time.Nanosecond}, quiet)
	return f
}

// receive uploads a WAV completely and returns the recording ID.
func (f *fixture) receive(t *testing.T, wav []byte) string {
	t.Helper()
	ctx := context.Background()
	s := sha256.Sum256(wav)
	dev := &device.Device{ID: "dev-1"}
	up, _, err := f.uploads.Create(ctx, dev, service.CreateUploadInput{RecordingID: "r1", Size: int64(len(wav)), SHA256: hex.EncodeToString(s[:])})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uploads.Append(ctx, dev, up.Recording.ID, 0, bytes.NewReader(wav)); err != nil {
		t.Fatal(err)
	}
	return up.Recording.ID
}

func TestArchiveStage(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, true)
	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 32000, 16))
	id := f.receive(t, wav)

	if n := f.worker.RunOnce(ctx); n != 1 {
		t.Fatalf("processed %d, want 1", n)
	}
	rec, _ := f.recs.Get(ctx, id)
	if rec.Status != recording.StatusStored || rec.Audio == nil || rec.Original == nil || rec.StoredAt == nil || rec.Attempts != 0 {
		t.Fatalf("recording = %+v", rec)
	}
	if rec.Audio.Key != "recordings/dev-1/"+id+".flac" || rec.Audio.ContentType != "audio/flac" {
		t.Fatalf("audio = %+v", rec.Audio)
	}

	obj, ok := f.objects.Object(rec.Audio.Key)
	if !ok || int64(len(obj.Data)) != rec.Audio.Size || len(obj.Data) >= len(wav) {
		t.Fatalf("stored FLAC: ok=%v size=%d wav=%d", ok, len(obj.Data), len(wav))
	}
	path := filepath.Join(t.TempDir(), "x.flac")
	_ = os.WriteFile(path, obj.Data, 0o600)
	stream, err := flac.ParseFile(path)
	if err != nil {
		t.Fatalf("stored FLAC does not parse: %v", err)
	}
	stream.Close()
	if stream.Info.NSamples != 32000 {
		t.Fatalf("FLAC samples = %d", stream.Info.NSamples)
	}
	if orig, ok := f.objects.Object(rec.Original.Key); !ok || !bytes.Equal(orig.Data, wav) {
		t.Fatal("original WAV not archived unchanged")
	}
	if _, err := os.Stat(f.spool.WAVPath(id)); !os.IsNotExist(err) {
		t.Fatal("spooled WAV not cleaned up")
	}
	if n := f.worker.RunOnce(ctx); n != 0 {
		t.Fatalf("second run processed %d", n)
	}
}

func TestArchiveRetriesThenFails(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	f.objects.Err = errors.New("s3 unavailable")
	id := f.receive(t, audiotest.WAV(8000, 16, audiotest.Samples(1, 800, 16)))

	f.worker.RunOnce(ctx)
	rec, _ := f.recs.Get(ctx, id)
	// The first failure is retried after the (tiny) backoff within the same run; the second
	// reaches MaxAttempts.
	if rec.Status != recording.StatusFailed || rec.Attempts != 2 || rec.LastError == "" {
		t.Fatalf("recording = %+v", rec)
	}
	if _, err := os.Stat(f.spool.WAVPath(id)); err != nil {
		t.Fatalf("spooled WAV must be kept for manual recovery: %v", err)
	}
}

func TestArchiveRecoversAfterTransientFailure(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	f.worker = worker.New(f.recs, []worker.Stage{{
		Name: "archive", From: recording.StatusReceived, To: recording.StatusStored,
		Run: service.NewArchiver(f.spool, f.objects, false, quiet).Run,
	}}, worker.Options{MaxAttempts: 3, BaseBackoff: time.Hour}, quiet)
	f.objects.Err = errors.New("s3 unavailable")
	id := f.receive(t, audiotest.WAV(8000, 16, audiotest.Samples(1, 800, 16)))

	f.worker.RunOnce(ctx)
	rec, _ := f.recs.Get(ctx, id)
	if rec.Status != recording.StatusReceived || rec.Attempts != 1 || !rec.NotBefore.After(time.Now().Add(50*time.Minute)) {
		t.Fatalf("after failure: %+v", rec)
	}

	// Once the backoff has passed and storage is back, the next run succeeds.
	f.objects.Err = nil
	rec.NotBefore = time.Now()
	_ = f.recs.Update(ctx, rec)
	f.worker.RunOnce(ctx)
	rec, _ = f.recs.Get(ctx, id)
	if rec.Status != recording.StatusStored || rec.LastError != "" {
		t.Fatalf("after recovery: %+v", rec)
	}
}
