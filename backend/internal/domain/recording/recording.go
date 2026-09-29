// Package recording models the user's notes. Most notes are audio recordings pushed by a
// device (from the first uploaded byte to the archived FLAC file in object storage); text
// notes are written in the web UI and hold only a title and Markdown text.
package recording

import "time"

// Status is the lifecycle state of a recording.
type Status string

const (
	// StatusRemote: the audio is at an external source (e.g. Pocket) and waits to be fetched.
	StatusRemote Status = "remote"
	// StatusUploading: the device is still transferring the WAV file.
	StatusUploading Status = "uploading"
	// StatusReceived: the audio file is complete (uploaded and verified, or fetched); it
	// waits in the local spool for background processing.
	StatusReceived Status = "received"
	// StatusStored: the audio has been archived in object storage and waits to be transcribed.
	StatusStored Status = "stored"
	// StatusTranscribed: the transcript exists and the summary is pending.
	StatusTranscribed Status = "transcribed"
	// StatusSummarized: fully processed.
	StatusSummarized Status = "summarized"
	// StatusFailed: the recording was rejected or processing gave up after retries.
	StatusFailed Status = "failed"
)

// Type is the kind of note.
type Type string

const (
	// TypeAudio: an audio recording that is archived, transcribed and summarized (the
	// default; stored as empty).
	TypeAudio Type = ""
	// TypeText: a Markdown note written by the user. It has no audio, transcript or source;
	// its title and text are kept in Summary, so it is edited like a summary.
	TypeText Type = "text"
	// TypeDocument: a document read from the reMarkable cloud (a handwritten notebook, PDF
	// or EPUB), or a photo or PDF uploaded in the web app. File is its PDF, EPUB or image;
	// the text read from its pages is kept as the Transcript and summarized like one.
	TypeDocument Type = "document"
	// TypeBoard: a kanban board of the user's notes. Its title is kept in Summary like a
	// text note's; Board holds which notes it shows and its columns.
	TypeBoard Type = "board"
)

// Source says where a recording came from.
type Source string

const (
	// SourceDevice: uploaded by a registered recorder (the default; stored as empty).
	SourceDevice Source = ""
	// SourcePocket: announced by a Pocket (heypocketai.com) webhook and fetched from its API.
	SourcePocket Source = "pocket"
	// SourceUpload: a WAV or MP3 file uploaded in the web UI.
	SourceUpload Source = "upload"
	// SourceRemarkable: a document pulled from the user's reMarkable cloud.
	SourceRemarkable Source = "remarkable"
	// SourceRecorder: a voice memo recorded in the web app.
	SourceRecorder Source = "recorder"
	// SourceBriefing: a text note made as the user's daily briefing or weekly review.
	SourceBriefing Source = "briefing"
)

// PocketDeviceID returns the DeviceID of a user's Pocket recordings. Together with ClientID
// (the Pocket recording ID) it makes webhook deliveries idempotent.
func PocketDeviceID(userID string) string { return "pocket:" + userID }

// RemarkableDeviceID returns the DeviceID of a user's reMarkable documents. Their ClientID is
// the document's ID in the reMarkable cloud, so every document is imported once.
func RemarkableDeviceID(userID string) string { return "remarkable:" + userID }

// Format describes the PCM audio of the uploaded WAV file.
type Format struct {
	SampleRate    uint32 `bson:"sampleRate" json:"sampleRate"`
	Channels      uint16 `bson:"channels" json:"channels"`
	BitsPerSample uint16 `bson:"bitsPerSample" json:"bitsPerSample"`
	Frames        uint64 `bson:"frames" json:"frames"` // samples per channel
	DurationMs    int64  `bson:"durationMs" json:"durationMs"`
}

// Object references a file in object storage.
type Object struct {
	Key         string `bson:"key" json:"key"`
	ContentType string `bson:"contentType" json:"contentType"`
	Size        int64  `bson:"size" json:"size"`
}

