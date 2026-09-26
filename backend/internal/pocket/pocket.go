// Package pocket integrates the Pocket AI recorder (heypocketai.com): it verifies the
// webhooks Pocket sends and downloads recording audio through the Pocket public API.
//
// API reference: https://docs.heypocketai.com/docs/api
package pocket

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
)

// DefaultAPIURL is the base URL of the Pocket public API.
const DefaultAPIURL = "https://public.heypocketai.com/api/v1"

// Webhook request headers.
const (
	SignatureHeader = "X-HeyPocket-Signature"
	TimestampHeader = "X-HeyPocket-Timestamp"
)

// MaxClockSkew is how far a webhook's timestamp may be from the local clock. Older requests
// are rejected so that a captured request can't be replayed later.
const MaxClockSkew = 5 * time.Minute

var (
	// ErrBadSignature is returned for webhooks whose signature or timestamp doesn't verify.
	ErrBadSignature = errors.New("invalid webhook signature")
	// ErrNotFound is returned when Pocket doesn't know the recording (or has no audio yet).
	ErrNotFound = errors.New("not found at Pocket")
)

// VerifySignature checks a webhook: the signature must be the hex HMAC-SHA256, keyed with
// the webhook's signing secret, of "<timestamp>.<raw body>", and the timestamp (Unix
// milliseconds) must be within MaxClockSkew of now.
func VerifySignature(secret, timestamp, signature string, body []byte, now time.Time) error {
	ms, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: missing or malformed %s", ErrBadSignature, TimestampHeader)
	}
	if skew := now.Sub(time.UnixMilli(ms)); skew > MaxClockSkew || skew < -MaxClockSkew {
		return fmt.Errorf("%w: timestamp outside the allowed window", ErrBadSignature)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.TrimSpace(timestamp)))
	mac.Write([]byte("."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	got := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(signature), "sha256="))
	if !hmac.Equal([]byte(got), []byte(want)) {
		return ErrBadSignature
	}
	return nil
}

// Event is the part of a webhook payload this service uses. Pocket sends more (summaries,
// transcript, action items), which is ignored.
type Event struct {
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"`
	Recording struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Duration    float64 `json:"duration"`
		RecordingAt string  `json:"recordingAt"`
		CreatedAt   string  `json:"createdAt"`
	} `json:"recording"`
}

// ParseEvent decodes a webhook body.
func ParseEvent(body []byte) (*Event, error) {
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("decode webhook: %w", err)
	}
	return &ev, nil
}

// Client calls the Pocket public API. The API key is passed per call because every user
// has their own.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a client.
func NewClient(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{}}
}

// apiResponse is Pocket's response envelope.
type apiResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// AudioURL returns a pre-signed download URL for a recording's audio
// (GET /public/recordings/{id}/audio-url). apiKey is a Pocket API key ("pk_…").
func (c *Client) AudioURL(ctx context.Context, apiKey, recordingID string) (string, error) {
	if apiKey == "" {
		return "", errors.New("no Pocket API key")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u := fmt.Sprintf("%s/public/recordings/%s/audio-url?expires_in=3600", c.baseURL, url.PathEscape(recordingID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("pocket audio-url: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("pocket audio-url: %w", err)
	}

	var env apiResponse
	_ = json.Unmarshal(raw, &env)
	switch {
	case res.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("%w: recording %s: %s", ErrNotFound, recordingID, env.Error)
	case res.StatusCode != http.StatusOK:
		return "", fmt.Errorf("pocket audio-url: HTTP %d: %s", res.StatusCode, snippet(raw))
	}
	if link := findURL(env.Data); link != "" {
		return link, nil
	}
	return "", fmt.Errorf("pocket audio-url: no download URL in response: %s", snippet(raw))
}

// findURL extracts the download URL from the "data" field. The API docs don't spell out its
// shape, so accept a bare string or an object with one of the usual field names (also one
// level deeper).
func findURL(data json.RawMessage) string {
	var s string
	if json.Unmarshal(data, &s) == nil && isHTTPURL(s) {
		return s
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return ""
	}
	for _, key := range []string{"url", "download_url", "downloadUrl", "audio_url", "audioUrl", "signed_url", "signedUrl", "presigned_url", "presignedUrl"} {
		if json.Unmarshal(obj[key], &s) == nil && isHTTPURL(s) {
			return s
		}
	}
	for _, v := range obj {
		if link := findURL(v); link != "" && strings.HasPrefix(string(v), "{") {
			return link
		}
	}
	return ""
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// Download is a fetched audio file.
type Download struct {
	ContentType string
	Size        int64
	SHA256      string
}

// Download streams the file at a pre-signed URL into dst (at most maxBytes) and identifies
// its format. On error, dst is removed.
func (c *Client) Download(ctx context.Context, link, dst string, maxBytes int64) (d *Download, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	// No Authorization header: the pre-signed URL carries its own credentials.
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download audio: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("download audio: HTTP %d: %s", res.StatusCode, snippet(body))
	}

	f, err := os.Create(dst)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()

	h := sha256.New()
	head := &headBuffer{max: 64}
	n, err := io.Copy(io.MultiWriter(f, h, head), io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download audio: %w", err)
	}
	if n > maxBytes {
		return nil, fmt.Errorf("download audio: larger than the %d byte limit", maxBytes)
	}
	if n == 0 {
		return nil, errors.New("download audio: empty file")
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}

	// Prefer the file's own signature: pre-signed storage URLs often answer with a generic
	// type such as binary/octet-stream.
	ctype, _ := audio.Sniff(head.buf)
	if ctype == "" {
		ctype = strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	return &Download{ContentType: ctype, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// headBuffer keeps the first max bytes written to it.
type headBuffer struct {
	buf []byte
	max int
}

func (b *headBuffer) Write(p []byte) (int, error) {
	if room := b.max - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
