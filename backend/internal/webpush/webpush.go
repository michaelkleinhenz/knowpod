// Package webpush sends Web Push messages (RFC 8030) to browsers: the payload is encrypted
// for the subscription (RFC 8291, aes128gcm) and the request is signed with the server's
// VAPID key (RFC 8292). It uses only the standard library.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrGone means the subscription no longer exists (the browser unsubscribed or the
// permission was withdrawn); it should be forgotten.
var ErrGone = errors.New("push subscription is gone")

// Keys is the server's VAPID key pair, base64url-encoded without padding: the private
// scalar (32 bytes) and the uncompressed public point (65 bytes), which browsers take as
// applicationServerKey.
type Keys struct {
	Private string
	Public  string
}

// GenerateKeys makes a new VAPID key pair.
func GenerateKeys() (Keys, error) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	return Keys{Private: b64(k.Bytes()), Public: b64(k.PublicKey().Bytes())}, nil
}

// Subscription is a browser's push subscription (PushSubscription.toJSON()).
type Subscription struct {
	Endpoint string
	P256dh   string // the browser's public key, base64url
	Auth     string // the authentication secret, base64url
}

// Sender sends messages with a VAPID key pair.
type Sender struct {
	keys    Keys
	signer  *ecdsa.PrivateKey
	subject string
	client  *http.Client
	now     func() time.Time
}

// NewSender builds a sender. subject identifies the sender to push services: a mailto: or
// https: URL.
func NewSender(keys Keys, subject string, client *http.Client) (*Sender, error) {
	d, err := unb64(keys.Private)
	if err != nil || len(d) != 32 {
		return nil, errors.New("webpush: invalid private key")
	}
	priv, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		return nil, fmt.Errorf("webpush: %w", err)
	}
	pub := priv.PublicKey().Bytes()
	signer := &ecdsa.PrivateKey{D: new(big.Int).SetBytes(d)}
	signer.PublicKey.Curve = elliptic.P256()
	signer.PublicKey.X, signer.PublicKey.Y = new(big.Int).SetBytes(pub[1:33]), new(big.Int).SetBytes(pub[33:])
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Sender{keys: Keys{Private: keys.Private, Public: b64(pub)}, signer: signer, subject: subject, client: client, now: time.Now}, nil
}

// PublicKey returns the VAPID public key for the browser's applicationServerKey.
func (s *Sender) PublicKey() string { return s.keys.Public }

// Send delivers payload to the subscription. The push service keeps it for up to ttl while
// the device is offline. ErrGone means the subscription should be forgotten.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte, ttl time.Duration) error {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("webpush: the endpoint must be an https URL")
	}
	body, err := Encrypt(sub, payload)
	if err != nil {
		return err
	}
	jwt, err := s.token(u.Scheme + "://" + u.Host)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+s.keys.Public)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	req.Header.Set("Urgency", "high")
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("webpush: %w", err)
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return ErrGone
	case res.StatusCode >= 300:
		return fmt.Errorf("webpush: push service answered %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// token is the VAPID JWT (ES256) for a push service origin, valid for 12 hours.
func (s *Sender) token(audience string) (string, error) {
	header := b64([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": audience, "exp": s.now().Add(12 * time.Hour).Unix(), "sub": s.subject})
	signing := header + "." + b64(claims)
	digest := sha256.Sum256([]byte(signing))
	r, sig, err := ecdsa.Sign(rand.Reader, s.signer, digest[:])
	if err != nil {
		return "", err
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sig.FillBytes(raw[32:])
	return signing + "." + b64(raw), nil
}

// recordSize is the record size announced in the aes128gcm header. Payloads fit in one record.
const recordSize = 4096

// Encrypt encrypts payload for the subscription as one aes128gcm record (RFC 8291).
func Encrypt(sub Subscription, payload []byte) ([]byte, error) {
	uaPublic, err := unb64(sub.P256dh)
	if err != nil {
		return nil, errors.New("webpush: invalid p256dh key")
	}
	authSecret, err := unb64(sub.Auth)
	if err != nil || len(authSecret) == 0 {
		return nil, errors.New("webpush: invalid auth secret")
	}
	if len(payload) > recordSize-17-86 {
		return nil, errors.New("webpush: payload too large")
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, errors.New("webpush: invalid p256dh key")
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(as, ua, authSecret, salt, payload)
}

func encrypt(as *ecdh.PrivateKey, ua *ecdh.PublicKey, authSecret, salt, payload []byte) ([]byte, error) {
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := as.PublicKey().Bytes()
	prkKey, err := hkdf.Extract(sha256.New, secret, authSecret)
	if err != nil {
		return nil, err
	}
	keyInfo := "WebPush: info\x00" + string(ua.Bytes()) + string(asPublic)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// The last (and only) record ends with the delimiter 0x02.
	plain := append(append([]byte{}, payload...), 2)
	header := make([]byte, 0, 16+4+1+len(asPublic))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPublic)))
	header = append(header, asPublic...)
	return gcm.Seal(header, nonce, plain, nil), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// unb64 decodes base64url with or without padding (browsers send it without).
func unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
}
