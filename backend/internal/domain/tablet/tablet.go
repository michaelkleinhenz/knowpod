// Package tablet models a user's link to the reMarkable cloud: the pairing and what the last
// pull saw. Documents are only read from the cloud, never written.
package tablet

import (
	"strings"
	"time"
)

// Link is a user's paired reMarkable account.
type Link struct {
	UserID string `bson:"_id"`
	// DeviceToken is the long-lived credential from pairing. It is never shown.
	DeviceToken string    `bson:"deviceToken"`
	PairedAt    time.Time `bson:"pairedAt"`

	// RootHash is the cloud's root at the last complete pull; Items caches the documents
	// and folders seen then, so an unchanged account costs one request per pull and only
	// changed items are read again.
	RootHash string `bson:"rootHash,omitempty"`
	Items    []Item `bson:"items,omitempty"`

	// FolderID is the knowpod folder new notes are put into (see the service).
	FolderID string `bson:"folderId,omitempty"`
	// Folders maps the cloud's folders (by ID) to the knowpod folders mirroring them inside
	// FolderID. MirroredHash is the root the folders and notes were last all placed for.
	Folders      map[string]string `bson:"folders,omitempty"`
	MirroredHash string            `bson:"mirroredHash,omitempty"`

	// IgnoredNames are the names of documents that aren't imported (compared without case).
	IgnoredNames []string `bson:"ignoredNames,omitempty"`

	LastPullAt *time.Time  `bson:"lastPullAt,omitempty"`
	LastError  string      `bson:"lastError,omitempty"`
	LastResult *PullResult `bson:"lastResult,omitempty"`
}

// Item is a document or folder in the reMarkable cloud.
type Item struct {
	ID          string     `bson:"id"`
	Hash        string     `bson:"hash"`        // changes with any change, including renames
	ContentHash string     `bson:"contentHash"` // changes only when the content changes
	Name        string     `bson:"name"`
	Parent      string     `bson:"parent,omitempty"`
	Folder      bool       `bson:"folder,omitempty"`
	Deleted     bool       `bson:"deleted,omitempty"`
	CreatedAt   *time.Time `bson:"createdAt,omitempty"`
	ModifiedAt  *time.Time `bson:"modifiedAt,omitempty"`
}

// PullResult says what a pull found.
type PullResult struct {
	Documents int `bson:"documents" json:"documents"` // documents in the account (not in the trash)
	Imported  int `bson:"imported" json:"imported"`   // new notes
	Updated   int `bson:"updated" json:"updated"`     // notes queued again because the document changed
	Ignored   int `bson:"ignored" json:"ignored"`     // documents left out by name
}

// Ignores reports whether documents of the name are left out.
func (l *Link) Ignores(name string) bool {
	name = NormalizeName(name)
	for _, n := range l.IgnoredNames {
		if strings.EqualFold(NormalizeName(n), name) {
			return true
		}
	}
	return false
}

// NormalizeName trims a document name and collapses its runs of white space.
func NormalizeName(name string) string { return strings.Join(strings.Fields(name), " ") }
