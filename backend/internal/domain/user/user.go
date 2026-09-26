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
