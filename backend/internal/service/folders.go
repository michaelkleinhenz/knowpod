package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
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
}

// NewFolderService builds the service. recs is used to move notes out of deleted folders.
func NewFolderService(repo ports.FolderRepository, recs ports.RecordingRepository) *FolderService {
	return &FolderService{repo: repo, recs: recs, clock: time.Now}
}

// List returns the account's folders, by name.
func (s *FolderService) List(ctx context.Context, acc *Account) ([]*folder.Folder, error) {
	if acc.ID == "" {
		return []*folder.Folder{}, nil
	}
	return s.repo.List(ctx, acc.ID)
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
	return f, nil
}

// Update renames one of the account's folders or moves it into another folder.
func (s *FolderService) Update(ctx context.Context, acc *Account, id string, in FolderInput) (*folder.Folder, error) {
	f, err := s.own(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if err := s.validate(ctx, f.OwnerID, id, &in); err != nil {
		return nil, err
	}
	if f.ParentID != in.ParentID {
		// A folder moved elsewhere goes after the ordered folders there.
		f.Position = 0
	}
	f.Name, f.ParentID, f.UpdatedAt = in.Name, in.ParentID, s.clock().UTC()
	if err := s.repo.Update(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
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
	list := make([]*folder.Folder, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return invalid("folder %q is listed twice", id)
		}
		seen[id] = true
		f, err := s.own(ctx, acc, id)
		if err != nil {
			return err
		}
		if len(list) > 0 && f.ParentID != list[0].ParentID {
			return invalid("the folders to order must be in the same place")
		}
		list = append(list, f)
	}
	now := s.clock().UTC()
	for i, f := range list {
		if f.Position == i+1 {
			continue
		}
		f.Position, f.UpdatedAt = i+1, now
		if err := s.repo.Update(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes one of the account's folders. Its notes and folders move up into the
// folder it was in, so nothing is lost.
func (s *FolderService) Delete(ctx context.Context, acc *Account, id string) error {
	f, err := s.own(ctx, acc, id)
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
	return s.repo.Delete(ctx, id)
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
