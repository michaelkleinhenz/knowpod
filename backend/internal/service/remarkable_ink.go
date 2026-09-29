package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/tablet"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
)

// --- handwriting on the copies of text notes ---
//
// What is written or drawn on a note's copy on the tablet is kept in the cloud as page files
// of strokes. A pull renders those strokes to a PDF, on blank pages (the text of the note
// isn't part of it), and keeps it as an attachment of the note, replacing the earlier one
// when the handwriting changed.

// inkName is the name of the attachment with the handwriting.
const inkName = "reMarkable scribbles.pdf"

// pullInk looks at the handwriting on the copy d of the note when the document changed since
// it was last looked at. Failures are logged and tried again with the next pull.
func (s *RemarkableService) pullInk(ctx context.Context, sess *remarkable.Session, l *tablet.Link, d tablet.Item, note *recording.Recording) {
	c := note.Tablet
	if c == nil || c.Removed || d.ContentHash == "" || c.InkHash == d.ContentHash {
		return
	}
	if err := s.readInk(ctx, sess, d, note); err != nil {
		s.log.Warn("reading what was written on a reMarkable note failed", "user", l.UserID, "note", note.ID, "err", err)
	}
}

func (s *RemarkableService) readInk(ctx context.Context, sess *remarkable.Session, d tablet.Item, note *recording.Recording) error {
	it, err := sess.ReadItem(ctx, remarkable.Entry{ID: d.ID, Hash: d.Hash})
	if err != nil {
		return err
	}
	var pages [][]remarkable.Stroke
	if hasInk(it) {
		path := s.spool.DownloadPath("ink-" + note.ID)
		defer os.Remove(path)
		if _, err := sess.Download(ctx, it, path, s.maxSize); err != nil {
			return err
		}
		a, err := remarkable.OpenArchive(path)
		if err != nil {
			return err
		}
		defer a.Close()
		if pages, err = a.Ink(); err != nil {
			return err
		}
	}
	var pdf []byte
	if len(pages) > 0 {
		var buf bytes.Buffer
		if err := remarkable.WritePDF(&buf, pages, inkName); err != nil {
			return err
		}
		pdf = buf.Bytes()
	}
	hash := d.ContentHash
	return s.changeNote(ctx, note.ID, func(rec *recording.Recording) (bool, error) {
		c := recording.TabletCopy{}
		if rec.Tablet != nil {
			c = *rec.Tablet
		}
		c.InkHash = hash
		at := -1
		for i, a := range rec.Attachments {
			if a.ID == c.InkAttachment && a.ID != "" {
				at = i
			}
		}
		switch {
		case pdf == nil && at >= 0:
			// Everything written was erased.
			if err := s.objects.Delete(ctx, rec.Attachments[at].Key); err != nil && !errors.Is(err, ErrNotFound) {
				return false, err
			}
			rec.Attachments = append(rec.Attachments[:at:at], rec.Attachments[at+1:]...)
			c.InkAttachment = ""
		case pdf != nil:
			att := recording.Attachment{ID: newID(), Name: inkName, ContentType: "application/pdf"}
			if at >= 0 {
				att = rec.Attachments[at]
			}
			att.Size = int64(len(pdf))
			if att.Key == "" {
				att.Key = fmt.Sprintf("recordings/%s/%s/attachments/%s.pdf", rec.OwnerID, rec.ID, att.ID)
			}
			if err := s.objects.Put(ctx, att.Key, bytes.NewReader(pdf), att.Size, att.ContentType); err != nil {
				return false, err
			}
			if at >= 0 {
				rec.Attachments[at] = att
			} else {
				rec.Attachments = append(rec.Attachments[:len(rec.Attachments):len(rec.Attachments)], att)
			}
			c.InkAttachment = att.ID
		}
		rec.Tablet = &c
		return true, nil
	})
}

// hasInk reports whether the document has page files, i.e. something was written on it.
func hasInk(it *remarkable.Item) bool {
	for _, f := range it.Files {
		if strings.HasSuffix(f.ID, ".rm") {
			return true
		}
	}
	return false
}

// changeNote changes a stored note with fn, which reports whether it changed anything; the
// change is applied again to a newer copy of the note when someone changed it meanwhile. A
// note deleted meanwhile is skipped.
func (s *RemarkableService) changeNote(ctx context.Context, id string, fn func(rec *recording.Recording) (bool, error)) error {
	for attempt := 1; ; attempt++ {
		rec, err := s.recs.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if changed, err := fn(rec); err != nil || !changed {
			return err
		}
		err = s.recs.Update(ctx, rec)
		if errors.Is(err, ErrChanged) && attempt < maxChangeAttempts {
			continue
		}
		return err
	}
}
