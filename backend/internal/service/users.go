package service

import (
	"context"
	"errors"
	"net/mail"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

var (
	// ErrEmailTaken is returned when another user already has the email.
	ErrEmailTaken = errors.New("a user with this email already exists")
	// ErrSelfDelete: administrators can't delete their own account.
	ErrSelfDelete = errors.New("you can't delete your own account")
	// ErrBuiltInAdmin: the ADMIN_EMAIL account can't be deleted, renamed or demoted.
	ErrBuiltInAdmin = errors.New("the built-in admin can't be deleted, renamed or demoted")
	// ErrLastAdmin: at least one admin must remain.
	ErrLastAdmin = errors.New("there must be at least one admin")
)

// UserView is a user as shown to administrators (no secrets).
type UserView struct {
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	Role              user.Role  `json:"role"`
	BuiltIn           bool       `json:"builtIn"`          // the ADMIN_EMAIL account
	UsesEnvPassword   bool       `json:"usesEnvPassword"`  // built-in admin still on ADMIN_PASSWORD
	PocketConfigured  bool       `json:"pocketConfigured"` // has webhook secret and API key
	CreatedAt         time.Time  `json:"createdAt"`
	PasswordChangedAt *time.Time `json:"passwordChangedAt,omitempty"`
}

// UserInput creates or changes a user. Nil fields are left unchanged on update.
type UserInput struct {
	Email    *string    `json:"email,omitempty"`
	Role     *user.Role `json:"role,omitempty"`
	Password *string    `json:"password,omitempty"` // create only
}

// UserService lets administrators manage users.
type UserService struct {
	users      ports.UserRepository
	sessions   ports.SessionRepository
	devices    ports.DeviceRepository
	recs       ports.RecordingRepository
	themes     ports.ThemeRepository
	auth       *AuthService
	recordings *RecordingService
	// Labels holds users' labels, removed with the user. Optional.
	Labels ports.LabelRepository
	// Folders holds users' folders, removed with the user. Optional.
	Folders ports.FolderRepository
	// Push holds the browsers that receive users' notifications, removed with the user.
	// Optional.
	Push ports.PushSubscriptionRepository
	// Remarkable forgets users' reMarkable links with the user. Optional.
	Remarkable interface {
		DeleteByOwner(ctx context.Context, userID string) error
	}
	clock func() time.Time
}

// NewUserService builds the service.
func NewUserService(users ports.UserRepository, sessions ports.SessionRepository, devices ports.DeviceRepository,
	recs ports.RecordingRepository, themes ports.ThemeRepository, auth *AuthService, recordings *RecordingService) *UserService {
	return &UserService{users: users, sessions: sessions, devices: devices, recs: recs, themes: themes, auth: auth,
		recordings: recordings, clock: time.Now}
}

func (s *UserService) view(u *user.User) UserView {
	return UserView{
		ID: u.ID, Email: u.Email, Role: account(u).Role, BuiltIn: s.auth.IsBuiltIn(u),
		UsesEnvPassword:  s.auth.IsBuiltIn(u) && u.PasswordHash == "",
		PocketConfigured: u.Pocket.WebhookSecret != "" && u.Pocket.APIKey != "",
		CreatedAt:        u.CreatedAt, PasswordChangedAt: u.PasswordChangedAt,
	}
}

// List returns all users.
func (s *UserService) List(ctx context.Context) ([]UserView, error) {
	list, err := s.users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserView, 0, len(list))
	for _, u := range list {
		out = append(out, s.view(u))
	}
	return out, nil
}

