package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func newAuth() (*AuthService, *memory.Users, *memory.Sessions) {
	users, sessions := memory.NewUsers(), memory.NewSessions()
	return NewAuthService(users, sessions, " Admin@Example.com ", "env-secret", time.Hour), users, sessions
}

func TestLoginWithDefaultAdmin(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newAuth()

	acc, token, expires, err := s.Login(ctx, "ADMIN@example.com", "env-secret")
	if err != nil || acc.Email != "admin@example.com" || token == "" || expires.IsZero() {
		t.Fatalf("login: %+v %q %v %v", acc, token, expires, err)
	}
	if got, err := s.Authenticate(ctx, token); err != nil || got.Email != "admin@example.com" {
		t.Fatalf("authenticate: %+v, %v", got, err)
	}

	for _, tc := range []struct{ email, pw string }{
		{"admin@example.com", "wrong"},
		{"someone@example.com", "env-secret"},
		{"", ""},
	} {
		if _, _, _, err := s.Login(ctx, tc.email, tc.pw); !errors.Is(err, ErrInvalidLogin) {
			t.Errorf("login(%q, %q) = %v", tc.email, tc.pw, err)
		}
	}
}

func TestDefaultLoginDisabledWithoutEnv(t *testing.T) {
	s := NewAuthService(memory.NewUsers(), memory.NewSessions(), "", "", time.Hour)
	if _, _, _, err := s.Login(context.Background(), "", ""); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("err = %v", err)
	}
}

func TestChangePasswordOverridesEnv(t *testing.T) {
	ctx := context.Background()
	s, users, sessions := newAuth()
	acc, token, _, _ := s.Login(ctx, "admin@example.com", "env-secret")
	_, other, _, _ := s.Login(ctx, "admin@example.com", "env-secret")

	if err := s.ChangePassword(ctx, acc, token, "wrong", "new-password"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := s.ChangePassword(ctx, acc, token, "env-secret", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak: %v", err)
	}
	if err := s.ChangePassword(ctx, acc, token, "env-secret", "new-password"); err != nil {
		t.Fatal(err)
	}

	u, err := users.GetByEmail(ctx, "admin@example.com")
	if err != nil || u.PasswordHash == "" || u.PasswordHash == "new-password" {
		t.Fatalf("stored user: %+v, %v", u, err)
	}
	if _, _, _, err := s.Login(ctx, "admin@example.com", "env-secret"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("env password still works: %v", err)
	}
	if _, _, _, err := s.Login(ctx, "admin@example.com", "new-password"); err != nil {
		t.Fatalf("new password: %v", err)
	}

	// The session used for the change survives; other sessions are ended.
	if _, err := s.Authenticate(ctx, token); err != nil {
		t.Fatalf("current session ended: %v", err)
	}
	if _, err := s.Authenticate(ctx, other); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("other session survived: %v", err)
	}
	if sessions.Count() != 2 { // the current one and the new login
		t.Fatalf("sessions = %d", sessions.Count())
	}

	// Changing again verifies against the stored password.
	if err := s.ChangePassword(ctx, acc, token, "new-password", "newer-password"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionExpiryAndLogout(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newAuth()
	_, token, _, _ := s.Login(ctx, "admin@example.com", "env-secret")

	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("after logout: %v", err)
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatalf("second logout: %v", err)
	}

	_, token, _, _ = s.Login(ctx, "admin@example.com", "env-secret")
	s.clock = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("expired session: %v", err)
	}
}
