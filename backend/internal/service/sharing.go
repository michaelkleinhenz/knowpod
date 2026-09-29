package service

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// ErrChanged is returned when a note was changed by someone else since it was read.
var ErrChanged = domain.ErrChanged

// maxShares bounds the users one note is shared with directly.
const maxShares = 50

// maxChangeAttempts bounds how often a change is applied again to a newer copy of a note
// that someone else saved in the meantime.
const maxChangeAttempts = 5

// roleOf is what the account may do with the note: everything as its owner (also
// ADMIN_TOKEN), what it was shared with the account for, or nothing ("").
func roleOf(acc *Account, rec *recording.Recording) recording.Role {
	if acc.Owns(rec.OwnerID) {
		return recording.RoleOwner
	}
	if m := rec.Member(acc.ID); m != nil {
		return m.Role
	}
	return ""
}

// roleIn is what the user may do with the note ("" for none).
func roleIn(rec *recording.Recording, userID string) recording.Role {
	if userID == rec.OwnerID {
		return recording.RoleOwner
	}
	if m := rec.Member(userID); m != nil {
		return m.Role
	}
	return ""
}

// load returns a note for an action that needs at least the role need. Notes the account
// can't see are not found; shared notes the owner put into the trash are gone for their
// members.
func (s *RecordingService) load(ctx context.Context, acc *Account, id string, need recording.Role) (*recording.Recording, recording.Role, error) {
	rec, err := s.recs.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	role := roleOf(acc, rec)
	if role == "" || (role != recording.RoleOwner && rec.DeletedAt != nil) {
		return nil, "", ErrNotFound
	}
	if !role.AtLeast(need) {
		if need == recording.RoleOwner {
			return nil, "", errors.Join(ErrForbidden, errors.New("only the owner of the note can do this"))
		}
		return nil, "", errors.Join(ErrForbidden, errors.New("the note is shared with you for viewing only"))
	}
	return rec, role, nil
}

// change applies fn to the note and saves it. When someone else saved the note in between,
// fn is applied again to the newer copy, so neither change is lost. fn must only change
// the note it is given. The result is the note as the account sees it.
func (s *RecordingService) change(ctx context.Context, acc *Account, id string, need recording.Role, fn func(rec *recording.Recording, role recording.Role) error) (*recording.Recording, error) {
	for attempt := 1; ; attempt++ {
		rec, role, err := s.load(ctx, acc, id, need)
		if err != nil {
			return nil, err
		}
		if err := fn(rec, role); err != nil {
			return nil, err
		}
		err = s.save(ctx, rec)
		if errors.Is(err, ErrChanged) && attempt < maxChangeAttempts {
			continue
		}
		if err != nil {
			return nil, err
		}
		return present(acc, rec), nil
	}
}

// present returns the note as the account sees it. The owner sees it as stored. A member
// sees their own labels and reminder instead of the owner's, a note shared with them by
// itself in their own folder and order rather than under its parent, and a note in a
// folder shared with them in that folder.
func present(acc *Account, rec *recording.Recording) *recording.Recording {
	out := *rec
	out.Shared = len(rec.Members) > 0
	out.Access = roleOf(acc, rec)
	m := rec.Member(acc.ID)
	if out.Access == recording.RoleOwner || m == nil {
		return &out
	}
	out.Labels = nil
	for _, l := range rec.Labels {
		if isBuiltInLabel(l) {
			out.Labels = append(out.Labels, l)
		}
	}
	out.Labels = append(out.Labels, m.Labels...)
	out.RemindAt = m.RemindAt
	if m.Root {
		out.ParentID, out.FolderID, out.Position = "", m.FolderID, m.Position
	}
	return &out
}

// ShareInput shares a note with the user who signs in with Email.
type ShareInput struct {
	Email string         `json:"email"`
	Role  recording.Role `json:"role"`
}

// ShareUser is a user a note is shared with, or its owner.
type ShareUser struct {
	UserID string         `json:"userId"`
	Email  string         `json:"email"`
	Role   recording.Role `json:"role"`
	// Inherited says the note is shared with the user through a note it is under; the
	// sharing is changed there.
	Inherited bool `json:"inherited,omitempty"`
}

// Sharing says who a note is shared with.
type Sharing struct {
	Owner   ShareUser   `json:"owner"`
	Members []ShareUser `json:"members"`
	// Access is what the account asking may do with the note.
	Access recording.Role `json:"access"`
	// Reporter is the user who made the note (a task's reporter): its owner, or the editor
	// who created it under a shared note. Only for notes.
	Reporter *ShareUser `json:"reporter,omitempty"`
}

