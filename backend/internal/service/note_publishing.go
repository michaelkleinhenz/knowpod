package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// SetTemplate marks a text note as a template, or makes it a plain note again. Templates are
// offered when a new note is made; the web app copies their title and text into it.
func (s *RecordingService) SetTemplate(ctx context.Context, acc *Account, id string, template bool) (*recording.Recording, error) {
	return s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if template && !rec.IsText() {
			return invalid("only text notes can be templates")
		}
		rec.Template = template
		return nil
	})
}

// publicToken matches the tokens of public links (see newPublicToken).
var publicToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

// newPublicToken returns the secret part of a new public link.
func newPublicToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Publish gives the note a public web link, which shows its title and text to anyone who has
// it, without signing in. A note that is published already keeps its link. Only the owner
// publishes; boards and notes without text yet can't be published.
func (s *RecordingService) Publish(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	return s.change(ctx, acc, id, recording.RoleOwner, func(rec *recording.Recording, _ recording.Role) error {
		switch {
		case rec.DeletedAt != nil:
			return invalid("notes in the trash can't be published")
		case rec.IsBoard():
			return invalid("boards can't be published")
		case rec.Summary == nil:
			return errors.Join(ErrNotReady, errors.New("the note has no text yet"))
		case rec.Public != nil:
			return nil
		}
		rec.Public = &recording.PublicLink{Token: newPublicToken(), CreatedAt: s.clock().UTC()}
		return nil
	})
}

// Unpublish takes the note's public link down: it stops working, and publishing the note
// again makes a new one.
func (s *RecordingService) Unpublish(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	return s.change(ctx, acc, id, recording.RoleOwner, func(rec *recording.Recording, _ recording.Role) error {
		rec.Public = nil
		return nil
	})
}

// PublicNote is a published note as anyone with its link sees it.
type PublicNote struct {
	Title string `json:"title"`
	// Markdown is the note's text; its pictures point at the public link's images.
	Markdown string         `json:"markdown"`
	Type     recording.Type `json:"type,omitempty"`
	// Date is when the note was recorded or made; UpdatedAt when its text last changed.
	Date      time.Time `json:"date"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// published returns the note published with the token. Links of notes in the trash don't
// work (they work again when the note is restored).
func (s *RecordingService) published(ctx context.Context, token string) (*recording.Recording, error) {
	if !publicToken.MatchString(token) {
		return nil, ErrNotFound
	}
	rec, err := s.recs.GetByPublicToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if rec.DeletedAt != nil || rec.Summary == nil || rec.Public == nil || rec.Public.Token != token {
		return nil, ErrNotFound
	}
	return rec, nil
}

// noteImageURL matches the URLs of a note's pictures in its text.
var noteImageURL = regexp.MustCompile(`/api/v1/recordings/([\w-]+)/images/([\w-]+)`)

// Published returns the note published with the token. Its pictures point at the public
// link; pictures of other notes are left out.
func (s *RecordingService) Published(ctx context.Context, token string) (*PublicNote, error) {
	rec, err := s.published(ctx, token)
	if err != nil {
		return nil, err
	}
	md := noteImageURL.ReplaceAllStringFunc(rec.Summary.Markdown, func(u string) string {
		m := noteImageURL.FindStringSubmatch(u)
		if m[1] != rec.ID {
			return "#"
		}
		return "/api/v1/public/" + token + "/images/" + m[2]
	})
	date := rec.CreatedAt
	if rec.RecordedAt != nil {
		date = *rec.RecordedAt
	}
	updated := rec.Summary.CreatedAt
	if rec.Summary.EditedAt != nil {
		updated = *rec.Summary.EditedAt
	}
	return &PublicNote{Title: rec.Summary.Title, Markdown: md, Type: rec.Type, Date: date, UpdatedAt: updated}, nil
}

// PublishedImage returns a picture of the note published with the token.
func (s *RecordingService) PublishedImage(ctx context.Context, token, imgID string) (*recording.Object, error) {
	rec, err := s.published(ctx, token)
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
