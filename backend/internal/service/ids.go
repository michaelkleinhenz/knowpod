package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// newID returns a random 24-character hex ID (the same shape as a MongoDB ObjectID).
func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// newToken returns a new random device token.
func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return "kpd_" + base64.RawURLEncoding.EncodeToString(b[:])
}

// hashToken returns the stored form of a device token. Tokens are 256-bit random values, so
// a fast unsalted hash is sufficient.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
