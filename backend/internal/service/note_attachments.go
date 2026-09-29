package service

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// maxAttachment limits a file attached to a note.
const maxAttachment = 50 << 20

// maxAttachments limits the files attached to one note.
const maxAttachments = 100

// attachmentName cleans a file name sent by the client: no directories, no control characters.
func attachmentName(name string) string {
	name = path.Base("/" + strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name))
	if name == "/" || name == "." || name == ".." {
		return ""
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return strings.ToValidUTF8(name, "")
}

// AddAttachment stores a file of any type as an attachment of the note and returns it. It
// needs an editor and doesn't change the note's revision.
func (s *RecordingService) AddAttachment(ctx context.Context, acc *Account, id, name string, body io.Reader) (*recording.Attachment, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxAttachment+1))
	if err != nil {
		return nil, fmt.Errorf("receiving file: %w", err)
	}
	if len(data) == 0 {
		return nil, invalid("the file is empty")
	}
	if len(data) > maxAttachment {
		return nil, fmt.Errorf("%w: attachments can be at most %d MB", ErrTooLarge, maxAttachment>>20)
	}
	name = cmp.Or(attachmentName(name), "attachment")
	att := recording.Attachment{
		ID:          newID(),
		Name:        name,
		ContentType: http.DetectContentType(data[:min(len(data), 512)]),
		Size:        int64(len(data)),
	}
	_, err = s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if rec.Source == recording.SourceRemarkable {
			return errors.Join(ErrForbidden, errors.New("notes of reMarkable documents are read-only"))
		}
		if len(rec.Attachments) >= maxAttachments {
			return invalid("a note can have at most %d attachments", maxAttachments)
		}
		att.Key = fmt.Sprintf("recordings/%s/%s/attachments/%s%s", cmp.Or(rec.OwnerID, "unowned"), rec.ID, att.ID, path.Ext(name))
		rec.Attachments = append(slices.Clone(rec.Attachments), att)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Stored after the note records it, like pictures: a listed but missing file 404s, an
	// orphaned file would never be deleted.
	if err := s.objects.Put(ctx, att.Key, bytes.NewReader(data), att.Size, att.ContentType); err != nil {
		return nil, fmt.Errorf("store %s: %w", att.Key, err)
	}
	return &att, nil
}

// Attachment returns the stored file attached to a note the account may see.
func (s *RecordingService) Attachment(ctx context.Context, acc *Account, id, attID string) (*recording.Attachment, error) {
	rec, _, err := s.load(ctx, acc, id, recording.RoleViewer)
	if err != nil {
		return nil, err
	}
	for _, a := range rec.Attachments {
		if a.ID == attID {
			return &a, nil
		}
	}
	return nil, ErrNotFound
}

// DeleteAttachment removes an attached file. It needs an editor.
func (s *RecordingService) DeleteAttachment(ctx context.Context, acc *Account, id, attID string) (*recording.Recording, error) {
	var key string
	rec, err := s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		i := slices.IndexFunc(rec.Attachments, func(a recording.Attachment) bool { return a.ID == attID })
		if i < 0 {
			return ErrNotFound
		}
		key = rec.Attachments[i].Key
		rec.Attachments = slices.Delete(slices.Clone(rec.Attachments), i, i+1)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.objects.Delete(ctx, key); err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return rec, nil
}
