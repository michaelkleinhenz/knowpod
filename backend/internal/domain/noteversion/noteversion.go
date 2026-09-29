// Package noteversion models the earlier versions of notes' titles and texts, kept so that
// a person can look at them and restore one.
package noteversion

import "time"

// Version is a note's title and text as they were before a change replaced them.
type Version struct {
	ID      string `bson:"_id" json:"id"`
	NoteID  string `bson:"noteId" json:"noteId"`
	OwnerID string `bson:"ownerId" json:"-"`
	// Revision is the note's revision the text had (see recording.Recording.Revision).
	Revision int64  `bson:"revision" json:"revision"`
	Title    string `bson:"title" json:"title"`
	// Markdown is left out of lists (see ports.NoteVersionRepository.List).
	Markdown string `bson:"markdown" json:"markdown,omitempty"`
	// SavedAt is when this text was saved: when it was last edited, or else made.
	SavedAt time.Time `bson:"savedAt" json:"savedAt"`
	// CreatedAt is when the version was kept: when a change replaced it.
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// Reason says what replaced it: "edit", "restore" or "regenerate".
	Reason string `bson:"reason" json:"reason"`
}

// Reasons a version was kept.
const (
	ReasonEdit       = "edit"
	ReasonRestore    = "restore"
	ReasonRegenerate = "regenerate"
)
