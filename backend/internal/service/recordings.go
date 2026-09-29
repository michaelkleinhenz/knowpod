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
	// Filters checks the saved filters boards show. Optional; without it boards can't show
	// filters.
	Filters *FilterService
	// TimeEntries holds the time logged on notes, deleted with them. Optional.
	TimeEntries ports.TimeEntryRepository
	// Users gives the time zone task dates are meant in. Optional; without it they are UTC.
	Users ports.UserRepository
	// OnRequeued is called when a recording was sent back into processing. Optional.
	OnRequeued func()
	// Events tells users' open apps about changes the repository can't see: a note no
	// longer shared with them. Optional.
	Events *NoteEvents
}

// NewRecordingService builds the service.
func NewRecordingService(recs ports.RecordingRepository, objects ports.ObjectStore, spool *Spool, themes *ThemeService) *RecordingService {
	return &RecordingService{recs: recs, objects: objects, spool: spool, themes: themes, clock: time.Now}
}

// Get returns a note the account may see, as the account sees it: its own, or one shared
// with it.
func (s *RecordingService) Get(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, _, err := s.load(ctx, acc, id, recording.RoleViewer)
	if err != nil {
		return nil, err
	}
	return present(acc, rec), nil
}

// List returns the account's notes and the notes shared with it (everyone's for
// ADMIN_TOKEN). Lists of the trash, of a device and by note number hold only the account's
// own notes.
func (s *RecordingService) List(ctx context.Context, acc *Account, f recording.ListFilter) ([]*recording.Recording, error) {
	f.OwnerID, f.UserID = acc.OwnerFilter(), ""
	if f.OwnerID != "" && f.Trash == recording.TrashExclude && f.DeviceID == "" && f.Number == 0 {
		f.OwnerID, f.UserID = "", acc.ID
	}
	list, err := s.recs.List(ctx, f)
	if err != nil {
		return nil, err
	}
	for i, rec := range list {
		list[i] = present(acc, rec)
	}
	return list, nil
}

