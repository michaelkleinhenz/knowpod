package mongo

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/push"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

// testStore connects to the MongoDB named by KNOWPOD_TEST_MONGO_URI in a fresh database, or
// skips the test when the variable is unset.
func testStore(t *testing.T) *Store {
	t.Helper()
	uri := os.Getenv("KNOWPOD_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("KNOWPOD_TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	s, err := Connect(ctx, uri, "knowpod_test_"+NewID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.DB().Drop(ctx)
		_ = s.Disconnect(ctx)
	})
	if err := Setup(ctx, s.DB()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecordingRepo(t *testing.T) {
	ctx := context.Background()
	repo := NewRecordingRepo(testStore(t))
	now := time.Now().UTC().Truncate(time.Millisecond)

	rec := &recording.Recording{ID: NewID(), DeviceID: "d1", ClientID: "c1", Status: recording.StatusReceived,
		Size: 10, SHA256: "x", NotBefore: now, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}
	dup := *rec
	dup.ID = NewID()
	if err := repo.Create(ctx, &dup); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate create: %v", err)
	}
	if got, err := repo.GetByClientID(ctx, "d1", "c1"); err != nil || got.ID != rec.ID {
		t.Fatalf("GetByClientID = %v, %v", got, err)
	}
	if _, err := repo.Get(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}

	claimed, err := repo.Claim(ctx, recording.StatusReceived, now, now.Add(time.Minute))
	if err != nil || claimed.ID != rec.ID || claimed.Attempts != 1 || !claimed.NotBefore.Equal(now.Add(time.Minute)) {
		t.Fatalf("Claim = %+v, %v", claimed, err)
	}
	if _, err := repo.Claim(ctx, recording.StatusReceived, now, now.Add(time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Claim while leased: %v", err)
	}

	claimed.Status = recording.StatusStored
	if err := repo.Update(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, recording.ListFilter{DeviceID: "d1", Status: recording.StatusStored})
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	stale, err := repo.ListStale(ctx, recording.StatusStored, now.Add(time.Hour), 10)
	if err != nil || len(stale) != 1 {
		t.Fatalf("ListStale = %v, %v", stale, err)
	}
	if err := repo.Delete(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceRepo(t *testing.T) {
	ctx := context.Background()
	repo := NewDeviceRepo(testStore(t))
	d := &device.Device{ID: NewID(), Name: "rec-1", TokenHash: "h1", CreatedAt: time.Now().UTC()}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &device.Device{ID: NewID(), TokenHash: "h1"}); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate token hash: %v", err)
	}
	got, err := repo.GetByTokenHash(ctx, "h1")
	if err != nil || got.ID != d.ID {
		t.Fatalf("GetByTokenHash = %v, %v", got, err)
	}
	now := time.Now().UTC()
	got.RevokedAt = &now
	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, "")
	if err != nil || len(list) != 1 || list[0].Active() {
		t.Fatalf("List = %v, %v", list, err)
	}
}

func TestUserAndSessionRepo(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	users, sessions := NewUserRepo(s), NewSessionRepo(s)

	if _, err := users.GetByEmail(ctx, "a@x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	u := &user.User{ID: NewID(), Email: "a@x", Role: user.RoleUser, PasswordHash: "h1"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := users.Create(ctx, &user.User{ID: NewID(), Email: "a@x"}); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate email: %v", err)
	}
	// Users without Pocket don't collide on the sparse webhook index.
	if err := users.Create(ctx, &user.User{ID: NewID(), Email: "b@x"}); err != nil {
		t.Fatal(err)
	}
	u.PasswordHash = "h2"
	u.Pocket.WebhookID = "hook1"
	if err := users.Update(ctx, u); err != nil {
		t.Fatal(err)
	}
	if got, err := users.GetByPocketWebhookID(ctx, "hook1"); err != nil || got.PasswordHash != "h2" {
		t.Fatalf("GetByPocketWebhookID = %+v, %v", got, err)
	}
	if list, err := users.List(ctx); err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}

	exp := time.Now().Add(time.Hour).UTC()
	for _, h := range []string{"s1", "s2", "s3"} {
		if err := sessions.Create(ctx, &user.Session{TokenHash: h, UserID: u.ID, ExpiresAt: exp}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sessions.DeleteByUser(ctx, u.ID, "s2"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Get(ctx, "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("s1 not deleted: %v", err)
	}
	if got, err := sessions.Get(ctx, "s2"); err != nil || got.UserID != u.ID {
		t.Fatalf("kept session: %+v, %v", got, err)
	}
	if err := users.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAssignOwnerless(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	recs, devs := NewRecordingRepo(s), NewDeviceRepo(s)
	_ = recs.Create(ctx, &recording.Recording{ID: "r1", DeviceID: "d", ClientID: "1"})
	_ = recs.Create(ctx, &recording.Recording{ID: "r2", OwnerID: "bob", DeviceID: "d", ClientID: "2"})
	_ = devs.Create(ctx, &device.Device{ID: "d1", TokenHash: "h"})
	if n, err := recs.AssignOwnerless(ctx, "admin"); err != nil || n != 1 {
		t.Fatalf("recordings: %d, %v", n, err)
	}
	if n, err := devs.AssignOwnerless(ctx, "admin"); err != nil || n != 1 {
		t.Fatalf("devices: %d, %v", n, err)
	}
	if r, _ := recs.Get(ctx, "r2"); r.OwnerID != "bob" {
		t.Fatal("owned recording reassigned")
	}
	if list, _ := devs.List(ctx, "admin"); len(list) != 1 {
		t.Fatalf("admin's devices: %d", len(list))
	}
}

func TestNoteNumbers(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	recs := NewRecordingRepo(s)
	t0 := time.Now().UTC()
	// Notes from before numbers: inserted directly, without one.
	for i, owner := range []string{"ann", "ann", "bob"} {
		rec := recording.Recording{ID: NewID(), OwnerID: owner, DeviceID: "d", ClientID: NewID(), CreatedAt: t0.Add(time.Duration(i) * time.Second)}
		if _, err := recs.c.InsertOne(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	// One numbered note already exists: the backfill continues after it.
	numbered := &recording.Recording{ID: NewID(), OwnerID: "ann", DeviceID: "d", ClientID: NewID(), Number: 7, CreatedAt: t0}
	if _, err := recs.c.InsertOne(ctx, numbered); err != nil {
		t.Fatal(err)
	}
	if n, err := recs.NumberNotes(ctx); err != nil || n != 3 {
		t.Fatalf("NumberNotes: %d %v", n, err)
	}
	if n, err := recs.NumberNotes(ctx); err != nil || n != 0 {
		t.Fatalf("second NumberNotes: %d %v", n, err)
	}
	ann, _ := recs.List(ctx, recording.ListFilter{OwnerID: "ann"})
	got := map[int64]bool{}
	for _, r := range ann {
		got[r.Number] = true
	}
	if len(ann) != 3 || !got[7] || !got[8] || !got[9] {
		t.Fatalf("ann's numbers: %v", got)
	}

	// New notes count on.
	rec := &recording.Recording{ID: NewID(), OwnerID: "ann", DeviceID: "d", ClientID: NewID()}
	if err := recs.Create(ctx, rec); err != nil || rec.Number != 10 {
		t.Fatalf("create: %d %v", rec.Number, err)
	}
	bob := &recording.Recording{ID: NewID(), OwnerID: "bob", DeviceID: "d", ClientID: NewID()}
	if err := recs.Create(ctx, bob); err != nil || bob.Number != 2 {
		t.Fatalf("create bob: %d %v", bob.Number, err)
	}
	if list, _ := recs.List(ctx, recording.ListFilter{OwnerID: "ann", Number: 10}); len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("by number: %+v", list)
	}
}

func TestReserveNumbers(t *testing.T) {
	ctx := context.Background()
	recs := NewRecordingRepo(testStore(t))
	if err := recs.ReserveNumbers(ctx, "ann", 41); err != nil {
		t.Fatal(err)
	}
	// A lower reservation never moves the counter back.
	if err := recs.ReserveNumbers(ctx, "ann", 5); err != nil {
		t.Fatal(err)
	}
	rec := &recording.Recording{ID: NewID(), OwnerID: "ann", DeviceID: "d", ClientID: NewID()}
	if err := recs.Create(ctx, rec); err != nil || rec.Number != 42 {
		t.Fatalf("create: %d %v", rec.Number, err)
	}
}

func TestReminderQueue(t *testing.T) {
	ctx := context.Background()
	repo := NewRecordingRepo(testStore(t))
	now := time.Now().UTC().Truncate(time.Millisecond)
	soon, later := now.Add(-time.Minute), now.Add(time.Hour)
	for i, at := range []*time.Time{&soon, &later, nil} {
		rec := &recording.Recording{ID: NewID(), OwnerID: "u1", DeviceID: "text:u1", ClientID: NewID(), Status: recording.StatusSummarized,
			Due: &recording.Due{Date: "2026-09-28"}, RemindAt: at, NotBefore: now, CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now}
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.ClaimReminder(ctx, now)
	if err != nil || got.RemindAt == nil || !got.RemindAt.Equal(soon) {
		t.Fatalf("claimed %+v, %v", got, err)
	}
	if stored, _ := repo.Get(ctx, got.ID); stored.RemindAt != nil || stored.Due == nil {
		t.Errorf("claimed reminder still pending: %+v", stored)
	}
	if _, err := repo.ClaimReminder(ctx, now); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second claim: %v", err)
	}
	if err := repo.SetRemindAt(ctx, got.ID, &soon); err != nil {
		t.Fatal(err)
	}
	if again, err := repo.ClaimReminder(ctx, now); err != nil || again.ID != got.ID {
		t.Errorf("rescheduled claim: %v, %v", again, err)
	}
	if err := repo.SetRemindAt(ctx, "missing", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestWebPushSettingsAndSubscriptions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	st := NewSettingsRepo(s)
	first, err := st.InitWebPush(ctx, &settings.WebPush{PrivateKey: "a", PublicKey: "A"})
	if err != nil || first.PublicKey != "A" {
		t.Fatalf("%+v, %v", first, err)
	}
	if again, err := st.InitWebPush(ctx, &settings.WebPush{PrivateKey: "b", PublicKey: "B"}); err != nil || again.PublicKey != "A" {
		t.Errorf("keys replaced: %+v, %v", again, err)
	}

	subs := NewPushSubscriptionRepo(s)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, sub := range []*push.Subscription{
		{ID: "s1", UserID: "u1", Endpoint: "https://p/1", CreatedAt: now},
		{ID: "s2", UserID: "u1", Endpoint: "https://p/2", CreatedAt: now.Add(time.Second)},
		{ID: "s3", UserID: "u2", Endpoint: "https://p/3", CreatedAt: now},
		{ID: "s1", UserID: "u1", Endpoint: "https://p/1", UserAgent: "again", CreatedAt: now},
	} {
		if err := subs.Save(ctx, sub); err != nil {
			t.Fatal(err)
		}
	}
	list, err := subs.List(ctx, "u1")
	if err != nil || len(list) != 2 || list[0].UserAgent != "again" {
		t.Fatalf("%+v, %v", list, err)
	}
	if err := subs.Delete(ctx, "s2"); err != nil {
		t.Fatal(err)
	}
	if err := subs.DeleteByUser(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := subs.List(ctx, "u1"); len(list) != 0 {
		t.Errorf("left %+v", list)
	}
	if list, _ := subs.List(ctx, "u2"); len(list) != 1 {
		t.Errorf("u2 has %+v", list)
	}
}
