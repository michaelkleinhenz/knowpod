package service

import (
	"context"
	"errors"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/noteversion"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// versionEvery is how long edits go into the same version: while a person keeps typing (the
// web app saves every few seconds), the text is kept once in this time, not on every save.
const versionEvery = 10 * time.Minute

// maxVersions is how many earlier versions are kept per note; older ones are deleted.
const maxVersions = 50

// keepVersion keeps the title and text a change replaced (before, at the note's revision
// rev) as a version of the note. An edit keeps it only when the last version is older than
// versionEvery; restoring and regenerating (force) always keep it, so they can be undone.
// Empty texts aren't kept. Keeping versions is best effort: the change is saved either way.
func (s *RecordingService) keepVersion(ctx context.Context, rec *recording.Recording, before recording.Summary, rev int64, reason string, force bool) {
	if s.Versions == nil || before.Markdown == "" {
		return
	}
	if after := rec.Summary; after != nil && after.Title == before.Title && after.Markdown == before.Markdown {
		return
	}
	now := s.clock().UTC()
	if !force {
		last, err := s.Versions.Latest(ctx, rec.ID)
		if err == nil && now.Sub(last.CreatedAt) < versionEvery {
			return
		}
	}
	savedAt := before.CreatedAt
	if before.EditedAt != nil {
		savedAt = *before.EditedAt
	}
	v := &noteversion.Version{
		ID: newID(), NoteID: rec.ID, OwnerID: rec.OwnerID, Revision: rev,
		Title: before.Title, Markdown: before.Markdown, SavedAt: savedAt, CreatedAt: now, Reason: reason,
	}
	if err := s.Versions.Create(ctx, v); err == nil {
		_ = s.Versions.Prune(ctx, rec.ID, maxVersions)
	}
}

// ListVersions lists the earlier versions of a note the account may see, newest first, without
// their text.
func (s *RecordingService) ListVersions(ctx context.Context, acc *Account, id string) ([]*noteversion.Version, error) {
	if _, _, err := s.load(ctx, acc, id, recording.RoleViewer); err != nil {
		return nil, err
	}
	if s.Versions == nil {
		return []*noteversion.Version{}, nil
	}
	return s.Versions.List(ctx, id)
}

// Version returns an earlier version of a note the account may see, with its text.
func (s *RecordingService) Version(ctx context.Context, acc *Account, id, versionID string) (*noteversion.Version, error) {
	if _, _, err := s.load(ctx, acc, id, recording.RoleViewer); err != nil {
		return nil, err
	}
	if s.Versions == nil {
		return nil, ErrNotFound
	}
	v, err := s.Versions.Get(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if v.NoteID != id {
		return nil, ErrNotFound
	}
	return v, nil
}

// RestoreVersion makes an earlier version the note's title and text again. The text it
// replaces is kept as a version, so restoring can be undone. With a baseRevision, text
// someone else changed meanwhile is not replaced (ErrChanged).
func (s *RecordingService) RestoreVersion(ctx context.Context, acc *Account, id, versionID string, baseRevision *int64) (*recording.Recording, error) {
	v, err := s.Version(ctx, acc, id, versionID)
	if err != nil {
		return nil, err
	}
	edit := SummaryEdit{Title: v.Title, Markdown: v.Markdown, BaseRevision: baseRevision}
	return s.editSummary(ctx, acc, id, edit, noteversion.ReasonRestore)
}

// deleteVersions deletes a note's versions when the note is deleted for good.
func (s *RecordingService) deleteVersions(ctx context.Context, id string) error {
	if s.Versions == nil {
		return nil
	}
	if err := s.Versions.DeleteByNote(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}
