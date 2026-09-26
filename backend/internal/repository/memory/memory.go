// Package memory provides in-memory implementations of the repository ports, used by tests.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

// Recordings is an in-memory ports.RecordingRepository. It stores copies, like a database.
type Recordings struct {
	mu   sync.Mutex
	recs map[string]recording.Recording
}

// NewRecordings builds an empty repository.
func NewRecordings() *Recordings { return &Recordings{recs: map[string]recording.Recording{}} }

func (m *Recordings) Create(_ context.Context, r *recording.Recording) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.recs {
		if x.ID == r.ID || (x.DeviceID == r.DeviceID && x.ClientID == r.ClientID) {
			return domain.ErrDuplicate
		}
	}
	m.recs[r.ID] = *r
	return nil
}

func (m *Recordings) Get(_ context.Context, id string) (*recording.Recording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &r, nil
}

func (m *Recordings) GetByClientID(_ context.Context, deviceID, clientID string) (*recording.Recording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.recs {
		if r.DeviceID == deviceID && r.ClientID == clientID {
			return &r, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *Recordings) Update(_ context.Context, r *recording.Recording) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.recs[r.ID]; !ok {
		return domain.ErrNotFound
	}
	m.recs[r.ID] = *r
	return nil
}

func (m *Recordings) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.recs[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.recs, id)
	return nil
}

