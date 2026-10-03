package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// BriefingKind is a daily briefing or a weekly review.
type BriefingKind string

const (
	BriefingDaily  BriefingKind = "daily"
	BriefingWeekly BriefingKind = "weekly"
)

const (
	// briefingFolder names the folder briefings go into (the same in English and German).
	briefingFolder = "Briefings"
	// staleTask is how long an open task without a date may go unchanged before the weekly
	// review brings it up.
	staleTask = 30 * 24 * time.Hour
	// maxDigestNotes bounds the notes the model reads for a briefing's digest; digestChars
	// each note's text.
	maxDigestNotes = 30
	digestChars    = 2500
	// maxBriefingItems bounds each list of a briefing.
	maxBriefingItems = 25
	// briefingTTL is how long a push service keeps a briefing's notification.
	briefingTTL = 12 * time.Hour
)

// BriefingService makes the users' daily briefings and weekly reviews with their tasks, what
// came in and (through the summary model, when it is set up) a digest of it, announced by a
// notification. The daily briefing is kept with the user and shown on the home page; the
// weekly review is a text note in their Briefings folder.
type BriefingService struct {
	users   ports.UserRepository
	notes   *RecordingService
	folders ports.FolderRepository
	ai      *AIService
	log     *slog.Logger
	clock   func() time.Time
	// Notifications announce new briefings. Optional.
	Notifications *NotificationService
	// TimeEntries adds the week's logged time to the weekly review. Optional.
	TimeEntries ports.TimeEntryRepository
}

// NewBriefingService builds the service. ai may be nil: briefings then have no digest.
func NewBriefingService(users ports.UserRepository, notes *RecordingService, folders ports.FolderRepository, ai *AIService, log *slog.Logger) *BriefingService {
	return &BriefingService{users: users, notes: notes, folders: folders, ai: ai, log: log, clock: time.Now}
}

// BriefingSettings are a user's briefing settings as the API shows and takes them.
type BriefingSettings struct {
	Daily  bool `json:"daily"`
	Weekly bool `json:"weekly"`
	// Time is when briefings are made, HH:MM in the user's time zone.
	Time string `json:"time"`
	// WeeklyDay is the day of the weekly review, 0 (Sunday) to 6.
	WeeklyDay int `json:"weeklyDay"`
	// Notify announces the daily briefing with a notification.
	Notify bool `json:"notify"`
	// Sections are the daily briefing's parts besides the tasks due today (see
	// user.BriefingSections); left out, they stay as they are.
	Sections []string `json:"sections"`
	// ActionItemDays is how many days back the daily briefing looks for open action items,
	// 1 to user.MaxActionItemDays; 0 keeps it as it is.
	ActionItemDays int `json:"actionItemDays"`
}

// Settings returns the account's briefing settings.
func (s *BriefingService) Settings(ctx context.Context, acc *Account) (*BriefingSettings, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	b := u.Briefing
	sections := []string{}
	for _, sec := range user.BriefingSections {
		if b.Shows(sec) {
			sections = append(sections, sec)
		}
	}
	return &BriefingSettings{Daily: b.Daily(), Weekly: b.Weekly, Time: b.At(), WeeklyDay: int(b.ReviewDay()),
		Notify: !b.NoNotify, Sections: sections, ActionItemDays: b.ActionDays()}, nil
}

// UpdateSettings changes the account's briefing settings.
func (s *BriefingService) UpdateSettings(ctx context.Context, acc *Account, in BriefingSettings) (*BriefingSettings, error) {
	in.Time = strings.TrimSpace(in.Time)
	if in.Time == "" {
		in.Time = user.DefaultBriefingTime
	}
	if _, err := time.Parse(recording.ClockLayout, in.Time); err != nil || len(in.Time) != 5 {
		return nil, invalid("time must look like 07:30")
	}
	if in.WeeklyDay < 0 || in.WeeklyDay > 6 {
		return nil, invalid("weeklyDay is 0 (Sunday) to 6 (Saturday)")
	}
	if in.ActionItemDays < 0 || in.ActionItemDays > user.MaxActionItemDays {
		return nil, invalid("actionItemDays is 1 to %d", user.MaxActionItemDays)
	}
	for _, sec := range in.Sections {
		if !slices.Contains(user.BriefingSections, sec) {
			return nil, invalid("unknown section %q; sections are %s", sec, strings.Join(user.BriefingSections, ", "))
		}
	}
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	b := &u.Briefing
	shown, days := slices.Clone(b.Sections), b.ActionItemDays
	day := in.WeeklyDay
	b.DailyOff, b.Weekly, b.Time, b.WeeklyDay, b.NoNotify = !in.Daily, in.Weekly, in.Time, &day, !in.Notify
	if in.Sections != nil {
		// Kept in their order, each once; an empty list is kept, too (not the default).
		b.Sections = []string{}
		for _, sec := range user.BriefingSections {
			if slices.Contains(in.Sections, sec) {
				b.Sections = append(b.Sections, sec)
			}
		}
	}
	if in.ActionItemDays > 0 {
		b.ActionItemDays = in.ActionItemDays
	}
	// Today's briefing shows what it shows no more: it is made again when it is next looked at.
	if !slices.Equal(shown, b.Sections) || (shown == nil) != (b.Sections == nil) || days != b.ActionItemDays {
		b.Today = nil
	}
	// Turned on (again) after today's time: the next one is made tomorrow, not right away.
	today, due := s.dueToday(u)
	if !due.After(s.clock()) {
		b.LastDaily = max(b.LastDaily, today)
		b.LastWeekly = max(b.LastWeekly, today)
	}
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return s.Settings(ctx, acc)
}

