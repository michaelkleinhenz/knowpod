// Package recording models an audio recording pushed by a device, from the first uploaded
// byte to the archived FLAC file in object storage.
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
	// StatusStored: the audio has been transcoded to FLAC and archived in object storage.
	StatusStored Status = "stored"
	// StatusFailed: the recording was rejected or processing gave up after retries.
	StatusFailed Status = "failed"
)

// Source says where a recording came from.
type Source string

const (
	// SourceDevice: uploaded by a registered recorder (the default; stored as empty).
	SourceDevice Source = ""
	// SourcePocket: announced by a Pocket (heypocketai.com) webhook and fetched from its API.
	SourcePocket Source = "pocket"
)

// PocketDeviceID is the DeviceID of recordings from Pocket. Together with ClientID (the
// Pocket recording ID) it makes webhook deliveries idempotent.
const PocketDeviceID = "pocket"

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
	ID       string `bson:"_id" json:"id"`
	DeviceID string `bson:"deviceId" json:"deviceId"`
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

// ListFilter selects recordings for listing. Zero values mean "no restriction".
type ListFilter struct {
	DeviceID string
	Status   Status
	Limit    int
	Offset   int
}
