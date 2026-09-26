package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// LabelView is a label as listed in the UI. Built-in labels have fixed IDs; their names are
// translated by the UI (by ID), the English name here is the fallback.
type LabelView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	BuiltIn bool   `json:"builtIn"`
}

// builtInLabels are available to every user and can't be changed or deleted.
var builtInLabels = []LabelView{
	{ID: label.Task, Name: "Task", Color: "#3b5f86", BuiltIn: true},
}

// LabelInput creates or changes a user's label.
type LabelInput struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// maxLabelsPerNote bounds the labels on one note.
const maxLabelsPerNote = 20

var labelColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// LabelService manages users' labels.
type LabelService struct {
	repo  ports.LabelRepository
	recs  ports.RecordingRepository
	clock func() time.Time
}

// NewLabelService builds the service. recs is used to take deleted labels off notes.
func NewLabelService(repo ports.LabelRepository, recs ports.RecordingRepository) *LabelService {
	return &LabelService{repo: repo, recs: recs, clock: time.Now}
}

// List returns the built-in labels followed by the account's own labels.
func (s *LabelService) List(ctx context.Context, acc *Account) ([]LabelView, error) {
	out := append([]LabelView{}, builtInLabels...)
	if acc.ID == "" {
		return out, nil
	}
	own, err := s.repo.List(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	for _, l := range own {
		out = append(out, labelViewOf(l))
	}
	return out, nil
}

// Create adds a label for the account's user.
func (s *LabelService) Create(ctx context.Context, acc *Account, in LabelInput) (*LabelView, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("labels belong to a user; sign in"))
	}
	if err := s.validate(ctx, acc.ID, "", &in); err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	l := &label.Label{ID: newID(), OwnerID: acc.ID, Name: in.Name, Color: in.Color, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.Create(ctx, l); err != nil {
		return nil, err
	}
	v := labelViewOf(l)
	return &v, nil
}

// Update renames or recolors one of the account's labels.
func (s *LabelService) Update(ctx context.Context, acc *Account, id string, in LabelInput) (*LabelView, error) {
	if isBuiltInLabel(id) {
		return nil, errors.Join(ErrForbidden, errors.New("built-in labels can't be changed"))
	}
	l, err := s.own(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if err := s.validate(ctx, l.OwnerID, id, &in); err != nil {
		return nil, err
	}
	l.Name, l.Color, l.UpdatedAt = in.Name, in.Color, s.clock().UTC()
	if err := s.repo.Update(ctx, l); err != nil {
		return nil, err
	}
	v := labelViewOf(l)
	return &v, nil
}

// Delete removes one of the account's labels and takes it off all their notes.
func (s *LabelService) Delete(ctx context.Context, acc *Account, id string) error {
	if isBuiltInLabel(id) {
		return errors.Join(ErrForbidden, errors.New("built-in labels can't be deleted"))
	}
	l, err := s.own(ctx, acc, id)
	if err != nil {
		return err
	}
	if err := s.recs.RemoveLabel(ctx, l.OwnerID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// Usable reports whether a note of ownerID may carry the label.
func (s *LabelService) Usable(ctx context.Context, ownerID, id string) bool {
	if isBuiltInLabel(id) {
		return true
	}
	if s == nil {
		return false
	}
	l, err := s.repo.Get(ctx, id)
	return err == nil && l.OwnerID == ownerID
}

func (s *LabelService) own(ctx context.Context, acc *Account, id string) (*label.Label, error) {
	l, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(l.OwnerID) {
		return nil, ErrNotFound
	}
	return l, nil
}

// validate normalizes the input and checks it; names are unique per user (ignoring case),
// including the built-in names.
func (s *LabelService) validate(ctx context.Context, ownerID, id string, in *LabelInput) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.Color = strings.ToLower(strings.TrimSpace(in.Color))
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 40:
		return invalid("name must be 1-40 characters")
	case !labelColor.MatchString(in.Color):
		return invalid("color must look like #3b5f86")
	}
	for _, b := range builtInLabels {
		if strings.EqualFold(b.Name, in.Name) {
			return invalid("a label named %q exists", b.Name)
		}
	}
	own, err := s.repo.List(ctx, ownerID)
	if err != nil {
		return err
	}
	for _, l := range own {
		if l.ID != id && strings.EqualFold(l.Name, in.Name) {
			return invalid("a label named %q exists", l.Name)
		}
	}
	return nil
}

func isBuiltInLabel(id string) bool {
	for _, b := range builtInLabels {
		if b.ID == id {
			return true
		}
	}
	return false
}

func labelViewOf(l *label.Label) LabelView {
	return LabelView{ID: l.ID, Name: l.Name, Color: l.Color}
}
