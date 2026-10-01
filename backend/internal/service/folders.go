package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// FolderInput creates, renames or moves a user's folder. ParentID is the folder it goes
// into; empty for the top level.
type FolderInput struct {
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}

// maxFolderDepth bounds how deeply folders can be nested.
const maxFolderDepth = 8

// FolderService manages the folders users sort their notes into.
type FolderService struct {
	repo  ports.FolderRepository
	recs  ports.RecordingRepository
	clock func() time.Time
	// Notes shares the notes in shared folders like the folders when they move or go.
	// Optional; without it folders aren't shared.
	Notes *RecordingService
	// Tablets finds the folder of a paired reMarkable, which can't be deleted. Optional.
	Tablets ports.TabletLinkRepository
}

// NewFolderService builds the service. recs is used to move notes out of deleted folders.
func NewFolderService(repo ports.FolderRepository, recs ports.RecordingRepository) *FolderService {
	return &FolderService{repo: repo, recs: recs, clock: time.Now}
}

// List returns the account's folders, by name, and the folders shared with the account
// with the folders in them. A shared folder is at the account's top level unless the
// folder it is in is shared with the account, too; each says what the account may do in it
// (Access) and whether it is shared (Shared).
func (s *FolderService) List(ctx context.Context, acc *Account) ([]*folder.Folder, error) {
	if acc.ID == "" {
		return []*folder.Folder{}, nil
	}
	own, err := s.repo.List(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	markShared(own)
	remarkableFolder := s.remarkableFolder(ctx, acc.ID)
	for _, f := range own {
		f.Access = recording.RoleOwner
		f.Remarkable = f.ID == remarkableFolder
	}
	shared, err := s.repo.ListSharedWith(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	ownByID := make(map[string]*folder.Folder, len(own))
	for _, f := range own {
		seen[f.ID] = true
		ownByID[f.ID] = f
	}
	out := own
	owners := map[string]bool{}
	for _, sf := range shared {
		if sf.OwnerID == acc.ID || owners[sf.OwnerID] {
			continue
		}
		owners[sf.OwnerID] = true
		all, err := s.repo.List(ctx, sf.OwnerID)
		if err != nil {
			return nil, err
		}
		byID := make(map[string]*folder.Folder, len(all))
		for _, f := range all {
			byID[f.ID] = f
		}
		// Each folder of the owner gets the highest role of the shares with the account on
		// it and the folders it is in.
		for _, f := range all {
			var role recording.Role
			for p, depth := f, 0; p != nil && depth <= maxFolderDepth; p, depth = byID[p.ParentID], depth+1 {
				if sh := p.Share(acc.ID); sh != nil && !role.AtLeast(sh.Role) {
					role = sh.Role
				}
			}
			if role == "" || seen[f.ID] {
				continue
			}
			seen[f.ID] = true
			c := *f
			c.Access, c.Shared, c.Shares, c.Placements = role, true, nil, nil
			if parent := byID[f.ParentID]; parent == nil || !inShared(byID, parent, acc.ID) {
				// Shown where the account filed it among its own folders, else at its top
				// level, after the account's own ordered folders.
				c.ParentID, c.Position, c.Movable = "", 0, true
				if pl := f.Placement(acc.ID); pl != nil {
					c.Position = pl.Position
					if pl.ParentID == "" || ownByID[pl.ParentID] != nil {
						c.ParentID = pl.ParentID
					}
				}
			}
			out = append(out, &c)
		}
	}
	return out, nil
}

// markShared marks the folders that are shared or in a shared folder.
func markShared(list []*folder.Folder) {
	byID := make(map[string]*folder.Folder, len(list))
	for _, f := range list {
		byID[f.ID] = f
	}
	for _, f := range list {
		for p, depth := f, 0; p != nil && depth <= maxFolderDepth; p, depth = byID[p.ParentID], depth+1 {
			if len(p.Shares) > 0 {
				f.Shared = true
				break
			}
		}
	}
}

// inShared reports whether folder f, or a folder it is in, is shared with the user.
func inShared(byID map[string]*folder.Folder, f *folder.Folder, userID string) bool {
	for p, depth := f, 0; p != nil && depth <= maxFolderDepth; p, depth = byID[p.ParentID], depth+1 {
		if p.Share(userID) != nil {
			return true
		}
	}
	return false
}

// Create adds a folder for the account's user.
func (s *FolderService) Create(ctx context.Context, acc *Account, in FolderInput) (*folder.Folder, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("folders belong to a user; sign in"))
	}
	if err := s.validate(ctx, acc.ID, "", &in); err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	f := &folder.Folder{ID: newID(), OwnerID: acc.ID, Name: in.Name, ParentID: in.ParentID, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.Create(ctx, f); err != nil {
		return nil, err
	}
	// Made in a shared folder, it shows up for the users it is shared with, too.
	members, err := s.members(ctx, f.OwnerID, f.ID)
	if err != nil {
		return nil, err
	}
	s.changed(f.OwnerID, members)
	return f, nil
}

// Update renames one of the account's folders or moves it into another folder.
func (s *FolderService) Update(ctx context.Context, acc *Account, id string, in FolderInput) (*folder.Folder, error) {
	f, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(f.OwnerID) {
		// Filing is all the recipient may do; the name is the owner's.
		if name := strings.Join(strings.Fields(in.Name), " "); name != "" && name != f.Name {
			return nil, ErrNotFound
		}
		return s.place(ctx, acc, f, strings.TrimSpace(in.ParentID))
	}
	if err := s.validate(ctx, f.OwnerID, id, &in); err != nil {
		return nil, err
	}
	moved := f.ParentID != in.ParentID
	if moved {
		// A folder moved elsewhere goes after the ordered folders there.
		f.Position = 0
	}
	before, err := s.members(ctx, f.OwnerID, id)
	if err != nil {
		return nil, err
	}
	f.Name, f.ParentID, f.UpdatedAt = in.Name, in.ParentID, s.clock().UTC()
	if err := s.repo.Update(ctx, f); err != nil {
		return nil, err
	}
	after, err := s.members(ctx, f.OwnerID, id)
	if err != nil {
		return nil, err
	}
	s.changed(f.OwnerID, slices.Concat(before, after))
	if moved {
		// Moved into or out of a shared folder, the notes in it are shared differently.
		if s.Notes != nil && (len(before) > 0 || len(after) > 0) {
			if err := s.Notes.folderChanged(ctx, f.OwnerID, id, before); err != nil {
				return nil, err
			}
		}
	}
	markShared([]*folder.Folder{f})
	f.Access = recording.RoleOwner
	return f, nil
}

// sharedRoot reports whether f, a folder of someone else, is shared with the account
// directly or through a folder, but not through a folder the account sees it in: the
// account's own tree is where such a folder can be filed.
func (s *FolderService) sharedRoot(ctx context.Context, acc *Account, f *folder.Folder) (bool, error) {
	if acc.ID == "" || acc.Owns(f.OwnerID) {
		return false, nil
	}
	all, err := s.repo.List(ctx, f.OwnerID)
	if err != nil {
		return false, err
	}
	byID := make(map[string]*folder.Folder, len(all))
	for _, x := range all {
		byID[x.ID] = x
	}
	if !inShared(byID, byID[f.ID], acc.ID) {
		return false, nil
	}
	parent := byID[f.ParentID]
	return parent == nil || !inShared(byID, parent, acc.ID), nil
}

// place files a folder shared with the account in one of the account's own folders (empty
// is the top level). Only the account sees the change; the folder stays where it is for
// its owner and everyone else.
func (s *FolderService) place(ctx context.Context, acc *Account, f *folder.Folder, parentID string) (*folder.Folder, error) {
	root, err := s.sharedRoot(ctx, acc, f)
	if err != nil {
		return nil, err
	}
	if !root {
		return nil, ErrNotFound
	}
	if parentID != "" {
		p, err := s.repo.Get(ctx, parentID)
		if err != nil || !acc.Owns(p.OwnerID) {
			return nil, invalid("unknown folder %q", parentID)
		}
	}
	pos := 0
	if pl := f.Placement(acc.ID); pl != nil && pl.ParentID == parentID {
		pos = pl.Position
	}
	f.SetPlacement(acc.ID, parentID, pos)
	if err := s.repo.Update(ctx, f); err != nil {
		return nil, err
	}
	s.changed(acc.ID, nil)
	return s.shownTo(ctx, acc, f.ID)
}

// shownTo returns folder id as List shows it to the account.
func (s *FolderService) shownTo(ctx context.Context, acc *Account, id string) (*folder.Folder, error) {
	list, err := s.List(ctx, acc)
	if err != nil {
		return nil, err
	}
	for _, f := range list {
		if f.ID == id {
			return f, nil
		}
	}
	return nil, ErrNotFound
}

// changed tells the open apps of the user and of the members that the folders they see
// changed, so that they load them again: a note filed in a folder an app doesn't know yet
// would otherwise show up at the top level.
func (s *FolderService) changed(userID string, members []recording.Member) {
	if s.Notes == nil || s.Notes.Events == nil {
		return
	}
	users := []string{userID}
	for _, m := range members {
		if !slices.Contains(users, m.UserID) {
			users = append(users, m.UserID)
		}
	}
	s.Notes.Events.Publish(users, NoteEvent{Type: FoldersChanged})
}

// members returns everyone the folder is shared with (see folderMembers).
func (s *FolderService) members(ctx context.Context, ownerID, id string) ([]recording.Member, error) {
	return folderMembers(ctx, s.repo, ownerID, id)
}

// maxReorder bounds how many folders or notes one reorder can place.
const maxReorder = 2000

// Reorder puts the account's folders ids, which must all be in the same place, in this
// order. Folders there that aren't listed follow them, by name.
func (s *FolderService) Reorder(ctx context.Context, acc *Account, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxReorder {
		return invalid("at most %d folders can be ordered at once", maxReorder)
	}
	type item struct {
		f      *folder.Folder
		parent string
		mine   bool
	}
	list := make([]item, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return invalid("folder %q is listed twice", id)
		}
		seen[id] = true
		f, err := s.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		it := item{f: f, parent: f.ParentID, mine: acc.Owns(f.OwnerID)}
		if !it.mine {
			root, err := s.sharedRoot(ctx, acc, f)
			if err != nil {
				return err
			}
			if !root {
				return ErrNotFound
			}
			it.parent = ""
			if pl := f.Placement(acc.ID); pl != nil {
				it.parent = pl.ParentID
			}
		}
		if len(list) > 0 && it.parent != list[0].parent {
			return invalid("the folders to order must be in the same place")
		}
		list = append(list, it)
	}
	now := s.clock().UTC()
	var members []recording.Member
	for i, it := range list {
		f := it.f
		if it.mine {
			if f.Position == i+1 {
				continue
			}
			f.Position, f.UpdatedAt = i+1, now
		} else {
			if pl := f.Placement(acc.ID); pl != nil && pl.Position == i+1 {
				continue
			}
			f.SetPlacement(acc.ID, it.parent, i+1)
		}
		if err := s.repo.Update(ctx, f); err != nil {
			return err
		}
		if it.mine {
			m, err := s.members(ctx, f.OwnerID, f.ID)
			if err != nil {
				return err
			}
			members = append(members, m...)
		}
	}
	s.changed(acc.ID, members)
	return nil
}