// Trash moves a note to the trash, where it stays for recording.TrashRetention before it is
// deleted for good. Its sub-notes move up to where it was, so they stay in place, and its
// reminder is dropped until it is restored.
//
// Editors of a shared note can put the notes under it into the trash (the owner's trash);
// a note shared with them by itself they leave instead (Unshare).
func (s *RecordingService) Trash(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	rec, role, err := s.load(ctx, acc, id, recording.RoleEditor)
	if err != nil {
		return nil, err
	}
	if role != recording.RoleOwner && rec.Member(acc.ID).Root {
		return nil, errors.Join(ErrForbidden, errors.New("only the owner can delete a shared note; stop sharing it with yourself instead"))
	}
	if rec.DeletedAt != nil {
		return present(acc, rec), nil
	}
	children, err := s.childIDs(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	if err := s.recs.MoveSubNotes(ctx, rec.OwnerID, rec.ID, rec.ParentID, rec.FolderID); err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	out, err := s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if rec.DeletedAt == nil {
			rec.DeletedAt = &now
			clearReminders(rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The sub-notes moved up are shared like the place they moved to.
	return out, s.syncAll(ctx, children)
}

// Restore takes a note out of the trash. When the note it was a sub-note of is gone or in
// the trash itself, it comes back at the top level.
func (s *RecordingService) Restore(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	_, err := s.change(ctx, acc, id, recording.RoleOwner, func(rec *recording.Recording, _ recording.Role) error {
		if rec.DeletedAt == nil {
			return nil
		}
		if rec.ParentID != "" {
			parent, err := s.recs.Get(ctx, rec.ParentID)
			switch {
			case errors.Is(err, ErrNotFound) || (err == nil && parent.DeletedAt != nil):
				rec.ParentID = ""
			case err != nil:
				return err
			}
		}
		rec.DeletedAt = nil
		s.scheduleReminders(ctx, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.syncMembers(ctx, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, acc, id)
}

// Delete removes a note for good: the recording, its archived audio and any spooled files.
// A note that isn't in the trash yet has its sub-notes moved up to where it was first, so
// nothing else is lost.
func (s *RecordingService) Delete(ctx context.Context, acc *Account, id string) error {
	rec, _, err := s.load(ctx, acc, id, recording.RoleOwner)
	if err != nil {
		return err
	}
	var children []string
	if rec.DeletedAt == nil {
		if children, err = s.childIDs(ctx, rec.ID); err != nil {
			return err
		}
		if err := s.recs.MoveSubNotes(ctx, rec.OwnerID, rec.ID, rec.ParentID, rec.FolderID); err != nil {
			return err
		}
	}
	if err := s.delete(ctx, rec); err != nil {
		return err
	}
	return s.syncAll(ctx, children)
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
	if s.TimeEntries != nil {
		if err := s.TimeEntries.DeleteByNote(ctx, rec.ID); err != nil {
			return err
		}
	}
	return s.recs.Delete(ctx, rec.ID)
}

// Retranscribe discards the transcript and summary and queues the recording for
// transcription again (for documents: reading the pages again).
func (s *RecordingService) Retranscribe(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	return s.requeue(ctx, acc, id, func(rec *recording.Recording) (recording.Status, error) {
		switch {
		case rec.IsText():
			return "", errors.Join(ErrNotReady, errors.New("text notes have no audio"))
		case rec.IsDocument() && rec.Original == nil && rec.File == nil:
			return "", errors.Join(ErrNotReady, errors.New("the document has not been stored yet"))
		case !rec.IsDocument() && rec.Audio == nil:
			return "", errors.Join(ErrNotReady, errors.New("the audio has not been archived yet"))
		}
		rec.Transcript, rec.Summary = nil, nil
		return recording.StatusStored, nil
	})
}

// Resummarize discards the summary and queues the recording for summarizing again, with new
// options if given (nil keeps the current ones).
func (s *RecordingService) Resummarize(ctx context.Context, acc *Account, id string, opts *recording.SummaryOptions) (*recording.Recording, error) {
	if opts != nil {
		if err := s.validOptions(ctx, acc, opts); err != nil {
			return nil, err
		}
	}
	return s.requeue(ctx, acc, id, func(rec *recording.Recording) (recording.Status, error) {
		if rec.IsText() {
			return "", errors.Join(ErrNotReady, errors.New("text notes have no transcript to summarize"))
		}
		if rec.Transcript == nil {
			return "", errors.Join(ErrNotReady, errors.New("the recording has no transcript yet"))
		}
		if opts != nil {
			rec.SummaryOptions = *opts
		}
		rec.Summary = nil
		return recording.StatusTranscribed, nil
	})
}

// SummaryEdit is a person's change to a summary.
type SummaryEdit struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
	// BaseRevision is the note's Revision the edit was made on. When given and the title or
	// text changed since (someone else edited them), the edit is refused with ErrChanged
	// instead of undoing theirs.
	BaseRevision *int64 `json:"baseRevision,omitempty"`
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
// a sub-note of or else the folder it goes into, and optionally as a task with a date and
// priority.
type TextNoteInput struct {
	SummaryEdit
	ParentID string `json:"parentId,omitempty"`
	FolderID string `json:"folderId,omitempty"`
	TaskFields
}

// CreateText creates a text note for the account's user. The title and Markdown text are
// kept as the note's summary, so the note is shown, edited, copied and downloaded like the
// summary of a recording. It needs no processing and is stored as summarized. With a
// ParentID it is created as a sub-note of that note, otherwise in folder FolderID (the top
// level when empty).
//
// A sub-note of a note shared with the account (as an editor), or a note in a folder shared
// with the account (as an editor), belongs to that note's or folder's owner and is shared
// like it.
func (s *RecordingService) CreateText(ctx context.Context, acc *Account, in TextNoteInput) (*recording.Recording, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("notes belong to a user; sign in"))
	}
	title, markdown, err := in.clean()
	if err != nil {
		return nil, err
	}
	parentID := strings.TrimSpace(in.ParentID)
	owner, parent := acc.ID, (*recording.Recording)(nil)
	if parentID != "" {
		p, _, err := s.load(ctx, acc, parentID, recording.RoleEditor)
		switch {
		case errors.Is(err, ErrNotFound):
			return nil, invalid("unknown note %q", parentID)
		case err != nil:
			return nil, err
		}
		owner, parent = p.OwnerID, p
	} else if folderID := strings.TrimSpace(in.FolderID); folderID != "" && !s.Folders.Usable(ctx, owner, folderID) {
		f, _, err := s.loadFolder(ctx, acc, folderID, recording.RoleEditor)
		switch {
		case errors.Is(err, ErrNotFound):
			return nil, invalid("unknown folder %q", folderID)
		case err != nil:
			return nil, err
		}
		owner = f.OwnerID
	}
	if err := s.validParent(ctx, owner, "", parentID); err != nil {
		return nil, err
	}
	folderID, err := s.newNoteFolder(ctx, owner, parentID, in.FolderID)
	if err != nil {
		return nil, err
	}
	id := newID()
	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: id, OwnerID: owner, DeviceID: recording.TextDeviceID(owner), ClientID: id,
		Type: recording.TypeText, Status: recording.StatusSummarized, ParentID: parentID, FolderID: folderID,
		Summary:   &recording.Summary{Title: title, Markdown: markdown, CreatedAt: now},
		NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	if owner != acc.ID {
		rec.CreatedBy = acc.ID
	}
	if parent != nil {
		rec.Members = recording.ComputeMembers(parent.Members, nil, nil)
	} else {
		inherited, err := s.inFolderMembers(ctx, rec)
		if err != nil {
			return nil, err
		}
		rec.Members = recording.ComputeMembers(inherited, nil, nil)
	}
	if err := in.TaskFields.apply(s, ctx, rec); err != nil {
		return nil, err
	}
	if err := s.recs.Create(ctx, rec); err != nil {
		return nil, err
	}
	return present(acc, rec), nil
}

// newNoteFolder checks the folder a new note of ownerID goes into; a sub-note (parentID set)
// is in no folder of its own.
func (s *RecordingService) newNoteFolder(ctx context.Context, ownerID, parentID, folderID string) (string, error) {
	folderID = strings.TrimSpace(folderID)
	if parentID != "" {
		return "", nil
	}
	if !s.Folders.Usable(ctx, ownerID, folderID) {
		return "", invalid("unknown folder %q", folderID)
	}
	return folderID, nil
}

// EditSummary replaces the summary's title and Markdown text with the user's version. The
// model, theme and language it was made with are kept for reference. With a BaseRevision,
// an edit of text someone else changed in the meantime is refused with ErrChanged. Notes
// of reMarkable documents are read-only: edits can't go back to the tablet, and the next
// change of the document replaces them.
func (s *RecordingService) EditSummary(ctx context.Context, acc *Account, id string, in SummaryEdit) (*recording.Recording, error) {
	title, markdown, err := in.clean()
	if err != nil {
		return nil, err
	}
	return s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if rec.Source == recording.SourceRemarkable {
			return errors.Join(ErrForbidden, errors.New("notes of reMarkable documents are read-only"))
		}
		if rec.Summary == nil {
			return errors.Join(ErrNotReady, errors.New("the recording has no summary yet"))
		}
		if in.BaseRevision != nil && *in.BaseRevision != rec.Revision {
			return errors.Join(ErrChanged, errors.New("the note's text was changed by someone else"))
		}
		now := s.clock().UTC()
		summary := *rec.Summary
		summary.Title, summary.Markdown, summary.EditedAt = title, markdown, &now
		rec.Summary = &summary
		rec.Revision++
		return nil
	})
}

// SetLabels replaces the note's labels. Unknown and duplicate IDs are refused. Taking the
// task label off clears the note's check mark, date and priority.
//
// On a note shared with the account, its own labels are kept for the account alone; the
// built-in labels (the task label) are the note's and change it for everyone, which needs
// an editor.
func (s *RecordingService) SetLabels(ctx context.Context, acc *Account, id string, labels []string) (*recording.Recording, error) {
	if len(labels) > maxLabelsPerNote {
		return nil, invalid("a note can have at most %d labels", maxLabelsPerNote)
	}
	return s.change(ctx, acc, id, recording.RoleViewer, func(rec *recording.Recording, role recording.Role) error {
		labeler := rec.OwnerID
		if role != recording.RoleOwner {
			labeler = acc.ID
		}
		out := make([]string, 0, len(labels))
		for _, l := range labels {
			if slices.Contains(out, l) {
				return invalid("label %q is given twice", l)
			}
			if !s.Labels.Usable(ctx, labeler, l) {
				return invalid("unknown label %q", l)
			}
			out = append(out, l)
		}
		if role != recording.RoleOwner {
			shared, own := splitBuiltIn(out)
			if was, _ := splitBuiltIn(rec.Labels); !sameSet(was, shared) {
				if !role.AtLeast(recording.RoleEditor) {
					return errors.Join(ErrForbidden, errors.New("the note is shared with you for viewing only"))
				}
				_, owners := splitBuiltIn(rec.Labels)
				rec.Labels = append(owners, shared...)
			}
			rec.Member(acc.ID).Labels = own
		} else {
			rec.Labels = out
		}
		if !slices.Contains(rec.Labels, label.Task) {
			clearTask(rec)
		}
		return nil
	})
}

// splitBuiltIn separates the built-in labels from a user's own.
func splitBuiltIn(labels []string) (builtIn, own []string) {
	for _, l := range labels {
		if isBuiltInLabel(l) {
			builtIn = append(builtIn, l)
		} else {
			own = append(own, l)
		}
	}
	return builtIn, own
}

func sameSet(a, b []string) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(x string) bool { return !slices.Contains(b, x) })
}