func (m *Recordings) List(_ context.Context, f recording.ListFilter) ([]*recording.Recording, error) {
	out := m.filter(func(r *recording.Recording) bool {
		return (f.OwnerID == "" || r.OwnerID == f.OwnerID) && (f.DeviceID == "" || r.DeviceID == f.DeviceID) && (f.Status == "" || r.Status == f.Status)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Offset >= len(out) {
		return []*recording.Recording{}, nil
	}
	out = out[f.Offset:]
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	if f.Brief {
		for _, r := range out {
			r.Transcript = nil
			if r.Summary != nil {
				s := *r.Summary
				s.Markdown = ""
				r.Summary = &s
			}
		}
	}
	return out, nil
}

func (m *Recordings) Claim(_ context.Context, status recording.Status, now, leaseUntil time.Time) (*recording.Recording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *recording.Recording
	for _, r := range m.recs {
		if r.Status == status && !r.NotBefore.After(now) && (best == nil || r.NotBefore.Before(best.NotBefore)) {
			r := r
			best = &r
		}
	}
	if best == nil {
		return nil, domain.ErrNotFound
	}
	best.NotBefore = leaseUntil
	best.UpdatedAt = now
	best.Attempts++
	m.recs[best.ID] = *best
	out := *best
	return &out, nil
}

func (m *Recordings) ListStale(_ context.Context, status recording.Status, before time.Time, limit int) ([]*recording.Recording, error) {
	out := m.filter(func(r *recording.Recording) bool { return r.Status == status && r.UpdatedAt.Before(before) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Recordings) AssignOwnerless(_ context.Context, ownerID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, r := range m.recs {
		if r.OwnerID == "" {
			r.OwnerID = ownerID
			m.recs[id] = r
			n++
		}
	}
	return n, nil
}

func (m *Recordings) filter(keep func(*recording.Recording) bool) []*recording.Recording {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*recording.Recording{}
	for _, r := range m.recs {
		r := r
		if keep(&r) {
			out = append(out, &r)
		}
	}
	return out
}

// Devices is an in-memory ports.DeviceRepository.
type Devices struct {
	mu   sync.Mutex
	devs map[string]device.Device
}

// NewDevices builds an empty repository.
func NewDevices() *Devices { return &Devices{devs: map[string]device.Device{}} }

func (m *Devices) Create(_ context.Context, d *device.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.devs {
		if x.ID == d.ID || x.TokenHash == d.TokenHash {
			return domain.ErrDuplicate
		}
	}
	m.devs[d.ID] = *d
	return nil
}

func (m *Devices) Get(_ context.Context, id string) (*device.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &d, nil
}

func (m *Devices) GetByTokenHash(_ context.Context, hash string) (*device.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.devs {
		if d.TokenHash == hash {
			return &d, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *Devices) List(_ context.Context, ownerID string) ([]*device.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*device.Device{}
	for _, d := range m.devs {
		d := d
		if ownerID == "" || d.OwnerID == ownerID {
			out = append(out, &d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Devices) Update(_ context.Context, d *device.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devs[d.ID]; !ok {
		return domain.ErrNotFound
	}
	m.devs[d.ID] = *d
	return nil
}

func (m *Devices) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devs[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.devs, id)
	return nil
}

func (m *Devices) AssignOwnerless(_ context.Context, ownerID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, d := range m.devs {
		if d.OwnerID == "" {
			d.OwnerID = ownerID
			m.devs[id] = d
			n++
		}
	}
	return n, nil
}

// Users is an in-memory ports.UserRepository.
type Users struct {
	mu    sync.Mutex
	users map[string]user.User // by ID
}

// NewUsers builds an empty repository.
func NewUsers() *Users { return &Users{users: map[string]user.User{}} }

func (m *Users) Create(_ context.Context, u *user.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.users {
		if x.ID == u.ID || x.Email == u.Email || (u.Pocket.WebhookID != "" && x.Pocket.WebhookID == u.Pocket.WebhookID) {
			return domain.ErrDuplicate
		}
	}
	m.users[u.ID] = *u
	return nil
}

func (m *Users) Get(_ context.Context, id string) (*user.User, error) {
	return m.find(func(u *user.User) bool { return u.ID == id })
}

func (m *Users) GetByEmail(_ context.Context, email string) (*user.User, error) {
	return m.find(func(u *user.User) bool { return u.Email == email })
}

func (m *Users) GetByPocketWebhookID(_ context.Context, webhookID string) (*user.User, error) {
	return m.find(func(u *user.User) bool { return webhookID != "" && u.Pocket.WebhookID == webhookID })
}

func (m *Users) List(context.Context) ([]*user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*user.User{}
	for _, u := range m.users {
		u := u
		out = append(out, &u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (m *Users) Update(_ context.Context, u *user.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.ID]; !ok {
		return domain.ErrNotFound
	}
	for _, x := range m.users {
		if x.ID != u.ID && (x.Email == u.Email || (u.Pocket.WebhookID != "" && x.Pocket.WebhookID == u.Pocket.WebhookID)) {
			return domain.ErrDuplicate
		}
	}
	m.users[u.ID] = *u
	return nil
}

func (m *Users) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.users, id)
	return nil
}

func (m *Users) find(match func(*user.User) bool) (*user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		u := u
		if match(&u) {
			return &u, nil
		}
	}
	return nil, domain.ErrNotFound
}

// Sessions is an in-memory ports.SessionRepository.
type Sessions struct {
	mu       sync.Mutex
	sessions map[string]user.Session // by token hash
}

// NewSessions builds an empty repository.
func NewSessions() *Sessions { return &Sessions{sessions: map[string]user.Session{}} }

func (m *Sessions) Create(_ context.Context, s *user.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[s.TokenHash]; ok {
		return domain.ErrDuplicate
	}
	m.sessions[s.TokenHash] = *s
	return nil
}

func (m *Sessions) Get(_ context.Context, tokenHash string) (*user.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[tokenHash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &s, nil
}

func (m *Sessions) Delete(_ context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[tokenHash]; !ok {
		return domain.ErrNotFound
	}
	delete(m.sessions, tokenHash)
	return nil
}

func (m *Sessions) DeleteByUser(_ context.Context, userID, keepTokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, s := range m.sessions {
		if s.UserID == userID && h != keepTokenHash {
			delete(m.sessions, h)
		}
	}
	return nil
}

// Count returns the number of stored sessions.
func (m *Sessions) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Settings is an in-memory ports.SettingsRepository.
type Settings struct {
	mu         sync.Mutex
	openRouter settings.OpenRouter
}

// NewSettings builds an empty repository.
func NewSettings() *Settings { return &Settings{} }

func (m *Settings) OpenRouter(context.Context) (*settings.OpenRouter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.openRouter
	return &s, nil
}

func (m *Settings) SaveOpenRouter(_ context.Context, s *settings.OpenRouter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openRouter = *s
	return nil
}