// Delete removes one of the account's folders. Its notes and folders move up into the
// folder it was in, so nothing is lost. The folder of a paired reMarkable's documents stays.
func (s *FolderService) Delete(ctx context.Context, acc *Account, id string) error {
	f, err := s.own(ctx, acc, id)
	if err != nil {
		return err
	}
	if s.remarkableFolder(ctx, f.OwnerID) == id {
		return errors.Join(ErrForbidden, errors.New("the reMarkable folder can't be deleted while a reMarkable is paired"))
	}
	before, err := s.members(ctx, f.OwnerID, id)
	if err != nil {
		return err
	}
	all, err := s.repo.List(ctx, f.OwnerID)
	if err != nil {
		return err
	}
	for _, c := range all {
		if c.ParentID != id {
			continue
		}
		c.ParentID, c.Position, c.UpdatedAt = f.ParentID, 0, s.clock().UTC()
		if err := s.repo.Update(ctx, c); err != nil {
			return err
		}
	}
	if err := s.recs.MoveFolder(ctx, f.OwnerID, id, f.ParentID); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.changed(f.OwnerID, before)
	if s.Notes != nil && len(before) > 0 {
		// The notes and folders moved up are shared like the folder they moved to.
		return s.Notes.folderChanged(ctx, f.OwnerID, f.ParentID, before)
	}
	return nil
}

