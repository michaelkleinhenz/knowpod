package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/webpush"
)

// taskFixture is a recording service for a user in Berlin, at a fixed time.
type taskFixture struct {
	s     *RecordingService
	recs  *memory.Recordings
	users *memory.Users
	acc   *Account
	now   time.Time
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()
	recs, users := memory.NewRecordings(), memory.NewUsers()
	spool, err := NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewRecordingService(recs, memstore.New(), spool, nil)
	s.Users = users
	f := &taskFixture{s: s, recs: recs, users: users, acc: &Account{ID: "u1"}, now: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)}
	s.clock = func() time.Time { return f.now }
	if err := users.Create(context.Background(), &user.User{ID: "u1", Email: "a@example.com", TimeZone: "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *taskFixture) note(t *testing.T, in TextNoteInput) *recording.Recording {
	t.Helper()
	if in.Title == "" {
		in.Title = "Note"
	}
	rec, err := f.s.CreateText(context.Background(), f.acc, in)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestSetDueMakesATaskAndSchedulesTheReminder(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := f.note(t, TextNoteInput{})

	got, err := f.s.SetDue(ctx, f.acc, rec.ID, &recording.Due{Date: "2026-09-28", Time: "9:05", Remind: ptr(10)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.Labels, label.Task) || got.Due.Time != "09:05" {
		t.Fatalf("got %+v, due %+v", got.Labels, got.Due)
	}
	// 9:05 in Berlin (UTC+2 in September) minus 10 minutes.
	if got.RemindAt == nil || !got.RemindAt.Equal(time.Date(2026, 9, 28, 6, 55, 0, 0, time.UTC)) {
		t.Errorf("remind at %v", got.RemindAt)
	}

	// A reminder in the past isn't scheduled.
	got, _ = f.s.SetDue(ctx, f.acc, rec.ID, &recording.Due{Date: "2026-09-27", Remind: ptr(0)})
	if got.RemindAt != nil {
		t.Errorf("past reminder scheduled at %v", got.RemindAt)
	}

	for _, bad := range []*recording.Due{
		{Date: "28.09.2026"}, {Date: "2026-09-28", Time: "25:00"},
		{Date: "2026-09-28", Remind: ptr(-5)}, {Date: "2026-09-28", Repeat: &recording.Repeat{Every: 1, Unit: "hour"}},
	} {
		if _, err := f.s.SetDue(ctx, f.acc, rec.ID, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	got, _ = f.s.SetDue(ctx, f.acc, rec.ID, nil)
	if got.Due != nil || got.RemindAt != nil {
		t.Errorf("cleared: %+v", got)
	}
}

func TestPriorityAndTakingTheTaskLabelOff(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := f.note(t, TextNoteInput{})
	if _, err := f.s.SetPriority(ctx, f.acc, rec.ID, 4); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("priority 4: %v", err)
	}
	got, err := f.s.SetPriority(ctx, f.acc, rec.ID, 1)
	if err != nil || got.Priority != 1 || !slices.Contains(got.Labels, label.Task) {
		t.Fatalf("%+v, %v", got, err)
	}
	if _, err := f.s.SetDue(ctx, f.acc, rec.ID, &recording.Due{Date: "2026-10-01", Remind: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	f.s.Labels = &LabelService{}
	got, err = f.s.SetLabels(ctx, f.acc, rec.ID, nil)
	if err != nil || got.Priority != 0 || got.Due != nil || got.RemindAt != nil {
		t.Errorf("after removing the task label: %+v, %v", got, err)
	}
}

func TestFinishingARecurringTaskEndsIt(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := f.note(t, TextNoteInput{TaskFields: TaskFields{Due: &recording.Due{
		Date: "2026-09-21", Time: "08:00", Remind: ptr(0), Repeat: &recording.Repeat{Every: 1, Unit: recording.RepeatWeek},
	}}})
	got, err := f.s.FinishTask(ctx, f.acc, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Done || got.DoneAt == nil || got.Due.Repeat != nil || got.Due.Date != "2026-09-21" || got.RemindAt != nil {
		t.Errorf("after finishing: done %v, due %+v, remind %v", got.Done, got.Due, got.RemindAt)
	}

	plain := f.note(t, TextNoteInput{})
	if _, err := f.s.FinishTask(ctx, f.acc, plain.ID); err == nil {
		t.Error("finished a note that isn't a task")
	}
}

func TestCheckingOffARecurringTaskMovesIt(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := f.note(t, TextNoteInput{TaskFields: TaskFields{Due: &recording.Due{
		Date: "2026-09-21", Time: "08:00", Remind: ptr(0), Repeat: &recording.Repeat{Every: 1, Unit: recording.RepeatWeek},
	}}})
	if !slices.Contains(rec.Labels, label.Task) || rec.RemindAt != nil {
		t.Fatalf("created %+v", rec)
	}
	got, err := f.s.SetDone(ctx, f.acc, rec.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Done || got.Due.Date != "2026-09-28" || got.RemindAt == nil || !got.RemindAt.Equal(time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)) {
		t.Errorf("after checking off: done %v, due %+v, remind %v", got.Done, got.Due, got.RemindAt)
	}

	once := f.note(t, TextNoteInput{TaskFields: TaskFields{Due: &recording.Due{Date: "2026-09-30", Remind: ptr(0)}}})
	got, _ = f.s.SetDone(ctx, f.acc, once.ID, true)
	if !got.Done || got.RemindAt != nil || got.Due.Date != "2026-09-30" {
		t.Errorf("one-off task: %+v", got)
	}
	got, _ = f.s.SetDone(ctx, f.acc, once.ID, false)
	if got.RemindAt == nil {
		t.Error("unchecking didn't schedule the reminder again")
	}
}

func TestRescheduleRemindersForANewTimeZone(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := f.note(t, TextNoteInput{TaskFields: TaskFields{Due: &recording.Due{Date: "2026-10-01", Time: "12:00", Remind: ptr(0)}}})
	u, _ := f.users.Get(ctx, "u1")
	u.TimeZone = "America/New_York"
	if err := f.s.RescheduleReminders(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, rec.ID)
	if got.RemindAt == nil || !got.RemindAt.Equal(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("remind at %v", got.RemindAt)
	}
}

func TestActionItemTask(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	src := &recording.Recording{ID: "r1", OwnerID: "u1", DeviceID: "d", ClientID: "c", Status: recording.StatusSummarized,
		Summary: &recording.Summary{Title: "Meeting", ActionItems: []recording.ActionItem{
			{ID: "a1", Text: "Send the offer to Anna", Owner: "Ben", Due: "2026-10-02"},
			{ID: "a2", Text: "Book a room"},
		}}}
	if err := f.recs.Create(ctx, src); err != nil {
		t.Fatal(err)
	}
	task, note, err := f.s.CreateActionItemTask(ctx, f.acc, "r1", "a1", ActionItemTask{})
	if err != nil {
		t.Fatal(err)
	}
	if task.Summary.Title != "Send the offer to Anna" || task.ParentID != "r1" || !slices.Contains(task.Labels, label.Task) ||
		task.Due == nil || task.Due.Date != "2026-10-02" || task.RemindAt == nil {
		t.Errorf("task %+v, due %+v", task, task.Due)
	}
	if note.Summary.ActionItems[0].TaskID != task.ID {
		t.Errorf("item %+v", note.Summary.ActionItems[0])
	}
	if _, _, err := f.s.CreateActionItemTask(ctx, f.acc, "r1", "a1", ActionItemTask{}); !errors.Is(err, ErrNotReady) {
		t.Errorf("second task: %v", err)
	}
	if _, _, err := f.s.CreateActionItemTask(ctx, f.acc, "r1", "nope", ActionItemTask{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown item: %v", err)
	}
	if _, _, err := f.s.CreateActionItemTask(ctx, &Account{ID: "other"}, "r1", "a2", ActionItemTask{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("someone else's note: %v", err)
	}
	got, err := f.s.DismissActionItem(ctx, f.acc, "r1", "a2", true)
	if err != nil || !got.Summary.ActionItems[1].Dismissed {
		t.Errorf("dismissed: %+v, %v", got.Summary.ActionItems, err)
	}
}

func TestParseActionItems(t *testing.T) {
	_, _, items := parseSummary(`{"title":"T","summary":"S","actionItems":[
		{"text":"  Call   the bank ","owner":"Ana","due":"2026-10-01"},
		{"text":"Write the report","due":"next week"},
		"Water the plants",
		{"text":""}, 42]}`)
	if len(items) != 3 {
		t.Fatalf("items %+v", items)
	}
	if items[0].Text != "Call the bank" || items[0].Owner != "Ana" || items[0].Due != "2026-10-01" || items[0].ID == "" {
		t.Errorf("first %+v", items[0])
	}
	if items[1].Due != "" || items[2].Text != "Water the plants" {
		t.Errorf("rest %+v", items[1:])
	}
}

// fakePusher records the messages sent and answers with err for endpoints in gone.
type fakePusher struct {
	sent []string
	gone map[string]bool
}

func (p *fakePusher) PublicKey() string { return "BPublic" }

func (p *fakePusher) Send(_ context.Context, sub webpush.Subscription, payload []byte, _ time.Duration) error {
	if p.gone[sub.Endpoint] {
		return webpush.ErrGone
	}
	p.sent = append(p.sent, sub.Endpoint+" "+string(payload))
	return nil
}

func TestReminders(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	subs := memory.NewPushSubscriptions()
	p := &fakePusher{gone: map[string]bool{}}
	n := NewNotificationService(subs, f.users, f.recs, p, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n.clock = func() time.Time { return f.now }
	n.allowHost = func(host string) bool { return host == "push.example" }

	key := strings.Repeat("A", 86) + "8" // 65 bytes base64url, not checked as a point here
	in := PushSubscriptionInput{Endpoint: "https://push.example/phone"}
	in.Keys.P256dh, in.Keys.Auth = "B"+key[1:], "c2VjcmV0c2VjcmV0c2VjcmV0"
	if _, err := n.Subscribe(ctx, f.acc, in, "Safari on iPhone"); err != nil {
		t.Fatal(err)
	}
	in.Endpoint = "https://push.example/old-laptop"
	if _, err := n.Subscribe(ctx, f.acc, in, "Firefox"); err != nil {
		t.Fatal(err)
	}
	p.gone["https://push.example/old-laptop"] = true
	for _, bad := range []string{"http://push.example/x", "https://169.254.169.254/latest", "https://push.example:8443/x", "https://evil.example/x"} {
		in.Endpoint = bad
		if _, err := n.Subscribe(ctx, f.acc, in, ""); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", bad, err)
		}
	}

	rec := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Call Anna"}, TaskFields: TaskFields{Due: &recording.Due{Date: "2026-09-27", Time: "12:30", Remind: ptr(15)}}})
	if rec.RemindAt == nil {
		t.Fatal("no reminder scheduled")
	}
	if c, _ := n.SendDueReminders(ctx); c != 0 {
		t.Errorf("sent %d reminders before they were due", c)
	}
	f.now = time.Date(2026, 9, 27, 10, 15, 0, 0, time.UTC) // 12:15 in Berlin
	if c, err := n.SendDueReminders(ctx); c != 1 || err != nil {
		t.Fatalf("sent %d, %v", c, err)
	}
	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "push.example/phone") || !strings.Contains(p.sent[0], `"title":"Call Anna"`) ||
		!strings.Contains(p.sent[0], "Due today at 12:30") || !strings.Contains(p.sent[0], "/conversations/"+rec.ID) {
		t.Errorf("sent %q", p.sent)
	}
	// Each reminder goes out once, and the gone subscription was forgotten.
	if c, _ := n.SendDueReminders(ctx); c != 0 {
		t.Errorf("sent the reminder again")
	}
	st, _ := n.Status(ctx, f.acc)
	if !st.Available || st.PublicKey != "BPublic" || len(st.Devices) != 1 || st.Devices[0].UserAgent != "Safari on iPhone" {
		t.Errorf("status %+v", st)
	}
	if err := n.Unsubscribe(ctx, f.acc, "https://push.example/phone"); err != nil {
		t.Fatal(err)
	}
	if st, _ := n.Status(ctx, f.acc); len(st.Devices) != 0 {
		t.Errorf("still subscribed: %+v", st.Devices)
	}
}

func TestDescribeDue(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		d    recording.Due
		lang string
		want string
	}{
		{recording.Due{Date: "2026-09-27", Time: "15:00"}, "", "Due today at 15:00"},
		{recording.Due{Date: "2026-09-28"}, "en", "Due tomorrow"},
		{recording.Due{Date: "2026-10-05", Time: "09:30"}, "", "Due on Mon, Oct 5 at 09:30"},
		{recording.Due{Date: "2026-09-27"}, "de", "Fällig heute"},
		{recording.Due{Date: "2026-10-05", Time: "09:30"}, "de", "Fällig am Mo. 05.10. um 09:30"},
	} {
		if got := describeDue(&c.d, c.lang, now); got != c.want {
			t.Errorf("%+v %s = %q, want %q", c.d, c.lang, got, c.want)
		}
	}
}

func TestKnownPushService(t *testing.T) {
	for host, ok := range map[string]bool{
		"fcm.googleapis.com": true, "web.push.apple.com": true, "updates.push.services.mozilla.com": true,
		"wns2-par02p.notify.windows.com": true, "FCM.googleapis.com.": true,
		"googleapis.com": false, "evilfcm.googleapis.com.evil": false, "push.apple.com.evil.example": false, "localhost": false, "10.0.0.1": false,
	} {
		if knownPushService(host) != ok {
			t.Errorf("%s: %v", host, !ok)
		}
	}
}

func TestCheckingOffRecordsWhen(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := f.note(t, TextNoteInput{TaskFields: TaskFields{Task: true}})
	got, err := f.s.SetDone(ctx, f.acc, rec.ID, true)
	if err != nil || got.DoneAt == nil || !got.DoneAt.Equal(f.now) {
		t.Fatalf("done: %+v, %v", got.DoneAt, err)
	}
	if got, _ = f.s.SetDone(ctx, f.acc, rec.ID, false); got.DoneAt != nil {
		t.Fatalf("reopened: %v", got.DoneAt)
	}
	// A repeating task stays open, but counts as done now.
	rep := f.note(t, TextNoteInput{TaskFields: TaskFields{Due: &recording.Due{Date: "2026-09-27", Repeat: &recording.Repeat{Every: 1, Unit: recording.RepeatDay}}}})
	if got, _ = f.s.SetDone(ctx, f.acc, rep.ID, true); got.Done || got.DoneAt == nil || got.Due.Date != "2026-09-28" {
		t.Fatalf("repeating: done %v at %v, due %+v", got.Done, got.DoneAt, got.Due)
	}
}

func TestEveryThirdFridayOfTheMonth(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := f.note(t, TextNoteInput{TaskFields: TaskFields{Due: &recording.Due{
		Date: "2026-10-16", Repeat: &recording.Repeat{Every: 1, Unit: recording.RepeatMonth, Weekdays: []int{5}, Nth: 3},
	}}})
	if r := rec.Due.Repeat; r.Nth != 3 || !slices.Equal(r.Weekdays, []int{5}) || r.MonthDay != 0 {
		t.Fatalf("stored repeat %+v", r)
	}
	f.now = time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	got, err := f.s.SetDone(ctx, f.acc, rec.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Done || got.Due.Date != "2026-11-20" {
		t.Errorf("after checking off: done %v, due %+v", got.Done, got.Due)
	}
	if _, err := f.s.SetDue(ctx, f.acc, rec.ID, &recording.Due{Date: "2026-10-16",
		Repeat: &recording.Repeat{Every: 1, Unit: recording.RepeatMonth, Weekdays: []int{5}, Nth: 5}}); err == nil {
		t.Error("a fifth Friday was accepted")
	}
}
