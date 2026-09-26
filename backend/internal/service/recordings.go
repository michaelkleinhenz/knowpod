package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// ErrNotReady is returned for actions that need a processing step that hasn't happened.
var ErrNotReady = errors.New("recording is not ready for this")

// RecordingService implements the user actions on recordings: delete, re-transcribe and
// re-summarize.
type RecordingService struct {
	themes  *ThemeService
	recs    ports.RecordingRepository
	objects ports.ObjectStore
	spool   *Spool
	clock   func() time.Time
	// OnRequeued is called when a recording was sent back into processing. Optional.
	OnRequeued func()
}

// NewRecordingService builds the service.
func NewRecordingService(recs ports.RecordingRepository, objects ports.ObjectStore, spool *Spool, themes *ThemeService) *RecordingService {
	return &RecordingService{recs: recs, objects: objects, spool: spool, themes: themes, clock: time.Now}
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

// Resummarize discards the summary and queues the recording for summarizing again, with new
// options if given (nil keeps the current ones).
func (s *RecordingService) Resummarize(ctx context.Context, acc *Account, id string, opts *recording.SummaryOptions) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if rec.Transcript == nil {
		return nil, errors.Join(ErrNotReady, errors.New("the recording has no transcript yet"))
	}
	if opts != nil {
		if err := s.validOptions(ctx, acc, opts); err != nil {
			return nil, err
		}
		rec.SummaryOptions = *opts
	}
	rec.Summary = nil
	return s.requeue(ctx, rec, recording.StatusTranscribed)
}

// SummaryEdit is a person's change to a summary.
type SummaryEdit struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

// maxSummaryMarkdown bounds an edited summary (a long summary is a few thousand characters).
const maxSummaryMarkdown = 100_000

// EditSummary replaces the summary's title and Markdown text with the user's version. The
// model, theme and language it was made with are kept for reference.
func (s *RecordingService) EditSummary(ctx context.Context, acc *Account, id string, in SummaryEdit) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if rec.Summary == nil {
		return nil, errors.Join(ErrNotReady, errors.New("the recording has no summary yet"))
	}
	title := strings.TrimSpace(in.Title)
	markdown := strings.TrimSpace(strings.ReplaceAll(in.Markdown, "\r\n", "\n"))
	switch {
	case title == "" || utf8.RuneCountInString(title) > 200:
		return nil, invalid("title must be 1-200 characters")
	case len(markdown) > maxSummaryMarkdown:
		return nil, invalid("summary must be at most %d characters", maxSummaryMarkdown)
	}
	now := s.clock().UTC()
	rec.Summary.Title, rec.Summary.Markdown, rec.Summary.EditedAt = title, markdown, &now
	rec.UpdatedAt = now
	if err := s.recs.Update(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *RecordingService) validOptions(ctx context.Context, acc *Account, o *recording.SummaryOptions) error {
	o.Language, o.Model, o.ThemeID = strings.TrimSpace(o.Language), strings.TrimSpace(o.Model), strings.TrimSpace(o.ThemeID)
	if o.Language == "auto" {
		o.Language = ""
	}
	if _, ok := SummaryLanguages[o.Language]; o.Language != "" && !ok {
		return invalid("unsupported summary language %q", o.Language)
	}
	if len(o.Model) > 200 || strings.ContainsAny(o.Model, " \t\n") {
		return invalid("model IDs look like \"google/gemini-2.5-flash\"")
	}
	if o.ThemeID == AutoTheme {
		o.ThemeID = ""
	}
	if o.ThemeID != "" && (s.themes == nil || !s.themes.Accessible(ctx, acc, o.ThemeID)) {
		return invalid("unknown theme")
	}
	return nil
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
