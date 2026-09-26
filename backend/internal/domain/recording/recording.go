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
)

// PocketDeviceID returns the DeviceID of a user's Pocket recordings. Together with ClientID
// (the Pocket recording ID) it makes webhook deliveries idempotent.
func PocketDeviceID(userID string) string { return "pocket:" + userID }

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
	OwnerID  string `bson:"ownerId" json:"ownerId"`
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
	Original          *Object    `bson:"original,omitempty" json:"original,omitempty"` // archived WAV, if kept

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

// IsText reports whether the note is a text note.
func (r *Recording) IsText() bool { return r.Type == TypeText }

// TextDeviceID returns the DeviceID of a user's text notes. Their ClientID is the note ID.
func TextDeviceID(userID string) string { return "text:" + userID }

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
	EditedAt  *time.Time `bson:"editedAt,omitempty" json:"editedAt,omitempty"`
	CreatedAt time.Time  `bson:"createdAt" json:"createdAt"`
}

// SummaryOptions choose how a recording is summarized. Empty fields use the defaults: the
// transcript's language, the configured summary model and the "auto" theme.
type SummaryOptions struct {
	Language string `bson:"language,omitempty" json:"language,omitempty"` // "auto" or e.g. "de-DE"
	Model    string `bson:"model,omitempty" json:"model,omitempty"`
	ThemeID  string `bson:"themeId,omitempty" json:"themeId,omitempty"`
}

// ListFilter selects recordings for listing. Zero values mean "no restriction".
type ListFilter struct {
	OwnerID  string
	DeviceID string
	Status   Status
	Limit    int
	Offset   int
	// Brief leaves out the transcript and the summary text (the summary title is kept),
	// for lists.
	Brief bool
}
