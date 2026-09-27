// Package memory provides in-memory implementations of the repository ports, used by tests.
package memory

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/push"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/tablet"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/theme"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

// Recordings is an in-memory ports.RecordingRepository. It stores copies, like a database.
type Recordings struct {
	mu   sync.Mutex
	recs map[string]recording.Recording
	// numbers is each owner's last note number.
	numbers map[string]int64
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
	if r.Number == 0 && r.OwnerID != "" {
		if m.numbers == nil {
			m.numbers = map[string]int64{}
		}
		m.numbers[r.OwnerID]++
		r.Number = m.numbers[r.OwnerID]
	}
	m.recs[r.ID] = clone(r)
	return nil
}

// clone copies a recording with the parts a caller might change in place (task date,
// summary and action items), like a database keeps its own copy.
func clone(r *recording.Recording) recording.Recording {
	c := *r
	if r.Due != nil {
		d := *r.Due
		if d.Repeat != nil {
			rp := *d.Repeat
			rp.Weekdays = slices.Clone(rp.Weekdays)
			d.Repeat = &rp
		}
		c.Due = &d
	}
	if r.Summary != nil {
		s := *r.Summary
		s.ActionItems = slices.Clone(s.ActionItems)
		c.Summary = &s
	}
	c.Labels = slices.Clone(r.Labels)
	return c
}

func (m *Recordings) Get(_ context.Context, id string) (*recording.Recording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := clone(&r)
	return &c, nil
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

func (m *Recordings) MoveFolder(_ context.Context, ownerID, from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.recs {
		if r.OwnerID != ownerID {
			continue
		}
		if r.FolderID == from {
			r.FolderID = to
		}
		if r.Board != nil && r.Board.Scope.Kind == recording.ScopeFolder && r.Board.Scope.ID == from {
			b := *r.Board
			b.Scope.ID = to
			r.Board = &b
		}
		m.recs[id] = r
	}
	return nil
}

func (m *Recordings) MoveSubNotes(_ context.Context, ownerID, from, toParent, toFolder string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.recs {
		if r.OwnerID == ownerID && r.ParentID == from {
			r.ParentID, r.FolderID = toParent, toFolder
			m.recs[id] = r
		}
	}
	return nil
}

func (m *Recordings) RemoveLabel(_ context.Context, ownerID, labelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.recs {
		if r.OwnerID != ownerID {
			continue
		}
		r.Labels = slices.DeleteFunc(slices.Clone(r.Labels), func(l string) bool { return l == labelID })
		if r.Board != nil && r.Board.Scope.Kind == recording.ScopeLabel && r.Board.Scope.ID == labelID {
			b := *r.Board
			b.Scope = recording.BoardScope{}
			r.Board = &b
		}
		m.recs[id] = r
	}
	return nil
}

func (m *Recordings) Update(_ context.Context, r *recording.Recording) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.recs[r.ID]; !ok {
		return domain.ErrNotFound
	}
	m.recs[r.ID] = clone(r)
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
		return (f.OwnerID == "" || r.OwnerID == f.OwnerID) && (f.DeviceID == "" || r.DeviceID == f.DeviceID) && (f.Status == "" || r.Status == f.Status) && (f.Number == 0 || r.Number == f.Number)
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
				s.Markdown, s.ActionItems = "", nil
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
		r := clone(&r)
		if keep(&r) {
			out = append(out, &r)
		}
	}
	return out
}

func (m *Recordings) SetRemindAt(_ context.Context, id string, at *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok {
		return domain.ErrNotFound
	}
	r.RemindAt = at
	m.recs[id] = r
	return nil
}

