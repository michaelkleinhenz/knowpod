package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// ErrNotReady is returned for actions that need a processing step that hasn't happened.
var ErrNotReady = errors.New("recording is not ready for this")

// RecordingService implements the user actions on notes: create text notes, edit, delete,
// re-transcribe and re-summarize.
type RecordingService struct {
	themes  *ThemeService
	recs    ports.RecordingRepository
	objects ports.ObjectStore
	spool   *Spool
	clock   func() time.Time
	// Labels checks the labels put on notes. Optional; without it only the built-in labels
	// can be used.
	Labels *LabelService
	// Folders checks the folders notes are moved into. Optional; without it notes stay at
	// the top level.
	Folders *FolderService
	// Users gives the time zone task dates are meant in. Optional; without it they are UTC.
	Users ports.UserRepository
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

// Trash moves a note to the trash, where it stays for recording.TrashRetention before it is
// deleted for good. Its sub-notes move up to where it was, so they stay in place, and its
// reminder is dropped until it is restored.
func (s *RecordingService) Trash(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if rec.DeletedAt != nil {
		return rec, nil
	}
	if err := s.recs.MoveSubNotes(ctx, rec.OwnerID, rec.ID, rec.ParentID, rec.FolderID); err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	rec.DeletedAt, rec.RemindAt = &now, nil
	return rec, s.save(ctx, rec)
}

// Restore takes a note out of the trash. When the note it was a sub-note of is gone or in
// the trash itself, it comes back at the top level.
func (s *RecordingService) Restore(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if rec.DeletedAt == nil {
		return rec, nil
	}
	if rec.ParentID != "" {
		parent, err := s.recs.Get(ctx, rec.ParentID)
		switch {
		case errors.Is(err, ErrNotFound) || (err == nil && parent.DeletedAt != nil):
			rec.ParentID = ""
		case err != nil:
			return nil, err
		}
	}
	rec.DeletedAt = nil
	s.scheduleReminder(rec, s.location(ctx, rec.OwnerID))
	return rec, s.save(ctx, rec)
}

// Delete removes a note for good: the recording, its archived audio and any spooled files.
// A note that isn't in the trash yet has its sub-notes moved up to where it was first, so
// nothing else is lost.
func (s *RecordingService) Delete(ctx context.Context, acc *Account, id string) error {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return err
	}
	if rec.DeletedAt == nil {
		if err := s.recs.MoveSubNotes(ctx, rec.OwnerID, rec.ID, rec.ParentID, rec.FolderID); err != nil {
			return err
		}
	}
	return s.delete(ctx, rec)
}

// EmptyTrash deletes all the account's notes in the trash for good and returns how many.
func (s *RecordingService) EmptyTrash(ctx context.Context, acc *Account) (int, error) {
	n := 0
	for {
		list, err := s.recs.List(ctx, recording.ListFilter{OwnerID: acc.OwnerFilter(), Trash: recording.TrashOnly, Limit: 100, Brief: true})
		if err != nil || len(list) == 0 {
			return n, err
		}
		for _, rec := range list {
			if err := s.delete(ctx, rec); err != nil && !errors.Is(err, ErrNotFound) {
				return n, err
			}
			n++
		}
	}
}

// PurgeTrash deletes the notes that have been in the trash for longer than
// recording.TrashRetention, and returns how many.
func (s *RecordingService) PurgeTrash(ctx context.Context) (int, error) {
	n := 0
	for {
		list, err := s.recs.ListTrashed(ctx, s.clock().Add(-recording.TrashRetention), 100)
		if err != nil || len(list) == 0 {
			return n, err
		}
		for _, rec := range list {
			if err := s.delete(ctx, rec); err != nil && !errors.Is(err, ErrNotFound) {
				return n, err
			}
			n++
		}
	}
}

