package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/tablet"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
)

// --- sending text notes to the tablet ---
//
// Text notes in the reMarkable folder (or a folder inside it) are sent to the tablet as
// EPUBs, into the cloud folder the knowpod folder mirrors (the top level for the reMarkable
// folder itself and for folders made in knowpod). A note's copy follows its title and text
// and its moves between these folders; when the note leaves them or goes into the trash,
// the copy goes into the tablet's trash, and comes back with the note. What is written on
// the copy on the tablet stays on the tablet. Pulls leave these documents out.

// Sends wait until a user's notes were left alone for pushDelay (typing saves the text
// every few seconds), but at most pushMaxDelay after the first change.
const (
	pushDelay    = 20 * time.Second
	pushMaxDelay = 2 * time.Minute
	pushTimeout  = 5 * time.Minute
)

// pendingPush is a user's send waiting to start.
type pendingPush struct {
	timer *time.Timer
	first time.Time
}

// Nudge says that some of the user's notes changed: a send of the user's text notes starts
// once they were left alone for a moment. Users without a paired reMarkable are skipped
// then.
func (s *RemarkableService) Nudge(userID string) {
	if userID == "" || s.pushDelay <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	now := s.clock()
	if p, ok := s.pending[userID]; ok {
		if now.Sub(p.first)+s.pushDelay <= pushMaxDelay {
			p.timer.Reset(s.pushDelay)
		}
		return
	}
	p := &pendingPush{first: now}
	p.timer = time.AfterFunc(s.pushDelay, func() {
		s.mu.Lock()
		delete(s.pending, userID)
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()
		if err := s.PushUser(ctx, userID); err != nil {
			s.log.Warn("sending notes to the reMarkable failed", "user", userID, "err", err)
		}
	})
	s.pending[userID] = p
}

// StopNudges cancels the waiting sends and ignores further nudges (on shutdown).
func (s *RemarkableService) StopNudges() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	for id, p := range s.pending {
		p.timer.Stop()
		delete(s.pending, id)
	}
}

