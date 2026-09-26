// Package openrouter calls the OpenRouter API (openrouter.ai), which gives access to many
// AI models through one OpenAI-compatible interface.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the OpenRouter API.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// ErrUnauthorized is returned when OpenRouter rejects the API key.
var ErrUnauthorized = errors.New("OpenRouter rejected the API key")

// Client calls OpenRouter. The API key is passed per call because it is stored in the
// settings, which administrators can change at any time.
type Client struct {
	baseURL string
	http    *http.Client
	appURL  string
}

// NewClient builds a client. appURL identifies this service to OpenRouter (optional).
func NewClient(baseURL, appURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{}, appURL: appURL}
}

// Model describes an available model.
type Model struct {
	ID               string
	Name             string
	ContextLength    int
	InputModalities  []string
	OutputModalities []string
	PromptPrice      string // USD per input token
	CompletionPrice  string // USD per output token
	AudioPrice       string // USD per audio input token, if priced separately
}

// Accepts reports whether the model takes the modality as input.
func (m Model) Accepts(modality string) bool { return contains(m.InputModalities, modality) }

// Produces reports whether the model outputs the modality.
func (m Model) Produces(modality string) bool { return contains(m.OutputModalities, modality) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Models lists the models OpenRouter offers (GET /models; no API key needed).
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter models: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("openrouter models: HTTP %d: %s", res.StatusCode, body)
	}
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Architecture  struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
				Audio      string `json:"audio"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openrouter models: %w", err)
	}
	models := make([]Model, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, Model{
			ID: m.ID, Name: m.Name, ContextLength: m.ContextLength,
			InputModalities: m.Architecture.InputModalities, OutputModalities: m.Architecture.OutputModalities,
			PromptPrice: m.Pricing.Prompt, CompletionPrice: m.Pricing.Completion, AudioPrice: m.Pricing.Audio,
		})
	}
	return models, nil
}

// Message is a chat message. Content is a string or a list of parts (TextPart, AudioPart).
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// TextPart is a text content part.
type TextPart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// AudioPart is an audio content part: base64 data and its format (wav, mp3, flac, m4a, …).
type AudioPart struct {
	Type       string     `json:"type"` // "input_audio"
	InputAudio AudioInput `json:"input_audio"`
}

// AudioInput holds base64-encoded audio.
type AudioInput struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

// Text builds a text part.
func Text(s string) TextPart { return TextPart{Type: "text", Text: s} }

// Audio builds an audio part from base64 data.
func Audio(base64Data, format string) AudioPart {
	return AudioPart{Type: "input_audio", InputAudio: AudioInput{Data: base64Data, Format: format}}
}

// Request is a chat completion request.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	// JSON asks the model to answer with a JSON object (where the model supports it).
	JSON bool `json:"-"`
}

// Complete runs a chat completion and returns the text of the first choice.
func (c *Client) Complete(ctx context.Context, apiKey string, r Request) (string, error) {
	body := map[string]any{"model": r.Model, "messages": r.Messages}
	if r.JSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "knowpod")
	if c.appURL != "" {
		req.Header.Set("HTTP-Referer", c.appURL)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openrouter: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return "", fmt.Errorf("openrouter: %w", err)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return "", ErrUnauthorized
	case out.Error != nil:
		return "", fmt.Errorf("openrouter (%s): %s", r.Model, out.Error.Message)
	case res.StatusCode != http.StatusOK:
		return "", fmt.Errorf("openrouter (%s): HTTP %d: %s", r.Model, res.StatusCode, snippet(raw))
	case len(out.Choices) == 0:
		return "", fmt.Errorf("openrouter (%s): no answer: %s", r.Model, snippet(raw))
	}
	choice := out.Choices[0]
	text := contentText(choice.Message.Content)
	if choice.FinishReason == "length" {
		return text, fmt.Errorf("openrouter (%s): answer was cut off at the model's output limit", r.Model)
	}
	return text, nil
}

// contentText reads message content given as a string or as a list of text parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []TextPart
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
