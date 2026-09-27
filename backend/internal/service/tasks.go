package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

// maxRemindMinutes bounds how early a reminder can go off: four weeks before.
const maxRemindMinutes = 4 * 7 * 24 * 60

// location returns the time zone the owner's task dates are meant in.
func (s *RecordingService) location(ctx context.Context, ownerID string) *time.Location {
	if s.Users == nil {
		return time.UTC
	}
	u, err := s.Users.Get(ctx, ownerID)
	if err != nil {
		return time.UTC
	}
	return u.Location()
}

// cleanDue normalizes a due date and checks it. Nil is no date.
func cleanDue(d *recording.Due) (*recording.Due, error) {
	if d == nil {
		return nil, nil
	}
	out := *d
	out.Date, out.Time = strings.TrimSpace(out.Date), strings.TrimSpace(out.Time)
	day, err := recording.ParseDate(out.Date)
	if err != nil {
		return nil, invalid("the date must look like 2026-09-28")
	}
	if out.Time != "" {
		t, err := time.Parse(recording.ClockLayout, out.Time)
		if err != nil {
			return nil, invalid("the time must look like 15:30")
		}
		out.Time = t.Format(recording.ClockLayout)
	}
	if out.Remind != nil && (*out.Remind < 0 || *out.Remind > maxRemindMinutes) {
		return nil, invalid("a reminder can be at most four weeks before the date")
	}
	if out.Repeat != nil {
		r := *out.Repeat
		if !r.Valid() {
			return nil, invalid("unsupported repeat rule")
		}
		if r.Unit == recording.RepeatWeek {
			r.Weekdays = slices.Sorted(slices.Values(r.Weekdays))
		} else {
			r.Weekdays = nil
		}
		if r.Unit == recording.RepeatMonth && r.MonthDay == 0 {
			r.MonthDay = day.Day()
		} else if r.Unit != recording.RepeatMonth {
			r.MonthDay = 0
		}
		out.Repeat = &r
	}
	return &out, nil
}

// scheduleReminder sets when the task's next reminder is sent: only for an open task, and
// only for a moment still to come (a date set in the past sends none), and not in the trash.
func (s *RecordingService) scheduleReminder(rec *recording.Recording, loc *time.Location) {
	rec.RemindAt = nil
	if rec.Done || rec.Due == nil || rec.DeletedAt != nil {
		return
	}
	if at := rec.Due.ReminderAt(loc); at != nil && at.After(s.clock()) {
		rec.RemindAt = at
	}
}

// makeTask puts the task label on the note if it isn't there yet.
func makeTask(rec *recording.Recording) error {
	if slices.Contains(rec.Labels, label.Task) {
		return nil
	}
	if len(rec.Labels) >= maxLabelsPerNote {
		return invalid("a note can have at most %d labels", maxLabelsPerNote)
	}
	rec.Labels = append(rec.Labels, label.Task)
	return nil
}

// clearTask removes the fields only tasks have.
func clearTask(rec *recording.Recording) {
	rec.Done, rec.Due, rec.Priority, rec.RemindAt = false, nil, 0, nil
}

// SetDue sets when the task is due, how it repeats and when to remind, or clears it (nil).
// A note gets the task label when a date is set.
func (s *RecordingService) SetDue(ctx context.Context, acc *Account, id string, due *recording.Due) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if rec.Due, err = cleanDue(due); err != nil {
		return nil, err
	}
	if rec.Due != nil {
		if err := makeTask(rec); err != nil {
			return nil, err
		}
	}
	s.scheduleReminder(rec, s.location(ctx, rec.OwnerID))
	return rec, s.save(ctx, rec)
}

// SetPriority ranks the task (1 most urgent … 3; 0 none). A note gets the task label when a
// priority is set.
func (s *RecordingService) SetPriority(ctx context.Context, acc *Account, id string, p recording.Priority) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if p < 0 || p > recording.MaxPriority {
		return nil, invalid("priority must be 0 (none) to %d", recording.MaxPriority)
	}
	if p > 0 {
		if err := makeTask(rec); err != nil {
			return nil, err
		}
	}
	rec.Priority = p
	return rec, s.save(ctx, rec)
}

// completeTask checks a task off or on. A recurring task checked off stays open and moves
// to its next date instead.
func (s *RecordingService) completeTask(ctx context.Context, rec *recording.Recording, done bool) {
	loc := s.location(ctx, rec.OwnerID)
	if done && rec.Due != nil && rec.Due.Repeat != nil {
		rec.Due.Advance(s.clock().In(loc).Format(recording.DateLayout))
		done = false
	}
	rec.Done = done
	s.scheduleReminder(rec, loc)
}

