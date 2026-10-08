package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

var (
	dev1 = &device.Device{ID: "dev-1", OwnerID: "user-1"}
	dev2 = &device.Device{ID: "dev-2", OwnerID: "user-2"}
)

func testWAV() []byte { return audiotest.WAV(16000, 16, audiotest.Samples(1, 16000, 16)) }

// testMP3 starts like the MP3 the 1.54" recorder writes (MPEG-2 Layer III, 16 kHz mono,
// 32 kbps), which is all the upload checks look at.
func testMP3() []byte {
	b := bytes.Repeat([]byte{0x55}, 400)
	copy(b, []byte{0xFF, 0xF3, 0x48, 0xC4})
	return b
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newUploads(t *testing.T) (*UploadService, *memory.Recordings, *Spool) {
	t.Helper()
	recs := memory.NewRecordings()
	spool, err := NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewUploadService(recs, spool, 1<<30), recs, spool
}

func create(t *testing.T, s *UploadService, wav []byte) *Upload {
	t.Helper()
	up, created, err := s.Create(context.Background(), dev1, CreateUploadInput{RecordingID: "rec-1", Size: int64(len(wav)), SHA256: sum(wav)})
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	return up
}

func TestUploadInChunks(t *testing.T) {
	ctx := context.Background()
	s, _, spool := newUploads(t)
	woken := 0
	s.OnReceived = func() { woken++ }
	wav := testWAV()
	up := create(t, s, wav)
	id := up.Recording.ID

	for off := 0; off < len(wav); off += 10000 {
		end := min(off+10000, len(wav))
		got, err := s.Append(ctx, dev1, id, int64(off), bytes.NewReader(wav[off:end]))
		if err != nil {
			t.Fatalf("append at %d: %v", off, err)
		}
		if got.Offset != int64(end) {
			t.Fatalf("offset = %d, want %d", got.Offset, end)
		}
	}

	got, err := s.Get(ctx, dev1, id)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Recording
	if r.Status != recording.StatusReceived || r.Format == nil || r.Format.DurationMs != 1000 || woken != 1 {
		t.Fatalf("recording = %+v (format %+v), woken=%d", r, r.Format, woken)
	}
	if _, err := spool.SHA256(id); err != nil {
		t.Fatalf("spooled WAV missing: %v", err)
	}

	// Retrying the final chunk after completion is a harmless no-op.
	again, err := s.Append(ctx, dev1, id, 0, bytes.NewReader(wav))
	if err != nil || again.Recording.Status != recording.StatusReceived || woken != 1 {
		t.Fatalf("retry after completion: %+v, %v, woken=%d", again, err, woken)
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newUploads(t)
	wav := testWAV()
	first := create(t, s, wav)
	if _, err := s.Append(ctx, dev1, first.Recording.ID, 0, bytes.NewReader(wav[:100])); err != nil {
		t.Fatal(err)
	}

	in := CreateUploadInput{RecordingID: "rec-1", Size: int64(len(wav)), SHA256: sum(wav)}
	again, created, err := s.Create(ctx, dev1, in)
	if err != nil || created || again.Recording.ID != first.Recording.ID || again.Offset != 100 {
		t.Fatalf("second create: %+v created=%v err=%v", again, created, err)
	}

	in.Size++
	if _, _, err := s.Create(ctx, dev1, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("create with different size: %v", err)
	}

	// The same recording ID on another device is a different recording.
	in.Size--
	other, created, err := s.Create(ctx, dev2, in)
	if err != nil || !created || other.Recording.ID == first.Recording.ID {
		t.Fatalf("other device: %+v created=%v err=%v", other, created, err)
	}
}

func TestCreateValidates(t *testing.T) {
	s, _, _ := newUploads(t)
	s.maxSize = 1000
	good := CreateUploadInput{RecordingID: "r", Size: 100, SHA256: sum(nil)}
	for name, tc := range map[string]struct {
		mod  func(*CreateUploadInput)
		want error
	}{
		"bad id":     {func(in *CreateUploadInput) { in.RecordingID = "a/b" }, ErrInvalidInput},
		"too small":  {func(in *CreateUploadInput) { in.Size = 10 }, ErrInvalidInput},
		"too large":  {func(in *CreateUploadInput) { in.Size = 1001 }, ErrTooLarge},
		"bad sha256": {func(in *CreateUploadInput) { in.SHA256 = "abc" }, ErrInvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			in := good
			tc.mod(&in)
			if _, _, err := s.Create(context.Background(), dev1, in); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAppendOffsetMismatch(t *testing.T) {
	s, _, _ := newUploads(t)
	wav := testWAV()
	up := create(t, s, wav)
	_, err := s.Append(context.Background(), dev1, up.Recording.ID, 50, bytes.NewReader(wav[50:100]))
	var om *OffsetMismatchError
	if !errors.As(err, &om) || om.Current != 0 {
		t.Fatalf("err = %v", err)
	}
}

// failingReader delivers n bytes and then fails, like a dropped connection.
type failingReader struct {
	r io.Reader
	n int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("connection reset")
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	n, err := f.r.Read(p)
	f.n -= n
	return n, err
}

func TestAppendResumesAfterDroppedConnection(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newUploads(t)
	wav := testWAV()
	up := create(t, s, wav)
	id := up.Recording.ID

	if _, err := s.Append(ctx, dev1, id, 0, &failingReader{r: bytes.NewReader(wav), n: 1234}); err == nil {
		t.Fatal("expected error from dropped connection")
	}
	st, err := s.Get(ctx, dev1, id)
	if err != nil || st.Offset != 1234 {
		t.Fatalf("offset after drop = %+v, %v", st, err)
	}
	done, err := s.Append(ctx, dev1, id, 1234, bytes.NewReader(wav[1234:]))
	if err != nil || done.Recording.Status != recording.StatusReceived {
		t.Fatalf("resume: %+v, %v", done, err)
	}
}

func TestChecksumMismatchResetsUpload(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newUploads(t)
	wav := testWAV()
	up := create(t, s, wav)
	corrupt := append([]byte{}, wav...)
	corrupt[len(corrupt)-1] ^= 0xFF

	if _, err := s.Append(ctx, dev1, up.Recording.ID, 0, bytes.NewReader(corrupt)); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v", err)
	}
	st, _ := s.Get(ctx, dev1, up.Recording.ID)
	if st.Offset != 0 || st.Recording.Status != recording.StatusUploading {
		t.Fatalf("after mismatch: %+v", st)
	}
	if done, err := s.Append(ctx, dev1, up.Recording.ID, 0, bytes.NewReader(wav)); err != nil || done.Recording.Status != recording.StatusReceived {
		t.Fatalf("resend: %+v, %v", done, err)
	}
}

func TestInvalidAudioFailsRecording(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newUploads(t)
	junk := bytes.Repeat([]byte("x"), 100)
	up, _, err := s.Create(ctx, dev1, CreateUploadInput{RecordingID: "junk", Size: 100, SHA256: sum(junk)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, dev1, up.Recording.ID, 0, bytes.NewReader(junk)); !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("err = %v", err)
	}
	st, _ := s.Get(ctx, dev1, up.Recording.ID)
	if st.Recording.Status != recording.StatusFailed || st.Recording.LastError == "" {
		t.Fatalf("after invalid audio: %+v", st.Recording)
	}
}

func TestMP3UploadIsAccepted(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newUploads(t)
	mp3 := testMP3()
	up := create(t, s, mp3)
	got, err := s.Append(ctx, dev1, up.Recording.ID, 0, bytes.NewReader(mp3))
	if err != nil {
		t.Fatal(err)
	}
	r := got.Recording
	if r.Status != recording.StatusReceived || r.SourceContentType != "audio/mpeg" || r.Format != nil {
		t.Fatalf("recording = %+v", r)
	}
}

func TestArchiveStoresDeviceMP3AsIs(t *testing.T) {
	objects := memstore.New()
	spool, err := NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	arch := NewArchiver(spool, objects, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := os.WriteFile(spool.WAVPath("m1"), testMP3(), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recording.Recording{ID: "m1", OwnerID: "alice", SourceContentType: "audio/mpeg"}
	if err := arch.Run(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if rec.Audio == nil || rec.Audio.Key != "recordings/alice/m1.mp3" || rec.Audio.ContentType != "audio/mpeg" || rec.Original != nil {
		t.Fatalf("archived audio %+v, original %+v", rec.Audio, rec.Original)
	}
}

func TestOtherDevicesUploadsAreHidden(t *testing.T) {
	s, _, _ := newUploads(t)
	up := create(t, s, testWAV())
	if _, err := s.Get(context.Background(), dev2, up.Recording.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Append(context.Background(), dev2, up.Recording.ID, 0, bytes.NewReader(nil)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPurgeStale(t *testing.T) {
	ctx := context.Background()
	s, recs, spool := newUploads(t)
	wav := testWAV()
	up := create(t, s, wav)
	if _, err := s.Append(ctx, dev1, up.Recording.ID, 0, bytes.NewReader(wav[:100])); err != nil {
		t.Fatal(err)
	}

	if n, err := s.PurgeStale(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("fresh upload purged: n=%d err=%v", n, err)
	}
	s.clock = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if n, err := s.PurgeStale(ctx, time.Hour); err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	if _, err := recs.Get(ctx, up.Recording.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recording not deleted: %v", err)
	}
	if size, _ := spool.Size(up.Recording.ID); size != 0 {
		t.Fatalf("spool file not removed: %d bytes", size)
	}
}
