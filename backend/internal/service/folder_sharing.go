package service

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// A folder is shared like a note: its owner shares it with users by email, as viewers or
// editors, and it is then shared together with everything in it: the notes in it (with
// their sub-notes) and its folders, at any depth. The notes get the folder's users as
// members (see syncMembers), so everything that works for a shared note works for them.
// The users see the folder in their folder tree (at the top level), with its folders and
// notes; editors can also add notes to it, which belong to the folder's owner.

// folderMembers returns everyone the folder id of ownerID is shared with: the users of its
// own shares and of the folders it is in, each with the highest role they are given. They
// are the members the notes in the folder get from it (not Root: they see the notes in the
// folder). A folder of someone else, or none, has none.
func folderMembers(ctx context.Context, repo ports.FolderRepository, ownerID, id string) ([]recording.Member, error) {
	roles := map[string]recording.Role{}
	for depth := 0; id != "" && depth <= maxFolderDepth; depth++ {
		f, err := repo.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		if f.OwnerID != ownerID {
			break
		}
		for _, sh := range f.Shares {
			if !roles[sh.UserID].AtLeast(sh.Role) {
				roles[sh.UserID] = sh.Role
			}
		}
		id = f.ParentID
	}
	if len(roles) == 0 {
		return nil, nil
	}
	out := make([]recording.Member, 0, len(roles))
	for u, r := range roles {
		out = append(out, recording.Member{UserID: u, Role: r})
	}
	slices.SortFunc(out, func(a, b recording.Member) int { return strings.Compare(a.UserID, b.UserID) })
	return out, nil
}

// folderRole is what the account may do in folder f: everything as its owner, what the
// folder (or one it is in) is shared with the account for, or nothing ("").
func folderRole(ctx context.Context, repo ports.FolderRepository, acc *Account, f *folder.Folder) (recording.Role, error) {
	if acc.Owns(f.OwnerID) {
		return recording.RoleOwner, nil
	}
	members, err := folderMembers(ctx, repo, f.OwnerID, f.ID)
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if m.UserID == acc.ID {
			return m.Role, nil
		}
	}
	return "", nil
}

// loadFolder returns a folder the account may see, and what the account may do in it.
// Folders the account can't see are not found.
func (s *RecordingService) loadFolder(ctx context.Context, acc *Account, id string, need recording.Role) (*folder.Folder, recording.Role, error) {
	if s.Folders == nil {
		return nil, "", ErrNotFound
	}
	f, err := s.Folders.repo.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	role, err := folderRole(ctx, s.Folders.repo, acc, f)
	if err != nil {
		return nil, "", err
	}
	if role == "" {
		return nil, "", ErrNotFound
	}
	if !role.AtLeast(need) {
		if need == recording.RoleOwner {
			return nil, "", errors.Join(ErrForbidden, errors.New("only the owner of the folder can do this"))
		}
		return nil, "", errors.Join(ErrForbidden, errors.New("the folder is shared with you for viewing only"))
	}
	return f, role, nil
}

// FolderSharing returns who the folder is shared with.
func (s *RecordingService) FolderSharing(ctx context.Context, acc *Account, id string) (*Sharing, error) {
	f, role, err := s.loadFolder(ctx, acc, id, recording.RoleViewer)
	if err != nil {
		return nil, err
	}
	members, err := folderMembers(ctx, s.Folders.repo, f.OwnerID, f.ID)
	if err != nil {
		return nil, err
	}
	out := &Sharing{Owner: ShareUser{UserID: f.OwnerID, Email: s.email(ctx, f.OwnerID), Role: recording.RoleOwner},
		Members: []ShareUser{}, Access: role}
	for _, m := range members {
		out.Members = append(out.Members, ShareUser{UserID: m.UserID, Email: s.email(ctx, m.UserID), Role: m.Role,
			Inherited: f.Share(m.UserID) == nil})
	}
	return out, nil
}

// ShareFolder shares the folder and everything in it with a user, or changes what the user
// may do with it. Only the owner shares.
func (s *RecordingService) ShareFolder(ctx context.Context, acc *Account, id string, in ShareInput) (*Sharing, error) {
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
	return s.setFolderShare(ctx, acc, id, u.ID, in.Role)
}

// SetFolderShareRole changes what a user the folder is shared with may do in it.
func (s *RecordingService) SetFolderShareRole(ctx context.Context, acc *Account, id, userID string, role recording.Role) (*Sharing, error) {
	if !role.Valid() {
		return nil, invalid("role must be %q or %q", recording.RoleViewer, recording.RoleEditor)
	}
	f, _, err := s.loadFolder(ctx, acc, id, recording.RoleOwner)
	if err != nil {
		return nil, err
	}
	if f.Share(userID) == nil {
		return nil, invalid("the folder is not shared with this user here; change it on the folder it is shared through")
	}
	return s.setFolderShare(ctx, acc, id, userID, role)
}

