package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// copySuffix is appended to the title or name of a duplicate.
const copySuffix = " (copy)"

// withCopySuffix returns name with the suffix, cut so that it stays within max characters.
func withCopySuffix(name string, max int) string {
	if utf8.RuneCountInString(name)+len(copySuffix) > max {
		name = string([]rune(name)[:max-len(copySuffix)])
	}
	return name + copySuffix
}

// Duplicate copies one of the account's notes, with its sub-notes, next to the original.
// The copy holds the title and text, labels and pictures; files, sharing, the public link,
// the copy on the tablet, due dates and the time logged aren't copied. A note without
// text of its own (a recording, a document) is copied as a text note of its summary.
func (s *RecordingService) Duplicate(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	src, _, err := s.load(ctx, acc, id, recording.RoleOwner)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(src.OwnerID) {
		return nil, ErrNotFound
	}
	byParent, err := s.notesByParent(ctx, src.OwnerID)
	if err != nil {
		return nil, err
	}
	return s.copyTree(ctx, byParent, src, src.FolderID, src.ParentID, true)
}

// notesByParent returns the owner's notes (not in the trash) that are sub-notes, by parent.
func (s *RecordingService) notesByParent(ctx context.Context, ownerID string) (map[string][]*recording.Recording, error) {
	all, err := s.recs.List(ctx, recording.ListFilter{OwnerID: ownerID})
	if err != nil {
		return nil, err
	}
	byParent := map[string][]*recording.Recording{}
	for _, n := range all {
		if n.ParentID != "" {
			byParent[n.ParentID] = append(byParent[n.ParentID], n)
		}
	}
	return byParent, nil
}

// copyTree copies a note and its sub-notes into the folder or under the parent note.
func (s *RecordingService) copyTree(ctx context.Context, byParent map[string][]*recording.Recording, src *recording.Recording, folderID, parentID string, rename bool) (*recording.Recording, error) {
	cp, err := s.copyNote(ctx, src, folderID, parentID, rename)
	if err != nil {
		return nil, err
	}
	for _, sub := range byParent[src.ID] {
		if _, err := s.copyTree(ctx, byParent, sub, "", cp.ID, false); err != nil {
			return nil, err
		}
	}
	return cp, nil
}

