package mongo

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

func TestBackupRepoExportsAndReplaces(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	users := NewUserRepo(store)
	backup := NewBackupRepo(store)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := users.Create(ctx, &user.User{ID: NewID(), Email: email, Role: user.RoleUser, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range backup.Collections() {
		if c == CollSessions {
			t.Fatal("sessions are not backed up")
		}
	}

	var docs [][]byte
	if err := backup.Export(ctx, CollUsers, func(d []byte) error { docs = append(docs, append([]byte(nil), d...)); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("exported %d users", len(docs))
	}

	// Replace swaps the collection's contents for the exported documents.
	if err := users.Create(ctx, &user.User{ID: NewID(), Email: "c@example.com", Role: user.RoleUser, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	i := 0
	err := backup.Replace(ctx, CollUsers, func() ([]byte, error) {
		if i == len(docs) {
			return nil, io.EOF
		}
		i++
		return docs[i-1], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := store.DB().Collection(CollUsers).CountDocuments(ctx, bson.M{})
	if err != nil || n != 2 {
		t.Fatalf("users after replace: %d, %v", n, err)
	}
	if _, err := users.GetByEmail(ctx, "c@example.com"); err == nil {
		t.Error("c@example.com survived the replace")
	}

	if err := backup.Replace(ctx, CollSessions, func() ([]byte, error) { return nil, io.EOF }); err == nil {
		t.Error("sessions must not be replaceable")
	}
	if err := backup.Replace(ctx, CollUsers, func() ([]byte, error) { return nil, errors.New("boom") }); err == nil {
		t.Error("an error from next must fail the replace")
	}
}
