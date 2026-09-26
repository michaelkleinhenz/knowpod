package service

import (
	"context"
	"errors"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func TestDeviceLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewDeviceService(memory.NewDevices())
	alice, bob, script := &Account{ID: "alice"}, &Account{ID: "bob"}, &Account{All: true}

	if _, _, err := s.Register(ctx, alice, "  "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty name: %v", err)
	}
	d, token, err := s.Register(ctx, alice, "kitchen recorder")
	if err != nil {
		t.Fatal(err)
	}
	if d.OwnerID != "alice" || d.TokenHash == token || d.TokenHash != hashToken(token) {
		t.Fatalf("device = %+v", d)
	}
	if list, _ := s.List(ctx, bob); len(list) != 0 {
		t.Fatalf("bob sees %d devices", len(list))
	}
	if list, _ := s.List(ctx, script); len(list) != 1 {
		t.Fatalf("ADMIN_TOKEN sees %d devices", len(list))
	}
	if err := s.Revoke(ctx, bob, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob revoking alice's device: %v", err)
	}

	got, err := s.Authenticate(ctx, token)
	if err != nil || got.ID != d.ID || got.LastSeenAt == nil {
		t.Fatalf("authenticate: %+v, %v", got, err)
	}
	if _, err := s.Authenticate(ctx, token+"x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token: %v", err)
	}

	_, rotated, err := s.RotateToken(ctx, alice, d.ID)
	if err != nil || rotated == token {
		t.Fatalf("rotate: %q, %v", rotated, err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token after rotation: %v", err)
	}
	token = rotated

	if err := s.Revoke(ctx, alice, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token: %v", err)
	}
	if _, _, err := s.RotateToken(ctx, alice, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotate revoked device: %v", err)
	}
}
