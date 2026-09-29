// Package remarkable reads documents from the reMarkable cloud. It pairs with an account
// through a one-time code, lists the account's documents and folders, downloads a
// document's files and renders handwritten notebooks to PDF and PNG. The only writes are
// EPUB documents made from notes (see WriteDocuments and NoteEPUB).
//
// The cloud API is not documented by reMarkable. This implementation follows the protocol
// as used by rmapi (https://github.com/juruen/rmapi and its maintained fork
// https://github.com/ddvk/rmapi): a device token from a one-time code, a short-lived user
// token from the device token, and content-addressed blobs (the "sync 1.5" scheme) below a
// root index.
package remarkable

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Default endpoints of the reMarkable cloud.
const (
	DefaultAuthURL = "https://webapp-prod.cloud.remarkable.engineering"
	DefaultSyncURL = "https://internal.cloud.remarkable.com"
	// ConnectURL is where users get the one-time code for pairing.
	ConnectURL = "https://my.remarkable.com/device/browser/connect"
)

// deviceDesc is how the paired app is listed among the account's connected devices. The
// cloud only accepts a fixed set of values; rmapi uses this one.
const deviceDesc = "desktop-linux"

var (
	// ErrInvalidCode is returned when the cloud doesn't accept a one-time code.
	ErrInvalidCode = errors.New("the reMarkable cloud did not accept the one-time code")
	// ErrUnauthorized is returned when the device token was revoked (the app was removed
	// from the account's connected devices).
	ErrUnauthorized = errors.New("the reMarkable cloud rejected the pairing; pair again")
	// ErrTooLarge is returned for files larger than the allowed size.
	ErrTooLarge = errors.New("file is larger than allowed")
)

// Client calls the reMarkable cloud. Tokens are passed per call because every user pairs
// their own account.
type Client struct {
	authURL string
	syncURL string
	http    *http.Client
}

// NewClient builds a client. Empty URLs use the defaults.
func NewClient(authURL, syncURL string) *Client {
	if authURL == "" {
		authURL = DefaultAuthURL
	}
	if syncURL == "" {
		syncURL = DefaultSyncURL
	}
	return &Client{
		authURL: strings.TrimRight(authURL, "/"),
		syncURL: strings.TrimRight(syncURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// Pair registers this service as a device of the account the one-time code belongs to and
// returns the device token, which stays valid until the user removes the device.
func (c *Client) Pair(ctx context.Context, code string) (string, error) {
	code = strings.TrimSpace(code)
	if len(code) != 8 {
		return "", fmt.Errorf("%w: the code has 8 characters", ErrInvalidCode)
	}
	body, _ := json.Marshal(map[string]string{"code": code, "deviceDesc": deviceDesc, "deviceID": newUUID()})
	res, raw, err := c.do(ctx, http.MethodPost, c.authURL+"/token/json/2/device/new", "", bytes.NewReader(body), nil, 64<<10)
	if err != nil {
		return "", fmt.Errorf("remarkable pair: %w", err)
	}
	switch {
	case res.StatusCode >= 400 && res.StatusCode < 500:
		return "", ErrInvalidCode
	case res.StatusCode != http.StatusOK:
		return "", fmt.Errorf("remarkable pair: HTTP %d: %s", res.StatusCode, snippet(raw))
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("remarkable pair: empty device token")
	}
	return token, nil
}

// Session is an authenticated connection to one account's documents.
type Session struct {
	c     *Client
	token string
}

// Open exchanges a device token for a user token (valid for about a day).
func (c *Client) Open(ctx context.Context, deviceToken string) (*Session, error) {
	if deviceToken == "" {
		return nil, ErrUnauthorized
	}
	res, raw, err := c.do(ctx, http.MethodPost, c.authURL+"/token/json/2/user/new", deviceToken, nil, nil, 64<<10)
	if err != nil {
		return nil, fmt.Errorf("remarkable sign-in: %w", err)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("remarkable sign-in: HTTP %d: %s", res.StatusCode, snippet(raw))
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, errors.New("remarkable sign-in: empty user token")
	}
	return &Session{c: c, token: token}, nil
}

// Root is the current state of the account: the hash of the root index, which lists every
// document and folder, and a generation counter that grows with every change.
type Root struct {
	Hash          string `json:"hash"`
	Generation    int64  `json:"generation"`
	SchemaVersion int    `json:"schemaVersion"`
}

// Root reads the account's root. An account without any documents has an empty hash.
func (s *Session) Root(ctx context.Context) (Root, error) {
	res, raw, err := s.c.do(ctx, http.MethodGet, s.c.syncURL+"/sync/v4/root", s.token, nil, nil, 64<<10)
	if err != nil {
		return Root{}, fmt.Errorf("remarkable root: %w", err)
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return Root{}, nil
	case res.StatusCode == http.StatusUnauthorized:
		return Root{}, ErrUnauthorized
	case res.StatusCode != http.StatusOK:
		return Root{}, fmt.Errorf("remarkable root: HTTP %d: %s", res.StatusCode, snippet(raw))
	}
	var r Root
	if err := json.Unmarshal(raw, &r); err != nil {
		return Root{}, fmt.Errorf("remarkable root: %w", err)
	}
	return r, nil
}

// Blob reads a file by its hash; name is the file's name in its index (the cloud wants it).
// At most maxBytes are read.
func (s *Session) Blob(ctx context.Context, hash, name string, maxBytes int64) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := s.CopyBlob(ctx, &buf, hash, name, maxBytes); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CopyBlob streams a file into w and returns its size. At most maxBytes are copied.
func (s *Session) CopyBlob(ctx context.Context, w io.Writer, hash, name string, maxBytes int64) (int64, error) {
	if !isHash(hash) {
		return 0, fmt.Errorf("remarkable file %s: invalid hash %q", name, hash)
	}
	req, err := s.c.request(ctx, http.MethodGet, s.c.syncURL+"/sync/v3/files/"+hash, s.token, nil, map[string]string{"rm-filename": name})
	if err != nil {
		return 0, err
	}
	res, err := s.c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("remarkable file %s: %w", name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		if res.StatusCode == http.StatusUnauthorized {
			return 0, ErrUnauthorized
		}
		return 0, fmt.Errorf("remarkable file %s: HTTP %d: %s", name, res.StatusCode, snippet(raw))
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return n, fmt.Errorf("remarkable file %s: %w", name, err)
	}
	if n > maxBytes {
		return n, fmt.Errorf("remarkable file %s: %w (%d bytes)", name, ErrTooLarge, maxBytes)
	}
	return n, nil
}

// Index reads an index file (the root index or a document's list of files).
func (s *Session) Index(ctx context.Context, hash, name string) ([]Entry, error) {
	data, err := s.Blob(ctx, hash, name, 16<<20)
	if err != nil {
		return nil, err
	}
	entries, err := ParseIndex(data)
	if err != nil {
		return nil, fmt.Errorf("remarkable index %s: %w", name, err)
	}
	return entries, nil
}

func (c *Client) request(ctx context.Context, method, url, token string, body io.Reader, headers map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "knowpod")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// do sends a request and reads at most limit bytes of the answer.
func (c *Client) do(ctx context.Context, method, url, token string, body io.Reader, headers map[string]string, limit int64) (*http.Response, []byte, error) {
	req, err := c.request(ctx, method, url, token, body, headers)
	if err != nil {
		return nil, nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit))
	if err != nil {
		return nil, nil, err
	}
	return res, raw, nil
}

// isHash reports whether s is a hex SHA-256, so it can be put into a URL path as it is.
func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