// SetDone checks or unchecks a note labeled as a task. Checking off a recurring task moves
// it to its next date instead.
func (s *RecordingService) SetDone(ctx context.Context, acc *Account, id string, done bool) (*recording.Recording, error) {
	return s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if !slices.Contains(rec.Labels, label.Task) {
			return invalid("only notes labeled as a task can be checked off")
		}
		s.completeTask(ctx, rec, done)
		return nil
	})
}

// FinishTask checks off a task for good: a recurring task stops repeating instead of moving
// to its next date.
func (s *RecordingService) FinishTask(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	return s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if !slices.Contains(rec.Labels, label.Task) {
			return invalid("only notes labeled as a task can be checked off")
		}
		if rec.Due != nil {
			rec.Due.Repeat = nil
		}
		s.completeTask(ctx, rec, true)
		return nil
	})
}

// SetFolder moves the note into one of its owner's folders, or to the top level (""). A
// sub-note moved into a folder is no longer under its parent note; its own sub-notes come
// along.
//
// A note shared with the account by itself goes into one of the account's folders, for the
// account alone. The notes under it stay there. Editors of a folder shared with them move
// its notes to other folders of its owner they can edit; only the owner takes them out.
func (s *RecordingService) SetFolder(ctx context.Context, acc *Account, id, folderID string) (*recording.Recording, error) {
	folderID = strings.TrimSpace(folderID)
	var moved bool
	out, err := s.change(ctx, acc, id, recording.RoleViewer, func(rec *recording.Recording, role recording.Role) error {
		if role != recording.RoleOwner {
			m := rec.Member(acc.ID)
			if !m.Root && rec.ParentID == "" && rec.FolderID != "" {
				// In a folder shared with the account.
				if !role.AtLeast(recording.RoleEditor) {
					return errors.Join(ErrForbidden, errors.New("the note is shared with you for viewing only"))
				}
				f, _, err := s.loadFolder(ctx, acc, folderID, recording.RoleEditor)
				if folderID == "" || errors.Is(err, ErrNotFound) || (err == nil && f.OwnerID != rec.OwnerID) {
					return errors.Join(ErrForbidden, errors.New("a note in a shared folder stays in the folders shared with you; only its owner moves it out"))
				}
				if err != nil {
					return err
				}
				if rec.FolderID != folderID {
					rec.Position = 0
				}
				moved = rec.FolderID != folderID
				rec.FolderID = folderID
				return nil
			}
			if !m.Root {
				return invalid("a note under a shared note stays under it")
			}
			if !s.Folders.Usable(ctx, acc.ID, folderID) {
				return invalid("unknown folder %q", folderID)
			}
			if m.FolderID != folderID {
				m.Position = 0
			}
			m.FolderID = folderID
			return nil
		}
		if !s.Folders.Usable(ctx, rec.OwnerID, folderID) {
			return invalid("unknown folder %q", folderID)
		}
		if rec.FolderID != folderID || rec.ParentID != "" {
			// A note moved elsewhere goes after the ordered notes there.
			rec.Position = 0
		}
		moved = rec.FolderID != folderID || rec.ParentID != ""
		rec.FolderID, rec.ParentID = folderID, ""
		return nil
	})
	if err != nil || !moved {
		return out, err
	}
	// A note taken from under its parent, or moved to another folder, is shared like the
	// place it moved to.
	if err := s.syncMembers(ctx, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, acc, id)
}

