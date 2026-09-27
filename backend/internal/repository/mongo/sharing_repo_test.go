package mongo

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

func TestRecordingVersions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	repo := NewRecordingRepo(s)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// A recording from before versions has no version field; it counts as 0.
	id := NewID()
	if _, err := s.DB().Collection(CollRecordings).InsertOne(ctx, bson.M{"_id": id, "deviceId": "d", "clientId": id, "ownerId": "u1", "status": "summarized", "createdAt": now}); err != nil {
		t.Fatal(err)
	}
	a, err := repo.Get(ctx, id)
	if err != nil || a.Version != 0 {
		t.Fatalf("old recording: %v %+v", err, a)
	}
	b, _ := repo.Get(ctx, id)
	a.Title = "a"
	if err := repo.Update(ctx, a); err != nil || a.Version != 1 {
		t.Fatalf("first save: %v version %d", err, a.Version)
	}
	// The copy read before that save can't be saved over it.
	b.Title = "b"
	if err := repo.Update(ctx, b); !errors.Is(err, domain.ErrChanged) || b.Version != 0 {
		t.Fatalf("stale save: %v version %d", err, b.Version)
	}
	if got, _ := repo.Get(ctx, id); got.Title != "a" || got.Version != 1 {
		t.Fatalf("stored: %q %d", got.Title, got.Version)
	}
	// Targeted changes count the version up too.
	if err := repo.AddTrackedSeconds(ctx, id, 5); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, a); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("save after a targeted change: %v", err)
	}
	gone := &recording.Recording{ID: NewID()}
	if err := repo.Update(ctx, gone); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestSharedRecordings(t *testing.T) {
	ctx := context.Background()
	repo := NewRecordingRepo(testStore(t))
	now := time.Now().UTC().Truncate(time.Millisecond)
	due := now.Add(-time.Minute)
	later := now.Add(time.Hour)

	mk := func(owner, parent string, members ...recording.Member) *recording.Recording {
		id := NewID()
		r := &recording.Recording{ID: id, OwnerID: owner, DeviceID: "text:" + owner, ClientID: id, ParentID: parent,
			Status: recording.StatusSummarized, Members: members, NotBefore: now, CreatedAt: now, UpdatedAt: now}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	list := mk("alice", "", recording.Member{UserID: "bob", Role: recording.RoleEditor, Root: true, FolderID: "bobs",
		Labels: []string{"l1", "l2"}, RemindAt: &due})
	milk := mk("alice", list.ID, recording.Member{UserID: "bob", Role: recording.RoleEditor, RemindAt: &later},
		recording.Member{UserID: "carol", Role: recording.RoleViewer, RemindAt: &due})
	mine := mk("bob", "")
	mk("alice", "")

	ids := func(f recording.ListFilter) []string {
		t.Helper()
		got, err := repo.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range got {
			out = append(out, r.ID)
		}
		slices.Sort(out)
		return out
	}
	sorted := func(s ...string) []string { slices.Sort(s); return s }
	if got := ids(recording.ListFilter{UserID: "bob"}); !slices.Equal(got, sorted(list.ID, milk.ID, mine.ID)) {
		t.Fatalf("bob's notes: %v", got)
	}
	if got := ids(recording.ListFilter{OwnerID: "alice", ParentID: list.ID}); !slices.Equal(got, []string{milk.ID}) {
		t.Fatalf("sub-notes: %v", got)
	}

	// Members' reminders are taken once each.
	claimed := map[string]string{}
	for {
		r, user, err := repo.ClaimMemberReminder(ctx, now)
		if errors.Is(err, domain.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		claimed[user] = r.ID
	}
	if len(claimed) != 2 || claimed["bob"] != list.ID || claimed["carol"] != milk.ID {
		t.Fatalf("claimed %v", claimed)
	}
	if got, _ := repo.Get(ctx, milk.ID); got.Member("bob").RemindAt == nil || got.Member("carol").RemindAt != nil {
		t.Fatalf("after claiming: %+v", got.Members)
	}
	if err := repo.SetMemberRemindAt(ctx, milk.ID, "carol", &due); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, milk.ID); got.Member("carol").RemindAt == nil {
		t.Fatal("SetMemberRemindAt")
	}
	if err := repo.SetMemberRemindAt(ctx, milk.ID, "dave", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("not a member: %v", err)
	}

	// Bob's folder and label changes reach his member entries, and only his.
	if err := repo.MoveFolder(ctx, "bob", "bobs", "other"); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveLabel(ctx, "bob", "l1"); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, list.ID)
	if m := got.Member("bob"); m.FolderID != "other" || !slices.Equal(m.Labels, []string{"l2"}) {
		t.Fatalf("bob's entry: %+v", m)
	}
	if err := repo.MoveFolder(ctx, "bob", "other", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, list.ID); got.Member("bob").FolderID != "" {
		t.Fatalf("to the top level: %+v", got.Member("bob"))
	}

	// Deleting a user takes them off everywhere.
	if err := repo.RemoveMember(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if got := ids(recording.ListFilter{UserID: "bob"}); !slices.Equal(got, []string{mine.ID}) {
		t.Fatalf("bob's notes after removal: %v", got)
	}
	if got, _ := repo.Get(ctx, milk.ID); len(got.Members) != 1 || got.Members[0].UserID != "carol" {
		t.Fatalf("members after removal: %+v", got.Members)
	}
}