func (s *BriefingService) user(ctx context.Context, acc *Account) (*user.User, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("briefings belong to a user; sign in"))
	}
	return s.users.Get(ctx, acc.ID)
}

// dueToday returns today's date in the user's time zone and when today's briefings are due.
func (s *BriefingService) dueToday(u *user.User) (day string, due time.Time) {
	loc := u.Location()
	now := s.clock().In(loc)
	at, _ := time.Parse(recording.ClockLayout, u.Briefing.At())
	return now.Format(recording.DateLayout), time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, loc)
}

// briefingLanguage is the language the user's briefings are written in: the one chosen in
// the settings; without one, lang (the language the web app shows, e.g. the browser's), else
// the one the last daily briefing was written in, else English.
func briefingLanguage(u *user.User, lang string) string {
	switch {
	case u.Language != "":
		return u.Language
	case lang != "" && user.ValidLanguage(lang):
		return lang
	case u.Briefing.Today != nil && u.Briefing.Today.Language != "":
		return u.Briefing.Today.Language
	}
	return "en"
}

// MakeNow makes a weekly review of the account's right away (e.g. to try it) and returns its
// note. The daily briefing is made again with Today. lang is the language the web app shows,
// used when the user chose none in the settings.
func (s *BriefingService) MakeNow(ctx context.Context, acc *Account, kind BriefingKind, lang string) (*recording.Recording, error) {
	if kind == BriefingDaily {
		return nil, invalid("the daily briefing is no note; it is made again with POST /me/briefing/today")
	}
	if kind != BriefingWeekly {
		return nil, invalid("kind is %q", BriefingWeekly)
	}
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	rec, err := s.makeWeekly(ctx, u, briefingLanguage(u, lang))
	if err != nil {
		return nil, err
	}
	return present(acc, rec), nil
}

// TodayBriefing is the daily briefing the home page shows; Off (and nothing else) when the
// user turned the daily briefing off.
type TodayBriefing struct {
	Off bool `json:"off,omitempty"`
	*user.DailyBriefing
}

// Today returns the account's daily briefing for today, making it when there is none yet
// (or with again, anew), so it is there before its time, too. One made here is not
// announced; one made before the briefings' time is made again (and announced) then. lang is
// the language the web app shows, used when the user chose none in the settings; one kept in
// another language is made again in this one.
func (s *BriefingService) Today(ctx context.Context, acc *Account, again bool, lang string) (*TodayBriefing, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	if !u.Briefing.Daily() {
		return &TodayBriefing{Off: true}, nil
	}
	day, _ := s.dueToday(u)
	language := briefingLanguage(u, lang)
	if t := u.Briefing.Today; t != nil && t.Day == day && t.Language == language && !again {
		return &TodayBriefing{DailyBriefing: t}, nil
	}
	t, err := s.makeDaily(ctx, u, day, language)
	if err != nil {
		return nil, err
	}
	return &TodayBriefing{DailyBriefing: t}, nil
}

