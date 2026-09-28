// Package ports defines the interfaces between the application services and their
// infrastructure (database, object storage). Implementations live in repository/ and
// storage/.
package ports

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/filter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/oauth"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/push"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/tablet"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/theme"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

// RecordingRepository persists recordings. Lookups of missing documents return
// domain.ErrNotFound; Create returns domain.ErrDuplicate when the device already has a
// recording with the same client ID. Every change counts up the recording's Version.
type RecordingRepository interface {
	Create(ctx context.Context, r *recording.Recording) error
	Get(ctx context.Context, id string) (*recording.Recording, error)
	GetByClientID(ctx context.Context, deviceID, clientID string) (*recording.Recording, error)
	// Update replaces the stored recording only while it is still at r.Version, and then
	// counts r.Version up; otherwise it returns domain.ErrChanged and changes nothing.
	Update(ctx context.Context, r *recording.Recording) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, f recording.ListFilter) ([]*recording.Recording, error)
	// Claim atomically picks the recording in the given status whose NotBefore is due,
	// sets NotBefore to leaseUntil and increments Attempts. Returns domain.ErrNotFound when
	// nothing is due.
	Claim(ctx context.Context, status recording.Status, now, leaseUntil time.Time) (*recording.Recording, error)
	// ListStale returns recordings in the given status not updated since before.
	ListStale(ctx context.Context, status recording.Status, before time.Time, limit int) ([]*recording.Recording, error)
	// ListTrashed returns recordings moved to the trash before the given time.
	ListTrashed(ctx context.Context, before time.Time, limit int) ([]*recording.Recording, error)
	// AssignOwnerless gives recordings without an owner to ownerID (data from before users).
	AssignOwnerless(ctx context.Context, ownerID string) (int, error)
	// RemoveLabel takes the label off all of ownerID's recordings (also the shared ones
	// ownerID labeled as a member) and clears the scope of boards showing it.
	RemoveLabel(ctx context.Context, ownerID, labelID string) error
	// ClearBoardScope makes ownerID's boards showing the scope show nothing until another
	// scope is chosen (e.g. after a saved filter was deleted).
	ClearBoardScope(ctx context.Context, ownerID string, scope recording.BoardScope) error
	// AddTrackedSeconds adds to the time logged on the recording (negative: takes off).
	AddTrackedSeconds(ctx context.Context, id string, seconds int64) error
	// MoveFolder moves all of ownerID's recordings in folder from into folder to ("" is the
	// top level), also the shared ones ownerID put there as a member, and points boards
	// showing folder from at folder to.
	MoveFolder(ctx context.Context, ownerID, from, to string) error
	// MoveSubNotes moves all of ownerID's sub-notes of note from to where that note was:
	// under parent toParent, or into folder toFolder when toParent is empty.
	MoveSubNotes(ctx context.Context, ownerID, from, toParent, toFolder string) error
	// SetRemindAt changes only when the recording's next reminder is sent (nil: none).
	SetRemindAt(ctx context.Context, id string, at *time.Time) error
	// ClaimReminder atomically takes a recording whose reminder is due (RemindAt <= now) and
	// clears its RemindAt, so each reminder is sent once. Returns domain.ErrNotFound when
	// none is due.
	ClaimReminder(ctx context.Context, now time.Time) (*recording.Recording, error)
	// SetMemberRemindAt changes only when a member's next reminder of the recording is sent
	// (nil: none).
	SetMemberRemindAt(ctx context.Context, id, userID string, at *time.Time) error
	// ClaimMemberReminder is ClaimReminder for the members' reminders: it takes a recording
	// with a member whose reminder is due, clears that member's RemindAt and returns the
	// member's user ID.
	ClaimMemberReminder(ctx context.Context, now time.Time) (*recording.Recording, string, error)
	// RemoveMember takes the user off the shares and members of all recordings (when the
	// user is deleted).
	RemoveMember(ctx context.Context, userID string) error
}

// maxSaveAttempts bounds how often SaveProcessed tries again.
const maxSaveAttempts = 5

