// Package filter models saved searches: a name and a query in the web app's filter language
// (e.g. "label:Task & due:week & !done"). Filters can be pinned to the notes list and
// chosen as the source of a board.
package filter

import "time"

// Filter is a user's saved search.
type Filter struct {
	ID      string `bson:"_id" json:"id"`
	OwnerID string `bson:"ownerId" json:"-"`
	Name    string `bson:"name" json:"name"`
	// Query selects the notes, in the filter language the web app evaluates.
	Query string `bson:"query" json:"query"`
	// Pinned shows the filter in the notes list.
	Pinned    bool      `bson:"pinned,omitempty" json:"pinned"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}
