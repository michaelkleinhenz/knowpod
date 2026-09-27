package recording

import (
	"slices"
	"strings"
	"time"
)

// Role is what a user may do with a note.
type Role string

const (
	// RoleViewer reads the note and its sub-notes, and files them for themselves (folder,
	// labels, order).
	RoleViewer Role = "viewer"
	// RoleEditor also changes them: title and text, the task fields, and the sub-notes
	// under them.
	RoleEditor Role = "editor"
	// RoleOwner is the note's owner (never stored on a share).
	RoleOwner Role = "owner"
)

// Valid reports whether a note can be shared with this role.
func (r Role) Valid() bool { return r == RoleViewer || r == RoleEditor }

func (r Role) rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleEditor:
		return 2
	case RoleOwner:
		return 3
	}
	return 0
}

// AtLeast reports whether r allows everything o does.
func (r Role) AtLeast(o Role) bool { return r.rank() >= o.rank() }

// Share gives a user access to a note and everything under it.
type Share struct {
	UserID    string    `bson:"userId" json:"userId"`
	Role      Role      `bson:"role" json:"role"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}

// Member is a user a note is shared with, and that user's own view of it.
type Member struct {
	UserID string `bson:"userId" json:"userId"`
	Role   Role   `bson:"role" json:"role"`
	// Root says the member doesn't have the note it is under: it is shared with them by
	// itself, so they see it in a folder of their own rather than under its parent.
	Root bool `bson:"root,omitempty" json:"root,omitempty"`

	// FolderID is the member's folder the note is in (only for a Root note); empty is the
	// top level.
	FolderID string `bson:"folderId,omitempty" json:"folderId,omitempty"`
	// Labels are the member's own labels on the note (the built-in labels are the note's).
	Labels []string `bson:"labels,omitempty" json:"labels,omitempty"`
	// Position orders a Root note among the member's notes in its folder, like
	// Recording.Position.
	Position int `bson:"position,omitempty" json:"position,omitempty"`
	// RemindAt is when the member's next reminder of the task is sent, in the member's time
	// zone; nil when none is pending.
	RemindAt *time.Time `bson:"remindAt,omitempty" json:"remindAt,omitempty"`
}

// Member returns the member entry of a user, or nil when the note isn't shared with them.
func (r *Recording) Member(userID string) *Member {
	if userID == "" {
		return nil
	}
	for i := range r.Members {
		if r.Members[i].UserID == userID {
			return &r.Members[i]
		}
	}
	return nil
}

// Share returns the note's own share with a user, or nil.
func (r *Recording) Share(userID string) *Share {
	for i := range r.Shares {
		if r.Shares[i].UserID == userID {
			return &r.Shares[i]
		}
	}
	return nil
}

// Audience returns the users who see the note: its owner and its members.
func (r *Recording) Audience() []string {
	out := make([]string, 0, len(r.Members)+1)
	if r.OwnerID != "" {
		out = append(out, r.OwnerID)
	}
	for _, m := range r.Members {
		out = append(out, m.UserID)
	}
	return out
}

// ComputeMembers derives the members of a note from those of the note it is under
// (parent), and its own shares. A user gets the higher of the two roles. What each member
// set for themselves is taken over from the current members (old).
func ComputeMembers(parent []Member, shares []Share, old []Member) []Member {
	byUser := map[string]*Member{}
	for _, p := range parent {
		byUser[p.UserID] = &Member{UserID: p.UserID, Role: p.Role}
	}
	for _, s := range shares {
		if m, ok := byUser[s.UserID]; ok {
			if !m.Role.AtLeast(s.Role) {
				m.Role = s.Role
			}
			continue
		}
		byUser[s.UserID] = &Member{UserID: s.UserID, Role: s.Role, Root: true}
	}
	out := make([]Member, 0, len(byUser))
	for _, m := range byUser {
		for _, o := range old {
			if o.UserID == m.UserID {
				m.Labels, m.RemindAt = o.Labels, o.RemindAt
				if m.Root && o.Root {
					m.FolderID, m.Position = o.FolderID, o.Position
				}
			}
		}
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b Member) int { return strings.Compare(a.UserID, b.UserID) })
	if len(out) == 0 {
		return nil
	}
	return out
}

// SameMembers reports whether two member lists give the same users the same roles and
// roots (what ComputeMembers decides).
func SameMembers(a, b []Member) bool {
	return slices.EqualFunc(a, b, func(x, y Member) bool {
		return x.UserID == y.UserID && x.Role == y.Role && x.Root == y.Root
	})
}