// remarkableFolder returns the folder of the user's paired reMarkable's documents, or ""
// when no reMarkable is paired.
func (s *FolderService) remarkableFolder(ctx context.Context, userID string) string {
	if s.Tablets == nil {
		return ""
	}
	l, err := s.Tablets.Get(ctx, userID)
	if err != nil {
		return ""
	}
	return l.FolderID
}

// Usable reports whether a note of ownerID may be put into the folder ("" is the top level).
func (s *FolderService) Usable(ctx context.Context, ownerID, id string) bool {
	if id == "" {
		return true
	}
	if s == nil {
		return false
	}
	f, err := s.repo.Get(ctx, id)
	return err == nil && f.OwnerID == ownerID
}

func (s *FolderService) own(ctx context.Context, acc *Account, id string) (*folder.Folder, error) {
	f, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(f.OwnerID) {
		return nil, ErrNotFound
	}
	return f, nil
}

// validate normalizes the input and checks it: names are unique among the folders in the
// same place (ignoring case), the parent is one of the user's folders, a folder can't go
// into itself or one of its own folders, and nesting is bounded.
func (s *FolderService) validate(ctx context.Context, ownerID, id string, in *FolderInput) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.ParentID = strings.TrimSpace(in.ParentID)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return invalid("name must be 1-80 characters")
	}
	all, err := s.repo.List(ctx, ownerID)
	if err != nil {
		return err
	}
	byID := make(map[string]*folder.Folder, len(all))
	for _, f := range all {
		byID[f.ID] = f
	}
	depth := 1
	for p := in.ParentID; p != ""; p = byID[p].ParentID {
		if p == id {
			return invalid("a folder can't be moved into itself")
		}
		if _, ok := byID[p]; !ok {
			return invalid("unknown folder %q", p)
		}
		if depth++; depth > maxFolderDepth {
			return invalid("folders can be nested at most %d deep", maxFolderDepth)
		}
	}
	for _, f := range all {
		if f.ID != id && f.ParentID == in.ParentID && strings.EqualFold(f.Name, in.Name) {
			return invalid("a folder named %q exists here", f.Name)
		}
	}
	return nil
}
