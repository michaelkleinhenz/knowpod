package mongo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/filter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

func TestFilterAndTimeEntryRepos(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	filters, times, recs, users := NewFilterRepo(s), NewTimeEntryRepo(s), NewRecordingRepo(s), NewUserRepo(s)
	now := time.Now().UTC().Truncate(time.Millisecond)

	f := &filter.Filter{ID: NewID(), OwnerID: "u1", Name: "Soon", Query: "due:week", CreatedAt: now, UpdatedAt: now}
	if err := filters.Create(ctx, f); err != nil {
		t.Fatal(err)
	}
	if list, err := filters.List(ctx, "u1"); err != nil || len(list) != 1 || list[0].Query != "due:week" {
		t.Fatalf("list: %+v %v", list, err)
	}

	// One running timer per user.
	run := &timelog.Entry{ID: NewID(), OwnerID: "u1", NoteID: "n1", Start: now, CreatedAt: now, Open: true}
	if err := times.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := times.Create(ctx, &timelog.Entry{ID: NewID(), OwnerID: "u1", NoteID: "n2", Start: now, CreatedAt: now, Open: true}); !errors.Is(err, domain.ErrDuplicate) {
		t.Errorf("second running timer: %v", err)
	}
	if got, err := times.Running(ctx, "u1"); err != nil || got.ID != run.ID {
		t.Fatalf("running: %+v %v", got, err)
	}
	end := now.Add(time.Minute)
	run.End, run.Open = &end, false
	if err := times.Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := times.Running(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stopped timer still running: %v", err)
	}
	if list, err := times.List(ctx, timelog.Range{OwnerID: "u1", From: now.Add(-time.Hour), To: now.Add(time.Hour)}); err != nil || len(list) != 1 {
		t.Errorf("list: %d %v", len(list), err)
	}

	rec := &recording.Recording{ID: NewID(), OwnerID: "u1", DeviceID: "board:u1", ClientID: "b", Type: recording.TypeBoard,
		Board: &recording.Board{Scope: recording.BoardScope{Kind: recording.ScopeFilter, ID: f.ID}}, NotBefore: now, CreatedAt: now, UpdatedAt: now}
	if err := recs.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := recs.AddTrackedSeconds(ctx, rec.ID, 90); err != nil {
		t.Fatal(err)
	}
	if err := recs.ClearBoardScope(ctx, "u1", rec.Board.Scope); err != nil {
		t.Fatal(err)
	}
	got, _ := recs.Get(ctx, rec.ID)
	if got.TrackedSeconds != 90 || got.Board.Scope != (recording.BoardScope{}) {
		t.Errorf("tracked %d, scope %+v", got.TrackedSeconds, got.Board.Scope)
	}

	// Calendar tokens are looked up by hash; users without one don't collide.
	for _, u := range []*user.User{{ID: NewID(), Email: "a@x"}, {ID: NewID(), Email: "b@x"}, {ID: NewID(), Email: "c@x", Calendar: user.Calendar{TokenHash: "h1"}}} {
		if err := users.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if u, err := users.GetByCalendarTokenHash(ctx, "h1"); err != nil || u.Email != "c@x" {
		t.Errorf("by calendar token: %+v %v", u, err)
	}
	if _, err := users.GetByCalendarTokenHash(ctx, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("empty token: %v", err)
	}
}
