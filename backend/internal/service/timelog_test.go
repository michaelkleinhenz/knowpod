package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func rangeAll(owner string) timelog.Range {
	return timelog.Range{OwnerID: owner, From: time.Time{}, To: time.Now().AddDate(10, 0, 0)}
}

// timeFixture is a time service on the task fixture's notes and clock.
type timeFixture struct {
	*taskFixture
	times   *TimeService
	entries *memory.TimeEntries
}

func newTimeFixture(t *testing.T) *timeFixture {
	f := newTaskFixture(t)
	entries := memory.NewTimeEntries()
	times := NewTimeService(entries, f.recs, f.users)
	times.clock = func() time.Time { return f.now }
	f.s.TimeEntries = entries
	return &timeFixture{taskFixture: f, times: times, entries: entries}
}

func (f *timeFixture) tracked(t *testing.T, id string) int64 {
	t.Helper()
	rec, err := f.recs.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rec.TrackedSeconds
}

func TestTimerStartStopTracksTime(t *testing.T) {
	f := newTimeFixture(t)
	ctx := context.Background()
	a, b := f.note(t, TextNoteInput{}), f.note(t, TextNoteInput{})

	if cur, err := f.times.Running(ctx, f.acc); err != nil || cur != nil {
		t.Fatalf("running before start: %v %v", cur, err)
	}
	started, err := f.times.Start(ctx, f.acc, TimerStart{NoteID: a.ID})
	if err != nil || started.NoteID != a.ID || started.End != nil || started.NoteTitle != "Note" {
		t.Fatalf("start: %+v %v", started, err)
	}
	// Starting another note stops the first one.
	f.now = f.now.Add(10 * time.Minute)
	if _, err := f.times.Start(ctx, f.acc, TimerStart{NoteID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := f.tracked(t, a.ID); got != 600 {
		t.Errorf("a tracked %d", got)
	}
	f.now = f.now.Add(5 * time.Minute)
	stopped, err := f.times.Stop(ctx, f.acc)
	if err != nil || stopped == nil || stopped.Seconds != 300 {
		t.Fatalf("stop: %+v %v", stopped, err)
	}
	if got := f.tracked(t, b.ID); got != 300 {
		t.Errorf("b tracked %d", got)
	}
	if stopped, err := f.times.Stop(ctx, f.acc); err != nil || stopped != nil {
		t.Errorf("second stop: %+v %v", stopped, err)
	}

	// Other users' notes can't be timed.
	if _, err := f.times.Start(ctx, &Account{ID: "u2"}, TimerStart{NoteID: a.ID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign note: %v", err)
	}
}

func TestFocusSessionStopsByItself(t *testing.T) {
	f := newTimeFixture(t)
	ctx := context.Background()
	a := f.note(t, TextNoteInput{})
	if _, err := f.times.Start(ctx, f.acc, TimerStart{NoteID: a.ID, Minutes: 25}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(40 * time.Minute)
	if cur, err := f.times.Running(ctx, f.acc); err != nil || cur != nil {
		t.Fatalf("still running: %+v %v", cur, err)
	}
	if got := f.tracked(t, a.ID); got != 25*60 {
		t.Errorf("tracked %d, want the session's 25 minutes", got)
	}
	if _, err := f.times.Start(ctx, f.acc, TimerStart{NoteID: a.ID, Minutes: 500}); err == nil {
		t.Error("overlong session accepted")
	}
}

func TestTimeLogListExportAndDelete(t *testing.T) {
	f := newTimeFixture(t)
	ctx := context.Background()
	a := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "=Write report"}})
	if _, err := f.s.SetEstimate(ctx, f.acc, a.ID, 90); err != nil {
		t.Fatal(err)
	}
	// Sunday 27 Sep 2026, 10:00 UTC: the week is Mon 21 to Sun 27 (Berlin).
	start := time.Date(2026, 9, 25, 7, 0, 0, 0, time.UTC)
	e, err := f.times.Add(ctx, f.acc, TimeEntryInput{NoteID: a.ID, Start: start, End: start.Add(45 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.times.Add(ctx, f.acc, TimeEntryInput{NoteID: a.ID, Start: f.now, End: f.now.Add(time.Hour)}); err == nil {
		t.Error("future time accepted")
	}
	if _, err := f.times.Add(ctx, f.acc, TimeEntryInput{NoteID: a.ID, Start: start, End: start}); err == nil {
		t.Error("empty entry accepted")
	}
	// One last week isn't listed.
	if _, err := f.times.Add(ctx, f.acc, TimeEntryInput{NoteID: a.ID, Start: start.AddDate(0, 0, -7), End: start.AddDate(0, 0, -7).Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	list, err := f.times.List(ctx, f.acc, LogRange{})
	if err != nil || len(list) != 1 || list[0].Seconds != 45*60 || list[0].NoteTitle != "=Write report" {
		t.Fatalf("list: %+v %v", list, err)
	}
	csv, err := f.times.Export(ctx, f.acc, LogRange{From: "2026-09-21", To: "2026-09-27"})
	if err != nil {
		t.Fatal(err)
	}
	want := "date,start,end,minutes,note,number,estimate_minutes\n2026-09-25,09:00,09:45,45.0,'=Write report,1,90\n"
	if string(csv) != want {
		t.Errorf("csv:\n%s\nwant:\n%s", csv, want)
	}
	if _, err := f.times.List(ctx, f.acc, LogRange{From: "2026-09-27", To: "2026-09-21"}); err == nil {
		t.Error("backwards range accepted")
	}

	if got := f.tracked(t, a.ID); got != 46*60 {
		t.Errorf("tracked %d", got)
	}
	if err := f.times.Delete(ctx, f.acc, e.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.tracked(t, a.ID); got != 60 {
		t.Errorf("tracked after delete %d", got)
	}

	// Deleting the note for good deletes its time log.
	if err := f.s.Delete(ctx, f.acc, a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := f.entries.List(ctx, rangeAll(f.acc.ID)); len(list) != 0 {
		t.Errorf("entries left: %d", len(list))
	}
}

func TestSetEstimate(t *testing.T) {
	f := newTimeFixture(t)
	ctx := context.Background()
	a := f.note(t, TextNoteInput{})
	got, err := f.s.SetEstimate(ctx, f.acc, a.ID, 30)
	if err != nil || got.Estimate != 30 || !strings.Contains(strings.Join(got.Labels, ","), "task") {
		t.Fatalf("estimate: %+v %v", got, err)
	}
	if _, err := f.s.SetEstimate(ctx, f.acc, a.ID, -1); err == nil {
		t.Error("negative estimate accepted")
	}
	if got, _ := f.s.SetEstimate(ctx, f.acc, a.ID, 0); got.Estimate != 0 {
		t.Error("estimate not cleared")
	}
}