// PushUser sends the user's changed text notes now, if the user has a paired reMarkable.
func (s *RemarkableService) PushUser(ctx context.Context, userID string) error {
	defer s.lock(userID)()
	l, err := s.links.Get(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.push(ctx, l)
}

// tabletSend is a note's copy to write, and what the note records once it was written.
type tabletSend struct {
	id       string
	revision int64
	write    remarkable.DocumentWrite
}

// push sends the link's user's text notes whose copy on the tablet is out of date. The
// caller holds the user's lock.
func (s *RemarkableService) push(ctx context.Context, l *tablet.Link) error {
	if l.FolderID == "" {
		return nil // nothing was pulled yet, so there is no reMarkable folder
	}
	place, err := s.cloudFolders(ctx, l)
	if err != nil {
		return err
	}
	notes, err := s.recs.List(ctx, recording.ListFilter{OwnerID: l.UserID, Trash: recording.TrashAny, Brief: true})
	if err != nil {
		return err
	}
	var sends []tabletSend
	for _, r := range notes {
		if r.Type != recording.TypeText || r.OwnerID != l.UserID {
			continue
		}
		parent, in := place(r.FolderID)
		in = in && r.ParentID == "" && r.DeletedAt == nil && r.Summary != nil
		c := r.Tablet
		switch {
		case in:
			name := documentName(r.Summary.Title)
			if c != nil && !c.Removed && c.Error == "" && c.Revision == r.Revision && c.Name == name && c.Parent == parent && c.Scaled {
				continue
			}
			full, err := s.recs.Get(ctx, r.ID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if full.Summary == nil {
				continue
			}
			lang := full.Summary.Language
			if lang == "auto" {
				lang = ""
			}
			epub, err := remarkable.NoteEPUB(full.ID, name, full.Summary.Markdown, lang, full.CreatedAt)
			if err != nil {
				return err
			}
			w := remarkable.DocumentWrite{Name: name, Parent: parent, EPUB: epub}
			if c != nil {
				w.ID = c.DocumentID
			}
			if c == nil || !c.Scaled {
				w.TextScale = remarkable.TextScale
			}
			sends = append(sends, tabletSend{id: r.ID, revision: full.Revision, write: w})
		case c != nil && !c.Removed && c.DocumentID != "":
			// Taken off the tablet: into its trash, so it comes back with the note.
			sends = append(sends, tabletSend{id: r.ID, revision: c.Revision,
				write: remarkable.DocumentWrite{ID: c.DocumentID, Name: c.Name, Parent: remarkable.TrashParent}})
		}
	}
	if len(sends) == 0 {
		return nil
	}

	writes := make([]remarkable.DocumentWrite, len(sends))
	for i, snd := range sends {
		writes[i] = snd.write
	}
	var ids []string
	sess, err := s.cloud.Open(ctx, l.DeviceToken)
	if err == nil {
		ids, err = sess.WriteDocuments(ctx, writes, s.clock().UTC())
	}
	if err != nil {
		err = cloudErr(err)
		for _, snd := range sends {
			msg := err.Error()
			if serr := s.setTablet(ctx, snd.id, func(c *recording.TabletCopy) bool {
				if c.Error == msg {
					return false
				}
				c.Error = msg
				return true
			}); serr != nil {
				return serr
			}
		}
		return err
	}
	now := s.clock().UTC()
	for i, snd := range sends {
		id := ids[i]
		if err := s.setTablet(ctx, snd.id, func(c *recording.TabletCopy) bool {
			if id != "" {
				c.DocumentID = id
			}
			c.Revision, c.Name, c.Parent = snd.revision, snd.write.Name, snd.write.Parent
			c.Removed = snd.write.Parent == remarkable.TrashParent
			c.Scaled = c.Scaled || snd.write.TextScale > 0
			c.SentAt, c.Error = &now, ""
			return true
		}); err != nil {
			return err
		}
	}
	s.log.Info("notes sent to the reMarkable", "user", l.UserID, "notes", len(sends))
	return nil
}

// setTablet changes a note's tablet copy (made when missing) with fn, which reports
// whether it changed anything. A note deleted meanwhile is skipped.
func (s *RemarkableService) setTablet(ctx context.Context, id string, fn func(c *recording.TabletCopy) bool) error {
	for attempt := 1; ; attempt++ {
		rec, err := s.recs.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		c := recording.TabletCopy{}
		if rec.Tablet != nil {
			c = *rec.Tablet
		}
		if !fn(&c) {
			return nil
		}
		rec.Tablet = &c
		err = s.recs.Update(ctx, rec)
		if errors.Is(err, ErrChanged) && attempt < maxChangeAttempts {
			continue
		}
		return err
	}
}

// cloudFolders returns where a note of a knowpod folder goes on the tablet: in reports
// whether the folder is the reMarkable folder or inside it, and parent is the cloud folder
// mirrored by it or by the nearest folder it is in ("" for the top level).
func (s *RemarkableService) cloudFolders(ctx context.Context, l *tablet.Link) (func(folderID string) (parent string, in bool), error) {
	all, err := s.folders.List(ctx, l.UserID)
	if err != nil {
		return nil, err
	}
	parentOf := make(map[string]string, len(all))
	for _, f := range all {
		parentOf[f.ID] = f.ParentID
	}
	cloudOf := make(map[string]string, len(l.Folders))
	for dir, kp := range l.Folders {
		cloudOf[kp] = dir
	}
	return func(folderID string) (string, bool) {
		parent, found := "", false
		for depth, id := 0, folderID; id != "" && depth < 64; depth++ {
			if dir, ok := cloudOf[id]; ok && !found {
				parent, found = dir, true
			}
			if id == l.FolderID {
				return parent, true
			}
			id = parentOf[id]
		}
		return "", false
	}, nil
}

// documentName is the name of a note's document on the tablet.
func documentName(title string) string {
	name := tablet.NormalizeName(title)
	if r := []rune(name); len(r) > 200 {
		name = strings.TrimSpace(string(r[:200]))
	}
	if name == "" {
		return "Untitled"
	}
	return name
}

// sentDocuments returns the user's text notes that have a copy in the cloud, by the copy's
// document ID.
func (s *RemarkableService) sentDocuments(ctx context.Context, userID string) (map[string]*recording.Recording, error) {
	notes, err := s.recs.List(ctx, recording.ListFilter{OwnerID: userID, Trash: recording.TrashAny, Brief: true})
	if err != nil {
		return nil, err
	}
	out := map[string]*recording.Recording{}
	for _, r := range notes {
		if r.Tablet != nil && r.Tablet.DocumentID != "" {
			out[r.Tablet.DocumentID] = r
		}
	}
	return out, nil
}