// SaveProcessed saves a recording that a background step (processing, an upload) held on
// to for a while. When a person changed the recording meanwhile, their changes
// (recording.KeepUserFields) are kept and the save is tried again.
func SaveProcessed(ctx context.Context, recs RecordingRepository, rec *recording.Recording) error {
	for attempt := 1; ; attempt++ {
		err := recs.Update(ctx, rec)
		if !errors.Is(err, domain.ErrChanged) || attempt >= maxSaveAttempts {
			return err
		}
		stored, err := recs.Get(ctx, rec.ID)
		if err != nil {
			return err
		}
		rec.KeepUserFields(stored)
	}
}

// DeviceRepository persists devices. Lookups of missing documents return domain.ErrNotFound.
type DeviceRepository interface {
	Create(ctx context.Context, d *device.Device) error
	Get(ctx context.Context, id string) (*device.Device, error)
	GetByTokenHash(ctx context.Context, hash string) (*device.Device, error)
	// List returns the devices of ownerID, or all devices when ownerID is empty.
	List(ctx context.Context, ownerID string) ([]*device.Device, error)
	Update(ctx context.Context, d *device.Device) error
	Delete(ctx context.Context, id string) error
	// AssignOwnerless gives devices without an owner to ownerID (data from before users).
	AssignOwnerless(ctx context.Context, ownerID string) (int, error)
}

// UserRepository persists web UI accounts. Lookups of missing users return
// domain.ErrNotFound; Create and Update return domain.ErrDuplicate for a taken email.
type UserRepository interface {
	Create(ctx context.Context, u *user.User) error
	Get(ctx context.Context, id string) (*user.User, error)
	GetByEmail(ctx context.Context, email string) (*user.User, error)
	GetByPocketWebhookID(ctx context.Context, webhookID string) (*user.User, error)
	GetByCalendarTokenHash(ctx context.Context, hash string) (*user.User, error)
	GetByMCPTokenHash(ctx context.Context, hash string) (*user.User, error)
	List(ctx context.Context) ([]*user.User, error)
	Update(ctx context.Context, u *user.User) error
	Delete(ctx context.Context, id string) error
}

// OAuthRepository persists the OAuth clients registered for the MCP server and the grants
// users gave them. Lookups of missing documents return domain.ErrNotFound; grants past their
// ExpiresAt may still be returned and must be checked by the caller.
type OAuthRepository interface {
	CreateClient(ctx context.Context, c *oauth.Client) error
	GetClient(ctx context.Context, id string) (*oauth.Client, error)
	CreateGrant(ctx context.Context, g *oauth.Grant) error
	// ClaimCode atomically takes the grant with the authorization code hash and clears the
	// code, so each code is exchanged once.
	ClaimCode(ctx context.Context, codeHash string) (*oauth.Grant, error)
	GetGrantByAccessHash(ctx context.Context, hash string) (*oauth.Grant, error)
	GetGrantByRefreshHash(ctx context.Context, hash string) (*oauth.Grant, error)
	UpdateGrant(ctx context.Context, g *oauth.Grant) error
	// RotateGrant replaces the grant only while its refresh token hash is still
	// prevRefreshHash, so each refresh token is used once; otherwise it returns
	// domain.ErrNotFound.
	RotateGrant(ctx context.Context, g *oauth.Grant, prevRefreshHash string) error
	// TouchGrant records when the grant was last used, changing nothing else.
	TouchGrant(ctx context.Context, id string, at time.Time) error
	// ListGrants returns the user's grants, newest first.
	ListGrants(ctx context.Context, userID string) ([]*oauth.Grant, error)
	DeleteGrant(ctx context.Context, id string) error
	DeleteGrantsByUser(ctx context.Context, userID string) error
}

// SessionRepository persists web UI sessions. Get returns domain.ErrNotFound for unknown
// sessions; expired sessions may still be returned and must be checked by the caller.
type SessionRepository interface {
	Create(ctx context.Context, s *user.Session) error
	Get(ctx context.Context, tokenHash string) (*user.Session, error)
	Delete(ctx context.Context, tokenHash string) error
	// DeleteByUser removes all sessions of the user except the one with keepTokenHash.
	DeleteByUser(ctx context.Context, userID, keepTokenHash string) error
}

// SettingsRepository persists runtime settings. A missing document yields zero values.
type SettingsRepository interface {
	OpenRouter(ctx context.Context) (*settings.OpenRouter, error)
	SaveOpenRouter(ctx context.Context, s *settings.OpenRouter) error
	// InitWebPush stores the VAPID keys unless keys are stored already, and returns the
	// stored ones.
	InitWebPush(ctx context.Context, k *settings.WebPush) (*settings.WebPush, error)
}

