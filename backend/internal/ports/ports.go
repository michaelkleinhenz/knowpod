// Package ports defines the interfaces between the application services and their
// infrastructure (database, object storage). Implementations live in repository/ and
// storage/.
package ports

import (
	"context"
	"io"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

// RecordingRepository persists recordings. Lookups of missing documents return
// domain.ErrNotFound; Create returns domain.ErrDuplicate when the device already has a
// recording with the same client ID.
type RecordingRepository interface {
	Create(ctx context.Context, r *recording.Recording) error
	Get(ctx context.Context, id string) (*recording.Recording, error)
	GetByClientID(ctx context.Context, deviceID, clientID string) (*recording.Recording, error)
	Update(ctx context.Context, r *recording.Recording) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, f recording.ListFilter) ([]*recording.Recording, error)
	// Claim atomically picks the recording in the given status whose NotBefore is due,
	// sets NotBefore to leaseUntil and increments Attempts. Returns domain.ErrNotFound when
	// nothing is due.
	Claim(ctx context.Context, status recording.Status, now, leaseUntil time.Time) (*recording.Recording, error)
	// ListStale returns recordings in the given status not updated since before.
	ListStale(ctx context.Context, status recording.Status, before time.Time, limit int) ([]*recording.Recording, error)
}

// DeviceRepository persists devices. Lookups of missing documents return domain.ErrNotFound.
type DeviceRepository interface {
	Create(ctx context.Context, d *device.Device) error
	Get(ctx context.Context, id string) (*device.Device, error)
	GetByTokenHash(ctx context.Context, hash string) (*device.Device, error)
	List(ctx context.Context) ([]*device.Device, error)
	Update(ctx context.Context, d *device.Device) error
}

// UserRepository persists web UI accounts. GetByEmail returns domain.ErrNotFound when the
// email has no stored account.
type UserRepository interface {
	GetByEmail(ctx context.Context, email string) (*user.User, error)
	// Upsert creates the account or replaces the existing one with the same email.
	Upsert(ctx context.Context, u *user.User) error
}

// SessionRepository persists web UI sessions. Get returns domain.ErrNotFound for unknown
// sessions; expired sessions may still be returned and must be checked by the caller.
type SessionRepository interface {
	Create(ctx context.Context, s *user.Session) error
	Get(ctx context.Context, tokenHash string) (*user.Session, error)
	Delete(ctx context.Context, tokenHash string) error
	// DeleteByEmail removes all sessions of the account except the one with keepTokenHash.
	DeleteByEmail(ctx context.Context, email, keepTokenHash string) error
}

// SettingsRepository persists runtime settings. A missing document yields zero values.
type SettingsRepository interface {
	OpenRouter(ctx context.Context) (*settings.OpenRouter, error)
	SaveOpenRouter(ctx context.Context, s *settings.OpenRouter) error
}

// ObjectStore stores binary objects (the audio files). Get of a missing key returns
// domain.ErrNotFound.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	// Get reads length bytes starting at offset; length < 0 reads to the end.
	Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}
