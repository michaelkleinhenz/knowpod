package recording

import (
	"slices"
	"time"
)

// DateLayout and ClockLayout are the formats of Due.Date and Due.Time.
const (
	DateLayout  = "2006-01-02"
	ClockLayout = "15:04"
)

// AllDayReminderClock is when the reminder of a task without a time goes off, on its day
// (minus the reminder's lead time), in the owner's time zone.
const AllDayReminderClock = 9 * time.Hour

// Due is when a task is due: a calendar day, optionally a time of day, both in the owner's
// time zone (so "every Monday at 9:00" stays at 9:00 across daylight saving changes), how it
// repeats, and when to remind.
type Due struct {
	Date string `bson:"date" json:"date"`                     // YYYY-MM-DD
	Time string `bson:"time,omitempty" json:"time,omitempty"` // HH:MM; empty for the whole day
	// Repeat makes the task recurring: checking it off moves it to the next date.
	Repeat *Repeat `bson:"repeat,omitempty" json:"repeat,omitempty"`
	// Remind is how many minutes before the due time a reminder is sent (for a day without
	// a time: before 9:00 on that day); nil sends none.
	Remind *int `bson:"remind,omitempty" json:"remind,omitempty"`
}

// RepeatUnit is the step of a recurring task.
type RepeatUnit string

const (
	RepeatDay     RepeatUnit = "day"
	RepeatWeekday RepeatUnit = "weekday" // Monday to Friday
	RepeatWeek    RepeatUnit = "week"
	RepeatMonth   RepeatUnit = "month"
	RepeatYear    RepeatUnit = "year"
)

// Repeat says how a task recurs: every Every units; weekly repeats can name the weekdays
// (0 = Sunday … 6 = Saturday), monthly ones keep their day of the month (clamped to the
// month's last day, so the 31st stays the 31st where there is one) or, with Nth, fall on
// the Nth of one weekday in the month ("every third Friday": Nth 3, Weekdays [5]).
type Repeat struct {
	Every    int        `bson:"every" json:"every"`
	Unit     RepeatUnit `bson:"unit" json:"unit"`
	Weekdays []int      `bson:"weekdays,omitempty" json:"weekdays,omitempty"`
	MonthDay int        `bson:"monthDay,omitempty" json:"monthDay,omitempty"`
	// Nth is which of the weekday's days in the month a monthly repeat falls on: 1 to 4, or
	// LastWeek (-1) for the last one. 0 repeats on a day of the month instead.
	Nth int `bson:"nth,omitempty" json:"nth,omitempty"`
}

// LastWeek is Repeat.Nth for the last of a weekday's days in the month.
const LastWeek = -1

// NthWeekday returns the nth (1-4, or LastWeek) day with the weekday wd in the month of d.
func NthWeekday(d time.Time, nth int, wd time.Weekday) time.Time {
	first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location())
	if nth == LastWeek {
		last := first.AddDate(0, 1, -1)
		return last.AddDate(0, 0, -((int(last.Weekday()) - int(wd) + 7) % 7))
	}
	return first.AddDate(0, 0, (int(wd)-int(first.Weekday())+7)%7+7*(nth-1))
}

// ParseDate reads a Due.Date.
func ParseDate(s string) (time.Time, error) { return time.Parse(DateLayout, s) }

// Next returns the first date after d on which the task recurs.
func (r *Repeat) Next(d time.Time) time.Time {
	every := max(r.Every, 1)
	switch r.Unit {
	case RepeatWeekday:
		for {
			d = d.AddDate(0, 0, 1)
			if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
				return d
			}
		}
	case RepeatWeek:
		if len(r.Weekdays) == 0 {
			return d.AddDate(0, 0, 7*every)
		}
		// A later listed day in the same week, else the first listed day Every weeks on.
		days := slices.Sorted(slices.Values(r.Weekdays))
		wd := int(d.Weekday())
		for _, x := range days {
			if x > wd {
				return d.AddDate(0, 0, x-wd)
			}
		}
		weekStart := d.AddDate(0, 0, -wd)
		return weekStart.AddDate(0, 0, 7*every+days[0])
	case RepeatMonth:
		if r.Nth != 0 && len(r.Weekdays) == 1 {
			// This month's day if it is still ahead, else the one Every months on.
			wd := time.Weekday(r.Weekdays[0])
			if n := NthWeekday(d, r.Nth, wd); n.After(d) {
				return n
			}
			first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location()).AddDate(0, every, 0)
			return NthWeekday(first, r.Nth, wd)
		}
		day := r.MonthDay
		if day == 0 {
			day = d.Day()
		}
		first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location()).AddDate(0, every, 0)
		last := first.AddDate(0, 1, -1).Day()
		return first.AddDate(0, 0, min(day, last)-1)
	case RepeatYear:
		first := time.Date(d.Year()+every, d.Month(), 1, 0, 0, 0, 0, d.Location())
		last := first.AddDate(0, 1, -1).Day()
		return first.AddDate(0, 0, min(d.Day(), last)-1)
	default: // RepeatDay
		return d.AddDate(0, 0, every)
	}
}