// PushSubscriptionRepository persists the browsers that receive users' notifications. Save
// creates or replaces a subscription (by ID).
type PushSubscriptionRepository interface {
	Save(ctx context.Context, s *push.Subscription) error
	List(ctx context.Context, userID string) ([]*push.Subscription, error)
	Delete(ctx context.Context, id string) error
	DeleteByUser(ctx context.Context, userID string) error
}

// ThemeRepository persists users' own summary themes. Get of a missing theme returns
// domain.ErrNotFound.
type ThemeRepository interface {
	Create(ctx context.Context, t *theme.Theme) error
	Get(ctx context.Context, id string) (*theme.Theme, error)
	List(ctx context.Context, ownerID string) ([]*theme.Theme, error)
	Update(ctx context.Context, t *theme.Theme) error
	Delete(ctx context.Context, id string) error
	DeleteByOwner(ctx context.Context, ownerID string) error
}

// LabelRepository persists users' own labels. Get of a missing label returns
// domain.ErrNotFound.
type LabelRepository interface {
	Create(ctx context.Context, l *label.Label) error
	Get(ctx context.Context, id string) (*label.Label, error)
	List(ctx context.Context, ownerID string) ([]*label.Label, error)
	Update(ctx context.Context, l *label.Label) error
	Delete(ctx context.Context, id string) error
	DeleteByOwner(ctx context.Context, ownerID string) error
}

// FolderRepository persists users' folders. Get of a missing folder returns
// domain.ErrNotFound.
type FolderRepository interface {
	Create(ctx context.Context, f *folder.Folder) error
	Get(ctx context.Context, id string) (*folder.Folder, error)
	List(ctx context.Context, ownerID string) ([]*folder.Folder, error)
	Update(ctx context.Context, f *folder.Folder) error
	Delete(ctx context.Context, id string) error
	DeleteByOwner(ctx context.Context, ownerID string) error
	// ListSharedWith returns the folders shared with the user directly (not those in them).
	ListSharedWith(ctx context.Context, userID string) ([]*folder.Folder, error)
	// RemoveShares takes the user off the shares of all folders (when the user is deleted).
	RemoveShares(ctx context.Context, userID string) error
}

// TabletLinkRepository persists users' links to the reMarkable cloud, one per user. Get of a
// user without a link returns domain.ErrNotFound; Save creates or replaces the link.
type TabletLinkRepository interface {
	Get(ctx context.Context, userID string) (*tablet.Link, error)
	Save(ctx context.Context, l *tablet.Link) error
	Delete(ctx context.Context, userID string) error
	// UserIDs lists the users that have a link.
	UserIDs(ctx context.Context) ([]string, error)
}

// ObjectStore stores binary objects (the audio files). Get of a missing key returns
// domain.ErrNotFound.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	// Get reads length bytes starting at offset; length < 0 reads to the end.
	Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// FilterRepository persists users' saved filters. Get of a missing filter returns
// domain.ErrNotFound.
type FilterRepository interface {
	Create(ctx context.Context, f *filter.Filter) error
	Get(ctx context.Context, id string) (*filter.Filter, error)
	List(ctx context.Context, ownerID string) ([]*filter.Filter, error)
	Update(ctx context.Context, f *filter.Filter) error
	Delete(ctx context.Context, id string) error
	DeleteByOwner(ctx context.Context, ownerID string) error
}

// TimeEntryRepository persists the time logged on notes. Get of a missing entry and Running
// of a user without a running timer return domain.ErrNotFound.
type TimeEntryRepository interface {
	Create(ctx context.Context, e *timelog.Entry) error
	Get(ctx context.Context, id string) (*timelog.Entry, error)
	Update(ctx context.Context, e *timelog.Entry) error
	Delete(ctx context.Context, id string) error
	// List returns the entries that started in the range, oldest first.
	List(ctx context.Context, r timelog.Range) ([]*timelog.Entry, error)
	// Running returns the user's running timer (the entry without an end).
	Running(ctx context.Context, ownerID string) (*timelog.Entry, error)
	DeleteByNote(ctx context.Context, noteID string) error
	DeleteByOwner(ctx context.Context, ownerID string) error
}
