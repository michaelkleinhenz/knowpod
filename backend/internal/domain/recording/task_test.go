package recording

import (
	"testing"
	"time"
)

func TestRepeatNext(t *testing.T) {
	for _, c := range []struct {
		r    Repeat
		from string
		want string
	}{
		{Repeat{Every: 1, Unit: RepeatDay}, "2026-09-28", "2026-09-29"},
		{Repeat{Every: 3, Unit: RepeatDay}, "2026-09-30", "2026-10-03"},
		{Repeat{Every: 1, Unit: RepeatWeekday}, "2026-10-02", "2026-10-05"}, // Friday → Monday
		{Repeat{Every: 1, Unit: RepeatWeekday}, "2026-09-28", "2026-09-29"},
		{Repeat{Every: 2, Unit: RepeatWeek}, "2026-09-28", "2026-10-12"},
		{Repeat{Every: 1, Unit: RepeatWeek, Weekdays: []int{1, 5}}, "2026-09-28", "2026-10-02"}, // Mon → Fri
		{Repeat{Every: 1, Unit: RepeatWeek, Weekdays: []int{1, 5}}, "2026-10-02", "2026-10-05"}, // Fri → Mon
		{Repeat{Every: 2, Unit: RepeatWeek, Weekdays: []int{1, 5}}, "2026-10-02", "2026-10-12"},
		{Repeat{Every: 1, Unit: RepeatMonth, MonthDay: 31}, "2026-01-31", "2026-02-28"},
		{Repeat{Every: 1, Unit: RepeatMonth, MonthDay: 31}, "2026-02-28", "2026-03-31"},
		{Repeat{Every: 3, Unit: RepeatMonth}, "2026-11-15", "2027-02-15"},
		{Repeat{Every: 1, Unit: RepeatYear}, "2028-02-29", "2029-02-28"},
	} {
		d, _ := ParseDate(c.from)
		if got := c.r.Next(d).Format(DateLayout); got != c.want {
			t.Errorf("%+v from %s = %s, want %s", c.r, c.from, got, c.want)
		}
	}
}

func TestRepeatValid(t *testing.T) {
	for _, c := range []struct {
		r  Repeat
		ok bool
	}{
		{Repeat{Every: 1, Unit: RepeatDay}, true},
		{Repeat{Every: 0, Unit: RepeatDay}, false},
		{Repeat{Every: 1, Unit: "hour"}, false},
		{Repeat{Every: 2, Unit: RepeatWeekday}, false},
		{Repeat{Every: 1, Unit: RepeatWeek, Weekdays: []int{1, 1}}, false},
		{Repeat{Every: 1, Unit: RepeatWeek, Weekdays: []int{7}}, false},
		{Repeat{Every: 1, Unit: RepeatDay, Weekdays: []int{1}}, false},
	} {
		if c.r.Valid() != c.ok {
			t.Errorf("%+v valid = %v", c.r, !c.ok)
		}
	}
}

func TestDueAdvanceAndReminder(t *testing.T) {
	d := &Due{Date: "2026-09-01", Repeat: &Repeat{Every: 1, Unit: RepeatWeek}}
	// Long overdue: moved to the first occurrence from today on, not just one week.
	if !d.Advance("2026-09-27") || d.Date != "2026-09-29" {
		t.Errorf("advanced to %s", d.Date)
	}
	// Due today: moves at least one step.
	d = &Due{Date: "2026-09-27", Repeat: &Repeat{Every: 1, Unit: RepeatDay}}
	if d.Advance("2026-09-27"); d.Date != "2026-09-28" {
		t.Errorf("advanced to %s", d.Date)
	}
	if (&Due{Date: "2026-09-27"}).Advance("2026-09-27") {
		t.Error("a task without repeat advanced")
	}

	berlin, _ := time.LoadLocation("Europe/Berlin")
	fifteen := 15
	at := (&Due{Date: "2026-09-28", Time: "10:00", Remind: &fifteen}).ReminderAt(berlin)
	if at == nil || !at.Equal(time.Date(2026, 9, 28, 7, 45, 0, 0, time.UTC)) {
		t.Errorf("timed reminder at %v", at)
	}
	zero := 0
	at = (&Due{Date: "2026-12-01", Remind: &zero}).ReminderAt(berlin)
	if at == nil || !at.Equal(time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("all-day reminder at %v", at)
	}
	if (&Due{Date: "2026-12-01"}).ReminderAt(berlin) != nil {
		t.Error("a reminder without Remind")
	}
}
