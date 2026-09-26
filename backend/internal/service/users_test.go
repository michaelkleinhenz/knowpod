package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

func ptr[T any](v T) *T { return &v }

func TestUserManagement(t *testing.T) {
	ctx := context.Background()
	auth, users, sessions, builtIn := newAuth(t)
	devs, recs, objects := memory.NewDevices(), memory.NewRecordings(), memstore.New()
	spool, _ := NewSpool(t.TempDir())
	themes := memory.NewThemes()
	s := NewUserService(users, sessions, devs, recs, themes, auth, NewRecordingService(recs, objects, spool, nil))
	actor := &Account{ID: builtIn.ID, Role: user.RoleAdmin}

	// Create.
	if _, err := s.Create(ctx, UserInput{Email: ptr("not-an-email"), Password: ptr("long-enough")}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad email: %v", err)
	}
	if _, err := s.Create(ctx, UserInput{Email: ptr("bob@example.com"), Password: ptr("short")}); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}
	bob, err := s.Create(ctx, UserInput{Email: ptr(" Bob@Example.com "), Password: ptr("bob-password")})
	if err != nil || bob.Email != "bob@example.com" || bob.Role != user.RoleUser || bob.BuiltIn {
		t.Fatalf("create: %+v, %v", bob, err)
	}
	if _, err := s.Create(ctx, UserInput{Email: ptr("bob@example.com"), Password: ptr("bob-password")}); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, _, _, err := auth.Login(ctx, "bob@example.com", "bob-password"); err != nil {
		t.Fatalf("bob can't sign in: %v", err)
	}

	// List marks the built-in admin.
	list, _ := s.List(ctx)
	if len(list) != 2 || !list[0].BuiltIn || !list[0].UsesEnvPassword {
		t.Fatalf("list = %+v", list)
	}

	// Update.
	if _, err := s.Update(ctx, bob.ID, UserInput{Role: ptr(user.RoleAdmin), Email: ptr("robert@example.com")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, builtIn.ID, UserInput{Role: ptr(user.RoleUser)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("demote built-in admin: %v", err)
	}
	if _, err := s.Update(ctx, builtIn.ID, UserInput{Email: ptr("x@example.com")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("rename built-in admin: %v", err)
	}
	if _, err := s.Update(ctx, bob.ID, UserInput{Role: ptr(user.Role("root"))}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid role: %v", err)
	}

	// Password reset ends the user's sessions.
	_, bobToken, _, _ := auth.Login(ctx, "robert@example.com", "bob-password")
	if err := s.SetPassword(ctx, bob.ID, "new-bob-password", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, bobToken); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("session survived password reset: %v", err)
	}
	if _, _, _, err := auth.Login(ctx, "robert@example.com", "new-bob-password"); err != nil {
		t.Fatalf("new password: %v", err)
	}

	// Delete removes the user's devices and recordings (with audio).
	_ = devs.Create(ctx, &device.Device{ID: "d1", OwnerID: bob.ID, TokenHash: "h"})
	_ = objects.Put(ctx, "k.flac", bytes.NewReader([]byte("x")), 1, "audio/flac")
	_ = recs.Create(ctx, &recording.Recording{ID: "r1", OwnerID: bob.ID, DeviceID: "d1", ClientID: "c", Audio: &recording.Object{Key: "k.flac"}})
	_ = recs.Create(ctx, &recording.Recording{ID: "r2", OwnerID: builtIn.ID, DeviceID: "d9", ClientID: "c"})

	if err := s.Delete(ctx, actor, actor.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete self: %v", err)
	}
	if err := s.Delete(ctx, &Account{ID: bob.ID, Role: user.RoleAdmin}, builtIn.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete built-in admin: %v", err)
	}
	if err := s.Delete(ctx, actor, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Get(ctx, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("user not deleted")
	}
	if _, err := devs.Get(ctx, "d1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("device not deleted")
	}
	if _, err := recs.Get(ctx, "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("recording not deleted")
	}
	if _, ok := objects.Object("k.flac"); ok {
		t.Fatal("audio not deleted")
	}
	if _, err := recs.Get(ctx, "r2"); err != nil {
		t.Fatal("another user's recording was deleted")
	}
}

func TestLastAdminIsKept(t *testing.T) {
	ctx := context.Background()
	users, sessions := memory.NewUsers(), memory.NewSessions()
	auth := NewAuthService(users, sessions, "", "", 0) // no built-in admin
	s := NewUserService(users, sessions, memory.NewDevices(), memory.NewRecordings(), memory.NewThemes(), auth, nil)
	a, _ := s.Create(ctx, UserInput{Email: ptr("a@example.com"), Password: ptr("password-a"), Role: ptr(user.RoleAdmin)})
	b, _ := s.Create(ctx, UserInput{Email: ptr("b@example.com"), Password: ptr("password-b"), Role: ptr(user.RoleAdmin)})

	if _, err := s.Update(ctx, b.ID, UserInput{Role: ptr(user.RoleUser)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, a.ID, UserInput{Role: ptr(user.RoleUser)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("demoting the last admin: %v", err)
	}
}
