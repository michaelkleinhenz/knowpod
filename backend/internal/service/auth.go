package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

var (
	ErrInvalidLogin  = errors.New("invalid email or password")
	ErrNotSignedIn   = errors.New("not signed in")
	ErrWrongPassword = errors.New("current password is wrong")
	ErrWeakPassword  = errors.New("password must be 8-72 characters")
	ErrForbidden     = errors.New("not allowed")
)

const (
	minPasswordLength = 8
	maxPasswordLength = 72 // bcrypt ignores everything after 72 bytes
)

// dummyPasswordHash is compared against for unknown accounts so that a login attempt takes
// the same time whether or not the account exists.
var dummyPasswordHash = mustHash("knowpod-timing-equaliser")

func mustHash(pw string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}

// Account is the caller of a request: a signed-in user, or a script using ADMIN_TOKEN
// (All: it acts as the built-in admin and sees every user's data).
type Account struct {
	ID       string    `json:"id"`
	Email    string    `json:"email"`
	Role     user.Role `json:"role"`
	Language string    `json:"language,omitempty"` // web UI language; empty follows the browser
	TimeZone string    `json:"timeZone,omitempty"` // IANA time zone of task dates; empty is UTC
	// Appearance is the web UI color scheme ("light", "dark"); empty follows the system.
	Appearance string `json:"appearance,omitempty"`
	All        bool   `json:"-"`
}

// IsAdmin reports whether the account may manage users and global settings.
func (a *Account) IsAdmin() bool { return a.Role == user.RoleAdmin }

// Owns reports whether the account may access data owned by ownerID.
func (a *Account) Owns(ownerID string) bool { return a.All || a.ID == ownerID }

// OwnerFilter is the owner to filter lists by ("" = everyone's, for ADMIN_TOKEN).
func (a *Account) OwnerFilter() string {
	if a.All {
		return ""
	}
	return a.ID
}

// AuthService signs people in to the web UI.
//
// The built-in admin (ADMIN_EMAIL) is a normal user record created at startup, but until a
// password is set for it in the UI its password is ADMIN_PASSWORD from the environment.
type AuthService struct {
	users         ports.UserRepository
	sessions      ports.SessionRepository
	adminEmail    string
	adminPassword string
	sessionTTL    time.Duration
	clock         func() time.Time
	// OnTimeZoneChanged is called after a user changed their time zone, e.g. to move the
	// reminders of their tasks. Optional.
	OnTimeZoneChanged func(ctx context.Context, u *user.User) error
}

// NewAuthService builds the service. adminEmail/adminPassword are the built-in admin; empty
// values disable it.
func NewAuthService(users ports.UserRepository, sessions ports.SessionRepository, adminEmail, adminPassword string, sessionTTL time.Duration) *AuthService {
	return &AuthService{
		users: users, sessions: sessions,
		adminEmail: normalizeEmail(adminEmail), adminPassword: adminPassword,
		sessionTTL: sessionTTL, clock: time.Now,
	}
}

// IsBuiltIn reports whether u is the built-in admin.
func (s *AuthService) IsBuiltIn(u *user.User) bool {
	return s.adminEmail != "" && u.Email == s.adminEmail
}

// EnsureBuiltInAdmin creates the built-in admin's record if it is missing (and keeps it an
// admin). It returns the record, or nil when ADMIN_EMAIL is not set.
func (s *AuthService) EnsureBuiltInAdmin(ctx context.Context) (*user.User, error) {
	if s.adminEmail == "" {
		return nil, nil
	}
	u, err := s.users.GetByEmail(ctx, s.adminEmail)
	switch {
	case errors.Is(err, ErrNotFound):
		u = &user.User{ID: newID(), Email: s.adminEmail, Role: user.RoleAdmin, CreatedAt: s.clock().UTC()}
		if err := s.users.Create(ctx, u); err != nil && !errors.Is(err, errDuplicate) {
			return nil, err
		}
		return s.users.GetByEmail(ctx, s.adminEmail)
	case err != nil:
		return nil, err
	case u.Role != user.RoleAdmin:
		u.Role = user.RoleAdmin
		return u, s.users.Update(ctx, u)
	}
	return u, nil
}

// Login checks the credentials and starts a session. It returns the session token for the
// cookie and its expiry.
func (s *AuthService) Login(ctx context.Context, email, password string) (acc *Account, token string, expires time.Time, err error) {
	u, err := s.users.GetByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password)) // equal timing
		return nil, "", time.Time{}, ErrInvalidLogin
	}
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if !s.passwordMatches(u, password) {
		return nil, "", time.Time{}, ErrInvalidLogin
	}

	token = newToken()
	now := s.clock().UTC()
	sess := &user.Session{TokenHash: hashToken(token), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(s.sessionTTL)}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, "", time.Time{}, err
	}
	return account(u), token, sess.ExpiresAt, nil
}

