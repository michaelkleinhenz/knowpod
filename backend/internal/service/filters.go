package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/filter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// Limits of saved filters.
const (
	maxFilterName  = 60
	maxFilterQuery = 500
	maxFilters     = 100
)

// FilterInput creates or changes a saved filter.
type FilterInput struct {
	Name   string `json:"name"`
	Query  string `json:"query"`
	Pinned bool   `json:"pinned"`
}

// FilterService manages users' saved filters. The web app evaluates their queries; the
// service keeps them and the boards that show them consistent.
type FilterService struct {
	repo  ports.FilterRepository
	recs  ports.RecordingRepository
	clock func() time.Time
}

// NewFilterService builds the service. recs is used to clear the scope of boards showing a
// deleted filter.
func NewFilterService(repo ports.FilterRepository, recs ports.RecordingRepository) *FilterService {
	return &FilterService{repo: repo, recs: recs, clock: time.Now}
}

// List returns the account's filters by name.
func (s *FilterService) List(ctx context.Context, acc *Account) ([]*filter.Filter, error) {
	if acc.ID == "" {
		return []*filter.Filter{}, nil
	}
	return s.repo.List(ctx, acc.ID)
}

// Create saves a filter for the account's user.
func (s *FilterService) Create(ctx context.Context, acc *Account, in FilterInput) (*filter.Filter, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("filters belong to a user; sign in"))
	}
	own, err := s.repo.List(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	if len(own) >= maxFilters {
		return nil, invalid("you can save at most %d filters", maxFilters)
	}
	if err := validFilter(own, "", &in); err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	f := &filter.Filter{ID: newID(), OwnerID: acc.ID, Name: in.Name, Query: in.Query, Pinned: in.Pinned, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.Create(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// Update renames, changes the query of, or pins or unpins one of the account's filters.
func (s *FilterService) Update(ctx context.Context, acc *Account, id string, in FilterInput) (*filter.Filter, error) {
	f, err := s.own(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	own, err := s.repo.List(ctx, f.OwnerID)
	if err != nil {
		return nil, err
	}
	if err := validFilter(own, id, &in); err != nil {
		return nil, err
	}
	f.Name, f.Query, f.Pinned, f.UpdatedAt = in.Name, in.Query, in.Pinned, s.clock().UTC()
	if err := s.repo.Update(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// Delete removes one of the account's filters; boards showing it show nothing until another
// scope is chosen.
func (s *FilterService) Delete(ctx context.Context, acc *Account, id string) error {
	f, err := s.own(ctx, acc, id)
	if err != nil {
		return err
	}
	if err := s.recs.ClearBoardScope(ctx, f.OwnerID, recording.BoardScope{Kind: recording.ScopeFilter, ID: id}); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// Usable reports whether a board of ownerID may show the filter.
func (s *FilterService) Usable(ctx context.Context, ownerID, id string) bool {
	if s == nil || id == "" {
		return false
	}
	f, err := s.repo.Get(ctx, id)
	return err == nil && f.OwnerID == ownerID
}

func (s *FilterService) own(ctx context.Context, acc *Account, id string) (*filter.Filter, error) {
	f, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(f.OwnerID) {
		return nil, ErrNotFound
	}
	return f, nil
}

// validFilter normalizes the input and checks it; names are unique per user (ignoring case).
func validFilter(own []*filter.Filter, id string, in *FilterInput) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.Query = strings.TrimSpace(in.Query)
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > maxFilterName:
		return invalid("name must be 1-%d characters", maxFilterName)
	case in.Query == "" || utf8.RuneCountInString(in.Query) > maxFilterQuery:
		return invalid("query must be 1-%d characters", maxFilterQuery)
	case strings.ContainsAny(in.Query, "\n\r\t"):
		return invalid("the query is a single line")
	}
	for _, f := range own {
		if f.ID != id && strings.EqualFold(f.Name, in.Name) {
			return invalid("a filter named %q exists", f.Name)
		}
	}
	return nil
}