// Sharing returns who the note is shared with.
func (s *RecordingService) Sharing(ctx context.Context, acc *Account, id string) (*Sharing, error) {
	rec, role, err := s.load(ctx, acc, id, recording.RoleViewer)
	if err != nil {
		return nil, err
	}
	return s.sharingOf(ctx, rec, role), nil
}

func (s *RecordingService) sharingOf(ctx context.Context, rec *recording.Recording, role recording.Role) *Sharing {
	out := &Sharing{Owner: ShareUser{UserID: rec.OwnerID, Email: s.email(ctx, rec.OwnerID), Role: recording.RoleOwner},
		Members: []ShareUser{}, Access: role}
	for _, m := range rec.Members {
		out.Members = append(out.Members, ShareUser{UserID: m.UserID, Email: s.email(ctx, m.UserID), Role: m.Role,
			Inherited: rec.Share(m.UserID) == nil})
	}
	reporter := cmp.Or(rec.CreatedBy, rec.OwnerID)
	out.Reporter = &ShareUser{UserID: reporter, Email: s.email(ctx, reporter), Role: roleIn(rec, reporter)}
	return out
}

func (s *RecordingService) email(ctx context.Context, userID string) string {
	if s.Users == nil {
		return ""
	}
	u, err := s.Users.Get(ctx, userID)
	if err != nil {
		return ""
	}
	return u.Email
}

// Share shares the note and everything under it with a user, or changes what the user may
// do with it. Only the owner shares.
func (s *RecordingService) Share(ctx context.Context, acc *Account, id string, in ShareInput) (*Sharing, error) {
	if s.Users == nil {
		return nil, errors.Join(ErrNotReady, errors.New("sharing is not available"))
	}
	if !in.Role.Valid() {
		return nil, invalid("role must be %q or %q", recording.RoleViewer, recording.RoleEditor)
	}
	u, err := s.Users.GetByEmail(ctx, normalizeEmail(in.Email))
	if errors.Is(err, ErrNotFound) {
		return nil, invalid("there is no user with the email %q", in.Email)
	}
	if err != nil {
		return nil, err
	}
	return s.setShare(ctx, acc, id, u.ID, in.Role)
}

// SetShareRole changes what a user the note is shared with may do with it.
func (s *RecordingService) SetShareRole(ctx context.Context, acc *Account, id, userID string, role recording.Role) (*Sharing, error) {
	if !role.Valid() {
		return nil, invalid("role must be %q or %q", recording.RoleViewer, recording.RoleEditor)
	}
	rec, _, err := s.load(ctx, acc, id, recording.RoleOwner)
	if err != nil {
		return nil, err
	}
	if rec.Share(userID) == nil {
		return nil, invalid("the note is not shared with this user here; change it on the note it is shared through")
	}
	return s.setShare(ctx, acc, id, userID, role)
}