// maxNoteDepth bounds how deeply sub-notes can be nested.
const maxNoteDepth = 8

// SetParent makes the note a sub-note of another of its owner's notes, or takes it out from
// under its parent to the top level (""). A sub-note is in no folder; it is shown wherever
// its parent is. Its own sub-notes come along, and all of them are then shared like the
// new parent.
//
// Editors of a shared note can move the notes under it to other notes of the owner they
// can edit; only the owner takes them out of there.
func (s *RecordingService) SetParent(ctx context.Context, acc *Account, id, parentID string) (*recording.Recording, error) {
	parentID = strings.TrimSpace(parentID)
	_, err := s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, role recording.Role) error {
		if role != recording.RoleOwner {
			if rec.Member(acc.ID).Root || parentID == "" {
				return errors.Join(ErrForbidden, errors.New("only the owner can move a shared note out of where it is shared"))
			}
			if _, _, err := s.load(ctx, acc, parentID, recording.RoleEditor); err != nil {
				if errors.Is(err, ErrNotFound) {
					return invalid("unknown note %q", parentID)
				}
				return err
			}
		}
		if err := s.validParent(ctx, rec.OwnerID, rec.ID, parentID); err != nil {
			return err
		}
		if rec.ParentID != parentID || rec.FolderID != "" {
			rec.Position = 0
		}
		rec.ParentID, rec.FolderID = parentID, ""
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.syncMembers(ctx, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, acc, id)
}

