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
}

// Calendar is a user's read-only iCalendar feed of their tasks with dates. Its URL holds a
// random token; only the token's SHA-256 is stored, so the link is shown once and can be
// replaced.
type Calendar struct {
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
