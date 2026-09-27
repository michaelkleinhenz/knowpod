package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// defaultEventMinutes is how long a task with a time but no estimate lasts in the calendar.
const defaultEventMinutes = 30

// CalendarView is the state of the account's calendar feed. FeedPath is only returned when
// a feed link was just made: the link holds a secret that isn't stored.
type CalendarView struct {
	Enabled   bool       `json:"enabled"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	FeedPath  string     `json:"feedPath,omitempty"`
}

// CalendarService publishes each user's tasks with dates as a read-only iCalendar feed,
// which calendar apps subscribe to by its secret link.
type CalendarService struct {
	users ports.UserRepository
	recs  ports.RecordingRepository
	clock func() time.Time
}

// NewCalendarService builds the service.
func NewCalendarService(users ports.UserRepository, recs ports.RecordingRepository) *CalendarService {
	return &CalendarService{users: users, recs: recs, clock: time.Now}
}

// CalendarFeedPath is the path of a calendar feed.
func CalendarFeedPath(token string) string { return "/api/v1/calendar/" + token + ".ics" }

// Status says whether the account has a calendar feed.
func (s *CalendarService) Status(ctx context.Context, acc *Account) (*CalendarView, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	return &CalendarView{Enabled: u.Calendar.TokenHash != "", CreatedAt: u.Calendar.CreatedAt}, nil
}

// Enable makes a new feed link for the account, replacing the previous one (which stops
// working), and returns it.
func (s *CalendarService) Enable(ctx context.Context, acc *Account) (*CalendarView, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	var b [24]byte
	_, _ = rand.Read(b[:])
	token := "kpc_" + base64.RawURLEncoding.EncodeToString(b[:])
	now := s.clock().UTC()
	u.Calendar = user.Calendar{TokenHash: hashToken(token), CreatedAt: &now}
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return &CalendarView{Enabled: true, CreatedAt: &now, FeedPath: CalendarFeedPath(token)}, nil
}

// Disable turns the account's feed off; its link stops working.
func (s *CalendarService) Disable(ctx context.Context, acc *Account) error {
	u, err := s.user(ctx, acc)
	if err != nil {
		return err
	}
	u.Calendar = user.Calendar{}
	return s.users.Update(ctx, u)
}

func (s *CalendarService) user(ctx context.Context, acc *Account) (*user.User, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("calendar feeds belong to a user; sign in"))
	}
	return s.users.Get(ctx, acc.ID)
}

// Feed returns the iCalendar feed of the user whose link holds token: an event for every
// open task with a date (not in the trash), repeating like the task, with its reminder.
// baseURL (e.g. "https://knowpod.example.com") is used to link each event to its note.
// An unknown token returns ErrNotFound.
func (s *CalendarService) Feed(ctx context.Context, token, baseURL string) ([]byte, error) {
	if !strings.HasPrefix(token, "kpc_") {
		return nil, ErrNotFound
	}
	u, err := s.users.GetByCalendarTokenHash(ctx, hashToken(token))
	if err != nil {
		return nil, err
	}
	// The user's tasks and those shared with them.
	list, err := s.recs.List(ctx, recording.ListFilter{UserID: u.ID, Brief: true})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(list, func(a, b *recording.Recording) int { return strings.Compare(a.ID, b.ID) })
	loc := u.Location()
	var c ics
	c.prop("BEGIN", "VCALENDAR")
	c.prop("VERSION", "2.0")
	c.prop("PRODID", "-//knowpod//tasks//EN")
	c.prop("CALSCALE", "GREGORIAN")
	c.prop("METHOD", "PUBLISH")
	c.prop("X-WR-CALNAME", "knowpod")
	c.prop("X-WR-TIMEZONE", loc.String())
	c.prop("REFRESH-INTERVAL;VALUE=DURATION", "PT1H")
	c.prop("X-PUBLISHED-TTL", "PT1H")
	for _, r := range list {
		if r.Due == nil || r.Done || r.DeletedAt != nil || r.IsBoard() {
			continue
		}
		event(&c, r, loc, strings.TrimRight(baseURL, "/"), s.clock())
	}
	c.prop("END", "VCALENDAR")
	return []byte(c.buf.String()), nil
}

// event writes the calendar event of a task.
func event(c *ics, r *recording.Recording, loc *time.Location, baseURL string, now time.Time) {
	day, err := recording.ParseDate(r.Due.Date)
	if err != nil {
		return
	}
	start, err := r.Due.At(loc)
	if err != nil {
		return
	}
	title := noteTitle(r)
	c.prop("BEGIN", "VEVENT")
	c.prop("UID", r.ID+"@knowpod")
	stamp := r.UpdatedAt
	if stamp.IsZero() {
		stamp = now
	}
	c.prop("DTSTAMP", stamp.UTC().Format(icsUTC))
	c.text("SUMMARY", title)
	if r.Due.Time == "" {
		c.prop("DTSTART;VALUE=DATE", day.Format(icsDate))
		c.prop("DTEND;VALUE=DATE", day.AddDate(0, 0, 1).Format(icsDate))
	} else {
		minutes := r.Estimate
		if minutes <= 0 {
			minutes = defaultEventMinutes
		}
		end := start.Add(time.Duration(minutes) * time.Minute)
		// A repeating task keeps its time of day across daylight saving changes only in its
		// own time zone; a single one is written in UTC, which every app reads the same.
		if r.Due.Repeat != nil && loc != time.UTC {
			tz := ";TZID=" + loc.String()
			c.prop("DTSTART"+tz, start.Format(icsLocal))
			c.prop("DTEND"+tz, end.Format(icsLocal))
		} else {
			c.prop("DTSTART", start.UTC().Format(icsUTC))
			c.prop("DTEND", end.UTC().Format(icsUTC))
		}
	}
	if r.Due.Repeat != nil {
		c.prop("RRULE", rrule(r.Due.Repeat, day))
	}
	if p := r.Priority; p > 0 && p <= recording.MaxPriority {
		c.prop("PRIORITY", fmt.Sprint([]int{0, 1, 5, 9}[p]))
	}
	var desc []string
	if r.Number > 0 {
		desc = append(desc, fmt.Sprintf("#%d", r.Number))
	}
	if baseURL != "" {
		link := baseURL + "/conversations/" + r.ID
		c.prop("URL", link)
		desc = append(desc, link)
	}
	if len(desc) > 0 {
		c.text("DESCRIPTION", strings.Join(desc, "\n"))
	}
	c.prop("TRANSP", "TRANSPARENT")
	if r.Due.Remind != nil {
		// Reminders of a day without a time go off at 9:00 that day, minus the lead time.
		lead := time.Duration(*r.Due.Remind) * time.Minute
		if r.Due.Time == "" {
			lead -= recording.AllDayReminderClock
		}
		c.prop("BEGIN", "VALARM")
		c.prop("ACTION", "DISPLAY")
		c.text("DESCRIPTION", title)
		c.prop("TRIGGER", icsDuration(-lead))
		c.prop("END", "VALARM")
	}
	c.prop("END", "VEVENT")
}

// iCalendar date and time formats.
const (
	icsDate  = "20060102"
	icsUTC   = "20060102T150405Z"
	icsLocal = "20060102T150405"
)

var icsWeekdays = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// rrule writes a repeat rule as an iCalendar RRULE, from the task's current date.
func rrule(rp *recording.Repeat, day time.Time) string {
	interval := ""
	if rp.Every > 1 {
		interval = fmt.Sprintf(";INTERVAL=%d", rp.Every)
	}
	switch rp.Unit {
	case recording.RepeatWeekday:
		return "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
	case recording.RepeatWeek:
		// Weeks start on Sunday, as in Repeat.Next.
		out := "FREQ=WEEKLY" + interval + ";WKST=SU"
		if len(rp.Weekdays) > 0 {
			days := make([]string, 0, len(rp.Weekdays))
			for _, d := range slices.Sorted(slices.Values(rp.Weekdays)) {
				if d >= 0 && d <= 6 {
					days = append(days, icsWeekdays[d])
				}
			}
			out += ";BYDAY=" + strings.Join(days, ",")
		}
		return out
	case recording.RepeatMonth:
		md := rp.MonthDay
		if md == 0 {
			md = day.Day()
		}
		if md <= 28 {
			return fmt.Sprintf("FREQ=MONTHLY%s;BYMONTHDAY=%d", interval, md)
		}
		// The 29th-31st fall on the month's last day where the month is shorter: the last of
		// the days from the 28th up to it that the month has.
		days := []string{}
		for d := 28; d <= md; d++ {
			days = append(days, fmt.Sprint(d))
		}
		return fmt.Sprintf("FREQ=MONTHLY%s;BYMONTHDAY=%s;BYSETPOS=-1", interval, strings.Join(days, ","))
	case recording.RepeatYear:
		return "FREQ=YEARLY" + interval
	default:
		return "FREQ=DAILY" + interval
	}
}

// icsDuration writes a duration (whole minutes) as an iCalendar DURATION, e.g. "-PT15M".
func icsDuration(d time.Duration) string {
	m := int64(d / time.Minute)
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	days, hours, mins := m/1440, m%1440/60, m%60
	out := sign + "P"
	if days > 0 {
		out += fmt.Sprintf("%dD", days)
	}
	if hours > 0 || mins > 0 || days == 0 {
		out += "T"
		if hours > 0 {
			out += fmt.Sprintf("%dH", hours)
		}
		if mins > 0 || hours == 0 {
			out += fmt.Sprintf("%dM", mins)
		}
	}
	return out
}

// ics writes iCalendar content lines: CRLF line ends, lines folded at 75 octets.
type ics struct{ buf strings.Builder }

func (c *ics) prop(name, value string) {
	line := name + ":" + value
	// Continuation lines start with a space, which counts towards their 75 octets.
	for limit := 75; len(line) > limit; limit = 74 {
		cut := limit
		for cut > 1 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		c.buf.WriteString(line[:cut] + "\r\n ")
		line = line[cut:]
	}
	c.buf.WriteString(line + "\r\n")
}

// text writes a TEXT property, escaped.
func (c *ics) text(name, value string) {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	c.prop(name, r.Replace(value))
}
