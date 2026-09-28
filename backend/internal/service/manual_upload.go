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

// ErrUnsupportedMedia is returned for uploaded files that are not WAV, MP3, a photo or a PDF.
var ErrUnsupportedMedia = errors.New("only audio (WAV, MP3), photos (JPEG, PNG, WebP, GIF) and PDF files can be uploaded")

// maxUploadImage limits uploaded photos: they are sent to the model in one piece.
const maxUploadImage = 20 << 20

// ManualUploadService stores files that users upload in the web UI, and voice memos recorded
// there. The file is streamed into the spool and then processed like other recordings: WAV
// is archived as FLAC, MP3 as it is, and both are transcribed and summarized. Photos and PDFs
// become documents: stored as they are, read by the document model and summarized.
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
	// Recorded says the file is a voice memo recorded in the web app.
	Recorded bool
	// Highlights are the moments marked while recording (offsets in milliseconds).
	Highlights []int64
}

// sniffDocument identifies the photos and PDFs that can be uploaded, from their first bytes.
// heic reports an HEIC/HEIF photo, which the models can't read.
func sniffDocument(head []byte) (contentType string, heic bool) {
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg", false
	case strings.HasPrefix(string(head), "\x89PNG\r\n\x1a\n"):
		return "image/png", false
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return "image/webp", false
	case strings.HasPrefix(string(head), "GIF87a"), strings.HasPrefix(string(head), "GIF89a"):
		return "image/gif", false
	case strings.HasPrefix(string(head), "%PDF-"):
		return "application/pdf", false
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		switch string(head[8:12]) {
		case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1", "avif":
			return "", true
		}
	}
	return "", false
}

// documentExtension returns the file extension of an uploaded document's media type.
func documentExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "application/pdf":
		return "pdf"
	}
	return "bin"
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

	now := s.clock().UTC()
	if doc, heic := sniffDocument(head); doc != "" || heic {
		switch {
		case heic:
			return nil, errors.Join(ErrUnsupportedMedia, errors.New("HEIC photos can't be read; share or save the photo as JPEG"))
		case in.Recorded:
			return nil, ErrUnsupportedMedia
		case doc != "application/pdf" && n > maxUploadImage:
			return nil, fmt.Errorf("%w: photos can be at most %d MB", ErrTooLarge, maxUploadImage>>20)
		}
		rec = &recording.Recording{
			ID: id, OwnerID: acc.ID, DeviceID: "upload:" + acc.ID, ClientID: id, Type: recording.TypeDocument,
			Source: recording.SourceUpload, Title: titleFromFilename(in.Filename), Status: recording.StatusReceived,
			Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), SourceContentType: doc,
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

	source := recording.SourceUpload
	if in.Recorded {
		source = recording.SourceRecorder
	}
	var highlights []recording.Highlight
	if len(in.Highlights) > 0 {
		marks := make([]HighlightInput, len(in.Highlights))
		for i := range in.Highlights {
			marks[i].OffsetMs = &in.Highlights[i]
		}
		if highlights, err = normalizeHighlights(marks, nil); err != nil {
			return nil, err
		}
	}
	rec = &recording.Recording{
		ID: id, OwnerID: acc.ID, DeviceID: "upload:" + acc.ID, ClientID: id,
		Source: source, Title: titleFromFilename(in.Filename), Status: recording.StatusReceived,
		Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), SourceContentType: ctype, Format: format,
		Highlights: highlights,
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