// passwordMatches checks a password against the stored hash, or, for the built-in admin
// without a stored password, against ADMIN_PASSWORD.
func (s *AuthService) passwordMatches(u *user.User, password string) bool {
	if u.PasswordHash != "" {
		return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
	}
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password)) // equal timing
	return s.IsBuiltIn(u) && s.adminPassword != "" &&
		subtle.ConstantTimeCompare([]byte(password), []byte(s.adminPassword)) == 1
}

// Authenticate resolves a session token to the signed-in account. The user is loaded on
// every request, so role changes and deletions take effect immediately.
func (s *AuthService) Authenticate(ctx context.Context, token string) (*Account, error) {
	if token == "" {
		return nil, ErrNotSignedIn
	}
	sess, err := s.sessions.Get(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotSignedIn
	}
	if err != nil {
		return nil, err
	}
	if !s.clock().Before(sess.ExpiresAt) || sess.UserID == "" {
		_ = s.sessions.Delete(ctx, sess.TokenHash)
		return nil, ErrNotSignedIn
	}
	u, err := s.users.Get(ctx, sess.UserID)
	if errors.Is(err, ErrNotFound) {
		_ = s.sessions.Delete(ctx, sess.TokenHash)
		return nil, ErrNotSignedIn
	}
	if err != nil {
		return nil, err
	}
	return account(u), nil
}

// ScriptAccount is the account for requests authenticated with ADMIN_TOKEN: it acts as the
// built-in admin and sees all users' data.
func (s *AuthService) ScriptAccount(ctx context.Context) (*Account, error) {
	acc := &Account{Role: user.RoleAdmin, All: true}
	if s.adminEmail != "" {
		if u, err := s.users.GetByEmail(ctx, s.adminEmail); err == nil {
			acc.ID, acc.Email = u.ID, u.Email
		}
	}
	return acc, nil
}

// Logout ends the session.
func (s *AuthService) Logout(ctx context.Context, token string) error {
	err := s.sessions.Delete(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ChangePassword sets a new password for the signed-in user after checking the current one.
// For the built-in admin this replaces ADMIN_PASSWORD. Other sessions are ended.
func (s *AuthService) ChangePassword(ctx context.Context, acc *Account, currentToken, current, next string) error {
	u, err := s.users.Get(ctx, acc.ID)
	if err != nil {
		return err
	}
	if !s.passwordMatches(u, current) {
		return ErrWrongPassword
	}
	if err := s.setPassword(ctx, u, next); err != nil {
		return err
	}
	return s.sessions.DeleteByUser(ctx, u.ID, hashToken(currentToken))
}

// setPassword validates, hashes and stores a password.
func (s *AuthService) setPassword(ctx context.Context, u *user.User, password string) error {
	if err := s.setPasswordValue(u, password); err != nil {
		return err
	}
	return s.users.Update(ctx, u)
}

// setPasswordValue validates and hashes a password into u without saving it.
func (s *AuthService) setPasswordValue(u *user.User, password string) error {
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	u.PasswordHash, u.PasswordChangedAt = string(hash), &now
	return nil
}

func account(u *user.User) *Account {
	role := u.Role
	if !role.Valid() {
		role = user.RoleUser
	}
	return &Account{ID: u.ID, Email: u.Email, Role: role, Language: u.Language, TimeZone: u.TimeZone, Appearance: u.Appearance}
}

// Preferences are a user's own settings.
type Preferences struct {
	Language *string `json:"language,omitempty"`
	// TimeZone is an IANA time zone name such as "Europe/Berlin".
	TimeZone *string `json:"timeZone,omitempty"`
	// Appearance is the web UI color scheme: "light", "dark" or empty for the system's.
	Appearance *string `json:"appearance,omitempty"`
}

// UpdatePreferences changes the signed-in user's settings.
func (s *AuthService) UpdatePreferences(ctx context.Context, acc *Account, p Preferences) (*Account, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("preferences belong to a user; sign in"))
	}
	u, err := s.users.Get(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	if p.Language != nil {
		if !user.ValidLanguage(*p.Language) {
			return nil, invalid("language must be one of %v", user.Languages)
		}
		u.Language = *p.Language
	}
	if p.Appearance != nil {
		if !user.ValidAppearance(*p.Appearance) {
			return nil, invalid("appearance must be empty or one of %v", user.Appearances)
		}
		u.Appearance = *p.Appearance
	}
	zoneChanged := false
	if p.TimeZone != nil {
		tz := strings.TrimSpace(*p.TimeZone)
		if _, err := time.LoadLocation(tz); err != nil || len(tz) > 64 || strings.EqualFold(tz, "local") {
			return nil, invalid("unknown time zone %q", tz)
		}
		zoneChanged = u.TimeZone != tz
		u.TimeZone = tz
	}
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	if zoneChanged && s.OnTimeZoneChanged != nil {
		if err := s.OnTimeZoneChanged(ctx, u); err != nil {
			return nil, err
		}
	}
	return account(u), nil
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
