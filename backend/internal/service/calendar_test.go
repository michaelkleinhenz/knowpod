package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

func TestCalendarFeed(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	cal := NewCalendarService(f.users, f.recs)
	cal.clock = func() time.Time { return f.now }

	if _, err := cal.Feed(ctx, "kpc_nothing", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	v, err := cal.Enable(ctx, f.acc)
	if err != nil || !v.Enabled || !strings.HasPrefix(v.FeedPath, "/api/v1/calendar/kpc_") || !strings.HasSuffix(v.FeedPath, ".ics") {
		t.Fatalf("enable: %+v %v", v, err)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(v.FeedPath, "/api/v1/calendar/"), ".ics")
	if st, _ := cal.Status(ctx, f.acc); !st.Enabled || st.FeedPath != "" {
		t.Errorf("status: %+v", st)
	}

	allDay := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Pay rent, now; really"}, TaskFields: TaskFields{
		Due: &recording.Due{Date: "2026-10-01", Remind: ptr(0), Repeat: &recording.Repeat{Every: 1, Unit: recording.RepeatMonth, MonthDay: 31}}, Priority: 1}})
	timed := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Call Anna"}, TaskFields: TaskFields{Due: &recording.Due{Date: "2026-09-28", Time: "15:00", Remind: ptr(15)}}})
	if _, err := f.s.SetEstimate(ctx, f.acc, timed.ID, 45); err != nil {
		t.Fatal(err)
	}
	weekly := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Standup"}, TaskFields: TaskFields{
		Due: &recording.Due{Date: "2026-09-28", Time: "09:30", Repeat: &recording.Repeat{Every: 2, Unit: recording.RepeatWeek, Weekdays: []int{1, 4}}}}})
	done := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Done already"}, TaskFields: TaskFields{Due: &recording.Due{Date: "2026-09-28"}}})
	if _, err := f.s.SetDone(ctx, f.acc, done.ID, true); err != nil {
		t.Fatal(err)
	}
	f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "No date"}, TaskFields: TaskFields{Task: true}})

	data, err := cal.Feed(ctx, token, "https://kp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	feed := string(data)
	unfolded := strings.ReplaceAll(feed, "\r\n ", "")
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n", "X-WR-TIMEZONE:Europe/Berlin\r\n",
		// All day, the last day of the month, reminded at 9:00.
		"UID:" + allDay.ID + "@knowpod\r\n", `SUMMARY:Pay rent\, now\; really`, "DTSTART;VALUE=DATE:20261001\r\n", "DTEND;VALUE=DATE:20261002\r\n",
		"RRULE:FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1\r\n", "PRIORITY:1\r\n", "TRIGGER:PT9H\r\n",
		"URL:https://kp.example.com/conversations/" + allDay.ID + "\r\n",
		// Timed, in UTC (Berlin is UTC+2), lasting its estimate.
		"DTSTART:20260928T130000Z\r\nDTEND:20260928T134500Z\r\n", "TRIGGER:-PT15M\r\n",
		// Repeating with a time: in the user's time zone.
		"DTSTART;TZID=Europe/Berlin:20260928T093000\r\nDTEND;TZID=Europe/Berlin:20260928T100000\r\n",
		"RRULE:FREQ=WEEKLY;INTERVAL=2;WKST=SU;BYDAY=MO,TH\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(unfolded, want) {
			t.Errorf("feed lacks %q", want)
		}
	}
	for _, not := range []string{"Done already", "No date"} {
		if strings.Contains(unfolded, not) {
			t.Errorf("feed lists %q", not)
		}
	}
	for _, line := range strings.Split(feed, "\r\n") {
		if len(line) > 75 {
			t.Errorf("line longer than 75 octets: %q", line)
		}
	}
	_ = weekly

	// A new link replaces the old one; turning it off ends it.
	v2, _ := cal.Enable(ctx, f.acc)
	if _, err := cal.Feed(ctx, token, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("old link still works: %v", err)
	}
	if err := cal.Disable(ctx, f.acc); err != nil {
		t.Fatal(err)
	}
	token2 := strings.TrimSuffix(strings.TrimPrefix(v2.FeedPath, "/api/v1/calendar/"), ".ics")
	if _, err := cal.Feed(ctx, token2, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("disabled link still works: %v", err)
	}
}

func TestICSHelpers(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "PT0M", -15 * time.Minute: "-PT15M", 9 * time.Hour: "PT9H", -(24*time.Hour + 30*time.Minute): "-P1DT30M", 48 * time.Hour: "P2D",
	} {
		if got := icsDuration(d); got != want {
			t.Errorf("icsDuration(%v) = %q, want %q", d, got, want)
		}
	}
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		rp   recording.Repeat
		want string
	}{
		{recording.Repeat{Every: 1, Unit: recording.RepeatDay}, "FREQ=DAILY"},
		{recording.Repeat{Every: 3, Unit: recording.RepeatDay}, "FREQ=DAILY;INTERVAL=3"},
		{recording.Repeat{Every: 1, Unit: recording.RepeatWeekday}, "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{recording.Repeat{Every: 1, Unit: recording.RepeatWeek}, "FREQ=WEEKLY;WKST=SU"},
		{recording.Repeat{Every: 1, Unit: recording.RepeatMonth}, "FREQ=MONTHLY;BYMONTHDAY=15"},
		{recording.Repeat{Every: 1, Unit: recording.RepeatMonth, MonthDay: 30}, "FREQ=MONTHLY;BYMONTHDAY=28,29,30;BYSETPOS=-1"},
		{recording.Repeat{Every: 1, Unit: recording.RepeatMonth, Weekdays: []int{5}, Nth: 3}, "FREQ=MONTHLY;BYDAY=3FR"},
		{recording.Repeat{Every: 2, Unit: recording.RepeatMonth, Weekdays: []int{1}, Nth: recording.LastWeek}, "FREQ=MONTHLY;INTERVAL=2;BYDAY=-1MO"},
		{recording.Repeat{Every: 2, Unit: recording.RepeatYear}, "FREQ=YEARLY;INTERVAL=2"},
	} {
		if got := rrule(&tc.rp, day); got != tc.want {
			t.Errorf("rrule(%+v) = %q, want %q", tc.rp, got, tc.want)
		}
	}
	var c ics
	c.text("SUMMARY", strings.Repeat("ä", 60))
	for _, line := range strings.Split(strings.TrimSuffix(c.buf.String(), "\r\n"), "\r\n") {
		if len(line) > 75 || !strings.HasPrefix(strings.TrimPrefix(line, " "), "SUMMARY") && !strings.HasPrefix(line, " ") {
			t.Errorf("bad folded line %q", line)
		}
	}
}
