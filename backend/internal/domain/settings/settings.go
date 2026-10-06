// Package settings holds configuration that administrators change at runtime in the web UI
// (as opposed to environment variables).
package settings

import "time"

// Transcription providers: OpenRouter transcribes with the chosen audio model, ElevenLabs
// with its speech-to-text model.
const (
	ProviderOpenRouter = "openrouter"
	ProviderElevenLabs = "elevenlabs"
)

// OpenRouter configures the AI processing of recordings: through openrouter.ai, and
// optionally the transcription of audio through ElevenLabs.
type OpenRouter struct {
	APIKey             string `bson:"apiKey"`
	TranscriptionModel string `bson:"transcriptionModel"`
	// TranscriptionProvider transcribes audio: ProviderOpenRouter (also when empty) or
	// ProviderElevenLabs.
	TranscriptionProvider string `bson:"transcriptionProvider"`
	ElevenLabsAPIKey      string `bson:"elevenLabsApiKey"`
	SummaryModel          string `bson:"summaryModel"`
	// DocumentModel reads the pages of documents (images and PDFs); empty uses the
	// transcription model.
	DocumentModel string    `bson:"documentModel,omitempty"`
	UpdatedAt     time.Time `bson:"updatedAt"`
}

// Provider returns the transcription provider.
func (o *OpenRouter) Provider() string {
	if o.TranscriptionProvider == ProviderElevenLabs {
		return ProviderElevenLabs
	}
	return ProviderOpenRouter
}

// CanTranscribe reports whether transcription is configured.
func (o *OpenRouter) CanTranscribe() bool {
	if o.Provider() == ProviderElevenLabs {
		return o.ElevenLabsAPIKey != ""
	}
	return o.APIKey != "" && o.TranscriptionModel != ""
}

// CanReadDocuments reports whether documents can be read; they are always read through
// OpenRouter.
func (o *OpenRouter) CanReadDocuments() bool { return o.APIKey != "" && o.DocumentReader() != "" }

// DocumentReader returns the model that reads documents.
func (o *OpenRouter) DocumentReader() string {
	if o.DocumentModel != "" {
		return o.DocumentModel
	}
	return o.TranscriptionModel
}

// WebPush is the server's VAPID key pair for Web Push notifications, base64url-encoded. It
// is generated on first start; browsers subscribe with the public key, so it never changes.
type WebPush struct {
	PrivateKey string `bson:"privateKey"`
	PublicKey  string `bson:"publicKey"`
}

// CanSummarize reports whether summarization is configured.
func (o *OpenRouter) CanSummarize() bool { return o.APIKey != "" && o.SummaryModel != "" }

// Email configures outgoing email through Amazon SES. Like the other settings it is kept in
// the database, secret key included.
type Email struct {
	Region          string `bson:"region"`
	AccessKeyID     string `bson:"accessKeyId"`
	SecretAccessKey string `bson:"secretAccessKey"`
	// From is the sender address, which must be verified in SES.
	From      string    `bson:"from"`
	UpdatedAt time.Time `bson:"updatedAt"`
}

// Configured reports whether emails can be sent.
func (e *Email) Configured() bool {
	return e.Region != "" && e.AccessKeyID != "" && e.SecretAccessKey != "" && e.From != ""
}
