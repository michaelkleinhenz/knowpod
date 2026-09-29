package service

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// maxNoteImage limits a picture pasted or dropped into a note's text.
const maxNoteImage = 10 << 20

// maxNoteImages limits the pictures of one note.
const maxNoteImages = 200

// imageID returns the ID of a note's picture: the file name of its storage key without the
// extension.
func imageID(obj recording.Object) string {
	base := path.Base(obj.Key)
	return strings.TrimSuffix(base, path.Ext(base))
}

// AddImage stores a picture (JPEG, PNG, WebP or GIF) for the note's text and returns its ID.
// The text refers to it by /api/v1/recordings/<note>/images/<ID>. It needs an editor and
// doesn't change the note's revision.
func (s *RecordingService) AddImage(ctx context.Context, acc *Account, id string, body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxNoteImage+1))
	if err != nil {
		return "", fmt.Errorf("receiving image: %w", err)
	}
	if len(data) == 0 {
		return "", invalid("the image is empty")
	}
	if len(data) > maxNoteImage {
		return "", fmt.Errorf("%w: images can be at most %d MB", ErrTooLarge, maxNoteImage>>20)
	}
	head := data[:min(len(data), 64)]
	contentType, heic := sniffDocument(head)
	if heic {
		return "", errors.Join(ErrUnsupportedMedia, errors.New("HEIC images can't be shown; use JPEG or PNG"))
	}
	if !strings.HasPrefix(contentType, "image/") {
		return "", errors.Join(ErrUnsupportedMedia, errors.New("only JPEG, PNG, WebP and GIF images can be added"))
	}

	imgID := newID()
	var key string
	_, err = s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if rec.Source == recording.SourceRemarkable {
			return errors.Join(ErrForbidden, errors.New("notes of reMarkable documents are read-only"))
		}
		if len(rec.Images) >= maxNoteImages {
			return invalid("a note can have at most %d images", maxNoteImages)
		}
		key = fmt.Sprintf("recordings/%s/%s/images/%s.%s", cmp.Or(rec.OwnerID, "unowned"), rec.ID, imgID, documentExtension(contentType))
		rec.Images = append(slices.Clone(rec.Images), recording.Object{Key: key, ContentType: contentType, Size: int64(len(data))})
		return nil
	})
	if err != nil {
		return "", err
	}
	// The picture is stored after the note records it: a note that lists a missing picture
	// is harmless (it 404s), an orphaned file would never be deleted.
	if err := s.objects.Put(ctx, key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		return "", fmt.Errorf("store %s: %w", key, err)
	}
	return imgID, nil
}

// Image returns the stored picture of a note the account may see.
func (s *RecordingService) Image(ctx context.Context, acc *Account, id, imgID string) (*recording.Object, error) {
	rec, _, err := s.load(ctx, acc, id, recording.RoleViewer)
	if err != nil {
		return nil, err
	}
	for _, obj := range rec.Images {
		if imageID(obj) == imgID {
			return &obj, nil
		}
	}
	return nil, ErrNotFound
}
