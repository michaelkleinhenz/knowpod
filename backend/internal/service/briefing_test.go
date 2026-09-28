package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

// briefingFixture is a user in Berlin with tasks and new notes, on Sunday 2026-09-27 at
// 12:00 Berlin time.
type briefingFixture struct {
	*taskFixture
	ai      *scriptedAI
	s       *BriefingService
	folders *memory.Folders
	times   *memory.TimeEntries
	notify  *NotificationService
}

func newBriefingFixture(t *testing.T, answers ...string) *briefingFixture {
	t.Helper()
	f := newTaskFixture(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ai := &scriptedAI{answers: answers}
	aiSvc, _ := newAI(t, &fakeAI{})
	aiSvc.ai = ai
	folders, times := memory.NewFolders(), memory.NewTimeEntries()
	s := NewBriefingService(f.users, f.s, folders, aiSvc, log)
	s.clock = func() time.Time { return f.now }
	s.TimeEntries = times
	s.Notifications = NewNotificationService(memory.NewPushSubscriptions(), f.users, f.recs, nil, log)

	yesterday := f.now.Add(-20 * time.Hour)
	for _, r := range []*recording.Recording{
		{ID: "overdue", Number: 1, Type: recording.TypeText, Labels: []string{label.Task}, Due: &recording.Due{Date: "2026-09-25"},
			Summary: &recording.Summary{Title: "Pay the invoice"}, CreatedAt: f.now.AddDate(0, 0, -10)},
		{ID: "today", Number: 2, Type: recording.TypeText, Labels: []string{label.Task}, Due: &recording.Due{Date: "2026-09-27", Time: "15:00"}, Priority: 1,
			Summary: &recording.Summary{Title: "Call Anna"}, CreatedAt: f.now.AddDate(0, 0, -10)},
		{ID: "done", Number: 3, Type: recording.TypeText, Labels: []string{label.Task}, Done: true, DoneAt: &yesterday, Estimate: 60,
			Summary: &recording.Summary{Title: "Write the report"}, CreatedAt: f.now.AddDate(0, 0, -10)},
		{ID: "stale", Number: 4, Type: recording.TypeText, Labels: []string{label.Task},
			Summary: &recording.Summary{Title: "Clean the garage"}, CreatedAt: f.now.AddDate(0, -3, 0), UpdatedAt: f.now.AddDate(0, -2, 0)},
		{ID: "meeting", Number: 5, Status: recording.StatusSummarized, CreatedAt: yesterday,
			Summary: &recording.Summary{Title: "Budget meeting", Markdown: "The budget is 40k.",
				ActionItems: []recording.ActionItem{{ID: "a", Text: "Send the offer", Owner: "Ben"}, {ID: "b", Text: "Done already", TaskID: "x"}}}},
	} {
		r.OwnerID, r.DeviceID, r.ClientID = "u1", "d", r.ID
		if r.Status == "" {
			r.Status = recording.StatusSummarized
		}
		if r.UpdatedAt.IsZero() {
			r.UpdatedAt = r.CreatedAt
		}
		if err := f.recs.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	return &briefingFixture{taskFixture: f, ai: ai, s: s, folders: folders, times: times, notify: s.Notifications}
}

func TestDailyBriefing(t *testing.T) {
	f := newBriefingFixture(t, "- The budget was set at 40k (#5, #99)")
	ctx := context.Background()
	msgs, stop, err := f.notify.Listen(f.acc)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	got, err := f.s.Today(ctx, f.acc, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Off || got.Title != "Briefing for Sun, Sep 27" || got.Day != "2026-09-27" || got.Summary != "1 due today · 1 overdue · 1 new notes" {
		t.Fatalf("briefing = %+v", got)
	}
	md := got.Markdown
	for _, want := range []string{
		"## Due today\n\n- [ ] Call Anna #2 — 15:00, P1",
		"## Overdue\n\n- [ ] Pay the invoice #1 — Sep 25",
		"## New since yesterday\n\n- The budget was set at 40k (#5, 99)",
		"## All new notes\n\n- Budget meeting #5 (recording)",
		"## Open action items\n\n- Send the offer (Ben) — from Budget meeting #5",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("briefing lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Done already") || strings.Contains(md, "garage") || strings.Contains(md, "Coming up") {
		t.Errorf("briefing lists what it shouldn't:\n%s", md)
	}
	// The digest is made from the new notes' summaries.
	if len(f.ai.requests) != 1 || !strings.Contains(f.ai.requests[0].Messages[1].Content.(string), "#5 Budget meeting") {
		t.Errorf("digest requests = %+v", f.ai.requests)
	}
	// It is no note, and one made on demand isn't announced.
	list, _ := f.recs.List(ctx, recording.ListFilter{OwnerID: "u1"})
	for _, r := range list {
		if r.Source == recording.SourceBriefing {
			t.Errorf("briefing note %+v", r)
		}
	}
	select {
	case m := <-msgs:
		t.Errorf("notification = %+v", m)
	default:
	}

	// It is kept for the day: asked again, it is the same without asking the model again.
	again, err := f.s.Today(ctx, f.acc, false, "")
	if err != nil || again.MadeAt != got.MadeAt || len(f.ai.requests) != 1 {
		t.Fatalf("again: %+v, %v, %d requests", again, err, len(f.ai.requests))
	}
	// Made anew, it is new.
	f.ai.answers = []string{"- digest"}
	f.now = f.now.Add(time.Hour)
	fresh, err := f.s.Today(ctx, f.acc, true, "")
	if err != nil || !fresh.MadeAt.After(got.MadeAt) || !strings.Contains(fresh.Markdown, "- digest") {
		t.Fatalf("made again: %+v, %v", fresh, err)
	}
}

func TestDailyBriefingSections(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	f.s.ai = nil
	in := BriefingSettings{Daily: true, Time: "07:00", Notify: true, Sections: []string{"upcoming", "actionItems"}, ActionItemDays: 1}
	if _, err := f.s.UpdateSettings(ctx, f.acc, in); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Today(ctx, f.acc, false, "")
	if err != nil {
		t.Fatal(err)
	}
	md := got.Markdown
	if !strings.Contains(md, "## Due today") || !strings.Contains(md, "## Open action items\n\n- Send the offer") ||
		strings.Contains(md, "Overdue") || strings.Contains(md, "New since yesterday") {
		t.Errorf("briefing:\n%s", md)
	}
	// Changed settings make it again.
	in.Sections = append(in.Sections, "overdue")
	if _, err := f.s.UpdateSettings(ctx, f.acc, in); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Today(ctx, f.acc, false, ""); !strings.Contains(got.Markdown, "## Overdue") {
		t.Errorf("not made again after the sections changed:\n%s", got.Markdown)
	}
	// With a look back of one day, the meeting's action items drop out 24 hours after it.
	f.now = f.now.Add(10 * time.Hour)
	again, _ := f.s.Today(ctx, f.acc, true, "")
	if strings.Contains(again.Markdown, "Open action items") {
		t.Errorf("action items older than a day:\n%s", again.Markdown)
	}

	// Turned off, there is none.
	in.Daily = false
	if _, err := f.s.UpdateSettings(ctx, f.acc, in); err != nil {
		t.Fatal(err)
	}
	if off, err := f.s.Today(ctx, f.acc, false, ""); err != nil || !off.Off || off.DailyBriefing != nil {
		t.Errorf("off: %+v, %v", off, err)
	}
}

func TestDailyBriefingInGermanWithoutAI(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	u, _ := f.users.Get(ctx, "u1")
	u.Language = "de"
	_ = f.users.Update(ctx, u)
	f.s.ai = nil

	got, err := f.s.Today(ctx, f.acc, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Briefing für So. 27. Sept." || !strings.Contains(got.Markdown, "## Heute fällig") ||
		!strings.Contains(got.Markdown, "## Neu seit gestern\n\n- Budget meeting #5 (Aufnahme)") || strings.Contains(got.Markdown, "Alle neuen") {
		t.Fatalf("briefing:\n%s\n%s", got.Title, got.Markdown)
	}
}

func TestDailyBriefingFollowsLanguage(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	f.s.ai = nil

	// Without a language in the settings, it is written in the one the app shows.
	got, err := f.s.Today(ctx, f.acc, false, "de")
	if err != nil || got.Language != "de" || !strings.HasPrefix(got.Title, "Briefing für") {
		t.Fatalf("browser language: %+v, %v", got, err)
	}
	// One made at the briefings' time without a hint keeps that language.
	u, _ := f.users.Get(ctx, "u1")
	if lang := briefingLanguage(u, ""); lang != "de" {
		t.Errorf("language without hint = %q", lang)
	}
	if lang := briefingLanguage(u, "fr"); lang != "de" {
		t.Errorf("language with unknown hint = %q", lang)
	}

	// The language chosen in the settings wins, and today's briefing is made again in it.
	u.Language = "en"
	_ = f.users.Update(ctx, u)
	got, err = f.s.Today(ctx, f.acc, false, "de")
	if err != nil || got.Language != "en" || !strings.HasPrefix(got.Title, "Briefing for") || !strings.Contains(got.Markdown, "## Due today") {
		t.Fatalf("after choosing English: %+v, %v", got, err)
	}
	u, _ = f.users.Get(ctx, "u1")
	u.Language = "de"
	_ = f.users.Update(ctx, u)
	got, err = f.s.Today(ctx, f.acc, false, "")
	if err != nil || got.Language != "de" || !strings.Contains(got.Markdown, "## Heute fällig") {
		t.Fatalf("after choosing German: %+v, %v", got, err)
	}
}

func TestWeeklyReview(t *testing.T) {
	f := newBriefingFixture(t, "- A week of budgets (#5)")
	ctx := context.Background()
	start := f.now.Add(-2 * time.Hour)
	end := f.now.Add(-30 * time.Minute)
	if err := f.times.Create(ctx, &timelog.Entry{ID: "e1", OwnerID: "u1", NoteID: "done", Start: start, End: &end}); err != nil {
		t.Fatal(err)
	}

	rec, err := f.s.MakeNow(ctx, f.acc, BriefingWeekly, "")
	if err != nil {
		t.Fatal(err)
	}
	md := rec.Summary.Markdown
	if rec.Summary.Title != "Weekly review, Sep 20 – 26" {
		t.Errorf("title = %q", rec.Summary.Title)
	}
	for _, want := range []string{
		"## This week\n\nNew: 1 × recording\n\n- A week of budgets (#5)",
		"## Done\n\n- [x] Write the report #3",
		"## Time\n\nLogged: **1h 30m**\n\n- 1h 30m: Write the report #3 (estimate 1h)",
		"## Overdue\n\n- [ ] Pay the invoice #1 — Sep 25",
		"## Coming up\n\n- [ ] Call Anna #2 — Sep 27, 15:00, P1",
		"## Waiting for a while\n\n- [ ] Clean the garage #4",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("review lacks %q:\n%s", want, md)
		}
	}
}

func TestBriefingsAreMadeOnceADayWhenDue(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	f.s.ai = nil
	msgs, stop, err := f.notify.Listen(f.acc)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	// Settings saved at 12:00 for 13:00: none today yet.
	if _, err := f.s.UpdateSettings(ctx, f.acc, BriefingSettings{Daily: true, Weekly: true, Time: "13:00", WeeklyDay: 0, Notify: true}); err != nil {
		t.Fatal(err)
	}
	reviews := func() int {
		n := 0
		list, _ := f.recs.List(ctx, recording.ListFilter{OwnerID: "u1"})
		for _, r := range list {
			if r.Source == recording.SourceBriefing {
				n++
			}
		}
		return n
	}
	announced := func() []string {
		var urls []string
		for {
			select {
			case m := <-msgs:
				urls = append(urls, m.URL)
			default:
				return urls
			}
		}
	}
	if n, err := f.s.MakeDue(ctx); err != nil || n != 0 {
		t.Fatalf("before the time: %d, %v", n, err)
	}
	// Looked at before its time, the briefing is made, but made again at its time.
	early, _ := f.s.Today(ctx, f.acc, false, "")
	f.now = f.now.Add(time.Hour) // 13:00 in Berlin, a Sunday: the daily briefing and the weekly review
	if n, err := f.s.MakeDue(ctx); err != nil || n != 2 || reviews() != 1 {
		t.Fatalf("at the time: %d, %v, %d reviews", n, err, reviews())
	}
	if urls := announced(); len(urls) != 2 || urls[1] != "/briefing" {
		t.Errorf("announced %v", urls)
	}
	if now, _ := f.s.Today(ctx, f.acc, false, ""); !now.MadeAt.After(early.MadeAt) {
		t.Errorf("briefing not made again at its time")
	}
	f.now = f.now.Add(time.Minute)
	if n, _ := f.s.MakeDue(ctx); n != 0 {
		t.Fatalf("made again: %d", n)
	}
	f.now = f.now.Add(24 * time.Hour) // Monday: only the daily one
	if n, _ := f.s.MakeDue(ctx); n != 1 || reviews() != 1 {
		t.Fatalf("next day: %d, %d reviews", n, reviews())
	}
	if got, _ := f.s.Today(ctx, f.acc, false, ""); got.Day != "2026-09-28" {
		t.Errorf("today = %+v", got)
	}
	got, _ := f.s.Settings(ctx, f.acc)
	if !got.Daily || !got.Weekly || got.Time != "13:00" || got.WeeklyDay != 0 || !got.Notify {
		t.Errorf("settings = %+v", got)
	}

	// Without notifications, the briefing is made but not announced.
	_ = announced()
	if _, err := f.s.UpdateSettings(ctx, f.acc, BriefingSettings{Daily: true, Time: "13:00"}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(24 * time.Hour)
	if n, _ := f.s.MakeDue(ctx); n != 1 {
		t.Fatalf("without notifications: %d", n)
	}
	if urls := announced(); len(urls) != 0 {
		t.Errorf("announced %v", urls)
	}

	// Turned on after today's time: nothing until tomorrow.
	if _, err := f.s.UpdateSettings(ctx, f.acc, BriefingSettings{Daily: true, Time: "07:00"}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	if n, _ := f.s.MakeDue(ctx); n != 0 {
		t.Fatalf("made right after turning on: %d", n)
	}
}

func TestBriefingSettingsAreChecked(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	for _, in := range []BriefingSettings{{Time: "7"}, {Time: "25:00"}, {Time: "07:00", WeeklyDay: 7},
		{Time: "07:00", ActionItemDays: 31}, {Time: "07:00", Sections: []string{"weather"}}} {
		if _, err := f.s.UpdateSettings(ctx, f.acc, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	for _, kind := range []BriefingKind{"monthly", BriefingDaily} {
		if _, err := f.s.MakeNow(ctx, f.acc, kind, ""); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("kind %q: %v", kind, err)
		}
	}
	// The daily briefing is on by default, announced, with the default sections.
	if got, _ := f.s.Settings(ctx, f.acc); got.Time != "07:00" || !got.Daily || got.Weekly || got.WeeklyDay != 1 || !got.Notify ||
		strings.Join(got.Sections, ",") != "overdue,new,digest,actionItems" || got.ActionItemDays != 7 {
		t.Errorf("default settings = %+v", got)
	}
}
