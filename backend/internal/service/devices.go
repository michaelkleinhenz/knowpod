package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// lastSeenResolution throttles LastSeenAt writes so chunked uploads don't write per request.
const lastSeenResolution = time.Minute

// DeviceService registers recorder devices and authenticates their tokens.
type DeviceService struct {
	repo  ports.DeviceRepository
	clock func() time.Time
}

// NewDeviceService builds the service.
func NewDeviceService(repo ports.DeviceRepository) *DeviceService {
	return &DeviceService{repo: repo, clock: time.Now}
}

// Register creates a device for the account's user and returns it with its token. The token
// is only available here; only its hash is stored.
func (s *DeviceService) Register(ctx context.Context, acc *Account, name string) (*device.Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, "", invalid("name must be 1-100 characters")
	}
	token := newToken()
	d := &device.Device{ID: newID(), OwnerID: acc.ID, Name: name, TokenHash: hashToken(token), CreatedAt: s.clock().UTC()}
	if err := s.repo.Create(ctx, d); err != nil {
		return nil, "", err
	}
	return d, token, nil
}

// Authenticate resolves a bearer token to an active device.
func (s *DeviceService) Authenticate(ctx context.Context, token string) (*device.Device, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	d, err := s.repo.GetByTokenHash(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if !d.Active() {
		return nil, ErrUnauthorized
	}
	now := s.clock().UTC()
	if d.LastSeenAt == nil || now.Sub(*d.LastSeenAt) >= lastSeenResolution {
		d.LastSeenAt = &now
		_ = s.repo.Update(ctx, d) // best effort
	}
	return d, nil
}

// List returns the account's devices (all devices for ADMIN_TOKEN).
func (s *DeviceService) List(ctx context.Context, acc *Account) ([]*device.Device, error) {
	return s.repo.List(ctx, acc.OwnerFilter())
}

// owned loads a device the account may manage.
func (s *DeviceService) owned(ctx context.Context, acc *Account, id string) (*device.Device, error) {
	d, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(d.OwnerID) {
		return nil, ErrNotFound
	}
	return d, nil
}

// RotateToken replaces the token of an active device, e.g. when the old one was lost. The
// old token stops working immediately; the device keeps its ID and recordings.
func (s *DeviceService) RotateToken(ctx context.Context, acc *Account, id string) (*device.Device, string, error) {
	d, err := s.owned(ctx, acc, id)
	if err != nil {
		return nil, "", err
	}
	if !d.Active() {
		return nil, "", ErrNotFound
	}
	token := newToken()
	d.TokenHash = hashToken(token)
	if err := s.repo.Update(ctx, d); err != nil {
		return nil, "", err
	}
	return d, token, nil
}

// Revoke disables a device's token permanently. Its recordings are kept.
func (s *DeviceService) Revoke(ctx context.Context, acc *Account, id string) error {
	d, err := s.owned(ctx, acc, id)
	if err != nil {
		return err
	}
	if d.RevokedAt != nil {
		return nil
	}
	now := s.clock().UTC()
	d.RevokedAt = &now
	return s.repo.Update(ctx, d)
}