// Valid reports whether the repeat rule is usable.
func (r *Repeat) Valid() bool {
	if r.Every < 1 || r.Every > 365 || r.MonthDay < 0 || r.MonthDay > 31 {
		return false
	}
	if r.Nth != 0 {
		// Only monthly, on one weekday, and not also on a day of the month.
		return r.Unit == RepeatMonth && (r.Nth == LastWeek || r.Nth >= 1 && r.Nth <= 4) &&
			r.MonthDay == 0 && len(r.Weekdays) == 1 && r.Weekdays[0] >= 0 && r.Weekdays[0] <= 6
	}
	switch r.Unit {
	case RepeatDay, RepeatMonth, RepeatYear:
		return len(r.Weekdays) == 0
	case RepeatWeekday:
		return r.Every == 1 && len(r.Weekdays) == 0
	case RepeatWeek:
		seen := map[int]bool{}
		for _, d := range r.Weekdays {
			if d < 0 || d > 6 || seen[d] {
				return false
			}
			seen[d] = true
		}
		return true
	}
	return false
}

// At returns the moment the task is due in loc: its time, or the start of its day.
func (d *Due) At(loc *time.Location) (time.Time, error) {
	day, err := ParseDate(d.Date)
	if err != nil {
		return time.Time{}, err
	}
	var clock time.Duration
	if d.Time != "" {
		t, err := time.Parse(ClockLayout, d.Time)
		if err != nil {
			return time.Time{}, err
		}
		clock = time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc).Add(clock), nil
}

// ReminderAt returns when the reminder goes off in loc, or nil for none.
func (d *Due) ReminderAt(loc *time.Location) *time.Time {
	if d == nil || d.Remind == nil {
		return nil
	}
	at, err := d.At(loc)
	if err != nil {
		return nil
	}
	if d.Time == "" {
		at = at.Add(AllDayReminderClock)
	}
	at = at.Add(-time.Duration(*d.Remind) * time.Minute).UTC()
	return &at
}

// Advance moves a recurring task's date to its next occurrence that is not before today
// (a YYYY-MM-DD in the owner's time zone), at least one step. It reports whether it moved.
func (d *Due) Advance(today string) bool {
	if d == nil || d.Repeat == nil {
		return false
	}
	day, err := ParseDate(d.Date)
	if err != nil {
		return false
	}
	now, err := ParseDate(today)
	if err != nil {
		now = day
	}
	day = d.Repeat.Next(day)
	for i := 0; day.Before(now) && i < 10000; i++ {
		day = d.Repeat.Next(day)
	}
	d.Date = day.Format(DateLayout)
	return true
}

// Priority ranks a task, like Todoist: 1 is the most urgent, 3 the least; 0 is none.
type Priority int

// MaxPriority is the lowest-ranked priority that can be set.
const MaxPriority Priority = 3

// ActionItem is a follow-up the AI found in a recording or document: something someone
// committed to or was asked to do. It can be turned into a task note.
type ActionItem struct {
	ID    string `bson:"id" json:"id"`
	Text  string `bson:"text" json:"text"`
	Owner string `bson:"owner,omitempty" json:"owner,omitempty"`
	// Due is a date (YYYY-MM-DD) named or clearly meant in the conversation.
	Due string `bson:"due,omitempty" json:"due,omitempty"`
	// TaskID is the task note made from it.
	TaskID string `bson:"taskId,omitempty" json:"taskId,omitempty"`
	// Dismissed hides a suggestion the user doesn't want as a task.
	Dismissed bool `bson:"dismissed,omitempty" json:"dismissed,omitempty"`
}