func (s *RecordingService) setShare(ctx context.Context, acc *Account, id, userID string, role recording.Role) (*Sharing, error) {
	now := s.clock().UTC()
	_, err := s.change(ctx, acc, id, recording.RoleOwner, func(rec *recording.Recording, _ recording.Role) error {
		switch {
		case userID == rec.OwnerID:
			return invalid("the note is yours already")
		case rec.DeletedAt != nil:
			return invalid("notes in the trash can't be shared")
		case rec.IsBoard():
			return invalid("boards can't be shared; share the notes they show")
		}
		if sh := rec.Share(userID); sh != nil {
			sh.Role = role
			return nil
		}
		if len(rec.Shares) >= maxShares {
			return invalid("a note can be shared with at most %d users", maxShares)
		}
		rec.Shares = append(rec.Shares, recording.Share{UserID: userID, Role: role, CreatedAt: now})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.afterSharing(ctx, acc, id)
}

// Unshare stops sharing the note with a user. The owner can take anyone off; a member can
// take themselves off a note shared with them (leave it).
func (s *RecordingService) Unshare(ctx context.Context, acc *Account, id, userID string) (*Sharing, error) {
	need := recording.RoleOwner
	if userID == acc.ID {
		need = recording.RoleViewer
	}
	_, err := s.change(ctx, acc, id, need, func(rec *recording.Recording, _ recording.Role) error {
		if rec.Share(userID) == nil {
			if rec.Member(userID) != nil {
				return invalid("the note is shared through a note it is under; stop sharing that one")
			}
			return ErrNotFound
		}
		rec.Shares = slices.DeleteFunc(rec.Shares, func(sh recording.Share) bool { return sh.UserID == userID })
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := s.afterSharing(ctx, acc, id)
	if errors.Is(err, ErrNotFound) && userID == acc.ID {
		// The account left the note and can't see it any more.
		return nil, nil
	}
	return out, err
}

// afterSharing brings the members of the note and everything under it up to date and
// returns who the note is shared with now.
func (s *RecordingService) afterSharing(ctx context.Context, acc *Account, id string) (*Sharing, error) {
	if err := s.syncMembers(ctx, id); err != nil {
		return nil, err
	}
	return s.Sharing(ctx, acc, id)
}

// syncMembers recomputes the members of the note from its shares and the note it is under
// (or, for a note in a folder, the folder), and so on down its sub-notes. It is called
// whenever shares change or a note moves.
func (s *RecordingService) syncMembers(ctx context.Context, id string) error {
	return s.syncMembersAt(ctx, id, 1)
}

func (s *RecordingService) syncMembersAt(ctx context.Context, id string, depth int) error {
	for attempt := 1; ; attempt++ {
		rec, err := s.recs.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var inherited []recording.Member
		if rec.ParentID != "" {
			parent, err := s.recs.Get(ctx, rec.ParentID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if err == nil && parent.OwnerID == rec.OwnerID {
				inherited = parent.Members
			}
		} else if inherited, err = s.inFolderMembers(ctx, rec); err != nil {
			return err
		}
		next := recording.ComputeMembers(inherited, rec.Shares, rec.Members)
		if recording.SameMembers(rec.Members, next) {
			return nil
		}
		gone := []string{}
		for _, m := range rec.Members {
			if !slices.ContainsFunc(next, func(n recording.Member) bool { return n.UserID == m.UserID }) {
				gone = append(gone, m.UserID)
			}
		}
		rec.Members = next
		if rec.AssigneeID != "" && !slices.Contains(rec.Audience(), rec.AssigneeID) {
			rec.AssigneeID = ""
		}
		s.scheduleMemberReminders(ctx, rec)
		err = s.save(ctx, rec)
		if errors.Is(err, ErrChanged) && attempt < maxChangeAttempts {
			continue
		}
		if err != nil {
			return err
		}
		if s.Events != nil && len(gone) > 0 {
			// Those no longer members drop the note.
			s.Events.Publish(gone, NoteEvent{Type: NoteChanged, ID: rec.ID})
		}
		break
	}
	if depth >= maxNoteDepth {
		return nil
	}
	children, err := s.childIDs(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range children {
		if err := s.syncMembersAt(ctx, c, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// inFolderMembers returns the members a note that is under no other note gets from the
// folder it is in. Boards aren't shared (they show notes the users may not have).
func (s *RecordingService) inFolderMembers(ctx context.Context, rec *recording.Recording) ([]recording.Member, error) {
	if rec.FolderID == "" || rec.IsBoard() || s.Folders == nil {
		return nil, nil
	}
	return folderMembers(ctx, s.Folders.repo, rec.OwnerID, rec.FolderID)
}

// childIDs lists the sub-notes of a note (also those in the trash).
func (s *RecordingService) childIDs(ctx context.Context, id string) ([]string, error) {
	rec, err := s.recs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	list, err := s.recs.List(ctx, recording.ListFilter{OwnerID: rec.OwnerID, ParentID: id, Trash: recording.TrashAny, Brief: true})
	if err != nil {
		return nil, err
	}
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.ID
	}
	return out, nil
}

// syncAll brings the members of the notes up to date (after they moved).
func (s *RecordingService) syncAll(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.syncMembers(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// scheduleMemberReminders sets when each member is reminded of the task, like the owner
// (scheduleReminder) but in the member's own time zone.
func (s *RecordingService) scheduleMemberReminders(ctx context.Context, rec *recording.Recording) {
	for i := range rec.Members {
		m := &rec.Members[i]
		m.RemindAt = s.reminderAt(rec, s.location(ctx, m.UserID))
	}
}

// scheduleReminders sets when the owner and each member is reminded of the task.
func (s *RecordingService) scheduleReminders(ctx context.Context, rec *recording.Recording) {
	s.scheduleReminder(rec, s.location(ctx, rec.OwnerID))
	s.scheduleMemberReminders(ctx, rec)
}

// reminderAt is when a reminder of the task is due in the time zone loc: only for an open
// task, only for a moment still to come (a date set in the past sends none), and not in the
// trash.
func (s *RecordingService) reminderAt(rec *recording.Recording, loc *time.Location) *time.Time {
	if rec.Done || rec.Due == nil || rec.DeletedAt != nil {
		return nil
	}
	if at := rec.Due.ReminderAt(loc); at != nil && at.After(s.clock()) {
		return at
	}
	return nil
}
