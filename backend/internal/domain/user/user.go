// Package user models people who sign in to the web UI, and their sessions.
package user

import (
	"slices"
	"time"
)

// Role controls what a user may do.
type Role string

const (
	// RoleAdmin manages users and the global AI settings, in addition to own data.
	RoleAdmin Role = "admin"
	// RoleUser works with own devices, conversations and Pocket integration.
	RoleUser Role = "user"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser }

// User is a web UI account. Recordings and devices belong to a user.
//
// The built-in admin (ADMIN_EMAIL) is created at startup with an empty PasswordHash: until
// a password is set in the UI, ADMIN_PASSWORD from the environment is its password.
type User struct {
	ID                string     `bson:"_id"`
	Email             string     `bson:"email"` // lower-case, unique
	Role              Role       `bson:"role"`
	PasswordHash      string     `bson:"passwordHash"`
	CreatedAt         time.Time  `bson:"createdAt"`
	PasswordChangedAt *time.Time `bson:"passwordChangedAt,omitempty"`
	Pocket            Pocket     `bson:"pocket"`
	// Language is the web UI language ("en", "de"); empty follows the browser.
	Language string `bson:"language,omitempty"`
	// TimeZone is the IANA time zone (e.g. "Europe/Berlin") task dates and reminders are
	// meant in; the web app sets it from the browser. Empty is UTC.
	TimeZone string `bson:"timeZone,omitempty"`
	// Appearance is the web UI color scheme ("light", "dark"); empty follows the system.
	Appearance string `bson:"appearance,omitempty"`
	// FontSize is the web UI text size (see FontSizes); empty is the default size.
	FontSize string `bson:"fontSize,omitempty"`
	// Calendar is the user's calendar feed of their tasks.
	Calendar Calendar `bson:"calendar"`
	// MCP is the user's access for AI assistants through the MCP server.
	MCP MCP `bson:"mcp"`
	// Briefing is when the user gets a daily briefing and a weekly review.
	Briefing Briefing `bson:"briefing"`
}

// Briefing sets up a user's briefings: a daily briefing with the day's tasks and what came
// in since the day before, shown on the home page, and a weekly review, a note; each is
// announced by a notification.
type Briefing struct {
	// DailyOff turns the daily briefing off; it is on unless the user turned it off.
	DailyOff bool `bson:"dailyOff,omitempty"`
	Weekly   bool `bson:"weekly,omitempty"`
	// Time is when briefings are made, HH:MM in the user's time zone; empty is DefaultBriefingTime.
	Time string `bson:"time,omitempty"`
	// WeeklyDay is the day of the weekly review (0 is Sunday); nil is DefaultReviewDay.
	WeeklyDay *int `bson:"weeklyDay,omitempty"`
	// NoNotify makes the daily briefing without a notification announcing it.
	NoNotify bool `bson:"noNotify,omitempty"`
	// Sections are the parts of the daily briefing besides the tasks due today (see
	// BriefingSections); nil is DefaultBriefingSections.
	Sections []string `bson:"sections,omitempty"`
	// ActionItemDays is how many days back the daily briefing looks for open action items;
	// 0 is DefaultActionItemDays.
	ActionItemDays int `bson:"actionItemDays,omitempty"`
	// FolderID is the folder the weekly reviews go into, made on first use.
	FolderID string `bson:"folderId,omitempty"`
	// LastDaily and LastWeekly are the days (YYYY-MM-DD, in the user's time zone) the last
	// briefing and review were made, so each is made once.
	LastDaily  string `bson:"lastDaily,omitempty"`
	LastWeekly string `bson:"lastWeekly,omitempty"`
	// Today is the latest daily briefing.
	Today *DailyBriefing `bson:"today,omitempty"`
}

// DailyBriefing is a daily briefing as it is shown on the home page.
type DailyBriefing struct {
	// Day is the day it is for, YYYY-MM-DD in the user's time zone.
	Day      string `bson:"day" json:"day"`
	Title    string `bson:"title" json:"title"`
	Markdown string `bson:"markdown" json:"markdown"`
	// Summary is its gist in one line, e.g. "2 due today · 3 new notes".
	Summary string `bson:"summary" json:"summary"`
	// Language is the language it is written in ("en", "de"); empty for ones made before
	// it was kept.
	Language string    `bson:"language,omitempty" json:"language,omitempty"`
	MadeAt   time.Time `bson:"madeAt" json:"madeAt"`
}

// The parts of a daily briefing a user can leave out or add.
const (
	SectionOverdue     = "overdue"     // open tasks whose date has passed
	SectionUpcoming    = "upcoming"    // open tasks due in the next week
	SectionNew         = "new"         // the notes that came in since the day before
	SectionDigest      = "digest"      // the summary model's digest of the new notes
	SectionActionItems = "actionItems" // action items of recent notes not yet made tasks
)