// RescheduleReminders recomputes the pending reminders of a user's open tasks, e.g. after
// their time zone changed.
func (s *RecordingService) RescheduleReminders(ctx context.Context, u *user.User) error {
	list, err := s.recs.List(ctx, recording.ListFilter{OwnerID: u.ID, Brief: true})
	if err != nil {
		return err
	}
	loc := u.Location()
	for _, r := range list {
		if r.Due == nil || r.Done {
			continue
		}
		before := r.RemindAt
		s.scheduleReminder(r, loc)
		if !sameTime(before, r.RemindAt) {
			if err := s.recs.SetRemindAt(ctx, r.ID, r.RemindAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func sameTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

// TaskFields are the task properties a new note can be created with.
type TaskFields struct {
	// Task puts the task label on the note (also implied by Due or Priority).
	Task     bool               `json:"task,omitempty"`
	Due      *recording.Due     `json:"due,omitempty"`
	Priority recording.Priority `json:"priority,omitempty"`
}

// apply validates the fields and sets them on a new note.
func (f TaskFields) apply(s *RecordingService, ctx context.Context, rec *recording.Recording) error {
	if f.Priority < 0 || f.Priority > recording.MaxPriority {
		return invalid("priority must be 0 (none) to %d", recording.MaxPriority)
	}
	due, err := cleanDue(f.Due)
	if err != nil {
		return err
	}
	if !f.Task && due == nil && f.Priority == 0 {
		return nil
	}
	if err := makeTask(rec); err != nil {
		return err
	}
	rec.Due, rec.Priority = due, f.Priority
	s.scheduleReminder(rec, s.location(ctx, rec.OwnerID))
	return nil
}

// ActionItemTask turns one of the action items found in a note into a task: a text note
// under that note, labeled as a task. Title, text and task fields default to the item's.
type ActionItemTask struct {
	Title    string             `json:"title,omitempty"`
	Markdown string             `json:"markdown,omitempty"`
	Due      *recording.Due     `json:"due,omitempty"`
	Priority recording.Priority `json:"priority,omitempty"`
}

// CreateActionItemTask makes a task of an action item and remembers it on the item. It
// returns the new task and the note it came from.
func (s *RecordingService) CreateActionItemTask(ctx context.Context, acc *Account, id, itemID string, in ActionItemTask) (task, source *recording.Recording, err error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, nil, err
	}
	item := findActionItem(rec, itemID)
	if item == nil {
		return nil, nil, ErrNotFound
	}
	if item.TaskID != "" {
		if _, err := s.recs.Get(ctx, item.TaskID); err == nil {
			return nil, nil, errors.Join(ErrNotReady, errors.New("a task was already made of this action item"))
		}
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = truncateRunes(item.Text, 200)
	}
	due := in.Due
	if due == nil && item.Due != "" {
		zero := 0
		due = &recording.Due{Date: item.Due, Remind: &zero}
	}
	owner := rec.OwnerID
	task, err = s.CreateText(ctx, &Account{ID: owner}, TextNoteInput{
		SummaryEdit: SummaryEdit{Title: title, Markdown: in.Markdown}, ParentID: rec.ID,
		TaskFields: TaskFields{Task: true, Due: due, Priority: in.Priority},
	})
	if err != nil {
		return nil, nil, err
	}
	// Re-read the source: the note may have changed while the task was made.
	if rec, err = s.recs.Get(ctx, rec.ID); err != nil {
		return nil, nil, err
	}
	if item = findActionItem(rec, itemID); item != nil {
		item.TaskID, item.Dismissed = task.ID, false
		if err := s.save(ctx, rec); err != nil {
			return nil, nil, err
		}
	}
	return task, rec, nil
}

// DismissActionItem hides an action item the user doesn't want as a task, or shows it
// again.
func (s *RecordingService) DismissActionItem(ctx context.Context, acc *Account, id, itemID string, dismissed bool) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	item := findActionItem(rec, itemID)
	if item == nil {
		return nil, ErrNotFound
	}
	item.Dismissed = dismissed
	return rec, s.save(ctx, rec)
}

func findActionItem(rec *recording.Recording, itemID string) *recording.ActionItem {
	if rec.Summary == nil {
		return nil
	}
	for i := range rec.Summary.ActionItems {
		if rec.Summary.ActionItems[i].ID == itemID {
			return &rec.Summary.ActionItems[i]
		}
	}
	return nil
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}
