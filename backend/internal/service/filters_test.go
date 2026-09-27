package service

import (
	"context"
	"errors"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func TestSavedFiltersAndBoards(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	filters := NewFilterService(memory.NewFilters(), f.recs)
	f.s.Filters = filters

	week, err := filters.Create(ctx, f.acc, FilterInput{Name: "  This   week ", Query: " label:Task & due:week & !done ", Pinned: true})
	if err != nil || week.Name != "This week" || week.Query != "label:Task & due:week & !done" || !week.Pinned {
		t.Fatalf("create: %+v %v", week, err)
	}
	for _, bad := range []FilterInput{{Name: "this WEEK", Query: "x"}, {Name: "", Query: "x"}, {Name: "a", Query: " "}, {Name: "b", Query: "a\nb"}} {
		if _, err := filters.Create(ctx, f.acc, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if _, err := filters.Update(ctx, &Account{ID: "u2"}, week.ID, FilterInput{Name: "x", Query: "y"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign update: %v", err)
	}
	up, err := filters.Update(ctx, f.acc, week.ID, FilterInput{Name: "Week", Query: "due:week"})
	if err != nil || up.Name != "Week" || up.Pinned {
		t.Fatalf("update: %+v %v", up, err)
	}

	board, err := f.s.CreateBoard(ctx, f.acc, BoardInput{Title: "B", Board: &recording.Board{Scope: recording.BoardScope{Kind: recording.ScopeFilter, ID: week.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateBoard(ctx, &Account{ID: "u2"}, BoardInput{Title: "B", Board: &recording.Board{Scope: recording.BoardScope{Kind: recording.ScopeFilter, ID: week.ID}}}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("foreign filter on a board: %v", err)
	}

	// Deleting the filter leaves the board without a scope.
	if err := filters.Delete(ctx, f.acc, week.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, board.ID)
	if got.Board.Scope != (recording.BoardScope{}) {
		t.Errorf("scope kept: %+v", got.Board.Scope)
	}
	if list, _ := filters.List(ctx, f.acc); len(list) != 0 {
		t.Errorf("filters left: %+v", list)
	}
}
