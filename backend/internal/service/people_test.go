package service

import (
	"context"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

func TestPeopleAreTheUsersOneSharesWith(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	emails := func(acc *Account) []string {
		t.Helper()
		list, err := f.s.People(ctx, acc)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, p := range list {
			if p.Self != (p.UserID == acc.ID) {
				t.Errorf("%+v: self %v", p, p.Self)
			}
			out = append(out, p.Email)
		}
		return out
	}
	if got := emails(f.bob); len(got) != 1 || got[0] != "bob@example.com" {
		t.Fatalf("bob alone: %v", got)
	}

	note := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Plan"}})
	f.share(t, note.ID, "bob@example.com", recording.RoleEditor)
	dir, err := f.folders.Create(ctx, f.acc, FolderInput{Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ShareFolder(ctx, f.acc, dir.ID, ShareInput{Email: "carol@example.com", Role: recording.RoleViewer}); err != nil {
		t.Fatal(err)
	}

	if got := emails(f.acc); len(got) != 3 || got[0] != "a@example.com" || got[1] != "bob@example.com" || got[2] != "carol@example.com" {
		t.Errorf("owner's people: %v", got)
	}
	if got := emails(f.bob); len(got) != 2 || got[0] != "bob@example.com" || got[1] != "a@example.com" {
		t.Errorf("bob's people: %v", got)
	}
	if got := emails(&Account{ID: "u3"}); len(got) != 2 || got[1] != "a@example.com" {
		t.Errorf("carol's people: %v", got)
	}
}