func (s *RecordingService) delete(ctx context.Context, rec *recording.Recording) error {
	for _, obj := range []*recording.Object{rec.Audio, rec.Original, rec.File} {
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
// transcription again (for documents: reading the pages again).
func (s *RecordingService) Retranscribe(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	switch {
	case rec.IsText():
		return nil, errors.Join(ErrNotReady, errors.New("text notes have no audio"))
	case rec.IsDocument() && rec.Original == nil:
		return nil, errors.Join(ErrNotReady, errors.New("the document has not been stored yet"))
	case !rec.IsDocument() && rec.Audio == nil:
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
	if rec.IsText() {
		return nil, errors.Join(ErrNotReady, errors.New("text notes have no transcript to summarize"))
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

// clean normalizes the edit and checks its limits.
func (in SummaryEdit) clean() (title, markdown string, err error) {
	title = strings.TrimSpace(in.Title)
	markdown = strings.TrimSpace(strings.ReplaceAll(in.Markdown, "\r\n", "\n"))
	switch {
	case title == "" || utf8.RuneCountInString(title) > 200:
		return "", "", invalid("title must be 1-200 characters")
	case len(markdown) > maxSummaryMarkdown:
		return "", "", invalid("text must be at most %d characters", maxSummaryMarkdown)
	}
	return title, markdown, nil
}

// TextNoteInput creates a text note: its title and Markdown text, optionally the note it is
// a sub-note of, and optionally as a task with a date and priority.
type TextNoteInput struct {
	SummaryEdit
	ParentID string `json:"parentId,omitempty"`
	TaskFields
}

// CreateText creates a text note for the account's user. The title and Markdown text are
// kept as the note's summary, so the note is shown, edited, copied and downloaded like the
// summary of a recording. It needs no processing and is stored as summarized. With a
// ParentID it is created as a sub-note of that note.
func (s *RecordingService) CreateText(ctx context.Context, acc *Account, in TextNoteInput) (*recording.Recording, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("notes belong to a user; sign in"))
	}
	title, markdown, err := in.clean()
	if err != nil {
		return nil, err
	}
	parentID := strings.TrimSpace(in.ParentID)
	if err := s.validParent(ctx, acc.ID, "", parentID); err != nil {
		return nil, err
	}
	id := newID()
	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: id, OwnerID: acc.ID, DeviceID: recording.TextDeviceID(acc.ID), ClientID: id,
		Type: recording.TypeText, Status: recording.StatusSummarized, ParentID: parentID,
		Summary:   &recording.Summary{Title: title, Markdown: markdown, CreatedAt: now},
		NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := in.TaskFields.apply(s, ctx, rec); err != nil {
		return nil, err
	}
	if err := s.recs.Create(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

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
	title, markdown, err := in.clean()
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	rec.Summary.Title, rec.Summary.Markdown, rec.Summary.EditedAt = title, markdown, &now
	rec.UpdatedAt = now
	if err := s.recs.Update(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// SetLabels replaces the note's labels. Unknown and duplicate IDs are refused. Taking the
// task label off clears the note's check mark, date and priority.
func (s *RecordingService) SetLabels(ctx context.Context, acc *Account, id string, labels []string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if len(labels) > maxLabelsPerNote {
		return nil, invalid("a note can have at most %d labels", maxLabelsPerNote)
	}
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if slices.Contains(out, l) {
			return nil, invalid("label %q is given twice", l)
		}
		if !s.Labels.Usable(ctx, rec.OwnerID, l) {
			return nil, invalid("unknown label %q", l)
		}
		out = append(out, l)
	}
	rec.Labels = out
	if !slices.Contains(out, label.Task) {
		clearTask(rec)
	}
	return rec, s.save(ctx, rec)
}

// SetDone checks or unchecks a note labeled as a task. Checking off a recurring task moves
// it to its next date instead.
func (s *RecordingService) SetDone(ctx context.Context, acc *Account, id string, done bool) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(rec.Labels, label.Task) {
		return nil, invalid("only notes labeled as a task can be checked off")
	}
	s.completeTask(ctx, rec, done)
	return rec, s.save(ctx, rec)
}

// SetFolder moves the note into one of its owner's folders, or to the top level (""). A
// sub-note moved into a folder is no longer under its parent note; its own sub-notes come
// along.
func (s *RecordingService) SetFolder(ctx context.Context, acc *Account, id, folderID string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	folderID = strings.TrimSpace(folderID)
	if !s.Folders.Usable(ctx, rec.OwnerID, folderID) {
		return nil, invalid("unknown folder %q", folderID)
	}
	rec.FolderID, rec.ParentID = folderID, ""
	return rec, s.save(ctx, rec)
}

// maxNoteDepth bounds how deeply sub-notes can be nested.
const maxNoteDepth = 8

// SetParent makes the note a sub-note of another of its owner's notes, or takes it out from
// under its parent to the top level (""). A sub-note is in no folder; it is shown wherever
// its parent is. Its own sub-notes come along.
func (s *RecordingService) SetParent(ctx context.Context, acc *Account, id, parentID string) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	parentID = strings.TrimSpace(parentID)
	if err := s.validParent(ctx, rec.OwnerID, rec.ID, parentID); err != nil {
		return nil, err
	}
	rec.ParentID, rec.FolderID = parentID, ""
	return rec, s.save(ctx, rec)
}

// validParent checks that note id of ownerID may be put under parentID ("" is none, id ""
// is a new note): the parent is one of the owner's notes, a note can't go under itself or
// one of its own sub-notes, and nesting is bounded.
func (s *RecordingService) validParent(ctx context.Context, ownerID, id, parentID string) error {
	depth := 1
	for p := parentID; p != ""; {
		if p == id {
			return invalid("a note can't be a sub-note of itself or of its own sub-notes")
		}
		if depth++; depth > maxNoteDepth {
			return invalid("notes can be nested at most %d deep", maxNoteDepth)
		}
		parent, err := s.recs.Get(ctx, p)
		if errors.Is(err, ErrNotFound) || (err == nil && parent.OwnerID != ownerID) {
			return invalid("unknown note %q", p)
		}
		if err != nil {
			return err
		}
		if parent.DeletedAt != nil {
			return invalid("note %q is in the trash", p)
		}
		p = parent.ParentID
	}
	return nil
}

func (s *RecordingService) save(ctx context.Context, rec *recording.Recording) error {
	rec.UpdatedAt = s.clock().UTC()
	return s.recs.Update(ctx, rec)
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
