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
	ErrWeakPassword  = errors.New("new password must be 8-72 characters")
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

// Account is the signed-in person.
type Account struct {
	Email string `json:"email"`
}

// AuthService signs people in to the web UI. Accounts stored in the database take
// precedence; the default admin credentials from the environment only work for an email
// that has no stored account yet, i.e. until its password is changed.
type AuthService struct {
	users         ports.UserRepository
	sessions      ports.SessionRepository
	adminEmail    string
	adminPassword string
	sessionTTL    time.Duration
	clock         func() time.Time
}

// NewAuthService builds the service. adminEmail/adminPassword are the default login; empty
// values disable it.
func NewAuthService(users ports.UserRepository, sessions ports.SessionRepository, adminEmail, adminPassword string, sessionTTL time.Duration) *AuthService {
	return &AuthService{
		users: users, sessions: sessions,
		adminEmail: normalizeEmail(adminEmail), adminPassword: adminPassword,
		sessionTTL: sessionTTL, clock: time.Now,
	}
}

// Login checks the credentials and starts a session. It returns the session token for the
// cookie and its expiry.
func (s *AuthService) Login(ctx context.Context, email, password string) (acc *Account, token string, expires time.Time, err error) {
	email = normalizeEmail(email)
	ok, err := s.checkPassword(ctx, email, password)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if !ok {
		return nil, "", time.Time{}, ErrInvalidLogin
	}

	token = newToken()
	now := s.clock().UTC()
	sess := &user.Session{TokenHash: hashToken(token), Email: email, CreatedAt: now, ExpiresAt: now.Add(s.sessionTTL)}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, "", time.Time{}, err
	}
	return &Account{Email: email}, token, sess.ExpiresAt, nil
}

// checkPassword verifies the password against the stored account, or against the default
// admin credentials when the email has no stored account.
func (s *AuthService) checkPassword(ctx context.Context, email, password string) (bool, error) {
	u, err := s.users.GetByEmail(ctx, email)
	switch {
	case err == nil:
		return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil, nil
	case !errors.Is(err, ErrNotFound):
		return false, err
	}
	if s.adminEmail != "" && s.adminPassword != "" && email == s.adminEmail {
		return subtle.ConstantTimeCompare([]byte(password), []byte(s.adminPassword)) == 1, nil
	}
	// Unknown account: spend the same time as a real check so timing doesn't reveal it.
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
	return false, nil
}

// Authenticate resolves a session token to the signed-in account.
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
	if !s.clock().Before(sess.ExpiresAt) {
		_ = s.sessions.Delete(ctx, sess.TokenHash)
		return nil, ErrNotSignedIn
	}
	return &Account{Email: sess.Email}, nil
}

// Logout ends the session.
func (s *AuthService) Logout(ctx context.Context, token string) error {
	err := s.sessions.Delete(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ChangePassword stores a new password for the account, which from then on replaces the
// default credentials from the environment. All other sessions of the account are ended.
func (s *AuthService) ChangePassword(ctx context.Context, acc *Account, currentToken, current, next string) error {
	if len(next) < minPasswordLength || len(next) > maxPasswordLength {
		return ErrWeakPassword
	}
	ok, err := s.checkPassword(ctx, acc.Email, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	now := s.clock().UTC()
	u, err := s.users.GetByEmail(ctx, acc.Email)
	if errors.Is(err, ErrNotFound) {
		u = &user.User{ID: newID(), Email: acc.Email, CreatedAt: now}
	} else if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	u.PasswordChangedAt = now
	if err := s.users.Upsert(ctx, u); err != nil {
		return err
	}
	return s.sessions.DeleteByEmail(ctx, acc.Email, hashToken(currentToken))
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
