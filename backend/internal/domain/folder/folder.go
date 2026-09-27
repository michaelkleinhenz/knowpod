// Package folder models the folders users sort their notes into. Folders can hold other
// folders; a note is in at most one folder (or in none, at the top level).
package folder

import "time"

// Folder is a user's folder.
type Folder struct {
	ID      string `bson:"_id" json:"id"`
	OwnerID string `bson:"ownerId" json:"-"`
	Name    string `bson:"name" json:"name"`
	// ParentID is the folder this one is in; empty at the top level.
	ParentID string `bson:"parentId,omitempty" json:"parentId,omitempty"`
	// Position orders the folder among the folders in the same place, from 1 up; 0 is
	// unordered: those follow the ordered folders, by name.
	Position  int       `bson:"position,omitempty" json:"position,omitempty"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}
