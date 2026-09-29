package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// folderFixture is a share fixture where the owner has the folder Work with the folder
// Projects in it, and the folder Home; a note in each, with a sub-note under Work's.
type folderFixture struct {
	*shareFixture
	work, projects, home   *folder.Folder
	plan, step, idea, shop *recording.Recording
}

func newFolderFixture(t *testing.T) *folderFixture {
	t.Helper()
	f := &folderFixture{shareFixture: newShareFixture(t)}
	ctx := context.Background()
	mk := func(name, parent string) *folder.Folder {
		out, err := f.folders.Create(ctx, f.acc, FolderInput{Name: name, ParentID: parent})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	f.work = mk("Work", "")
	f.projects = mk("Projects", f.work.ID)
	f.home = mk("Home", "")
	f.plan = f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Plan"}, FolderID: f.work.ID})
	f.step = f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Step"}, ParentID: f.plan.ID})
	f.idea = f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Idea"}, FolderID: f.projects.ID})
	f.shop = f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Shop"}, FolderID: f.home.ID})
	return f
}

func (f *folderFixture) shareFolder(t *testing.T, id string, role recording.Role) *Sharing {
	t.Helper()
	out, err := f.s.ShareFolder(context.Background(), f.acc, id, ShareInput{Email: "bob@example.com", Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *folderFixture) bobSees(t *testing.T) []string {
	t.Helper()
	got, err := f.s.List(context.Background(), f.bob, recording.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return ids(got)
}

func (f *folderFixture) bobsFolders(t *testing.T) map[string]*folder.Folder {
	t.Helper()
	list, err := f.folders.List(context.Background(), f.bob)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*folder.Folder{}
	for _, x := range list {
		out[x.ID] = x
	}
	return out
}

func TestSharingAFolderSharesEverythingInIt(t *testing.T) {
	f := newFolderFixture(t)
	ctx := context.Background()
	if got := f.bobSees(t); len(got) != 0 {
		t.Fatalf("bob sees %v before sharing", got)
	}

	sh := f.shareFolder(t, f.work.ID, recording.RoleViewer)
	if len(sh.Members) != 1 || sh.Members[0].Email != "bob@example.com" || sh.Members[0].Inherited || sh.Access != recording.RoleOwner {
		t.Fatalf("sharing: %+v", sh)
	}
	// Bob sees the notes in the folder and the folder in it, with the notes under them.
	if got, want := f.bobSees(t), ids([]*recording.Recording{f.plan, f.step, f.idea}); !slices.Equal(got, want) {
		t.Fatalf("bob sees %v, want %v", got, want)
	}
	// … each in its folder, as a viewer.
	if got, _ := f.s.Get(ctx, f.bob, f.idea.ID); got.FolderID != f.projects.ID || got.Access != recording.RoleViewer || !got.Shared {
		t.Errorf("idea for bob: %+v", got)
	}
	// Bob has the folders, Work at his top level and Projects in it.
	folders := f.bobsFolders(t)
	if len(folders) != 2 || folders[f.work.ID] == nil || folders[f.work.ID].ParentID != "" || folders[f.projects.ID].ParentID != f.work.ID ||
		folders[f.work.ID].Access != recording.RoleViewer || !folders[f.projects.ID].Shared {
		t.Fatalf("bob's folders: %+v", folders)
	}
	// The owner sees Work and Projects as shared, Home not.
	own, _ := f.folders.List(ctx, f.acc)
	for _, x := range own {
		if x.Access != recording.RoleOwner || x.Shared != (x.ID != f.home.ID) {
			t.Errorf("owner's %s: access %q shared %v", x.Name, x.Access, x.Shared)
		}
	}
	// The folder in it is shared through it.
	if sh, _ := f.s.FolderSharing(ctx, f.bob, f.projects.ID); len(sh.Members) != 1 || !sh.Members[0].Inherited || sh.Access != recording.RoleViewer {
		t.Fatalf("inner folder's sharing: %+v", sh)
	}
	if _, err := f.s.FolderSharing(ctx, f.bob, f.home.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("unshared folder's sharing: %v", err)
	}

	// A new note in the folder is shared, too.
	late := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Late"}, FolderID: f.projects.ID})
	if _, err := f.s.Get(ctx, f.bob, late.ID); err != nil {
		t.Fatalf("new note: %v", err)
	}
	// Moved out, a note isn't shared any more; moved back, it is.
	if _, err := f.s.SetFolder(ctx, f.acc, late.ID, f.home.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.bob, late.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("moved out: %v", err)
	}
	if _, err := f.s.SetFolder(ctx, f.acc, late.ID, f.work.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.bob, late.ID); err != nil {
		t.Fatalf("moved back: %v", err)
	}

	// A folder moved out of the shared folder isn't shared any more, with its notes.
	if _, err := f.folders.Update(ctx, f.acc, f.projects.ID, FolderInput{Name: "Projects"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.bob, f.idea.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("folder moved out: %v", err)
	}
	if folders := f.bobsFolders(t); folders[f.projects.ID] != nil {
		t.Fatalf("bob still has the moved folder")
	}
	// Moved into it, the folder Home is shared, too.
	if _, err := f.folders.Update(ctx, f.acc, f.home.ID, FolderInput{Name: "Home", ParentID: f.work.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(ctx, f.bob, f.shop.ID); err != nil {
		t.Fatalf("folder moved in: %v", err)
	}

	// Deleting the shared folder moves its notes up to the top level, no longer shared.
	if err := f.folders.Delete(ctx, f.acc, f.work.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.bobSees(t); len(got) != 0 {
		t.Fatalf("bob sees %v after the folder was deleted", got)
	}
	if folders := f.bobsFolders(t); len(folders) != 0 {
		t.Fatalf("bob's folders after the folder was deleted: %+v", folders)
	}
}

func TestEditorsWorkInASharedFolder(t *testing.T) {
	f := newFolderFixture(t)
	ctx := context.Background()
	f.shareFolder(t, f.work.ID, recording.RoleViewer)

	// Viewers read only.
	if _, err := f.s.CreateText(ctx, f.bob, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Mine"}, FolderID: f.work.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer adds a note: %v", err)
	}
	if _, err := f.s.EditSummary(ctx, f.bob, f.plan.ID, SummaryEdit{Title: "Changed"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer edits: %v", err)
	}

	// Made an editor, bob adds notes to the folder; they are the owner's, shared like it.
	sh, err := f.s.SetFolderShareRole(ctx, f.acc, f.work.ID, "u2", recording.RoleEditor)
	if err != nil || sh.Members[0].Role != recording.RoleEditor {
		t.Fatalf("role: %+v, %v", sh, err)
	}
	added, err := f.s.CreateText(ctx, f.bob, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Mine"}, FolderID: f.projects.ID})
	if err != nil {
		t.Fatal(err)
	}
	if added.OwnerID != "u1" || added.CreatedBy != "u2" || added.FolderID != f.projects.ID || added.Access != recording.RoleEditor {
		t.Fatalf("added: %+v", added)
	}
	if got, err := f.s.Get(ctx, f.acc, added.ID); err != nil || got.FolderID != f.projects.ID {
		t.Fatalf("owner's view of it: %+v, %v", got, err)
	}
	if _, err := f.s.EditSummary(ctx, f.bob, f.plan.ID, SummaryEdit{Title: "Changed"}); err != nil {
		t.Fatalf("editor edits: %v", err)
	}
	// Bob moves the notes between the folders shared with him, but not out of them.
	if got, err := f.s.SetFolder(ctx, f.bob, f.plan.ID, f.projects.ID); err != nil || got.FolderID != f.projects.ID {
		t.Fatalf("move within: %+v, %v", got, err)
	}
	own, err := f.folders.Create(ctx, f.bob, FolderInput{Name: "Bob's"})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{own.ID, "", f.home.ID} {
		if _, err := f.s.SetFolder(ctx, f.bob, f.plan.ID, to); !errors.Is(err, ErrForbidden) {
			t.Errorf("move out to %q: %v", to, err)
		}
	}
	// Folders stay the owner's to change.
	if _, err := f.folders.Update(ctx, f.bob, f.work.ID, FolderInput{Name: "Mine"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename: %v", err)
	}
	if err := f.folders.Delete(ctx, f.bob, f.work.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	if _, err := f.s.ShareFolder(ctx, f.bob, f.work.ID, ShareInput{Email: "carol@example.com", Role: recording.RoleViewer}); !errors.Is(err, ErrForbidden) {
		t.Errorf("editor shares: %v", err)
	}
}

func TestLeavingAndUnsharingAFolder(t *testing.T) {
	f := newFolderFixture(t)
	ctx := context.Background()
	f.shareFolder(t, f.work.ID, recording.RoleEditor)
	msgs, stop, err := f.events.Listen(f.bob)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	for _, in := range []ShareInput{{Email: "a@example.com", Role: recording.RoleViewer}, {Email: "nobody@example.com", Role: recording.RoleViewer},
		{Email: "bob@example.com", Role: recording.RoleOwner}} {
		if _, err := f.s.ShareFolder(ctx, f.acc, f.work.ID, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	// The folder in the shared one is shared through it: it is left and unshared there.
	if _, err := f.s.UnshareFolder(ctx, f.bob, f.projects.ID, "u2"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("leave the inner folder: %v", err)
	}
	if _, err := f.s.SetFolderShareRole(ctx, f.acc, f.projects.ID, "u2", recording.RoleViewer); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("role on the inner folder: %v", err)
	}

	// Bob leaves: he no longer sees the folder or its notes, and his app reloads them.
	if out, err := f.s.UnshareFolder(ctx, f.bob, f.work.ID, "u2"); err != nil || out != nil {
		t.Fatalf("leave: %+v, %v", out, err)
	}
	if got := f.bobSees(t); len(got) != 0 {
		t.Fatalf("bob sees %v after leaving", got)
	}
	if folders := f.bobsFolders(t); len(folders) != 0 {
		t.Fatalf("bob's folders after leaving: %+v", folders)
	}
	reloaded := false
	for len(msgs) > 0 {
		if m := <-msgs; m.Type == NotesReload {
			reloaded = true
		}
	}
	if !reloaded {
		t.Error("bob's app wasn't told to reload")
	}

	// A note shared by itself stays shared when its folder is no longer.
	f.shareFolder(t, f.work.ID, recording.RoleViewer)
	f.share(t, f.plan.ID, "bob@example.com", recording.RoleViewer)
	if _, err := f.s.UnshareFolder(ctx, f.acc, f.work.ID, "u2"); err != nil {
		t.Fatal(err)
	}
	if got, want := f.bobSees(t), ids([]*recording.Recording{f.plan, f.step}); !slices.Equal(got, want) {
		t.Fatalf("bob sees %v, want %v", got, want)
	}
	if got, _ := f.s.Get(ctx, f.bob, f.plan.ID); got.FolderID != "" {
		t.Errorf("a note shared by itself is in bob's folder %q", got.FolderID)
	}
}

func TestFilingASharedFolderInOwnFolders(t *testing.T) {
	f := newFolderFixture(t)
	ctx := context.Background()
	f.shareFolder(t, f.work.ID, recording.RoleViewer)
	mine, err := f.folders.Create(ctx, f.bob, FolderInput{Name: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.folders.Create(ctx, f.bob, FolderInput{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}

	if !f.bobsFolders(t)[f.work.ID].Movable {
		t.Fatal("a shared folder at the top level is not movable")
	}
	if f.bobsFolders(t)[f.projects.ID].Movable {
		t.Fatal("a folder inside a shared folder is movable")
	}

	// Into one of bob's folders; only bob sees it there.
	got, err := f.folders.Update(ctx, f.bob, f.work.ID, FolderInput{ParentID: mine.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentID != mine.ID || got.Access != recording.RoleViewer {
		t.Fatalf("got parent %q access %q", got.ParentID, got.Access)
	}
	if p := f.bobsFolders(t)[f.work.ID].ParentID; p != mine.ID {
		t.Fatalf("bob sees the folder in %q", p)
	}
	own, _ := f.folders.List(ctx, f.acc)
	for _, x := range own {
		if x.ID == f.work.ID && x.ParentID != "" {
			t.Fatalf("the owner's folder moved to %q", x.ParentID)
		}
	}
	if !f.bobsFolders(t)[f.projects.ID].Shared || f.bobsFolders(t)[f.projects.ID].ParentID != f.work.ID {
		t.Fatal("sub-folder no longer under the shared folder")
	}

	// Not into someone else's folder, nor a folder that is not visible; not renamed.
	if _, err := f.folders.Update(ctx, f.bob, f.work.ID, FolderInput{ParentID: f.home.ID}); err == nil {
		t.Fatal("filed into the owner's folder")
	}
	if _, err := f.folders.Update(ctx, f.bob, f.projects.ID, FolderInput{ParentID: mine.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("moved a folder inside a shared one: %v", err)
	}
	if _, err := f.folders.Update(ctx, f.bob, f.home.ID, FolderInput{ParentID: mine.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("moved an unshared folder: %v", err)
	}

	// Ordered among bob's folders.
	if _, err := f.folders.Update(ctx, f.bob, f.work.ID, FolderInput{}); err != nil {
		t.Fatal(err)
	}
	if err := f.folders.Reorder(ctx, f.bob, []string{f.work.ID, mine.ID, other.ID}); err != nil {
		t.Fatal(err)
	}
	m := f.bobsFolders(t)
	if m[f.work.ID].Position != 1 || m[mine.ID].Position != 2 || m[other.ID].Position != 3 {
		t.Fatalf("positions %d %d %d", m[f.work.ID].Position, m[mine.ID].Position, m[other.ID].Position)
	}
	if err := f.folders.Reorder(ctx, f.bob, []string{f.work.ID, f.projects.ID}); err == nil {
		t.Fatal("ordered a folder inside a shared one")
	}
}