// Reorder puts the account's notes ids, which must all be in the same place (the same folder,
// or under the same note), in this order. Notes there that aren't listed follow them, by
// title.
func (s *RecordingService) Reorder(ctx context.Context, acc *Account, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxReorder {
		return invalid("at most %d notes can be ordered at once", maxReorder)
	}
	// Each note is ordered where the account sees it: a note shared with the account by
	// itself among the account's notes (for the account alone), the others where they are
	// for everyone, which needs an editor.
	type place struct{ folder, parent string }
	var first *place
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return invalid("note %q is listed twice", id)
		}
		seen[id] = true
		rec, role, err := s.load(ctx, acc, id, recording.RoleViewer)
		if err != nil {
			return err
		}
		p := place{rec.FolderID, rec.ParentID}
		if role != recording.RoleOwner {
			if m := rec.Member(acc.ID); m.Root {
				p = place{m.FolderID, ""}
			} else if !role.AtLeast(recording.RoleEditor) {
				return errors.Join(ErrForbidden, errors.New("the note is shared with you for viewing only"))
			}
		}
		if first == nil {
			first = &p
		} else if p != *first {
			return invalid("the notes to order must be in the same place")
		}
	}
	for i, id := range ids {
		pos := i + 1
		_, err := s.change(ctx, acc, id, recording.RoleViewer, func(rec *recording.Recording, role recording.Role) error {
			if m := rec.Member(acc.ID); role != recording.RoleOwner && m.Root {
				m.Position = pos
			} else {
				rec.Position = pos
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
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

// save stores a changed note; it fails with ErrChanged when someone else saved it since it
// was read.
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

// requeue sends the owner's note back into processing: prepare clears what is made again
// and returns the status processing starts from.
func (s *RecordingService) requeue(ctx context.Context, acc *Account, id string, prepare func(rec *recording.Recording) (recording.Status, error)) (*recording.Recording, error) {
	rec, err := s.change(ctx, acc, id, recording.RoleOwner, func(rec *recording.Recording, _ recording.Role) error {
		status, err := prepare(rec)
		if err != nil {
			return err
		}
		now := s.clock().UTC()
		rec.Status, rec.Attempts, rec.LastError, rec.NotBefore = status, 0, "", now
		// The text is made again; edits of the old one no longer apply.
		rec.Revision++
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.OnRequeued != nil {
		s.OnRequeued()
	}
	return rec, nil
}
