// Package elevenlabs calls the ElevenLabs speech-to-text API (Scribe), which transcribes
// audio with word time stamps and tells the speakers apart by their voices.
package elevenlabs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the ElevenLabs API.
const DefaultBaseURL = "https://api.elevenlabs.io/v1"

// Model is the speech-to-text model used.
const Model = "scribe_v2"

// ErrUnauthorized is returned when ElevenLabs rejects the API key.
var ErrUnauthorized = errors.New("ElevenLabs rejected the API key")

// Client calls ElevenLabs. The API key is passed per call because it is stored in the
// settings, which administrators can change at any time.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a client.
func NewClient(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{}}
}

// Word is a word, the space between words, or a sound event ("(laughter)") of a transcript.
type Word struct {
	Text      string  `json:"text"`
	Start     float64 `json:"start"` // seconds from the start of the audio
	End       float64 `json:"end"`
	Type      string  `json:"type"`       // "word", "spacing" or "audio_event"
	SpeakerID string  `json:"speaker_id"` // "speaker_0", "speaker_1", …
}

// Transcript is the result of a transcription.
type Transcript struct {
	Text         string  `json:"text"`
	LanguageCode string  `json:"language_code"`
	Words        []Word  `json:"words"`
	Duration     float64 `json:"audio_duration_secs"`
}

// Transcribe transcribes audio (any common audio format; the file name's extension tells
// which) with speaker diarization and word time stamps. The audio is streamed, so it may
// be large: ElevenLabs takes files of up to several hours.
func (c *Client) Transcribe(ctx context.Context, apiKey string, audio io.Reader, filename string) (*Transcript, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	pr, pw := io.Pipe()
	form := multipart.NewWriter(pw)
	go func() {
		err := writeForm(form, audio, filename)
		if err == nil {
			err = form.Close()
		}
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/speech-to-text", pr)
	if err != nil {
		pr.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("xi-api-key", apiKey)
	res, err := c.http.Do(req)
	if err != nil {
		pr.Close()
		return nil, fmt.Errorf("elevenlabs: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: %w", err)
	}
	if err := responseError(res.StatusCode, raw); err != nil {
		return nil, err
	}
	var t Transcript
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("elevenlabs: %w", err)
	}
	return &t, nil
}

func writeForm(form *multipart.Writer, audio io.Reader, filename string) error {
	for _, f := range [][2]string{
		{"model_id", Model},
		{"diarize", "true"},
		{"timestamps_granularity", "word"},
		{"tag_audio_events", "true"},
	} {
		if err := form.WriteField(f[0], f[1]); err != nil {
			return err
		}
	}
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, audio)
	return err
}

// Check verifies that the API key may transcribe, by transcribing a second of silence.
func (c *Client) Check(ctx context.Context, apiKey string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	_, err := c.Transcribe(ctx, apiKey, bytes.NewReader(silence(time.Second)), "check.wav")
	return err
}

// silence returns a 16 kHz mono 16-bit WAV file of the given length.
func silence(d time.Duration) []byte {
	const rate = 16000
	n := int(d.Seconds()*rate) * 2
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+n))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(n))
	b.Write(make([]byte, n))
	return b.Bytes()
}

// responseError turns an error response into an error. ElevenLabs reports errors as
// {"detail": {"status": "…", "message": "…"}}, {"detail": "…"} or, for invalid requests,
// {"detail": [{"msg": "…"}]}.
func responseError(status int, raw []byte) error {
	if status == http.StatusOK {
		return nil
	}
	var body struct {
		Detail json.RawMessage `json:"detail"`
	}
	_ = json.Unmarshal(raw, &body)
	var msg string
	var obj struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	var list []struct {
		Msg string `json:"msg"`
	}
	switch {
	case json.Unmarshal(body.Detail, &msg) == nil:
	case json.Unmarshal(body.Detail, &obj) == nil && (obj.Message != "" || obj.Status != ""):
		msg = obj.Message
		if msg == "" {
			msg = obj.Status
		}
	case json.Unmarshal(body.Detail, &list) == nil && len(list) > 0:
		msg = list[0].Msg
	}
	// A valid key without the speech-to-text permission is also refused with 401; its
	// message says which permission is missing.
	if status == http.StatusUnauthorized && obj.Status != "missing_permissions" {
		return ErrUnauthorized
	}
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
	}
	return fmt.Errorf("elevenlabs: HTTP %d: %s", status, msg)
}
