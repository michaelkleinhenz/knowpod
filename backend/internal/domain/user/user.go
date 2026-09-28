// Package user models people who sign in to the web UI, and their sessions.
package user

import "time"

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
	// Calendar is the user's calendar feed of their tasks.
	Calendar Calendar `bson:"calendar"`
	// MCP is the user's access for AI assistants through the MCP server.
	MCP MCP `bson:"mcp"`
	// Briefing is when the user gets a daily briefing and a weekly review.
	Briefing Briefing `bson:"briefing"`
}

// Briefing sets up a user's briefings: a note made every morning with the day's tasks and
// what came in since the day before, and a weekly review, each announced by a notification.
type Briefing struct {
	Daily  bool `bson:"daily,omitempty"`
	Weekly bool `bson:"weekly,omitempty"`
	// Time is when briefings are made, HH:MM in the user's time zone; empty is DefaultBriefingTime.
	Time string `bson:"time,omitempty"`
	// WeeklyDay is the day of the weekly review (0 is Sunday).
	WeeklyDay int `bson:"weeklyDay"`
	// FolderID is the folder the briefings go into, made on first use.
	FolderID string `bson:"folderId,omitempty"`
	// LastDaily and LastWeekly are the days (YYYY-MM-DD, in the user's time zone) the last
	// briefing and review were made, so each is made once.
	LastDaily  string `bson:"lastDaily,omitempty"`
	LastWeekly string `bson:"lastWeekly,omitempty"`
}

// DefaultBriefingTime is when briefings are made unless the user chose another time.
const DefaultBriefingTime = "07:00"

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
}

// Session is a signed-in browser. Only the SHA-256 of the session token is stored.
type Session struct {
	TokenHash string    `bson:"_id"`
	UserID    string    `bson:"userId"`
	CreatedAt time.Time `bson:"createdAt"`
	ExpiresAt time.Time `bson:"expiresAt"`
}
