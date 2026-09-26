// Package theme models summary themes: instructions that define how a recording's summary
// is structured (e.g. meeting notes, call notes). Built-in themes exist for everyone; users
// can add their own.
package theme

import "time"

// Theme is a user's own summary theme.
type Theme struct {
	ID      string `bson:"_id" json:"id"`
	OwnerID string `bson:"ownerId" json:"-"`
	Name    string `bson:"name" json:"name"`
	// Description is a short subtitle, e.g. "Topic · Agreement · Conclusion".
	Description string `bson:"description" json:"description"`
	// Instructions tell the model how to structure the summary (Markdown).
	Instructions string    `bson:"instructions" json:"instructions"`
	CreatedAt    time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt    time.Time `bson:"updatedAt" json:"updatedAt"`
}