func (s *RecordingService) setFolderShare(ctx context.Context, acc *Account, id, userID string, role recording.Role) (*Sharing, error) {
	f, _, err := s.loadFolder(ctx, acc, id, recording.RoleOwner)
	if err != nil {
		return nil, err
	}
	if userID == f.OwnerID {
		return nil, invalid("the folder is yours already")
	}
	before, err := folderMembers(ctx, s.Folders.repo, f.OwnerID, f.ID)
	if err != nil {
		return nil, err
	}
	if sh := f.Share(userID); sh != nil {
		sh.Role = role
	} else {
		if len(f.Shares) >= maxShares {
			return nil, invalid("a folder can be shared with at most %d users", maxShares)
		}
		f.Shares = append(f.Shares, recording.Share{UserID: userID, Role: role, CreatedAt: s.clock().UTC()})
	}
	f.UpdatedAt = s.clock().UTC()
	if err := s.Folders.repo.Update(ctx, f); err != nil {
		return nil, err
	}
	if err := s.folderChanged(ctx, f.OwnerID, f.ID, before); err != nil {
		return nil, err
	}
	return s.FolderSharing(ctx, acc, id)
}

// UnshareFolder stops sharing the folder with a user. The owner can take anyone off; a
// member can take themselves off a folder shared with them (leave it). It returns nil when
// the account left the folder.
func (s *RecordingService) UnshareFolder(ctx context.Context, acc *Account, id, userID string) (*Sharing, error) {
	need := recording.RoleOwner
	if userID == acc.ID {
		need = recording.RoleViewer
	}
	f, _, err := s.loadFolder(ctx, acc, id, need)
	if err != nil {
		return nil, err
	}
	before, err := folderMembers(ctx, s.Folders.repo, f.OwnerID, f.ID)
	if err != nil {
		return nil, err
	}
	if f.Share(userID) == nil {
		if slices.ContainsFunc(before, func(m recording.Member) bool { return m.UserID == userID }) {
			return nil, invalid("the folder is shared through a folder it is in; stop sharing that one")
		}
		return nil, ErrNotFound
	}
	f.Shares = slices.DeleteFunc(f.Shares, func(sh recording.Share) bool { return sh.UserID == userID })
	f.UpdatedAt = s.clock().UTC()
	if err := s.Folders.repo.Update(ctx, f); err != nil {
		return nil, err
	}
	if err := s.folderChanged(ctx, f.OwnerID, f.ID, before); err != nil {
		return nil, err
	}
	out, err := s.FolderSharing(ctx, acc, id)
	if errors.Is(err, ErrNotFound) && userID == acc.ID {
		return nil, nil
	}
	return out, err
}

// folderChanged brings the members of the notes in the folder id of ownerID (and in its
// folders) up to date after the folder was shared differently or moved; id "" is all the
// owner's folders. The users who had (before) or have the folder load their notes and
// folders again.
func (s *RecordingService) folderChanged(ctx context.Context, ownerID, id string, before []recording.Member) error {
	if err := s.syncFolderTree(ctx, ownerID, id); err != nil {
		return err
	}
	after, err := folderMembers(ctx, s.Folders.repo, ownerID, id)
	if err != nil {
		return err
	}
	if s.Events != nil {
		users := []string{ownerID}
		for _, m := range slices.Concat(before, after) {
			if !slices.Contains(users, m.UserID) {
				users = append(users, m.UserID)
			}
		}
		s.Events.Publish(users, NoteEvent{Type: NotesReload})
	}
	return nil
}

// syncFolderTree brings the members of the owner's notes in the folder id and its folders
// up to date, with the notes under them; id "" is all the owner's notes.
func (s *RecordingService) syncFolderTree(ctx context.Context, ownerID, id string) error {
	in := map[string]bool{id: true}
	if id != "" {
		folders, err := s.Folders.repo.List(ctx, ownerID)
		if err != nil {
			return err
		}
		for changed := true; changed; {
			changed = false
			for _, f := range folders {
				if !in[f.ID] && in[f.ParentID] {
					in[f.ID], changed = true, true
				}
			}
		}
	}
	notes, err := s.recs.List(ctx, recording.ListFilter{OwnerID: ownerID, Trash: recording.TrashAny, Brief: true})
	if err != nil {
		return err
	}
	for _, r := range notes {
		if r.ParentID == "" && (id == "" || in[r.FolderID]) {
			if err := s.syncMembers(ctx, r.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
