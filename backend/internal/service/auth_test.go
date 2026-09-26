package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func newAuth(t *testing.T) (*AuthService, *memory.Users, *memory.Sessions, *user.User) {
	t.Helper()
	users, sessions := memory.NewUsers(), memory.NewSessions()
	s := NewAuthService(users, sessions, " Admin@Example.com ", "env-secret", time.Hour)
	admin, err := s.EnsureBuiltInAdmin(context.Background())
	if err != nil || admin == nil || admin.Role != user.RoleAdmin || admin.PasswordHash != "" {
		t.Fatalf("built-in admin = %+v, %v", admin, err)
	}
	return s, users, sessions, admin
}

func TestBuiltInAdminLogin(t *testing.T) {
	ctx := context.Background()
	s, _, _, admin := newAuth(t)

	acc, token, expires, err := s.Login(ctx, "ADMIN@example.com", "env-secret")
	if err != nil || acc.ID != admin.ID || acc.Role != user.RoleAdmin || token == "" || expires.IsZero() {
		t.Fatalf("login: %+v %q %v %v", acc, token, expires, err)
	}
	if got, err := s.Authenticate(ctx, token); err != nil || got.Email != "admin@example.com" || !got.IsAdmin() {
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
	// A second start doesn't create another record.
	again, _ := s.EnsureBuiltInAdmin(ctx)
	if again.ID != admin.ID {
		t.Fatal("built-in admin recreated")
	}
}

func TestPreferences(t *testing.T) {
	ctx := context.Background()
	s, _, _, admin := newAuth(t)
	acc := &Account{ID: admin.ID}
	de, bad := "de", "fr"
	if _, err := s.UpdatePreferences(ctx, acc, Preferences{Language: &bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsupported language: %v", err)
	}
	got, err := s.UpdatePreferences(ctx, acc, Preferences{Language: &de})
	if err != nil || got.Language != "de" {
		t.Fatalf("update: %+v, %v", got, err)
	}
	_, token, _, _ := s.Login(ctx, "admin@example.com", "env-secret")
	if a, _ := s.Authenticate(ctx, token); a.Language != "de" {
		t.Fatalf("language not on account: %+v", a)
	}
}

func TestNoBuiltInAdminWithoutEnv(t *testing.T) {
	s := NewAuthService(memory.NewUsers(), memory.NewSessions(), "", "", time.Hour)
	if u, err := s.EnsureBuiltInAdmin(context.Background()); u != nil || err != nil {
		t.Fatalf("EnsureBuiltInAdmin = %v, %v", u, err)
	}
}

func TestChangePasswordOverridesEnv(t *testing.T) {
	ctx := context.Background()
	s, users, sessions, admin := newAuth(t)
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
	u, _ := users.Get(ctx, admin.ID)
	if u.PasswordHash == "" || u.PasswordChangedAt == nil {
		t.Fatalf("stored user: %+v", u)
	}
	if _, _, _, err := s.Login(ctx, "admin@example.com", "env-secret"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("env password still works: %v", err)
	}
	if _, _, _, err := s.Login(ctx, "admin@example.com", "new-password"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	if _, err := s.Authenticate(ctx, token); err != nil {
		t.Fatalf("current session ended: %v", err)
	}
	if _, err := s.Authenticate(ctx, other); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("other session survived: %v", err)
	}
	if sessions.Count() != 2 {
		t.Fatalf("sessions = %d", sessions.Count())
	}
}

func TestSessionEndsWhenUserIsDeletedOrExpires(t *testing.T) {
	ctx := context.Background()
	s, users, _, _ := newAuth(t)
	u := &user.User{ID: "u1", Email: "bob@example.com", Role: user.RoleUser}
	_ = s.setPasswordValue(u, "bob-password")
	_ = users.Create(ctx, u)

	_, token, _, err := s.Login(ctx, "bob@example.com", "bob-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("after logout: %v", err)
	}

	_, token, _, _ = s.Login(ctx, "bob@example.com", "bob-password")
	_ = users.Delete(ctx, "u1")
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("deleted user still signed in: %v", err)
	}

	_ = users.Create(ctx, u)
	_, token, _, _ = s.Login(ctx, "bob@example.com", "bob-password")
	s.clock = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("expired session: %v", err)
	}
}
