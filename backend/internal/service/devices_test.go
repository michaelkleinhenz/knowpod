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

	if err := s.Revoke(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token: %v", err)
	}
}