func (m *Recordings) ClaimReminder(_ context.Context, now time.Time) (*recording.Recording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *recording.Recording
	for _, r := range m.recs {
		if r.RemindAt != nil && !r.RemindAt.After(now) && (best == nil || r.RemindAt.Before(*best.RemindAt)) {
			c := clone(&r)
			best = &c
		}
	}
	if best == nil {
		return nil, domain.ErrNotFound
	}
	stored := m.recs[best.ID]
	stored.RemindAt = nil
	m.recs[best.ID] = stored
	return best, nil
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
	webPush    *settings.WebPush
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

func (m *Settings) InitWebPush(_ context.Context, k *settings.WebPush) (*settings.WebPush, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.webPush == nil {
		c := *k
		m.webPush = &c
	}
	out := *m.webPush
	return &out, nil
}

// PushSubscriptions is an in-memory ports.PushSubscriptionRepository.
type PushSubscriptions struct {
	mu   sync.Mutex
	subs map[string]push.Subscription
}

// NewPushSubscriptions builds an empty repository.
func NewPushSubscriptions() *PushSubscriptions {
	return &PushSubscriptions{subs: map[string]push.Subscription{}}
}

func (m *PushSubscriptions) Save(_ context.Context, s *push.Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[s.ID] = *s
	return nil
}

func (m *PushSubscriptions) List(_ context.Context, userID string) ([]*push.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*push.Subscription{}
	for _, s := range m.subs {
		if s.UserID == userID {
			s := s
			out = append(out, &s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *PushSubscriptions) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, id)
	return nil
}

func (m *PushSubscriptions) DeleteByUser(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.subs {
		if s.UserID == userID {
			delete(m.subs, id)
		}
	}
	return nil
}

// Themes is an in-memory ports.ThemeRepository.
type Themes struct {
	mu     sync.Mutex
	themes map[string]theme.Theme
}

// NewThemes builds an empty repository.
func NewThemes() *Themes { return &Themes{themes: map[string]theme.Theme{}} }

func (m *Themes) Create(_ context.Context, t *theme.Theme) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.themes[t.ID]; ok {
		return domain.ErrDuplicate
	}
	m.themes[t.ID] = *t
	return nil
}

func (m *Themes) Get(_ context.Context, id string) (*theme.Theme, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.themes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &t, nil
}

func (m *Themes) List(_ context.Context, ownerID string) ([]*theme.Theme, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*theme.Theme{}
	for _, t := range m.themes {
		t := t
		if t.OwnerID == ownerID {
			out = append(out, &t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Themes) Update(_ context.Context, t *theme.Theme) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.themes[t.ID]; !ok {
		return domain.ErrNotFound
	}
	m.themes[t.ID] = *t
	return nil
}

func (m *Themes) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.themes[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.themes, id)
	return nil
}

func (m *Themes) DeleteByOwner(_ context.Context, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.themes {
		if t.OwnerID == ownerID {
			delete(m.themes, id)
		}
	}
	return nil
}

// Labels is an in-memory ports.LabelRepository.
type Labels struct {
	mu     sync.Mutex
	labels map[string]label.Label
}

// NewLabels builds an empty repository.
func NewLabels() *Labels { return &Labels{labels: map[string]label.Label{}} }

func (m *Labels) Create(_ context.Context, t *label.Label) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.labels[t.ID]; ok {
		return domain.ErrDuplicate
	}
	m.labels[t.ID] = *t
	return nil
}

func (m *Labels) Get(_ context.Context, id string) (*label.Label, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.labels[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &t, nil
}

func (m *Labels) List(_ context.Context, ownerID string) ([]*label.Label, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*label.Label{}
	for _, t := range m.labels {
		t := t
		if t.OwnerID == ownerID {
			out = append(out, &t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Labels) Update(_ context.Context, t *label.Label) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.labels[t.ID]; !ok {
		return domain.ErrNotFound
	}
	m.labels[t.ID] = *t
	return nil
}

func (m *Labels) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.labels[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.labels, id)
	return nil
}

func (m *Labels) DeleteByOwner(_ context.Context, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.labels {
		if t.OwnerID == ownerID {
			delete(m.labels, id)
		}
	}
	return nil
}

// Folders is an in-memory ports.FolderRepository.
type Folders struct {
	mu      sync.Mutex
	folders map[string]folder.Folder
}

// NewFolders builds an empty repository.
func NewFolders() *Folders { return &Folders{folders: map[string]folder.Folder{}} }

func (m *Folders) Create(_ context.Context, f *folder.Folder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.folders[f.ID]; ok {
		return domain.ErrDuplicate
	}
	m.folders[f.ID] = *f
	return nil
}

func (m *Folders) Get(_ context.Context, id string) (*folder.Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.folders[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &f, nil
}

func (m *Folders) List(_ context.Context, ownerID string) ([]*folder.Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*folder.Folder{}
	for _, f := range m.folders {
		f := f
		if f.OwnerID == ownerID {
			out = append(out, &f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Folders) Update(_ context.Context, f *folder.Folder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.folders[f.ID]; !ok {
		return domain.ErrNotFound
	}
	m.folders[f.ID] = *f
	return nil
}

func (m *Folders) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.folders[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.folders, id)
	return nil
}

func (m *Folders) DeleteByOwner(_ context.Context, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, f := range m.folders {
		if f.OwnerID == ownerID {
			delete(m.folders, id)
		}
	}
	return nil
}

// TabletLinks is an in-memory ports.TabletLinkRepository.
type TabletLinks struct {
	mu    sync.Mutex
	links map[string]tablet.Link // by user ID
}

// NewTabletLinks builds an empty repository.
func NewTabletLinks() *TabletLinks { return &TabletLinks{links: map[string]tablet.Link{}} }

func (m *TabletLinks) Get(_ context.Context, userID string) (*tablet.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	l.Items = slices.Clone(l.Items)
	return &l, nil
}

func (m *TabletLinks) Save(_ context.Context, l *tablet.Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *l
	c.Items = slices.Clone(l.Items)
	m.links[l.UserID] = c
	return nil
}

func (m *TabletLinks) Delete(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[userID]; !ok {
		return domain.ErrNotFound
	}
	delete(m.links, userID)
	return nil
}

func (m *TabletLinks) UserIDs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.links))
	for id := range m.links {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
