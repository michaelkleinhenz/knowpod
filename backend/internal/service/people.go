package service

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// Person is a user the account shares notes or folders with (or the account itself), so the
// web app can tell whose a note is and filter by people.
type Person struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	// Self marks the account's own entry.
	Self bool `json:"self,omitempty"`
}

// People lists the account and the users it shares with: the owners, makers and assignees
// of the notes it sees, everyone those notes and its folders are shared with, and the owners
// of folders shared with it. Ordered by email, the account first.
func (s *RecordingService) People(ctx context.Context, acc *Account) ([]Person, error) {
	if acc.ID == "" {
		return []Person{}, nil
	}
	ids := map[string]bool{acc.ID: true}
	add := func(id string) {
		if id != "" {
			ids[id] = true
		}
	}
	notes, err := s.recs.List(ctx, recording.ListFilter{UserID: acc.ID, Brief: true})
	if err != nil {
		return nil, err
	}
	for _, r := range notes {
		add(r.OwnerID)
		add(r.CreatedBy)
		add(r.AssigneeID)
		for _, m := range r.Members {
			add(m.UserID)
		}
	}
	if s.Folders != nil {
		own, err := s.Folders.repo.List(ctx, acc.ID)
		if err != nil {
			return nil, err
		}
		shared, err := s.Folders.repo.ListSharedWith(ctx, acc.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range append(own, shared...) {
			add(f.OwnerID)
			for _, sh := range f.Shares {
				add(sh.UserID)
			}
		}
	}
	out := make([]Person, 0, len(ids))
	for id := range ids {
		email := s.email(ctx, id)
		if email == "" && id != acc.ID {
			continue // deleted user
		}
		out = append(out, Person{UserID: id, Email: email, Self: id == acc.ID})
	}
	slices.SortFunc(out, func(a, b Person) int {
		if a.Self != b.Self {
			if a.Self {
				return -1
			}
			return 1
		}
		return cmp.Or(strings.Compare(a.Email, b.Email), strings.Compare(a.UserID, b.UserID))
	})
	return out, nil
}