// BriefingSections are the sections a daily briefing can have, in their order.
var BriefingSections = []string{SectionOverdue, SectionUpcoming, SectionNew, SectionDigest, SectionActionItems}

// DefaultBriefingSections are the daily briefing's sections unless the user chose others.
var DefaultBriefingSections = []string{SectionOverdue, SectionNew, SectionDigest, SectionActionItems}

// DefaultActionItemDays is how many days back open action items are looked for, unless
// the user chose another number; MaxActionItemDays is the most they can choose.
const (
	DefaultActionItemDays = 7
	MaxActionItemDays     = 30
)

// Daily tells whether the user gets a daily briefing.
func (b Briefing) Daily() bool { return !b.DailyOff }

// Shows tells whether the daily briefing has section.
func (b Briefing) Shows(section string) bool {
	if b.Sections == nil {
		return slices.Contains(DefaultBriefingSections, section)
	}
	return slices.Contains(b.Sections, section)
}

// ActionDays returns how many days back open action items are looked for.
func (b Briefing) ActionDays() int {
	if b.ActionItemDays <= 0 {
		return DefaultActionItemDays
	}
	return b.ActionItemDays
}

// DefaultBriefingTime is when briefings are made unless the user chose another time.
const DefaultBriefingTime = "07:00"

// DefaultReviewDay is the day of the weekly review unless the user chose another day: it
// looks back on the week before.
const DefaultReviewDay = time.Monday

// ReviewDay returns the day of the weekly review.
func (b Briefing) ReviewDay() time.Weekday {
	if b.WeeklyDay == nil {
		return DefaultReviewDay
	}
	return time.Weekday(*b.WeeklyDay)
}

// At returns the time of day briefings are made.
func (b Briefing) At() string {
	if b.Time == "" {
		return DefaultBriefingTime
	}
	return b.Time
}

// Calendar is a user's read-only iCalendar feed of their tasks with dates. Its URL holds a
// random token; only the token's SHA-256 is stored, so the link is shown once and can be
// replaced.
type Calendar struct {
	TokenHash string     `bson:"tokenHash,omitempty"`
	CreatedAt *time.Time `bson:"createdAt,omitempty"`
}

// MCP is a user's access token for the MCP server, which AI assistants (Claude, ChatGPT)
// use to work with the user's notes. Only the token's SHA-256 is stored, so the token is
// shown once and can be replaced.
type MCP struct {
	TokenHash string     `bson:"tokenHash,omitempty"`
	CreatedAt *time.Time `bson:"createdAt,omitempty"`
}

// Location returns the user's time zone, UTC when unset or unknown.
func (u *User) Location() *time.Location {
	return LoadLocation(u.TimeZone)
}

// LoadLocation returns the named time zone, UTC when empty or unknown.
func LoadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Languages lists the supported web UI languages.
var Languages = []string{"en", "de"}

// Appearances lists the web UI color schemes.
var Appearances = []string{"light", "dark"}

// ValidAppearance reports whether a is a supported color scheme (or empty).
func ValidAppearance(a string) bool {
	if a == "" {
		return true
	}
	for _, v := range Appearances {
		if v == a {
			return true
		}
	}
	return false
}

// FontSizes lists the web UI text sizes, smallest first; empty is the default size.
var FontSizes = []string{"xsmall", "small", "large", "xlarge"}

// ValidFontSize reports whether f is a supported text size (or empty).
func ValidFontSize(f string) bool {
	if f == "" {
		return true
	}
	for _, v := range FontSizes {
		if v == f {
			return true
		}
	}
	return false
}

// ValidLanguage reports whether lang is a supported UI language (or empty).
func ValidLanguage(lang string) bool {
	if lang == "" {
		return true
	}
	for _, l := range Languages {
		if l == lang {
			return true
		}
	}
	return false
}

// Pocket is a user's Pocket (heypocketai.com) integration. WebhookID is the random part of
// the user's webhook URL; it identifies the user whose secret verifies the request.
type Pocket struct {
	WebhookID     string `bson:"webhookId,omitempty"`
	WebhookSecret string `bson:"webhookSecret,omitempty"`
	APIKey        string `bson:"apiKey,omitempty"`
	// FolderID is the folder imported recordings go into, made on first use.
	FolderID string `bson:"folderId,omitempty"`
}

// Session is a signed-in browser. Only the SHA-256 of the session token is stored.
type Session struct {
	TokenHash string    `bson:"_id"`
	UserID    string    `bson:"userId"`
	CreatedAt time.Time `bson:"createdAt"`
	ExpiresAt time.Time `bson:"expiresAt"`
}
