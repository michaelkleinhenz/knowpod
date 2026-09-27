package mongo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/oauth"
)

func TestOAuthRepo(t *testing.T) {
	ctx := context.Background()
	repo := NewOAuthRepo(testStore(t))
	now := time.Now().UTC().Truncate(time.Millisecond)

	c := &oauth.Client{ID: NewID(), Name: "Claude", RedirectURIs: []string{"https://claude.ai/cb"}, AuthMethod: oauth.AuthNone, CreatedAt: now}
	if err := repo.CreateClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetClient(ctx, c.ID); err != nil || got.Name != "Claude" || len(got.RedirectURIs) != 1 {
		t.Fatalf("GetClient = %+v, %v", got, err)
	}

	g := &oauth.Grant{ID: NewID(), UserID: "u1", ClientID: c.ID, RedirectURI: "https://claude.ai/cb", CodeChallenge: "ch",
		CodeHash: "code", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := repo.CreateGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	// A second grant without a code must not clash on the sparse index.
	if err := repo.CreateGrant(ctx, &oauth.Grant{ID: NewID(), UserID: "u2", ClientID: c.ID, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimCode(ctx, "code")
	if err != nil || claimed.ID != g.ID || claimed.CodeHash != "" {
		t.Fatalf("ClaimCode = %+v, %v", claimed, err)
	}
	if _, err := repo.ClaimCode(ctx, "code"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second ClaimCode: %v", err)
	}

	exp := now.Add(time.Hour)
	claimed.AccessHash, claimed.AccessExpiresAt, claimed.RefreshHash = "a1", &exp, "r1"
	if err := repo.UpdateGrant(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetGrantByAccessHash(ctx, "a1"); err != nil || got.ID != g.ID {
		t.Fatalf("GetGrantByAccessHash = %+v, %v", got, err)
	}
	claimed.AccessHash, claimed.RefreshHash = "a2", "r2"
	if err := repo.RotateGrant(ctx, claimed, "r1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.RotateGrant(ctx, claimed, "r1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rotating with a used refresh token: %v", err)
	}
	if got, err := repo.GetGrantByRefreshHash(ctx, "r2"); err != nil || got.AccessHash != "a2" {
		t.Fatalf("GetGrantByRefreshHash = %+v, %v", got, err)
	}
	if err := repo.TouchGrant(ctx, g.ID, now); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListGrants(ctx, "u1")
	if err != nil || len(list) != 1 || list[0].LastUsedAt == nil || list[0].AccessHash != "a2" {
		t.Fatalf("ListGrants = %+v, %v", list, err)
	}
	if err := repo.DeleteGrant(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteGrantsByUser(ctx, "u2"); err != nil {
		t.Fatal(err)
	}
	if list, _ := repo.ListGrants(ctx, "u2"); len(list) != 0 {
		t.Fatalf("after DeleteGrantsByUser: %+v", list)
	}
}