// Recording is one audio recording of one device. The upload session and the recording are
// the same document: its ID doubles as the upload ID.
type Recording struct {
	ID string `bson:"_id" json:"id"`
	// OwnerID is the user the recording belongs to.
	OwnerID string `bson:"ownerId" json:"ownerId"`
	// Number identifies the note among its owner's notes (1, 2, 3, …), like an issue number;
	// "#12" in a note's text links to note 12. It is assigned when the note is created and
	// never reused.
	Number   int64  `bson:"number,omitempty" json:"number,omitempty"`
	DeviceID string `bson:"deviceId" json:"deviceId"`
	Type     Type   `bson:"type,omitempty" json:"type,omitempty"`
	Source   Source `bson:"source,omitempty" json:"source,omitempty"`
	Title    string `bson:"title,omitempty" json:"title,omitempty"`
	// ClientID is the recording ID assigned by the device. It is unique per device and makes
	// upload creation idempotent across retries.
	ClientID string `bson:"clientId" json:"recordingId"`
	Status   Status `bson:"status" json:"status"`
	Size     int64  `bson:"size" json:"size"`     // WAV size declared by the device, or bytes fetched
	SHA256   string `bson:"sha256" json:"sha256"` // hex SHA-256 declared by the device, or of the fetched file
	// SourceContentType is the media type of a fetched file (empty for device uploads, which
	// are always WAV).
	SourceContentType string     `bson:"sourceContentType,omitempty" json:"sourceContentType,omitempty"`
	RecordedAt        *time.Time `bson:"recordedAt,omitempty" json:"recordedAt,omitempty"`
	Format            *Format    `bson:"format,omitempty" json:"format,omitempty"`
	Audio             *Object    `bson:"audio,omitempty" json:"audio,omitempty"`       // archived FLAC
	Original          *Object    `bson:"original,omitempty" json:"original,omitempty"` // archived WAV, if kept; a document's files
	// File is a document's viewable file: a PDF (notebooks are rendered to one) or EPUB.
	File *Object `bson:"file,omitempty" json:"file,omitempty"`
	// Images are the pictures pasted or dropped into the note's text; the text refers to them
	// by their URL (see ImageID).
	Images []Object `bson:"images,omitempty" json:"-"`
	// Pages is the number of pages of a document.
	Pages int `bson:"pages,omitempty" json:"pages,omitempty"`
	// SourceRevision identifies the version of a document that was imported; a pull
	// imports the document again when it changed.
	SourceRevision string `bson:"sourceRevision,omitempty" json:"-"`

	// Labels are the IDs of the labels the user put on the note, in the order they were added.
	Labels []string `bson:"labels,omitempty" json:"labels,omitempty"`
	// Done is the check mark of a note labeled as a task; DoneAt is when it was checked off
	// (for a repeating task: when it was last checked off).
	Done   bool       `bson:"done,omitempty" json:"done,omitempty"`
	DoneAt *time.Time `bson:"doneAt,omitempty" json:"doneAt,omitempty"`
	// Due is when a task is due; Priority ranks it. Both belong to notes labeled as a task.
	Due      *Due     `bson:"due,omitempty" json:"due,omitempty"`
	Priority Priority `bson:"priority,omitempty" json:"priority,omitempty"`
	// RemindAt is when the task's next reminder is sent; nil when none is pending (no
	// reminder, done, or already sent).
	RemindAt *time.Time `bson:"remindAt,omitempty" json:"remindAt,omitempty"`
	// Estimate is how long the task is expected to take, in minutes; 0 is none.
	Estimate int `bson:"estimate,omitempty" json:"estimate,omitempty"`
	// TrackedSeconds is the time logged on the note with its timer (finished entries).
	TrackedSeconds int64 `bson:"trackedSeconds,omitempty" json:"trackedSeconds,omitempty"`
	// FolderID is the folder the note is in; empty at the top level.
	FolderID string `bson:"folderId,omitempty" json:"folderId,omitempty"`
	// ParentID is the note this one is a sub-note of, like a file in a folder whose head is a
	// note itself. A sub-note is listed under its parent and is in no folder of its own.
	ParentID string `bson:"parentId,omitempty" json:"parentId,omitempty"`
	// Position orders the note among the notes in the same place (folder or parent note),
	// from 1 up; 0 is unordered: those follow the ordered notes, by title.
	Position int `bson:"position,omitempty" json:"position,omitempty"`

	// CreatedBy is the user who made the note when it isn't its owner: someone the owner
	// shared the note it is under with. Empty for the owner's own notes.
	CreatedBy string `bson:"createdBy,omitempty" json:"createdBy,omitempty"`
	// Shares are the users the owner shared this note with directly, together with its
	// sub-notes.
	Shares []Share `bson:"shares,omitempty" json:"-"`
	// Members are everyone the note is shared with: the users of its own Shares and the
	// members of the note it is under. It is derived from the shares (see ComputeMembers)
	// and holds each member's own place, labels and reminder of the note.
	Members []Member `bson:"members,omitempty" json:"-"`
	// Access is what the user a note is shown to may do with it (only in responses).
	Access Role `bson:"-" json:"access,omitempty"`
	// Shared says the note is shared with anyone (only in responses).
	Shared bool `bson:"-" json:"shared,omitempty"`

	// Version counts the saves of the note. Saving a copy of an older version fails with
	// domain.ErrChanged, so that changes made at the same time never undo each other.
	Version int64 `bson:"version" json:"version"`
	// Revision counts the changes people made to the title and text (and regenerating
	// them). An edit made on an older revision is refused rather than undoing someone
	// else's edit.
	Revision int64 `bson:"revision,omitempty" json:"revision"`

	// Board is the setup of a board note: its scope and columns.
	Board *Board `bson:"board,omitempty" json:"board,omitempty"`

	// Tablet is the copy of a text note on its owner's reMarkable, sent while the note is in
	// the reMarkable folder; nil for notes never sent.
	Tablet *TabletCopy `bson:"tablet,omitempty" json:"tablet,omitempty"`

	// DeletedAt is when the note was moved to the trash; nil when it isn't in the trash.
	// Notes in the trash are left out of lists and deleted for good after TrashRetention.
	DeletedAt *time.Time `bson:"deletedAt,omitempty" json:"deletedAt,omitempty"`

	// Highlights are moments the user marked on the device while recording.
	Highlights     []Highlight    `bson:"highlights,omitempty" json:"highlights,omitempty"`
	Transcript     *Transcript    `bson:"transcript,omitempty" json:"transcript,omitempty"`
	Summary        *Summary       `bson:"summary,omitempty" json:"summary,omitempty"`
	SummaryOptions SummaryOptions `bson:"summaryOptions,omitempty" json:"summaryOptions"`

	// Background processing bookkeeping. NotBefore is both the retry backoff and the lease of
	// the worker currently processing the recording.
	Attempts  int       `bson:"attempts" json:"attempts"`
	NotBefore time.Time `bson:"notBefore" json:"-"`
	LastError string    `bson:"lastError,omitempty" json:"lastError,omitempty"`

	CreatedAt  time.Time  `bson:"createdAt" json:"createdAt"`
	UpdatedAt  time.Time  `bson:"updatedAt" json:"updatedAt"`
	ReceivedAt *time.Time `bson:"receivedAt,omitempty" json:"receivedAt,omitempty"`
	StoredAt   *time.Time `bson:"storedAt,omitempty" json:"storedAt,omitempty"`
}

