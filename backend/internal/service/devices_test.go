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

	if _, _, err := s.Register(ctx, "  "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty name: %v", err)
	}
	d, token, err := s.Register(ctx, "kitchen recorder")
	if err != nil {
		t.Fatal(err)
	}
	if d.TokenHash == token || d.TokenHash != hashToken(token) {
		t.Fatal("token must be stored hashed")
	}

	got, err := s.Authenticate(ctx, token)
	if err != nil || got.ID != d.ID || got.LastSeenAt == nil {
		t.Fatalf("authenticate: %+v, %v", got, err)
	}
	if _, err := s.Authenticate(ctx, token+"x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token: %v", err)
	}

	_, rotated, err := s.RotateToken(ctx, d.ID)
	if err != nil || rotated == token {
		t.Fatalf("rotate: %q, %v", rotated, err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token after rotation: %v", err)
	}
	if got, err := s.Authenticate(ctx, rotated); err != nil || got.ID != d.ID {
		t.Fatalf("new token: %+v, %v", got, err)
	}
	token = rotated

	if err := s.Revoke(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token: %v", err)
	}
	if _, _, err := s.RotateToken(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotate revoked device: %v", err)
	}
}
