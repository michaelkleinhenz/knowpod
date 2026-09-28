package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func TestManualUpload(t *testing.T) {
	ctx := context.Background()
	recs := memory.NewRecordings()
	spool, _ := NewSpool(t.TempDir())
	s := NewManualUploadService(recs, spool, 1<<20)
	woken := 0
	s.OnReceived = func() { woken++ }
	alice := &Account{ID: "alice"}

	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 16000, 16))
	rec, err := s.Upload(ctx, alice, ManualUpload{Filename: `C:\fakepath\Team_meeting.wav`, Body: bytes.NewReader(wav)})
	if err != nil {
		t.Fatal(err)
	}
	if rec.OwnerID != "alice" || rec.Source != recording.SourceUpload || rec.Status != recording.StatusReceived ||
		rec.Title != "Team meeting" || rec.SourceContentType != "audio/wav" || rec.Format == nil || rec.Size != int64(len(wav)) || woken != 1 {
		t.Fatalf("recording = %+v", rec)
	}

	mp3 := append([]byte("ID3\x04\x00"), make([]byte, 500)...)
	if rec, err := s.Upload(ctx, alice, ManualUpload{Filename: "note.mp3", Body: bytes.NewReader(mp3)}); err != nil || rec.SourceContentType != "audio/mpeg" {
		t.Fatalf("mp3: %+v, %v", rec, err)
	}

	for name, tc := range map[string]struct {
		body []byte
		want error
	}{
		"ogg":       {[]byte("OggS\x00\x02 rest"), ErrUnsupportedMedia},
		"text":      {[]byte("hello"), ErrUnsupportedMedia},
		"empty":     {nil, ErrInvalidInput},
		"too large": {make([]byte, 1<<20+1), ErrTooLarge},
		"float wav": {floatWAV(), ErrInvalidAudio},
	} {
		if _, err := s.Upload(ctx, alice, ManualUpload{Filename: "x", Body: bytes.NewReader(tc.body)}); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	if list, _ := recs.List(ctx, recording.ListFilter{}); len(list) != 2 {
		t.Fatalf("rejected uploads left recordings: %d", len(list))
	}
	if _, err := s.Upload(ctx, &Account{All: true}, ManualUpload{Body: bytes.NewReader(wav)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("upload without a user: %v", err)
	}
}

func floatWAV() []byte {
	b := audiotest.WAV(8000, 16, audiotest.Samples(1, 10, 16))
	b[20] = 3 // IEEE float
	return b
}

func TestUploadPhotosAndPDFs(t *testing.T) {
	ctx := context.Background()
	recs := memory.NewRecordings()
	spool, _ := NewSpool(t.TempDir())
	s := NewManualUploadService(recs, spool, 30<<20)
	alice := &Account{ID: "alice"}

	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 100)...)
	rec, err := s.Upload(ctx, alice, ManualUpload{Filename: "Whiteboard.jpg", Body: bytes.NewReader(jpeg)})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.IsDocument() || rec.Source != recording.SourceUpload || rec.SourceContentType != "image/jpeg" || rec.Title != "Whiteboard" || rec.Status != recording.StatusReceived {
		t.Fatalf("photo = %+v", rec)
	}
	pdf := []byte("%PDF-1.7\n...")
	if rec, err := s.Upload(ctx, alice, ManualUpload{Filename: "offer.pdf", Body: bytes.NewReader(pdf)}); err != nil || rec.SourceContentType != "application/pdf" || !rec.IsDocument() {
		t.Fatalf("pdf: %+v, %v", rec, err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 30)...)
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 30)...)
	for name, body := range map[string][]byte{"png": png, "webp": webp, "gif": []byte("GIF89a......")} {
		if rec, err := s.Upload(ctx, alice, ManualUpload{Filename: "x", Body: bytes.NewReader(body)}); err != nil || !rec.IsDocument() {
			t.Errorf("%s: %+v, %v", name, rec, err)
		}
	}

	heic := append([]byte("\x00\x00\x00\x18ftypheic"), make([]byte, 30)...)
	if _, err := s.Upload(ctx, alice, ManualUpload{Filename: "IMG_1.heic", Body: bytes.NewReader(heic)}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("heic: %v", err)
	}
	big := append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, maxUploadImage)...)
	if _, err := s.Upload(ctx, alice, ManualUpload{Filename: "big.jpg", Body: bytes.NewReader(big)}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("large photo: %v", err)
	}
	if _, err := s.Upload(ctx, alice, ManualUpload{Filename: "memo.wav", Body: bytes.NewReader(jpeg), Recorded: true}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("recorded photo: %v", err)
	}
}

func TestUploadVoiceMemoWithHighlights(t *testing.T) {
	ctx := context.Background()
	recs := memory.NewRecordings()
	spool, _ := NewSpool(t.TempDir())
	s := NewManualUploadService(recs, spool, 1<<20)
	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 16000, 16))
	rec, err := s.Upload(ctx, &Account{ID: "alice"}, ManualUpload{Filename: "Voice memo.wav", Body: bytes.NewReader(wav), Recorded: true, Highlights: []int64{900, 300, 900}})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Source != recording.SourceRecorder || len(rec.Highlights) != 2 || rec.Highlights[0].OffsetMs != 300 || rec.Highlights[1].OffsetMs != 900 {
		t.Fatalf("memo = %+v", rec)
	}
	if _, err := s.Upload(ctx, &Account{ID: "alice"}, ManualUpload{Body: bytes.NewReader(wav), Recorded: true, Highlights: []int64{-1}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative highlight: %v", err)
	}
}