// Create adds a user with an initial password.
func (s *UserService) Create(ctx context.Context, in UserInput) (*UserView, error) {
	if in.Email == nil || in.Password == nil {
		return nil, invalid("email and password are required")
	}
	email, err := validEmail(*in.Email)
	if err != nil {
		return nil, err
	}
	role := user.RoleUser
	if in.Role != nil {
		if !in.Role.Valid() {
			return nil, invalid("role must be \"admin\" or \"user\"")
		}
		role = *in.Role
	}
	u := &user.User{ID: newID(), Email: email, Role: role, CreatedAt: s.clock().UTC()}
	if err := s.auth.setPasswordValue(u, *in.Password); err != nil {
		return nil, err
	}
	if err := s.users.Create(ctx, u); err != nil {
		if errors.Is(err, errDuplicate) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	v := s.view(u)
	return &v, nil
}

// Update changes a user's email and/or role. The built-in admin keeps its email and role, and
// the last admin can't be demoted.
func (s *UserService) Update(ctx context.Context, id string, in UserInput) (*UserView, error) {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Email != nil {
		email, err := validEmail(*in.Email)
		if err != nil {
			return nil, err
		}
		if email != u.Email && s.auth.IsBuiltIn(u) {
			return nil, errors.Join(ErrForbidden, ErrBuiltInAdmin)
		}
		u.Email = email
	}
	if in.Role != nil && *in.Role != u.Role {
		if !in.Role.Valid() {
			return nil, invalid("role must be \"admin\" or \"user\"")
		}
		if u.Role == user.RoleAdmin {
			if s.auth.IsBuiltIn(u) {
				return nil, errors.Join(ErrForbidden, ErrBuiltInAdmin)
			}
			if err := s.keepAnAdmin(ctx, u.ID); err != nil {
				return nil, err
			}
		}
		u.Role = *in.Role
	}
	if err := s.users.Update(ctx, u); err != nil {
		if errors.Is(err, errDuplicate) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	v := s.view(u)
	return &v, nil
}

// SetPassword sets a user's password (e.g. a reset by an administrator) and signs the user
// out everywhere except in the administrator's own session.
func (s *UserService) SetPassword(ctx context.Context, id, password, keepTokenHash string) error {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.auth.setPassword(ctx, u, password); err != nil {
		return err
	}
	return s.sessions.DeleteByUser(ctx, u.ID, keepTokenHash)
}

// Delete removes a user together with their devices, recordings (including the audio in
// storage) and sessions. Administrators can't delete themselves, the built-in admin or the
// last admin.
func (s *UserService) Delete(ctx context.Context, actor *Account, id string) error {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case u.ID == actor.ID:
		return errors.Join(ErrForbidden, ErrSelfDelete)
	case s.auth.IsBuiltIn(u):
		return errors.Join(ErrForbidden, ErrBuiltInAdmin)
	case u.Role == user.RoleAdmin:
		if err := s.keepAnAdmin(ctx, u.ID); err != nil {
			return err
		}
	}

	devs, err := s.devices.List(ctx, u.ID)
	if err != nil {
		return err
	}
	for _, d := range devs {
		if err := s.devices.Delete(ctx, d.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	for {
		recs, err := s.recs.List(ctx, recording.ListFilter{OwnerID: u.ID, Limit: 100, Brief: true})
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			break
		}
		for _, r := range recs {
			if err := s.recordings.delete(ctx, r); err != nil {
				return err
			}
		}
	}
	if s.themes != nil {
		if err := s.themes.DeleteByOwner(ctx, u.ID); err != nil {
			return err
		}
	}
	if s.Labels != nil {
		if err := s.Labels.DeleteByOwner(ctx, u.ID); err != nil {
			return err
		}
	}
	if s.Folders != nil {
		if err := s.Folders.DeleteByOwner(ctx, u.ID); err != nil {
			return err
		}
	}
	if s.Remarkable != nil {
		if err := s.Remarkable.DeleteByOwner(ctx, u.ID); err != nil {
			return err
		}
	}
	if s.Push != nil {
		if err := s.Push.DeleteByUser(ctx, u.ID); err != nil {
			return err
		}
	}
	if err := s.sessions.DeleteByUser(ctx, u.ID, ""); err != nil {
		return err
	}
	return s.users.Delete(ctx, u.ID)
}

// keepAnAdmin fails if removing admin rights from exceptID would leave no admin.
func (s *UserService) keepAnAdmin(ctx context.Context, exceptID string) error {
	list, err := s.users.List(ctx)
	if err != nil {
		return err
	}
	for _, u := range list {
		if u.ID != exceptID && u.Role == user.RoleAdmin {
			return nil
		}
	}
	return errors.Join(ErrForbidden, ErrLastAdmin)
}

func validEmail(s string) (string, error) {
	email := normalizeEmail(s)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", invalid("enter a valid email address")
	}
	return email, nil
}
