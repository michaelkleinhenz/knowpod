package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

// shareFixture is a task fixture with a second user, bob (in New York), labels and folders.
type shareFixture struct {
	*taskFixture
	bob     *Account
	events  *NoteEvents
	labels  *LabelService
	folders *FolderService
}

func newShareFixture(t *testing.T) *shareFixture {
	t.Helper()
	f := newTaskFixture(t)
	ctx := context.Background()
	if err := f.users.Create(ctx, &user.User{ID: "u2", Email: "bob@example.com", TimeZone: "America/New_York"}); err != nil {
		t.Fatal(err)
	}
	if err := f.users.Create(ctx, &user.User{ID: "u3", Email: "carol@example.com"}); err != nil {
		t.Fatal(err)
	}
	events := NewNoteEvents()
	f.s.recs = events.Watch(f.recs)
	f.s.Events = events
	labels := NewLabelService(memory.NewLabels(), f.s.recs)
	folders := NewFolderService(memory.NewFolders(), f.s.recs)
	f.s.Labels, f.s.Folders = labels, folders
	folders.Notes = f.s
	return &shareFixture{taskFixture: f, bob: &Account{ID: "u2"}, events: events, labels: labels, folders: folders}
}

func (f *shareFixture) share(t *testing.T, id, email string, role recording.Role) *Sharing {
	t.Helper()
	out, err := f.s.Share(context.Background(), f.acc, id, ShareInput{Email: email, Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func ids(list []*recording.Recording) []string {
	out := []string{}
	for _, r := range list {
		out = append(out, r.ID)
	}
	slices.Sort(out)
	return out
}

func TestSharingANoteSharesEverythingUnderIt(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	list := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Groceries"}})
	milk := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Milk"}, ParentID: list.ID})
	eggs := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Eggs"}, ParentID: milk.ID})
	private := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Private"}})

	// Nothing is shared with bob yet.
	if got, _ := f.s.List(ctx, f.bob, recording.ListFilter{}); len(got) != 0 {
		t.Fatalf("bob sees %d notes", len(got))
	}
	if _, err := f.s.Get(ctx, f.bob, list.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get before sharing: %v", err)
	}

	sh := f.share(t, list.ID, " Bob@Example.com ", recording.RoleViewer)
	if sh.Owner.Email != "a@example.com" || len(sh.Members) != 1 || sh.Members[0].Email != "bob@example.com" ||
		sh.Members[0].Role != recording.RoleViewer || sh.Members[0].Inherited {
		t.Fatalf("sharing: %+v", sh)
	}
	got, err := f.s.List(ctx, f.bob, recording.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if want := ids([]*recording.Recording{list, milk, eggs}); !slices.Equal(ids(got), want) {
		t.Fatalf("bob sees %v, want %v", ids(got), want)
	}
	for _, r := range got {
		if r.Access != recording.RoleViewer || !r.Shared {
			t.Errorf("%s: access %q shared %v", r.ID, r.Access, r.Shared)
		}
	}
	// The owner sees the notes as shared, and their own private note isn't.
	own, _ := f.s.List(ctx, f.acc, recording.ListFilter{})
	for _, r := range own {
		if r.Access != recording.RoleOwner || r.Shared != (r.ID != private.ID) {
			t.Errorf("owner's %s: access %q shared %v", r.ID, r.Access, r.Shared)
		}
	}
	// A sub-note's sharing comes from the note it is under.
	if sh, _ := f.s.Sharing(ctx, f.bob, eggs.ID); len(sh.Members) != 1 || !sh.Members[0].Inherited || sh.Access != recording.RoleViewer {
		t.Fatalf("sub-note sharing: %+v", sh)
	}

	// A new sub-note is shared too.
	bread := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Bread"}, ParentID: list.ID})
	if _, err := f.s.Get(ctx, f.bob, bread.ID); err != nil {
		t.Fatalf("new sub-note: %v", err)
	}
	// Moving a note out of the shared note stops sharing it (and what is under it).
	if _, err := f.s.SetParent(ctx, f.acc, milk.ID, private.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{milk.ID, eggs.ID} {
		if _, err := f.s.Get(ctx, f.bob, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s after moving out: %v", id, err)
		}
	}
	// … and moving it back shares it again.
	if _, err := f.s.SetParent(ctx, f.acc, milk.ID, list.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.bob, eggs.ID); err != nil {
		t.Fatalf("after moving back: %v", err)
	}
}

func TestViewersReadAndEditorsChange(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	list := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Groceries"}})
	milk := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Milk"}, ParentID: list.ID, TaskFields: TaskFields{Task: true}})
	f.share(t, list.ID, "bob@example.com", recording.RoleViewer)

	forbidden := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("viewer %s: %v", what, err)
		}
	}
	_, err := f.s.EditSummary(ctx, f.bob, milk.ID, SummaryEdit{Title: "Oat milk"})
	forbidden("edits text", err)
	_, err = f.s.SetDone(ctx, f.bob, milk.ID, true)
	forbidden("checks off", err)
	_, err = f.s.CreateText(ctx, f.bob, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Bread"}, ParentID: list.ID})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("viewer adds a sub-note: %v", err)
	}
	_, err = f.s.Share(ctx, f.bob, list.ID, ShareInput{Email: "carol@example.com", Role: recording.RoleViewer})
	forbidden("shares", err)

	// As an editor, bob changes the notes for everyone.
	f.share(t, list.ID, "bob@example.com", recording.RoleEditor)
	if _, err := f.s.EditSummary(ctx, f.bob, milk.ID, SummaryEdit{Title: "Oat milk"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetDone(ctx, f.bob, milk.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, f.acc, milk.ID); got.Summary.Title != "Oat milk" || !got.Done {
		t.Fatalf("owner sees %q done %v", got.Summary.Title, got.Done)
	}
	// A note bob adds belongs to the owner and is shared like the list.
	bread, err := f.s.CreateText(ctx, f.bob, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Bread"}, ParentID: list.ID})
	if err != nil {
		t.Fatal(err)
	}
	if bread.OwnerID != f.acc.ID || bread.CreatedBy != f.bob.ID || bread.Access != recording.RoleEditor || bread.Number == 0 {
		t.Fatalf("bob's note: %+v", bread)
	}
	if got, err := f.s.Get(ctx, f.acc, bread.ID); err != nil || got.Access != recording.RoleOwner {
		t.Fatalf("owner gets bob's note: %v", err)
	}
	// Editors put notes under the list into the owner's trash, but not the list itself.
	if _, err := f.s.Trash(ctx, f.bob, bread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Trash(ctx, f.bob, list.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor trashes the shared note: %v", err)
	}
	if _, err := f.s.Get(ctx, f.bob, bread.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("member sees the owner's trash: %v", err)
	}
	if trash, _ := f.s.List(ctx, f.acc, recording.ListFilter{Trash: recording.TrashOnly}); len(trash) != 1 || trash[0].ID != bread.ID {
		t.Fatalf("owner's trash: %v", ids(trash))
	}
	// Only the owner deletes for good and processes again.
	if err := f.s.Delete(ctx, f.bob, milk.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor deletes for good: %v", err)
	}
	// Editors move notes within the owner's notes they can edit, but not out of them.
	if _, err := f.s.SetParent(ctx, f.bob, milk.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor moves out: %v", err)
	}
	private := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Private"}})
	if _, err := f.s.SetParent(ctx, f.bob, milk.ID, private.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("editor moves to a note they can't see: %v", err)
	}
}

func TestMembersFileSharedNotesForThemselves(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	project := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Project"}})
	list := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Groceries"}, ParentID: project.ID})
	milk := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Milk"}, ParentID: list.ID})
	// Only the list (a sub-note of the owner's project) is shared.
	f.share(t, list.ID, "bob@example.com", recording.RoleViewer)

	// Bob has the list at his top level, with the notes under it.
	got, _ := f.s.Get(ctx, f.bob, list.ID)
	if got.ParentID != "" || got.FolderID != "" {
		t.Fatalf("shared note for bob: parent %q folder %q", got.ParentID, got.FolderID)
	}
	if got, _ := f.s.Get(ctx, f.bob, milk.ID); got.ParentID != list.ID {
		t.Fatalf("sub-note for bob: parent %q", got.ParentID)
	}

	// He puts it into his own folder; the owner's note stays where it is.
	home, err := f.folders.Create(ctx, f.bob, FolderInput{Name: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.SetFolder(ctx, f.bob, list.ID, home.ID); err != nil || got.FolderID != home.ID || got.ParentID != "" {
		t.Fatalf("bob's folder: %v %+v", err, got)
	}
	if got, _ := f.s.Get(ctx, f.acc, list.ID); got.FolderID != "" || got.ParentID != project.ID {
		t.Fatalf("owner's place changed: folder %q parent %q", got.FolderID, got.ParentID)
	}
	if _, err := f.s.SetFolder(ctx, f.bob, milk.ID, home.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("moving a note from under the shared note: %v", err)
	}
	// Deleting his folder moves the note to his top level.
	if err := f.folders.Delete(ctx, f.bob, home.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, f.bob, list.ID); got.FolderID != "" {
		t.Fatalf("after deleting the folder: %q", got.FolderID)
	}

	// Labels: bob's own are his alone, even as a viewer.
	mine, err := f.labels.Create(ctx, f.bob, LabelInput{Name: "Weekend", Color: "#112233"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := f.labels.Create(ctx, f.acc, LabelInput{Name: "Errands", Color: "#445566"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetLabels(ctx, f.acc, milk.ID, []string{theirs.ID}); err != nil {
		t.Fatal(err)
	}
	got, err = f.s.SetLabels(ctx, f.bob, milk.ID, []string{mine.ID})
	if err != nil || !slices.Equal(got.Labels, []string{mine.ID}) {
		t.Fatalf("bob's labels: %v %v", err, got)
	}
	if got, _ := f.s.Get(ctx, f.acc, milk.ID); !slices.Equal(got.Labels, []string{theirs.ID}) {
		t.Fatalf("owner's labels: %v", got.Labels)
	}
	if _, err := f.s.SetLabels(ctx, f.bob, milk.ID, []string{theirs.ID}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bob uses the owner's label: %v", err)
	}
	// The task label is the note's: a viewer can't change it.
	if _, err := f.s.SetLabels(ctx, f.bob, milk.ID, []string{mine.ID, label.Task}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer makes a task: %v", err)
	}
	f.share(t, list.ID, "bob@example.com", recording.RoleEditor)
	if _, err := f.s.SetLabels(ctx, f.bob, milk.ID, []string{mine.ID, label.Task}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, f.acc, milk.ID); !slices.Equal(got.Labels, []string{theirs.ID, label.Task}) {
		t.Fatalf("owner's labels after bob made a task: %v", got.Labels)
	}
	// Deleting his label takes it off the shared note.
	if err := f.labels.Delete(ctx, f.bob, mine.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, f.bob, milk.ID); !slices.Equal(got.Labels, []string{label.Task}) {
		t.Fatalf("bob's labels after deleting his: %v", got.Labels)
	}

	// Ordering: bob orders the shared note among his own notes, for himself.
	own := f.s
	a, err := own.CreateText(ctx, f.bob, TextNoteInput{SummaryEdit: SummaryEdit{Title: "A"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Reorder(ctx, f.bob, []string{list.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, f.bob, list.ID); got.Position != 1 {
		t.Fatalf("bob's position: %d", got.Position)
	}
	if got, _ := f.s.Get(ctx, f.acc, list.ID); got.Position != 0 {
		t.Fatalf("owner's position changed: %d", got.Position)
	}
}

func TestLeavingAndUnsharing(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	list := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Groceries"}})
	milk := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Milk"}, ParentID: list.ID})
	f.share(t, list.ID, "bob@example.com", recording.RoleEditor)
	f.share(t, list.ID, "carol@example.com", recording.RoleViewer)

	if _, err := f.s.Share(ctx, f.acc, list.ID, ShareInput{Email: "nobody@example.com", Role: recording.RoleViewer}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, err := f.s.Share(ctx, f.acc, list.ID, ShareInput{Email: "a@example.com", Role: recording.RoleViewer}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("sharing with oneself: %v", err)
	}
	if _, err := f.s.Share(ctx, f.acc, list.ID, ShareInput{Email: "bob@example.com", Role: "admin"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad role: %v", err)
	}
	// The sharing of a sub-note is changed where it comes from.
	if _, err := f.s.Unshare(ctx, f.bob, milk.ID, f.bob.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("leaving a sub-note: %v", err)
	}
	// A member can't take others off.
	if _, err := f.s.Unshare(ctx, f.bob, list.ID, "u3"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor unshares carol: %v", err)
	}

	// Bob leaves.
	if out, err := f.s.Unshare(ctx, f.bob, list.ID, f.bob.ID); err != nil || out != nil {
		t.Fatalf("leave: %v %+v", err, out)
	}
	if got, _ := f.s.List(ctx, f.bob, recording.ListFilter{}); len(got) != 0 {
		t.Fatalf("bob still sees %v", ids(got))
	}
	// The owner takes carol off.
	sh, err := f.s.Unshare(ctx, f.acc, list.ID, "u3")
	if err != nil || len(sh.Members) != 0 {
		t.Fatalf("unshare: %v %+v", err, sh)
	}
	if got, _ := f.s.Get(ctx, f.acc, milk.ID); got.Shared {
		t.Fatal("sub-note still shared")
	}
}

func TestMembersAreRemindedInTheirTimeZone(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	list := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Groceries"}})
	milk := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Milk"}, ParentID: list.ID})
	f.share(t, list.ID, "bob@example.com", recording.RoleEditor)

	if _, err := f.s.SetDue(ctx, f.bob, milk.ID, &recording.Due{Date: "2026-09-28", Time: "09:00", Remind: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	// 9:00 in Berlin for the owner, 9:00 in New York for bob.
	owner, _ := f.s.Get(ctx, f.acc, milk.ID)
	if owner.RemindAt == nil || !owner.RemindAt.Equal(time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("owner's reminder: %v", owner.RemindAt)
	}
	bob, _ := f.s.Get(ctx, f.bob, milk.ID)
	if bob.RemindAt == nil || !bob.RemindAt.Equal(time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("bob's reminder: %v", bob.RemindAt)
	}

	// The reminders go out to each of them once.
	n := NewNotificationService(memory.NewPushSubscriptions(), f.users, f.recs, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n.clock = func() time.Time { return time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC) }
	ownerMsgs, stopOwner, _ := n.Listen(f.acc)
	defer stopOwner()
	bobMsgs, stopBob, _ := n.Listen(f.bob)
	defer stopBob()
	if sent, err := n.SendDueReminders(ctx); err != nil || sent != 1 {
		t.Fatalf("owner reminders: %d %v", sent, err)
	}
	if sent, err := n.SendDueMemberReminders(ctx); err != nil || sent != 1 {
		t.Fatalf("member reminders: %d %v", sent, err)
	}
	if sent, _ := n.SendDueMemberReminders(ctx); sent != 0 {
		t.Fatalf("member reminded twice: %d", sent)
	}
	for who, c := range map[string]<-chan Message{"owner": ownerMsgs, "bob": bobMsgs} {
		select {
		case m := <-c:
			if m.Title != "Milk" {
				t.Errorf("%s got %+v", who, m)
			}
		default:
			t.Errorf("%s got no reminder", who)
		}
	}

	// Checking the task off drops everyone's reminder.
	if _, err := f.s.SetDue(ctx, f.acc, milk.ID, &recording.Due{Date: "2026-09-29", Remind: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetDone(ctx, f.acc, milk.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, f.bob, milk.ID); got.RemindAt != nil {
		t.Fatalf("bob's reminder after done: %v", got.RemindAt)
	}
}

func TestConcurrentChangesDontUndoEachOther(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	note := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Plan", Markdown: "v1"}})
	f.share(t, note.ID, "bob@example.com", recording.RoleEditor)

	// A copy read before another change can't be saved over it.
	stale, _ := f.recs.Get(ctx, note.ID)
	if _, err := f.s.SetPriority(ctx, f.bob, note.ID, 1); err != nil {
		t.Fatal(err)
	}
	stale.Summary.Title = "Stale"
	if err := f.recs.Update(ctx, stale); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale save: %v", err)
	}

	// Text edits made on the latest revision go through, and count the revision up …
	cur, _ := f.s.Get(ctx, f.acc, note.ID)
	rev := cur.Revision
	edited, err := f.s.EditSummary(ctx, f.acc, note.ID, SummaryEdit{Title: "Plan", Markdown: "alice", BaseRevision: &rev})
	if err != nil || edited.Revision != rev+1 || edited.Version <= cur.Version {
		t.Fatalf("edit: %v %+v", err, edited)
	}
	// … while an edit made on the older revision is refused rather than undoing it.
	if _, err := f.s.EditSummary(ctx, f.bob, note.ID, SummaryEdit{Title: "Plan", Markdown: "bob", BaseRevision: &rev}); !errors.Is(err, ErrChanged) {
		t.Fatalf("edit on an old revision: %v", err)
	}
	// Other changes (the priority, labels) don't count as a changed text.
	if _, err := f.s.SetPriority(ctx, f.acc, note.ID, 2); err != nil {
		t.Fatal(err)
	}
	rev = edited.Revision
	if _, err := f.s.EditSummary(ctx, f.bob, note.ID, SummaryEdit{Title: "Plan", Markdown: "bob", BaseRevision: &rev}); err != nil {
		t.Fatalf("edit after a priority change: %v", err)
	}
	// Without a base revision the edit is saved as before.
	if got, err := f.s.EditSummary(ctx, f.acc, note.ID, SummaryEdit{Title: "Plan", Markdown: "last"}); err != nil || got.Summary.Markdown != "last" || got.Priority != 2 {
		t.Fatalf("edit without base: %v %+v", err, got)
	}
}

func TestChangesArePublishedToEveryoneWhoSeesTheNote(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	list := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Groceries"}})
	milk := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Milk"}, ParentID: list.ID})
	f.share(t, list.ID, "bob@example.com", recording.RoleEditor)

	ownerEvents, stopOwner, _ := f.events.Listen(f.acc)
	defer stopOwner()
	bobEvents, stopBob, _ := f.events.Listen(f.bob)
	defer stopBob()
	carolEvents, stopCarol, _ := f.events.Listen(&Account{ID: "u3"})
	defer stopCarol()
	next := func(c <-chan NoteEvent) *NoteEvent {
		select {
		case e := <-c:
			return &e
		default:
			return nil
		}
	}
	drain := func(c <-chan NoteEvent) {
		for next(c) != nil {
		}
	}

	edited, err := f.s.EditSummary(ctx, f.bob, milk.ID, SummaryEdit{Title: "Oat milk"})
	if err != nil {
		t.Fatal(err)
	}
	for who, c := range map[string]<-chan NoteEvent{"owner": ownerEvents, "bob": bobEvents} {
		if e := next(c); e == nil || e.Type != NoteChanged || e.ID != milk.ID || e.Version != edited.Version {
			t.Errorf("%s got %+v", who, e)
		}
	}
	if e := next(carolEvents); e != nil {
		t.Errorf("carol got %+v", e)
	}

	// Taking bob off tells him, so his app drops the notes.
	drain(ownerEvents)
	if _, err := f.s.Unshare(ctx, f.acc, list.ID, f.bob.ID); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for e := next(bobEvents); e != nil; e = next(bobEvents) {
		got[e.ID] = true
	}
	if !got[list.ID] || !got[milk.ID] {
		t.Fatalf("bob's events after unsharing: %v", got)
	}

	// Deleting a note tells its audience.
	drain(ownerEvents)
	if err := f.s.Delete(ctx, f.acc, milk.ID); err != nil {
		t.Fatal(err)
	}
	if e := next(ownerEvents); e == nil || e.ID != milk.ID {
		t.Fatalf("delete event: %+v", e)
	}
}

func TestDeletingAUserTakesThemOffSharedNotes(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	list := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Groceries"}})
	f.share(t, list.ID, "bob@example.com", recording.RoleEditor)
	if err := f.recs.RemoveMember(ctx, f.bob.ID); err != nil {
		t.Fatal(err)
	}
	if sh, _ := f.s.Sharing(ctx, f.acc, list.ID); len(sh.Members) != 0 {
		t.Fatalf("members: %+v", sh.Members)
	}
}