// copyNote copies one note.
func (s *RecordingService) copyNote(ctx context.Context, src *recording.Recording, folderID, parentID string, rename bool) (*recording.Recording, error) {
	title, markdown := "", ""
	if src.Summary != nil {
		title, markdown = src.Summary.Title, src.Summary.Markdown
	} else {
		title = src.Title
	}
	if title = strings.TrimSpace(title); title == "" {
		title = "Note"
	}
	if rename {
		title = withCopySuffix(title, 200)
	}
	id := newID()
	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: id, OwnerID: src.OwnerID, DeviceID: recording.TextDeviceID(src.OwnerID), ClientID: id,
		Type: recording.TypeText, Status: recording.StatusSummarized, ParentID: parentID, FolderID: folderID,
		Labels: append([]string(nil), src.Labels...), Priority: src.Priority, Estimate: src.Estimate, Template: src.Template,
		NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	if src.IsBoard() && src.Board != nil {
		rec.Type = recording.TypeBoard
		b := *src.Board
		rec.Board = &b
	}
	// The text refers to the pictures by the note's ID and theirs; the copies keep their
	// IDs, so only the note's ID changes.
	for _, obj := range src.Images {
		cpy, err := s.copyObject(ctx, obj, src.OwnerID, id)
		if err != nil {
			return nil, err
		}
		rec.Images = append(rec.Images, cpy)
	}
	markdown = strings.ReplaceAll(markdown, "/recordings/"+src.ID+"/images/", "/recordings/"+id+"/images/")
	rec.Summary = &recording.Summary{Title: title, Markdown: markdown, CreatedAt: now}
	if parentID != "" {
		parent, err := s.recs.Get(ctx, parentID)
		if err != nil {
			return nil, err
		}
		rec.Members = recording.ComputeMembers(parent.Members, nil, nil)
	} else {
		inherited, err := s.inFolderMembers(ctx, rec)
		if err != nil {
			return nil, err
		}
		rec.Members = recording.ComputeMembers(inherited, nil, nil)
	}
	if err := s.recs.Create(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// copyObject stores a copy of a note's picture for the note newID.
func (s *RecordingService) copyObject(ctx context.Context, obj recording.Object, ownerID, newID string) (recording.Object, error) {
	body, err := s.objects.Get(ctx, obj.Key, 0, -1)
	if err != nil {
		// A picture the note lists but that is gone is left out of the copy.
		return recording.Object{}, nil
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return recording.Object{}, fmt.Errorf("read %s: %w", obj.Key, err)
	}
	key := fmt.Sprintf("recordings/%s/%s/images/%s", ownerID, newID, path.Base(obj.Key))
	if err := s.objects.Put(ctx, key, bytes.NewReader(data), int64(len(data)), obj.ContentType); err != nil {
		return recording.Object{}, fmt.Errorf("store %s: %w", key, err)
	}
	return recording.Object{Key: key, ContentType: obj.ContentType, Size: int64(len(data))}, nil
}

// Duplicate copies one of the account's folders with the folders and notes in it, next to
// the original. Sharing isn't copied. The folder of a paired reMarkable can't be copied.
func (s *FolderService) Duplicate(ctx context.Context, acc *Account, id string) (*folder.Folder, error) {
	src, err := s.own(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if s.Notes == nil {
		return nil, errors.New("duplicating folders needs the notes")
	}
	if s.remarkableFolder(ctx, src.OwnerID) == id {
		return nil, errors.Join(ErrForbidden, errors.New("the reMarkable folder can't be duplicated while a reMarkable is paired"))
	}
	all, err := s.repo.List(ctx, src.OwnerID)
	if err != nil {
		return nil, err
	}
	children := map[string][]*folder.Folder{}
	taken := map[string]bool{}
	for _, f := range all {
		children[f.ParentID] = append(children[f.ParentID], f)
		if f.ParentID == src.ParentID {
			taken[strings.ToLower(f.Name)] = true
		}
	}
	notes, err := s.recs.List(ctx, recording.ListFilter{OwnerID: src.OwnerID})
	if err != nil {
		return nil, err
	}
	inFolder := map[string][]*recording.Recording{}
	byParent := map[string][]*recording.Recording{}
	for _, n := range notes {
		switch {
		case n.ParentID != "":
			byParent[n.ParentID] = append(byParent[n.ParentID], n)
		case n.FolderID != "":
			inFolder[n.FolderID] = append(inFolder[n.FolderID], n)
		}
	}
	name := withCopySuffix(src.Name, 80)
	for i := 2; taken[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s %d", withCopySuffix(src.Name, 70), i)
	}
	var copyFolder func(f *folder.Folder, parentID, name string) (*folder.Folder, error)
	copyFolder = func(f *folder.Folder, parentID, name string) (*folder.Folder, error) {
		now := s.clock().UTC()
		cp := &folder.Folder{ID: newID(), OwnerID: f.OwnerID, Name: name, ParentID: parentID, Position: 0, CreatedAt: now, UpdatedAt: now}
		if err := s.repo.Create(ctx, cp); err != nil {
			return nil, err
		}
		for _, n := range inFolder[f.ID] {
			if _, err := s.Notes.copyTree(ctx, byParent, n, cp.ID, "", false); err != nil {
				return nil, err
			}
		}
		for _, c := range children[f.ID] {
			if _, err := copyFolder(c, cp.ID, c.Name); err != nil {
				return nil, err
			}
		}
		return cp, nil
	}
	cp, err := copyFolder(src, src.ParentID, name)
	if err != nil {
		return nil, err
	}
	members, err := s.members(ctx, cp.OwnerID, cp.ID)
	if err != nil {
		return nil, err
	}
	s.changed(cp.OwnerID, members)
	markShared([]*folder.Folder{cp})
	cp.Access = recording.RoleOwner
	return cp, nil
}
