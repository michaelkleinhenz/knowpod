package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// ErrUnsupportedMedia is returned for uploaded files that are not WAV or MP3.
var ErrUnsupportedMedia = errors.New("only WAV and MP3 files can be uploaded")

// ManualUploadService stores audio files that users upload in the web UI. The file is
// streamed into the spool and then processed like other recordings: WAV is archived as
// FLAC, MP3 as it is, and both are transcribed and summarized.
type ManualUploadService struct {
	recs    ports.RecordingRepository
	spool   *Spool
	maxSize int64
	clock   func() time.Time
	// OnReceived is called after a file was stored, e.g. to wake the worker. Optional.
	OnReceived func()
}

// NewManualUploadService builds the service.
func NewManualUploadService(recs ports.RecordingRepository, spool *Spool, maxSize int64) *ManualUploadService {
	return &ManualUploadService{recs: recs, spool: spool, maxSize: maxSize, clock: time.Now}
}

// ManualUpload describes an uploaded file.
type ManualUpload struct {
	Filename   string
	RecordedAt *time.Time // e.g. the file's modification time, if the browser sends it
	Body       io.Reader
}

// Upload stores the file for the account's user and queues it for processing.
func (s *ManualUploadService) Upload(ctx context.Context, acc *Account, in ManualUpload) (rec *recording.Recording, err error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("uploads belong to a user; sign in"))
	}
	id := newID()
	dst := s.spool.DownloadPath(id)
	f, err := os.Create(dst)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dst)
		}
	}()

	h := sha256.New()
	head := make([]byte, 0, 64)
	n, err := io.Copy(io.MultiWriter(f, h, headWriter{&head}), io.LimitReader(in.Body, s.maxSize+1))
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return nil, fmt.Errorf("receiving file: %w", err)
	case n > s.maxSize:
		return nil, fmt.Errorf("%w: maximum is %d bytes", ErrTooLarge, s.maxSize)
	case n == 0:
		return nil, invalid("the file is empty")
	}

	ctype, _ := audio.Sniff(head)
	var format *recording.Format
	switch ctype {
	case "audio/wav":
		info, werr := wavInfoAt(dst)
		if werr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidAudio, werr)
		}
		format = info.Format()
	case "audio/mpeg":
	default:
		return nil, ErrUnsupportedMedia
	}

	now := s.clock().UTC()
	rec = &recording.Recording{
		ID: id, OwnerID: acc.ID, DeviceID: "upload:" + acc.ID, ClientID: id,
		Source: recording.SourceUpload, Title: titleFromFilename(in.Filename), Status: recording.StatusReceived,
		Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), SourceContentType: ctype, Format: format,
		RecordedAt: in.RecordedAt, ReceivedAt: &now, NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.recs.Create(ctx, rec); err != nil {
		return nil, err
	}
	if s.OnReceived != nil {
		s.OnReceived()
	}
	return rec, nil
}

// titleFromFilename turns "Team meeting 2026-09-26.wav" into "Team meeting 2026-09-26". It is
// shown until the AI summary provides a title.
func titleFromFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSuffix(name, path.Ext(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		if r == '_' {
			return ' '
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	if name == "" || name == "." {
		return ""
	}
	return name
}

// headWriter keeps the first bytes written (for format detection).
type headWriter struct{ buf *[]byte }

func (w headWriter) Write(p []byte) (int, error) {
	if room := cap(*w.buf) - len(*w.buf); room > 0 {
		*w.buf = append(*w.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
