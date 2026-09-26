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
