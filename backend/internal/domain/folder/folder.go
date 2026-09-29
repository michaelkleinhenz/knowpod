// Package folder models the folders users sort their notes into. Folders can hold other
// folders; a note is in at most one folder (or in none, at the top level). A folder can be
// shared with other users, who then see it and everything in it.
package folder

import (
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// Folder is a user's folder.
type Folder struct {
	ID      string `bson:"_id" json:"id"`
	OwnerID string `bson:"ownerId" json:"-"`
	Name    string `bson:"name" json:"name"`
	// ParentID is the folder this one is in; empty at the top level.
	ParentID string `bson:"parentId,omitempty" json:"parentId,omitempty"`
	// Position orders the folder among the folders in the same place, from 1 up; 0 is
	// unordered: those follow the ordered folders, by name.
	Position int `bson:"position,omitempty" json:"position,omitempty"`
	// Shares are the users the owner shared this folder with, together with everything in
	// it: its notes (and their sub-notes) and its folders.
	Shares []recording.Share `bson:"shares,omitempty" json:"-"`
	// Placements are where the users the folder is shared with filed it in their own tree
	// (see Placement).
	Placements []Placement `bson:"placements,omitempty" json:"-"`
	// Movable says the user the folder is shown to may file it elsewhere in their own tree:
	// a folder shared with them that is not in another shared folder (only in responses).
	Movable bool `bson:"-" json:"movable,omitempty"`
	// Access is what the user the folder is shown to may do with the notes in it (only in
	// responses): "owner", or the role it is shared with them for.
	Access recording.Role `bson:"-" json:"access,omitempty"`
	// Shared says the folder, or one it is in, is shared with anyone (only in responses).
	Shared bool `bson:"-" json:"shared,omitempty"`
	// Remarkable says the folder is where the owner's paired reMarkable's documents are,
	// so it can't be deleted (only in responses).
	Remarkable bool      `bson:"-" json:"remarkable,omitempty"`
	CreatedAt  time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt  time.Time `bson:"updatedAt" json:"updatedAt"`
}

// Share returns the folder's own share with a user, or nil.
func (f *Folder) Share(userID string) *recording.Share {
	for i := range f.Shares {
		if f.Shares[i].UserID == userID {
			return &f.Shares[i]
		}
	}
	return nil
}

// Placement is where a user the folder is shared with keeps it in their own folder tree:
// in one of their own folders (empty is their top level), at a position among the folders
// there. The folder itself stays where its owner put it.
type Placement struct {
	UserID   string `bson:"userId"`
	ParentID string `bson:"parentId,omitempty"`
	Position int    `bson:"position,omitempty"`
}

// Placement returns the user's placement of the folder, or nil.
func (f *Folder) Placement(userID string) *Placement {
	for i := range f.Placements {
		if f.Placements[i].UserID == userID {
			return &f.Placements[i]
		}
	}
	return nil
}

// SetPlacement stores where the user keeps the folder; the default place (top level,
// unordered) stores nothing.
func (f *Folder) SetPlacement(userID, parentID string, position int) {
	kept := f.Placements[:0]
	for _, p := range f.Placements {
		if p.UserID != userID {
			kept = append(kept, p)
		}
	}
	f.Placements = kept
	if parentID != "" || position != 0 {
		f.Placements = append(f.Placements, Placement{UserID: userID, ParentID: parentID, Position: position})
	}
}
