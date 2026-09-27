package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strconv"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// Limits of the time log.
const (
	// maxFocusMinutes bounds a focus session (Pomodoro).
	maxFocusMinutes = 4 * 60
	// maxEstimateMinutes bounds a task's estimate: a bit over a week of full days.
	maxEstimateMinutes = 10_000
	// maxLogDays bounds the period one time log or export covers.
	maxLogDays = 366
)

// TimeEntryView is a time log entry with the note it was spent on, as listed.
type TimeEntryView struct {
	timelog.Entry
	NoteTitle  string `json:"noteTitle"`
	NoteNumber int64  `json:"noteNumber,omitempty"`
	// Seconds is how long the entry lasted, a running one up to now.
	Seconds int64 `json:"seconds"`
}

// TimeEntryInput logs time spent on a note by hand.
type TimeEntryInput struct {
	NoteID string    `json:"noteId"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

// TimerStart starts the timer on a note; Minutes > 0 makes it a focus session that stops by
// itself after that many minutes.
type TimerStart struct {
	NoteID  string `json:"noteId"`
	Minutes int    `json:"minutes,omitempty"`
}

// TimeService runs the timer on notes and keeps the time log: one running timer per user,
// focus sessions (Pomodoro) that end by themselves, entries added by hand, and the log of a
// period as a list or a CSV file.
type TimeService struct {
	repo  ports.TimeEntryRepository
	recs  ports.RecordingRepository
	users ports.UserRepository
	clock func() time.Time
}

// NewTimeService builds the service. users gives the time zone the log's days are meant in.
func NewTimeService(repo ports.TimeEntryRepository, recs ports.RecordingRepository, users ports.UserRepository) *TimeService {
	return &TimeService{repo: repo, recs: recs, users: users, clock: time.Now}
}

// Running returns the account's running timer, or nil. A focus session that has run out is
// stopped first.
func (s *TimeService) Running(ctx context.Context, acc *Account) (*TimeEntryView, error) {
	e, err := s.running(ctx, acc)
	if err != nil || e == nil {
		return nil, err
	}
	return s.view(ctx, e), nil
}

// running returns the running timer after stopping it if it ran out, or nil.
func (s *TimeService) running(ctx context.Context, acc *Account) (*timelog.Entry, error) {
	if acc.ID == "" {
		return nil, nil
	}
	e, err := s.repo.Running(ctx, acc.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if end := e.Lapsed(s.clock()); end != nil {
		return nil, s.finish(ctx, e, *end)
	}
	return e, nil
}

// Start starts the timer on one of the account's notes, stopping the one that was running.
func (s *TimeService) Start(ctx context.Context, acc *Account, in TimerStart) (*TimeEntryView, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("timers belong to a user; sign in"))
	}
	if in.Minutes < 0 || in.Minutes > maxFocusMinutes {
		return nil, invalid("a focus session lasts 1-%d minutes", maxFocusMinutes)
	}
	note, err := s.note(ctx, acc, in.NoteID)
	if err != nil {
		return nil, err
	}
	if note.DeletedAt != nil {
		return nil, invalid("the note is in the trash")
	}
	now := s.clock().UTC()
	if cur, err := s.running(ctx, acc); err != nil {
		return nil, err
	} else if cur != nil {
		if err := s.finish(ctx, cur, now); err != nil {
			return nil, err
		}
	}
	e := &timelog.Entry{ID: newID(), OwnerID: acc.ID, NoteID: note.ID, Start: now, CreatedAt: now, Open: true}
	if in.Minutes > 0 {
		until := now.Add(time.Duration(in.Minutes) * time.Minute)
		e.Until = &until
	}
	if err := s.repo.Create(ctx, e); err != nil {
		if errors.Is(err, errDuplicate) {
			return nil, errors.Join(ErrNotReady, errors.New("another timer was started meanwhile"))
		}
		return nil, err
	}
	return s.view(ctx, e), nil
}

// Stop stops the account's running timer and returns it, or nil when none was running.
func (s *TimeService) Stop(ctx context.Context, acc *Account) (*TimeEntryView, error) {
	e, err := s.running(ctx, acc)
	if err != nil || e == nil {
		return nil, err
	}
	if err := s.finish(ctx, e, s.clock().UTC()); err != nil {
		return nil, err
	}
	return s.view(ctx, e), nil
}

// finish ends a running entry and adds its time to its note.
func (s *TimeService) finish(ctx context.Context, e *timelog.Entry, end time.Time) error {
	end = end.UTC()
	e.End, e.Open = &end, false
	if err := s.repo.Update(ctx, e); err != nil {
		return err
	}
	return s.track(ctx, e.NoteID, e.Seconds(end))
}

// track adds seconds to the time logged on a note; a note deleted meanwhile is skipped.
func (s *TimeService) track(ctx context.Context, noteID string, seconds int64) error {
	if seconds == 0 {
		return nil
	}
	if err := s.recs.AddTrackedSeconds(ctx, noteID, seconds); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// Add logs time spent on one of the account's notes by hand.
func (s *TimeService) Add(ctx context.Context, acc *Account, in TimeEntryInput) (*TimeEntryView, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("time logs belong to a user; sign in"))
	}
	note, err := s.note(ctx, acc, in.NoteID)
	if err != nil {
		return nil, err
	}
	start, end := in.Start.UTC().Truncate(time.Second), in.End.UTC().Truncate(time.Second)
	switch {
	case in.Start.IsZero() || !end.After(start):
		return nil, invalid("the end must be after the start")
	case end.Sub(start) > timelog.MaxEntry:
		return nil, invalid("an entry lasts at most %d hours", int(timelog.MaxEntry.Hours()))
	case end.After(s.clock().Add(time.Minute)):
		return nil, invalid("time can't be logged in the future")
	}
	e := &timelog.Entry{ID: newID(), OwnerID: note.OwnerID, NoteID: note.ID, Start: start, End: &end, CreatedAt: s.clock().UTC()}
	if err := s.repo.Create(ctx, e); err != nil {
		return nil, err
	}
	if err := s.track(ctx, e.NoteID, e.Seconds(end)); err != nil {
		return nil, err
	}
	return s.view(ctx, e), nil
}

// Delete removes an entry from the account's time log and takes its time off its note.
func (s *TimeService) Delete(ctx context.Context, acc *Account, id string) error {
	e, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !acc.Owns(e.OwnerID) {
		return ErrNotFound
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	if e.Running() {
		return nil
	}
	return s.track(ctx, e.NoteID, -e.Seconds(*e.End))
}

// LogRange is the period of a time log: the days from From to To (both YYYY-MM-DD,
// inclusive) in the user's time zone. Empty dates cover the current week, Monday to Sunday.
type LogRange struct {
	From, To string
}

// span resolves the period to moments.
func (s *TimeService) span(ctx context.Context, acc *Account, r LogRange) (from, to time.Time, loc *time.Location, err error) {
	loc = time.UTC
	if s.users != nil && acc.ID != "" {
		if u, err := s.users.Get(ctx, acc.ID); err == nil {
			loc = u.Location()
		}
	}
	if r.From == "" && r.To == "" {
		today := s.clock().In(loc)
		monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
		r.From = monday.Format(recording.DateLayout)
		r.To = monday.AddDate(0, 0, 6).Format(recording.DateLayout)
	}
	first, err1 := time.ParseInLocation(recording.DateLayout, r.From, loc)
	last, err2 := time.ParseInLocation(recording.DateLayout, r.To, loc)
	if err1 != nil || err2 != nil {
		return from, to, loc, invalid("from and to must look like 2026-09-28")
	}
	end := last.AddDate(0, 0, 1)
	if !end.After(first) || end.Sub(first) > maxLogDays*24*time.Hour+time.Hour {
		return from, to, loc, invalid("the period is 1-%d days", maxLogDays)
	}
	return first, end, loc, nil
}

// List returns the account's time log of a period, oldest first. A running timer is
// included with its time so far.
func (s *TimeService) List(ctx context.Context, acc *Account, r LogRange) ([]TimeEntryView, error) {
	from, to, _, err := s.span(ctx, acc, r)
	if err != nil {
		return nil, err
	}
	if _, err := s.running(ctx, acc); err != nil { // stops a focus session that ran out
		return nil, err
	}
	entries, err := s.repo.List(ctx, timelog.Range{OwnerID: acc.ID, From: from, To: to})
	if err != nil {
		return nil, err
	}
	out := make([]TimeEntryView, 0, len(entries))
	notes := map[string]*recording.Recording{}
	for _, e := range entries {
		out = append(out, *s.viewWith(ctx, e, notes))
	}
	return out, nil
}

// Export returns the account's time log of a period as CSV: one line per entry, with its
// day, start and end in the user's time zone, its minutes, and the note with its estimate.
func (s *TimeService) Export(ctx context.Context, acc *Account, r LogRange) ([]byte, error) {
	list, err := s.List(ctx, acc, r)
	if err != nil {
		return nil, err
	}
	_, _, loc, _ := s.span(ctx, acc, r)
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"date", "start", "end", "minutes", "note", "number", "estimate_minutes"})
	notes := map[string]*recording.Recording{}
	for _, v := range list {
		end := ""
		if v.End != nil {
			end = v.End.In(loc).Format("15:04")
		}
		estimate := ""
		if n := s.noteOf(ctx, v.NoteID, notes); n != nil && n.Estimate > 0 {
			estimate = strconv.Itoa(n.Estimate)
		}
		number := ""
		if v.NoteNumber > 0 {
			number = strconv.FormatInt(v.NoteNumber, 10)
		}
		minutes := strconv.FormatFloat(float64(v.Seconds)/60, 'f', 1, 64)
		_ = w.Write([]string{v.Start.In(loc).Format(recording.DateLayout), v.Start.In(loc).Format("15:04"), end, minutes, csvSafe(v.NoteTitle), number, estimate})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// csvSafe keeps a spreadsheet from reading a text cell as a formula.
func csvSafe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@') {
		return "'" + s
	}
	return s
}

// note returns one of the notes the account sees: its own, or one shared with it.
func (s *TimeService) note(ctx context.Context, acc *Account, id string) (*recording.Recording, error) {
	if id == "" {
		return nil, invalid("noteId is required")
	}
	rec, err := s.recs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if role := roleOf(acc, rec); role == "" || (role != recording.RoleOwner && rec.DeletedAt != nil) {
		return nil, ErrNotFound
	}
	return rec, nil
}

func (s *TimeService) view(ctx context.Context, e *timelog.Entry) *TimeEntryView {
	return s.viewWith(ctx, e, map[string]*recording.Recording{})
}

// viewWith describes an entry, looking its note up in (and adding it to) notes.
func (s *TimeService) viewWith(ctx context.Context, e *timelog.Entry, notes map[string]*recording.Recording) *TimeEntryView {
	v := &TimeEntryView{Entry: *e, Seconds: e.Seconds(s.clock())}
	if n := s.noteOf(ctx, e.NoteID, notes); n != nil {
		v.NoteTitle, v.NoteNumber = noteTitle(n), n.Number
	}
	return v
}

func (s *TimeService) noteOf(ctx context.Context, id string, notes map[string]*recording.Recording) *recording.Recording {
	if n, ok := notes[id]; ok {
		return n
	}
	n, err := s.recs.Get(ctx, id)
	if err != nil {
		n = nil
	}
	notes[id] = n
	return n
}

// noteTitle is what a note is called: its summary's title, else its source's title.
func noteTitle(r *recording.Recording) string {
	if r.Summary != nil && r.Summary.Title != "" {
		return r.Summary.Title
	}
	if r.Title != "" {
		return r.Title
	}
	return "Untitled"
}

// SetEstimate sets how many minutes a task is expected to take (0 clears it). A note gets
// the task label when an estimate is set.
func (s *RecordingService) SetEstimate(ctx context.Context, acc *Account, id string, minutes int) (*recording.Recording, error) {
	if minutes < 0 || minutes > maxEstimateMinutes {
		return nil, invalid("an estimate is 0 (none) to %d minutes", maxEstimateMinutes)
	}
	return s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if minutes > 0 {
			if err := makeTask(rec); err != nil {
				return err
			}
		}
		rec.Estimate = minutes
		return nil
	})
}