// TabletCopy is a text note's copy on its owner's reMarkable: an EPUB of its title and
// text. It follows the note's changes while the note is in the reMarkable folder, and goes
// into the tablet's trash when the note leaves the folder or goes into the trash.
type TabletCopy struct {
	// DocumentID is the document in the reMarkable cloud.
	DocumentID string `bson:"documentId" json:"documentId"`
	// Revision, Name and Parent are what was sent last: the note's revision, the
	// document's name and its cloud folder ("trash" once taken off the tablet).
	Revision int64  `bson:"revision" json:"-"`
	Name     string `bson:"name" json:"-"`
	Parent   string `bson:"parent,omitempty" json:"-"`
	// Removed says the copy was taken off the tablet (it is in the tablet's trash).
	Removed bool       `bson:"removed,omitempty" json:"removed,omitempty"`
	SentAt  *time.Time `bson:"sentAt,omitempty" json:"sentAt,omitempty"`
	// Error is why the last send failed; it is tried again.
	Error string `bson:"error,omitempty" json:"error,omitempty"`
}

// IsText reports whether the note is a text note.
func (r *Recording) IsText() bool { return r.Type == TypeText }

// IsDocument reports whether the note is a document from the reMarkable cloud.
func (r *Recording) IsDocument() bool { return r.Type == TypeDocument }

// IsImage reports whether the note is a photo (a document whose file is an image).
func (r *Recording) IsImage() bool {
	return r.IsDocument() && r.File != nil && len(r.File.ContentType) > 6 && r.File.ContentType[:6] == "image/"
}

// IsBoard reports whether the note is a board.
func (r *Recording) IsBoard() bool { return r.Type == TypeBoard }

// KeepUserFields copies the fields a person changes at any time (labels, task fields, time
// estimate and log, folder, parent note, position, trash, sharing), the note number, the
// tablet copy and the version from the stored copy, so that a processing step saving its
// long-held copy doesn't undo them.
func (r *Recording) KeepUserFields(stored *Recording) {
	r.Labels, r.Done, r.DoneAt, r.FolderID, r.ParentID, r.Number = stored.Labels, stored.Done, stored.DoneAt, stored.FolderID, stored.ParentID, stored.Number
	r.Due, r.Priority, r.RemindAt = stored.Due, stored.Priority, stored.RemindAt
	r.Estimate, r.TrackedSeconds = stored.Estimate, stored.TrackedSeconds
	r.Position, r.DeletedAt = stored.Position, stored.DeletedAt
	r.Shares, r.Members, r.CreatedBy = stored.Shares, stored.Members, stored.CreatedBy
	r.Tablet = stored.Tablet
	r.Version, r.Revision = stored.Version, stored.Revision
}

// TextDeviceID returns the DeviceID of a user's text notes. Their ClientID is the note ID.
func TextDeviceID(userID string) string { return "text:" + userID }

// ScopeKind says what selects the notes shown on a board.
type ScopeKind string

