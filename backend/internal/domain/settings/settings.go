// Package settings holds configuration that administrators change at runtime in the web UI
// (as opposed to environment variables).
package settings

import "time"

// OpenRouter configures the AI processing of recordings through openrouter.ai.
type OpenRouter struct {
	APIKey             string    `bson:"apiKey"`
	TranscriptionModel string    `bson:"transcriptionModel"`
	SummaryModel       string    `bson:"summaryModel"`
	UpdatedAt          time.Time `bson:"updatedAt"`
}

// CanTranscribe reports whether transcription is configured.
func (o *OpenRouter) CanTranscribe() bool { return o.APIKey != "" && o.TranscriptionModel != "" }

// CanSummarize reports whether summarization is configured.
func (o *OpenRouter) CanSummarize() bool { return o.APIKey != "" && o.SummaryModel != "" }