// Run makes the briefings that are due, every interval until ctx ends.
func (s *BriefingService) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if n, err := s.MakeDue(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("making briefings failed", "err", err)
		} else if n > 0 {
			s.log.Info("made briefings", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// MakeDue makes the briefings and reviews that are due and returns how many. Each is marked
// made before it is made, so it is made at most once, even when making it fails.
func (s *BriefingService) MakeDue(ctx context.Context) (int, error) {
	users, err := s.users.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range users {
		b := u.Briefing
		if !b.Daily() && !b.Weekly {
			continue
		}
		day, due := s.dueToday(u)
		if s.clock().Before(due) {
			continue
		}
		var kinds []BriefingKind
		if b.Weekly && b.LastWeekly != day && due.Weekday() == b.ReviewDay() {
			kinds = append(kinds, BriefingWeekly)
		}
		if b.Daily() && b.LastDaily != day {
			kinds = append(kinds, BriefingDaily)
		}
		if len(kinds) == 0 {
			continue
		}
		// Mark them on a fresh copy, so settings changed meanwhile stay.
		fresh, err := s.users.Get(ctx, u.ID)
		if err != nil {
			continue
		}
		for _, k := range kinds {
			if k == BriefingWeekly {
				fresh.Briefing.LastWeekly = day
			} else {
				fresh.Briefing.LastDaily = day
			}
		}
		if err := s.users.Update(ctx, fresh); err != nil {
			return n, err
		}
		for _, k := range kinds {
			var err error
			if k == BriefingWeekly {
				_, err = s.makeWeekly(ctx, fresh, briefingLanguage(fresh, ""))
			} else {
				err = s.announceDaily(ctx, fresh, day, briefingLanguage(fresh, ""))
			}
			if err != nil {
				s.log.Warn("making a briefing failed", "user", u.ID, "kind", string(k), "err", err)
				continue
			}
			n++
		}
	}
	return n, nil
}

// briefingData is what a briefing is made of.
type briefingData struct {
	u     *user.User
	acc   *Account
	loc   *time.Location
	now   time.Time
	today string
	de    bool
	notes []*recording.Recording
}

// data gathers what the user's briefings, written in lang, are made of.
func (s *BriefingService) data(ctx context.Context, u *user.User, lang string) (*briefingData, error) {
	acc := account(u)
	notes, err := s.notes.List(ctx, acc, recording.ListFilter{})
	if err != nil {
		return nil, err
	}
	loc := u.Location()
	now := s.clock().In(loc)
	return &briefingData{u: u, acc: acc, loc: loc, now: now, today: now.Format(recording.DateLayout), de: lang == "de", notes: notes}, nil
}

// makeDaily makes the user's daily briefing for day in lang and keeps it with the user.
func (s *BriefingService) makeDaily(ctx context.Context, u *user.User, day, lang string) (*user.DailyBriefing, error) {
	d, err := s.data(ctx, u, lang)
	if err != nil {
		return nil, err
	}
	title, markdown, summary := s.daily(ctx, d)
	t := &user.DailyBriefing{Day: day, Title: title, Markdown: truncateRunes(markdown, maxSummaryMarkdown/2), Summary: summary, Language: lang, MadeAt: s.clock().UTC()}
	// Kept on a fresh copy, so settings changed meanwhile stay.
	fresh, err := s.users.Get(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	fresh.Briefing.Today = t
	if err := s.users.Update(ctx, fresh); err != nil {
		return nil, err
	}
	u.Briefing.Today = t
	return t, nil
}

// announceDaily makes the user's daily briefing for day in lang and announces it, unless the
// user doesn't want that.
func (s *BriefingService) announceDaily(ctx context.Context, u *user.User, day, lang string) error {
	t, err := s.makeDaily(ctx, u, day, lang)
	if err != nil || s.Notifications == nil || u.Briefing.NoNotify {
		return err
	}
	m := Message{Title: t.Title, Body: t.Summary, URL: "/briefing", Tag: "briefing-" + string(BriefingDaily)}
	if _, err := s.Notifications.Notify(ctx, u.ID, m, briefingTTL); err != nil {
		s.log.Warn("announcing a briefing failed", "user", u.ID, "err", err)
	}
	return nil
}

// makeWeekly makes a weekly review of the user in lang: the note, and the notification
// announcing it.
func (s *BriefingService) makeWeekly(ctx context.Context, u *user.User, lang string) (*recording.Recording, error) {
	d, err := s.data(ctx, u, lang)
	if err != nil {
		return nil, err
	}
	title, markdown, summary := s.weekly(ctx, d)
	folderID, err := s.folder(ctx, u)
	if err != nil {
		return nil, err
	}
	id := newID()
	created := s.clock().UTC()
	rec := &recording.Recording{
		ID: id, OwnerID: u.ID, DeviceID: recording.TextDeviceID(u.ID), ClientID: id,
		Type: recording.TypeText, Source: recording.SourceBriefing, Status: recording.StatusSummarized, FolderID: folderID,
		Summary:   &recording.Summary{Title: title, Markdown: truncateRunes(markdown, maxSummaryMarkdown/2), CreatedAt: created},
		NotBefore: created, CreatedAt: created, UpdatedAt: created,
	}
	// A shared Briefings folder shares the review, too.
	members, err := folderMembers(ctx, s.folders, u.ID, folderID)
	if err != nil {
		return nil, err
	}
	rec.Members = recording.ComputeMembers(members, nil, nil)
	if err := s.notes.recs.Create(ctx, rec); err != nil {
		return nil, err
	}
	if s.Notifications != nil {
		m := Message{Title: title, Body: summary, URL: "/conversations/" + rec.ID, Tag: "briefing-" + string(BriefingWeekly)}
		if _, err := s.Notifications.Notify(ctx, u.ID, m, briefingTTL); err != nil {
			s.log.Warn("announcing a briefing failed", "user", u.ID, "err", err)
		}
	}
	return rec, nil
}

// folder returns the user's Briefings folder, making it when there is none.
func (s *BriefingService) folder(ctx context.Context, u *user.User) (string, error) {
	if id := u.Briefing.FolderID; id != "" {
		if f, err := s.folders.Get(ctx, id); err == nil && f.OwnerID == u.ID {
			return id, nil
		}
	}
	list, err := s.folders.List(ctx, u.ID)
	if err != nil {
		return "", err
	}
	id := ""
	for _, f := range list {
		if f.ParentID == "" && strings.EqualFold(f.Name, briefingFolder) {
			id = f.ID
			break
		}
	}
	if id == "" {
		now := s.clock().UTC()
		f := &folder.Folder{ID: newID(), OwnerID: u.ID, Name: briefingFolder, CreatedAt: now, UpdatedAt: now}
		if err := s.folders.Create(ctx, f); err != nil {
			return "", err
		}
		id = f.ID
	}
	fresh, err := s.users.Get(ctx, u.ID)
	if err != nil {
		return "", err
	}
	fresh.Briefing.FolderID = id
	u.Briefing.FolderID = id
	return id, s.users.Update(ctx, fresh)
}

// --- the briefings' contents ---

// tr picks the English or German text.
func (d *briefingData) tr(en, de string) string {
	if d.de {
		return de
	}
	return en
}

// ref names a note in a briefing: its title, linking to the note, then "#12" for the user's
// own notes (shared ones' numbers are their owner's, and notes may have none).
func (d *briefingData) ref(r *recording.Recording) string {
	link := fmt.Sprintf("[%s](/conversations/%s)", linkText.Replace(noteTitle(r)), r.ID)
	if r.OwnerID == d.u.ID && r.Number > 0 {
		return fmt.Sprintf("%s #%d", link, r.Number)
	}
	return link
}

// linkText keeps brackets in a title from ending its link's text early.
var linkText = strings.NewReplacer("[", "(", "]", ")")

// openTasks returns the open tasks matching keep, by date, time and priority.
func (d *briefingData) openTasks(keep func(r *recording.Recording) bool) []*recording.Recording {
	var out []*recording.Recording
	for _, r := range d.notes {
		if slices.Contains(r.Labels, label.Task) && !r.Done && r.DeletedAt == nil && r.Source != recording.SourceBriefing && keep(r) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b *recording.Recording) int {
		if c := strings.Compare(dueKey(a), dueKey(b)); c != 0 {
			return c
		}
		return int(priorityRank(a) - priorityRank(b))
	})
	return out
}

func dueKey(r *recording.Recording) string {
	if r.Due == nil {
		return "9999"
	}
	return r.Due.Date + " " + r.Due.Time
}

func priorityRank(r *recording.Recording) recording.Priority {
	if r.Priority == 0 {
		return 9
	}
	return r.Priority
}

// taskLine is a task in a list: its check box, title, time or date and priority.
func (d *briefingData) taskLine(r *recording.Recording, withDate bool) string {
	var b strings.Builder
	b.WriteString("- [ ] " + d.ref(r))
	var when []string
	if r.Due != nil {
		if withDate {
			if day, err := recording.ParseDate(r.Due.Date); err == nil {
				when = append(when, d.date(day))
			}
		}
		if r.Due.Time != "" {
			when = append(when, r.Due.Time)
		}
	}
	if r.Priority > 0 {
		when = append(when, fmt.Sprintf("P%d", r.Priority))
	}
	if len(when) > 0 {
		b.WriteString(" — " + strings.Join(when, ", "))
	}
	return b.String()
}

var monthsShortDE = []string{"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sept.", "Okt.", "Nov.", "Dez."}

// date formats a day in a list: "Sep 25", "25. Sept.".
func (d *briefingData) date(t time.Time) string {
	if d.de {
		return fmt.Sprintf("%d. %s", t.Day(), monthsShortDE[t.Month()-1])
	}
	return t.Format("Jan 2")
}

// shortDate formats a day briefly for a title: "Mon, Sep 28", "Mo. 28. Sept.".
func (d *briefingData) shortDate(t time.Time) string {
	if d.de {
		return fmt.Sprintf("%s %d. %s", weekdaysDE[t.Weekday()], t.Day(), monthsShortDE[t.Month()-1])
	}
	return t.Format("Mon, Jan 2")
}

// period formats the days from first to last briefly: "Sep 21 – 27", "21.–27. Sept.".
func (d *briefingData) period(first, last time.Time) string {
	same := first.Month() == last.Month()
	if d.de {
		if same {
			return fmt.Sprintf("%d.–%d. %s", first.Day(), last.Day(), monthsShortDE[last.Month()-1])
		}
		return fmt.Sprintf("%d. %s – %d. %s", first.Day(), monthsShortDE[first.Month()-1], last.Day(), monthsShortDE[last.Month()-1])
	}
	if same {
		return fmt.Sprintf("%s – %d", first.Format("Jan 2"), last.Day())
	}
	return first.Format("Jan 2") + " – " + last.Format("Jan 2")
}

// list writes a section with its lines; a section without lines is left out unless empty
// has a text for it.
func list(b *strings.Builder, heading string, lines []string, empty string) {
	if len(lines) == 0 && empty == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", heading)
	if len(lines) == 0 {
		b.WriteString("_" + empty + "_\n\n")
		return
	}
	if len(lines) > maxBriefingItems {
		more := len(lines) - maxBriefingItems
		lines = append(lines[:maxBriefingItems:maxBriefingItems], fmt.Sprintf("- … (+%d)", more))
	}
	b.WriteString(strings.Join(lines, "\n") + "\n\n")
}

// incoming returns the notes that came in since from: recordings, documents and text notes,
// but not boards or briefings; oldest first.
func (d *briefingData) incoming(from time.Time) []*recording.Recording {
	var out []*recording.Recording
	for _, r := range d.notes {
		if r.IsBoard() || r.Source == recording.SourceBriefing || r.CreatedAt.Before(from) || r.DeletedAt != nil {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *recording.Recording) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// kindName names the kind of a note for a briefing.
func (d *briefingData) kindName(r *recording.Recording) string {
	switch {
	case r.IsText():
		return d.tr("note", "Notiz")
	case r.IsImage():
		return d.tr("photo", "Foto")
	case r.IsDocument():
		return d.tr("document", "Dokument")
	default:
		return d.tr("recording", "Aufnahme")
	}
}

// daily makes the daily briefing: the tasks due today and, as the user chose, the overdue and
// upcoming ones, what came in since the last briefing (with a digest), and the open action
// items of the last days. summary is the notification's text.
func (s *BriefingService) daily(ctx context.Context, d *briefingData) (title, markdown, summary string) {
	title = d.tr("Briefing for ", "Briefing für ") + d.shortDate(d.now)
	b := d.u.Briefing
	var out strings.Builder

	today := d.openTasks(func(r *recording.Recording) bool { return r.Due != nil && r.Due.Date == d.today })
	var lines []string
	for _, r := range today {
		lines = append(lines, d.taskLine(r, false))
	}
	list(&out, d.tr("Due today", "Heute fällig"), lines, d.tr("Nothing is due today.", "Heute ist nichts fällig."))

	var overdue []*recording.Recording
	if b.Shows(user.SectionOverdue) {
		overdue = d.openTasks(func(r *recording.Recording) bool { return r.Due != nil && r.Due.Date < d.today })
		lines = nil
		for _, r := range overdue {
			lines = append(lines, d.taskLine(r, true))
		}
		list(&out, d.tr("Overdue", "Überfällig"), lines, "")
	}

	if b.Shows(user.SectionUpcoming) {
		next := d.now.AddDate(0, 0, 7).Format(recording.DateLayout)
		lines = nil
		for _, r := range d.openTasks(func(r *recording.Recording) bool { return r.Due != nil && r.Due.Date > d.today && r.Due.Date <= next }) {
			lines = append(lines, d.taskLine(r, true))
		}
		list(&out, d.tr("Coming up this week", "Diese Woche fällig"), lines, "")
	}

	// New is what came in since the day before, or since the last briefing when that was
	// longer ago (but no more than a week).
	since := d.now.Add(-24 * time.Hour)
	if last := b.Today; last != nil && last.MadeAt.Before(since) && d.now.Sub(last.MadeAt) < 7*24*time.Hour {
		since = last.MadeAt
	}
	var incoming []*recording.Recording
	if b.Shows(user.SectionNew) || b.Shows(user.SectionDigest) {
		incoming = d.incoming(since)
	}
	if len(incoming) > 0 {
		// The digest comes first, the notes below it; without a digest, the notes are all.
		newHeading := d.tr("New since yesterday", "Neu seit gestern")
		if b.Shows(user.SectionDigest) {
			if digest := s.digest(ctx, d, incoming, false); digest != "" {
				fmt.Fprintf(&out, "## %s\n\n%s\n\n", newHeading, digest)
				newHeading = d.tr("All new notes", "Alle neuen Notizen")
			}
		}
		if b.Shows(user.SectionNew) {
			lines = nil
			for _, r := range incoming {
				lines = append(lines, "- "+d.ref(r))
			}
			list(&out, newHeading, lines, "")
		}
	}

	if b.Shows(user.SectionActionItems) {
		items := d.actionItems(d.now.AddDate(0, 0, -b.ActionDays()))
		list(&out, d.tr("Open action items", "Offene Aufgaben aus Gesprächen"), items, "")
	}

	var parts []string
	if n := len(today); n > 0 {
		parts = append(parts, d.tr(fmt.Sprintf("%d due today", n), fmt.Sprintf("%d heute fällig", n)))
	}
	if n := len(overdue); n > 0 {
		parts = append(parts, d.tr(fmt.Sprintf("%d overdue", n), fmt.Sprintf("%d überfällig", n)))
	}
	if n := len(incoming); n > 0 {
		parts = append(parts, d.tr(fmt.Sprintf("%d new notes", n), fmt.Sprintf("%d neue Notizen", n)))
	}
	if len(parts) == 0 {
		parts = append(parts, d.tr("A quiet day: nothing due, nothing new.", "Ein ruhiger Tag: nichts fällig, nichts Neues."))
	}
	return title, strings.TrimSpace(out.String()), strings.Join(parts, " · ")
}

// actionItems lists the action items of the notes since from that were neither made into
// tasks nor dismissed.
func (d *briefingData) actionItems(from time.Time) []string {
	var lines []string
	for _, r := range d.incoming(from) {
		if r.Summary == nil {
			continue
		}
		for _, it := range r.Summary.ActionItems {
			if it.TaskID != "" || it.Dismissed {
				continue
			}
			line := "- " + it.Text
			if it.Owner != "" {
				line += " (" + it.Owner + ")"
			}
			lines = append(lines, line+" — "+d.tr("from ", "aus ")+d.ref(r))
		}
	}
	return lines
}

// weekly makes the weekly review of the seven days before today (and today so far): a digest
// of what came in, the tasks checked off, the time logged, and what is overdue, coming up and
// waiting.
func (s *BriefingService) weekly(ctx context.Context, d *briefingData) (title, markdown, summary string) {
	start := d.now.AddDate(0, 0, -7)
	first := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, d.loc)
	title = d.tr("Weekly review, ", "Wochenrückblick ") + d.period(first, d.now.AddDate(0, 0, -1))
	var b strings.Builder

	incoming := d.incoming(first)
	counts := map[string]int{}
	var kinds []string
	for _, r := range incoming {
		k := d.kindName(r)
		if counts[k] == 0 {
			kinds = append(kinds, k)
		}
		counts[k]++
	}
	fmt.Fprintf(&b, "## %s\n\n", d.tr("This week", "Diese Woche"))
	if len(incoming) == 0 {
		b.WriteString("_" + d.tr("No new notes this week.", "Diese Woche kamen keine neuen Notizen dazu.") + "_\n\n")
	} else {
		var parts []string
		for _, k := range kinds {
			parts = append(parts, fmt.Sprintf("%d × %s", counts[k], k))
		}
		b.WriteString(d.tr("New: ", "Neu: ") + strings.Join(parts, ", ") + "\n\n")
		if digest := s.digest(ctx, d, incoming, true); digest != "" {
			b.WriteString(digest + "\n\n")
		}
	}

	var done []string
	for _, r := range d.notes {
		if slices.Contains(r.Labels, label.Task) && r.DoneAt != nil && !r.DoneAt.Before(first) {
			done = append(done, "- [x] "+d.ref(r))
		}
	}
	list(&b, d.tr("Done", "Erledigt"), done, d.tr("No tasks checked off this week.", "Diese Woche wurden keine Aufgaben abgehakt."))

	if tracked := s.trackedTime(ctx, d, first); tracked != "" {
		b.WriteString(tracked)
	}

	var lines []string
	for _, r := range d.openTasks(func(r *recording.Recording) bool { return r.Due != nil && r.Due.Date < d.today }) {
		lines = append(lines, d.taskLine(r, true))
	}
	overdue := len(lines)
	list(&b, d.tr("Overdue", "Überfällig"), lines, "")

	next := d.now.AddDate(0, 0, 7).Format(recording.DateLayout)
	lines = nil
	for _, r := range d.openTasks(func(r *recording.Recording) bool { return r.Due != nil && r.Due.Date >= d.today && r.Due.Date <= next }) {
		lines = append(lines, d.taskLine(r, true))
	}
	list(&b, d.tr("Coming up", "Demnächst"), lines, "")

	lines = nil
	for _, r := range d.openTasks(func(r *recording.Recording) bool { return r.Due == nil && d.now.Sub(r.UpdatedAt) > staleTask }) {
		lines = append(lines, "- [ ] "+d.ref(r))
	}
	list(&b, d.tr("Waiting for a while", "Warten schon länger"), lines, "")

	list(&b, d.tr("Open action items", "Offene Aufgaben aus Gesprächen"), d.actionItems(first), "")

	summary = d.tr(fmt.Sprintf("%d new notes · %d tasks done", len(incoming), len(done)),
		fmt.Sprintf("%d neue Notizen · %d Aufgaben erledigt", len(incoming), len(done)))
	if overdue > 0 {
		summary += d.tr(fmt.Sprintf(" · %d overdue", overdue), fmt.Sprintf(" · %d überfällig", overdue))
	}
	return title, strings.TrimSpace(b.String()), summary
}

// trackedTime is the weekly review's section on the time logged since from: the total and
// the notes with the most time.
func (s *BriefingService) trackedTime(ctx context.Context, d *briefingData, from time.Time) string {
	if s.TimeEntries == nil {
		return ""
	}
	entries, err := s.TimeEntries.List(ctx, timelog.Range{OwnerID: d.u.ID, From: from, To: d.now.Add(time.Minute)})
	if err != nil || len(entries) == 0 {
		return ""
	}
	perNote := map[string]int64{}
	var total int64
	for _, e := range entries {
		sec := e.Seconds(d.now)
		perNote[e.NoteID] += sec
		total += sec
	}
	if total < 60 {
		return ""
	}
	ids := make([]string, 0, len(perNote))
	for id := range perNote {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int { return int(perNote[b] - perNote[a]) })
	byID := map[string]*recording.Recording{}
	for _, r := range d.notes {
		byID[r.ID] = r
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n%s: **%s**\n\n", d.tr("Time", "Zeit"), d.tr("Logged", "Erfasst"), hoursMinutes(total))
	for i, id := range ids {
		if i == 5 {
			break
		}
		name := d.tr("a deleted note", "eine gelöschte Notiz")
		if r := byID[id]; r != nil {
			name = d.ref(r)
			if r.Estimate > 0 {
				name += fmt.Sprintf(" (%s %s)", d.tr("estimate", "geschätzt"), hoursMinutes(int64(r.Estimate)*60))
			}
		}
		fmt.Fprintf(&b, "- %s: %s\n", hoursMinutes(perNote[id]), name)
	}
	b.WriteString("\n")
	return b.String()
}

// hoursMinutes formats a duration as "1h 30m".
func hoursMinutes(seconds int64) string {
	m := seconds / 60
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

// citeRun matches the sources the model cites in a digest: keys such as "[3]", "[3, 5]" or
// "[3][5]" (see digest).
var citeRun = regexp.MustCompile(`\[\d{1,3}(?:\s*[,;]\s*\d{1,3})*\](?:\s*[,;]?\s*\[\d{1,3}(?:\s*[,;]\s*\d{1,3})*\])*`)

// citeKey matches one key in a citeRun.
var citeKey = regexp.MustCompile(`\d{1,3}`)

// secretWords matches titles of notes that likely hold secrets (passwords, keys, tokens…),
// in English and German, which are never sent to the model. Short words must stand alone
// ("PIN", not "Shopping"); the longer ones also match within compounds ("Zugangsschlüssel").
var secretWords = regexp.MustCompile(`(?i)\b(keys?|tokens?|pins?|pin-?codes?|puk|tans?|otps?|totp|2fa|mfa|ssh|pgp|gpg|seed)\b|` +
	`passw(o|ö)r(d|t)|kennw(o|ö)rt|passphrase|password|credential|secret|geheim|token|api.?key|private.?key|` +
	`schl(ü|ue)ssel|zugangsdaten|anmeldedaten|login.?daten|logins?\b|zugangscode|sicherheitscode|` +
	`recovery.?codes?|backup.?codes?|wiederherstellungs|seed.?phrase|tresor|vault|iban|kreditkarte|credit.?card`)

// secretTitle says whether a note's title suggests it holds secrets.
func secretTitle(title string) bool {
	return secretWords.MatchString(title)
}

// digestRef names a note the digest draws on: "#12" for the user's own numbered notes, which
// the app shows as links, otherwise its title linking to the note.
func (d *briefingData) digestRef(r *recording.Recording) string {
	if r.OwnerID == d.u.ID && r.Number > 0 {
		return fmt.Sprintf("#%d", r.Number)
	}
	return fmt.Sprintf("[%s](/conversations/%s)", linkText.Replace(noteTitle(r)), r.ID)
}

// linkCitations turns the keys the model cited into links to the notes, as "(#12, [Title](…))";
// keys of notes it wasn't given are dropped. A "[3]" that is a Markdown link's text stays.
func (d *briefingData) linkCitations(text string, sources map[string]*recording.Recording) string {
	var out strings.Builder
	last := 0
	for _, m := range citeRun.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		if end < len(text) && text[end] == '(' {
			continue
		}
		// Parentheses the model put around the keys are replaced with ours.
		if open := strings.TrimRight(text[:start], " "); strings.HasSuffix(open, "(") {
			if rest := strings.TrimLeft(text[end:], " "); strings.HasPrefix(rest, ")") {
				start = len(open) - 1
				end = len(text) - len(rest) + 1
			}
		}
		var refs []string
		seen := map[string]bool{}
		for _, k := range citeKey.FindAllString(text[m[0]:m[1]], -1) {
			if r := sources[k]; r != nil && !seen[k] {
				seen[k] = true
				refs = append(refs, d.digestRef(r))
			}
		}
		head := text[last:start]
		if len(refs) == 0 {
			head = strings.TrimRight(head, " ")
		}
		out.WriteString(head)
		if len(refs) > 0 {
			out.WriteString("(" + strings.Join(refs, ", ") + ")")
		}
		last = end
	}
	out.WriteString(text[last:])
	return out.String()
}

// digest has the summary model sum up the notes that came in: what they were about, what was
// decided, what is open. It is empty when the model isn't set up, fails or there is nothing
// to sum up.
func (s *BriefingService) digest(ctx context.Context, d *briefingData, notes []*recording.Recording, week bool) string {
	if s.ai == nil {
		return ""
	}
	st, err := s.ai.settings.OpenRouter(ctx)
	if err != nil || !st.CanSummarize() {
		return ""
	}
	var in strings.Builder
	n := 0
	// Each note is given with a key ("[3]") the model cites it by; the keys become links.
	sources := map[string]*recording.Recording{}
	for i := len(notes) - 1; i >= 0 && n < maxDigestNotes; i-- {
		r := notes[i]
		if r.Summary == nil || strings.TrimSpace(r.Summary.Markdown) == "" || secretTitle(noteTitle(r)) {
			continue
		}
		key := fmt.Sprint(n + 1)
		sources[key] = r
		fmt.Fprintf(&in, "### [%s] %s (%s, %s)\n%s\n\n", key, noteTitle(r), d.kindName(r),
			r.CreatedAt.In(d.loc).Format("Mon 2006-01-02 15:04"), truncateRunes(r.Summary.Markdown, digestChars))
		n++
	}
	if n == 0 {
		return ""
	}
	period := "since yesterday"
	length := "3 to 6"
	if week {
		period, length = "this week", "4 to 8"
	}
	system := `You write the digest part of the user's personal briefing: what came into their notes ` + period + `, given as the notes' summaries below (each headed by its key such as [3], its title, its kind and when it was made).
Write ` + length + ` Markdown bullet points: the main topics, decisions, and things that need the user's attention, most important first. Be concrete (names, dates, numbers) and brief (one sentence each). End each bullet with the keys of the notes it draws on, exactly as given in square brackets, e.g. "[1][3]". Don't name notes by any other number.
Write in ` + d.tr("English", "German") + `. Output only the bullet list.`
	answer, err := s.ai.ai.Complete(ctx, st.APIKey, openrouter.Request{Model: st.SummaryModel, Messages: []openrouter.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: in.String()},
	}})
	if err != nil {
		s.log.Warn("briefing digest failed", "user", d.u.ID, "err", err)
		return ""
	}
	return d.linkCitations(strings.TrimSpace(stripFence(answer)), sources)
}
