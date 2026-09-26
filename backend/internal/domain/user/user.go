// Package user models people who sign in to the web UI, and their sessions.
package user

import "time"

// User is a stored web UI account. The default admin from ADMIN_EMAIL/ADMIN_PASSWORD has no
// document until its password is changed; from then on the stored hash is authoritative.
type User struct {
	ID                string    `bson:"_id"`
	Email             string    `bson:"email"` // lower-case, unique
	PasswordHash      string    `bson:"passwordHash"`
	CreatedAt         time.Time `bson:"createdAt"`
	PasswordChangedAt time.Time `bson:"passwordChangedAt"`
}

// Session is a signed-in browser. Only the SHA-256 of the session token is stored.
type Session struct {
	TokenHash string    `bson:"_id"`
	Email     string    `bson:"email"`
	CreatedAt time.Time `bson:"createdAt"`
	ExpiresAt time.Time `bson:"expiresAt"`
}