const (
	// ScopeNone: the board shows no notes until a scope is chosen.
	ScopeNone ScopeKind = ""
	// ScopeFolder: the notes in a folder (ID "" is the top level).
	ScopeFolder ScopeKind = "folder"
	// ScopeLabel: the notes carrying a label.
	ScopeLabel ScopeKind = "label"
	// ScopeFilter: the notes matching one of the owner's saved filters.
	ScopeFilter ScopeKind = "filter"
)

// BoardScope selects the notes a board shows.
type BoardScope struct {
	Kind ScopeKind `bson:"kind,omitempty" json:"kind"`
	// ID is the folder, label or saved filter.
	ID string `bson:"id,omitempty" json:"id"`
}

// Board is a kanban board: the notes in its scope, sorted into columns. Notes in the scope
// that are in no column are shown in the first one.
type Board struct {
	Scope   BoardScope    `bson:"scope" json:"scope"`
	Columns []BoardColumn `bson:"columns" json:"columns"`
}

// BoardColumn is one column of a board, with the IDs of the notes put into it, in order.
type BoardColumn struct {
	ID    string   `bson:"id" json:"id"`
	Name  string   `bson:"name" json:"name"`
	Notes []string `bson:"notes,omitempty" json:"notes,omitempty"`
}

// BoardDeviceID returns the DeviceID of a user's boards. Their ClientID is the note ID.
func BoardDeviceID(userID string) string { return "board:" + userID }

// Highlight is a moment the user marked while recording (e.g. with a button on the device).
type Highlight struct {
	// OffsetMs is the position in the recording, in milliseconds from its start.
	OffsetMs int64 `bson:"offsetMs" json:"offsetMs"`
	// At is the wall-clock time, when the device reported one.
	At *time.Time `bson:"at,omitempty" json:"at,omitempty"`
}

// Transcript is the text of a recording, produced by a speech model.
type Transcript struct {
	Text      string    `bson:"text" json:"text"`
	Model     string    `bson:"model" json:"model"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}

// Summary is an AI summary of the transcript. Its Title names the conversation in the UI.
type Summary struct {
	Title     string `bson:"title" json:"title"`
	Markdown  string `bson:"markdown,omitempty" json:"markdown,omitempty"`
	Model     string `bson:"model" json:"model"`
	Language  string `bson:"language,omitempty" json:"language,omitempty"` // "auto" or a language tag
	ThemeID   string `bson:"themeId,omitempty" json:"themeId,omitempty"`
	ThemeName string `bson:"themeName,omitempty" json:"themeName,omitempty"`
	// EditedAt is set when a person changed the title or text; regenerating replaces the edits.
	EditedAt *time.Time `bson:"editedAt,omitempty" json:"editedAt,omitempty"`
	// ActionItems are the follow-ups found in the conversation, offered as tasks.
	ActionItems []ActionItem `bson:"actionItems,omitempty" json:"actionItems,omitempty"`
	// Speakers are the names the model recognized for the transcript's speaker labels
	// (e.g. "Speaker 1" is Anna), offered for renaming the speakers.
	Speakers  []SpeakerName `bson:"speakers,omitempty" json:"speakers,omitempty"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
}

// SpeakerName names the speaker with a label in the transcript.
type SpeakerName struct {
	Label string `bson:"label" json:"label"`
	Name  string `bson:"name" json:"name"`
}

// SummaryOptions choose how a recording is summarized. Empty fields use the defaults: the
// transcript's language, the configured summary model and the "auto" theme.
type SummaryOptions struct {
	Language string `bson:"language,omitempty" json:"language,omitempty"` // "auto" or e.g. "de-DE"
	Model    string `bson:"model,omitempty" json:"model,omitempty"`
	ThemeID  string `bson:"themeId,omitempty" json:"themeId,omitempty"`
}

// TrashRetention is how long notes stay in the trash before they are deleted for good.
const TrashRetention = 14 * 24 * time.Hour

// Trash selects notes by whether they are in the trash.
type Trash string

const (
	// TrashExclude leaves out the notes in the trash (the default).
	TrashExclude Trash = ""
	// TrashOnly lists only the notes in the trash.
	TrashOnly Trash = "only"
	// TrashAny lists the notes whether they are in the trash or not.
	TrashAny Trash = "any"
)

// ListFilter selects recordings for listing. Zero values mean "no restriction", except that
// notes in the trash are left out unless Trash says otherwise.
type ListFilter struct {
	OwnerID string
	// UserID selects the notes the user owns or is a member of.
	UserID string
	// ParentID selects the sub-notes of a note.
	ParentID string
	DeviceID string
	Status   Status
	// Number selects the owner's note with this number.
	Number int64
	Limit  int
	Offset int
	// Brief leaves out the transcript and the summary text (the summary title is kept),
	// for lists.
	Brief bool
	// Trash selects notes in or out of the trash.
	Trash Trash
}
