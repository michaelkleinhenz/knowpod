// Package label models labels that users put on their notes, e.g. "task" or their own
// "work" or "ideas". The built-in labels exist for everyone; users can add their own.
package label

import "time"

// Task is the ID of the built-in "task" label. Notes with it can be checked off.
const Task = "task"

// Label is a user's own label.
type Label struct {
	ID      string `bson:"_id" json:"id"`
	OwnerID string `bson:"ownerId" json:"-"`
	Name    string `bson:"name" json:"name"`
	// Color is a CSS hex color, e.g. "#2f6f5e".
	Color     string    `bson:"color" json:"color"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}
