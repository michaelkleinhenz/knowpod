// Package device models a recorder gadget that is allowed to push recordings.
package device

import "time"

// Device is a registered recorder. It authenticates with a bearer token; only the token's
// SHA-256 hash is stored.
type Device struct {
	ID         string     `bson:"_id" json:"id"`
	Name       string     `bson:"name" json:"name"`
	TokenHash  string     `bson:"tokenHash" json:"-"`
	CreatedAt  time.Time  `bson:"createdAt" json:"createdAt"`
	LastSeenAt *time.Time `bson:"lastSeenAt,omitempty" json:"lastSeenAt,omitempty"`
	RevokedAt  *time.Time `bson:"revokedAt,omitempty" json:"revokedAt,omitempty"`
}

// Active reports whether the device may still authenticate.
func (d *Device) Active() bool { return d.RevokedAt == nil }
