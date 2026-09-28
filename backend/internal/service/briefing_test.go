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

	rec, err := f.s.MakeNow(ctx, f.acc, BriefingDaily)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Summary.Title != "Briefing for Sun, Sep 27" || rec.Source != recording.SourceBriefing || !rec.IsText() {
		t.Fatalf("briefing = %+v", rec)
	}
	md := rec.Summary.Markdown
	for _, want := range []string{
		"## Due today\n\n- [ ] Call Anna #2 — 15:00, P1",
		"## Overdue\n\n- [ ] Pay the invoice #1 — Sep 25",
		"## New since yesterday\n\n- The budget was set at 40k (#5, 99)",
		"- Budget meeting #5 (recording)",
		"## Open action items\n\n- Send the offer (Ben) — from Budget meeting #5",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("briefing lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Done already") || strings.Contains(md, "garage") {
		t.Errorf("briefing lists what it shouldn't:\n%s", md)
	}
	// The digest is made from the new notes' summaries.
	if len(f.ai.requests) != 1 || !strings.Contains(f.ai.requests[0].Messages[1].Content.(string), "#5 Budget meeting") {
		t.Errorf("digest requests = %+v", f.ai.requests)
	}
	// It goes into the Briefings folder, made on first use.
	list, _ := f.folders.List(ctx, "u1")
	if len(list) != 1 || list[0].Name != "Briefings" || rec.FolderID != list[0].ID {
		t.Errorf("folders = %+v, note in %q", list, rec.FolderID)
	}
	select {
	case m := <-msgs:
		if m.Title != rec.Summary.Title || m.Body != "1 due today · 1 overdue · 1 new notes" || m.URL != "/conversations/"+rec.ID {
			t.Errorf("notification = %+v", m)
		}
	default:
		t.Error("no notification")
	}

	// The next briefing goes into the same folder and doesn't list the first one as new.
	f.ai.answers = []string{"- digest"}
	next, err := f.s.MakeNow(ctx, f.acc, BriefingDaily)
	if err != nil {
		t.Fatal(err)
	}
	if next.FolderID != rec.FolderID || strings.Contains(next.Summary.Markdown, "Briefing for") {
		t.Errorf("second briefing:\n%s", next.Summary.Markdown)
	}
}

func TestDailyBriefingInGermanWithoutAI(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	u, _ := f.users.Get(ctx, "u1")
	u.Language = "de"
	_ = f.users.Update(ctx, u)
	f.s.ai = nil

	rec, err := f.s.MakeNow(ctx, f.acc, BriefingDaily)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Summary.Title != "Briefing für So. 27. Sept." || !strings.Contains(rec.Summary.Markdown, "## Heute fällig") ||
		!strings.Contains(rec.Summary.Markdown, "- Budget meeting #5 (Aufnahme)") {
		t.Fatalf("briefing:\n%s\n%s", rec.Summary.Title, rec.Summary.Markdown)
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

	rec, err := f.s.MakeNow(ctx, f.acc, BriefingWeekly)
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
	// Settings saved at 12:00 for 13:00: none today yet.
	if _, err := f.s.UpdateSettings(ctx, f.acc, BriefingSettings{Daily: true, Weekly: true, Time: "13:00", WeeklyDay: 0}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		n := 0
		list, _ := f.recs.List(ctx, recording.ListFilter{OwnerID: "u1"})
		for _, r := range list {
			if r.Source == recording.SourceBriefing {
				n++
			}
		}
		return n
	}
	if n, err := f.s.MakeDue(ctx); err != nil || n != 0 {
		t.Fatalf("before the time: %d, %v", n, err)
	}
	f.now = f.now.Add(time.Hour) // 13:00 in Berlin, a Sunday: the daily briefing and the weekly review
	if n, err := f.s.MakeDue(ctx); err != nil || n != 2 || count() != 2 {
		t.Fatalf("at the time: %d, %v, %d notes", n, err, count())
	}
	f.now = f.now.Add(time.Minute)
	if n, _ := f.s.MakeDue(ctx); n != 0 {
		t.Fatalf("made again: %d", n)
	}
	f.now = f.now.Add(24 * time.Hour) // Monday: only the daily one
	if n, _ := f.s.MakeDue(ctx); n != 1 || count() != 3 {
		t.Fatalf("next day: %d, %d notes", n, count())
	}
	got, _ := f.s.Settings(ctx, f.acc)
	if !got.Daily || !got.Weekly || got.Time != "13:00" || got.WeeklyDay != 0 {
		t.Errorf("settings = %+v", got)
	}

	// Turned on after today's time: nothing until tomorrow.
	if _, err := f.s.UpdateSettings(ctx, f.acc, BriefingSettings{Daily: true, Time: "07:00"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.s.MakeDue(ctx); n != 0 {
		t.Fatalf("made right after turning on: %d", n)
	}
}

func TestBriefingSettingsAreChecked(t *testing.T) {
	f := newBriefingFixture(t)
	ctx := context.Background()
	for _, in := range []BriefingSettings{{Time: "7"}, {Time: "25:00"}, {Time: "07:00", WeeklyDay: 7}} {
		if _, err := f.s.UpdateSettings(ctx, f.acc, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	if _, err := f.s.MakeNow(ctx, f.acc, "monthly"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown kind: %v", err)
	}
	if got, _ := f.s.Settings(ctx, f.acc); got.Time != "07:00" || got.Daily || got.WeeklyDay != 1 {
		t.Errorf("default settings = %+v", got)
	}
}
