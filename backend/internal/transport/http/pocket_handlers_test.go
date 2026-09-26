package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func signedWebhook(c *client, path, secret string, body []byte, age time.Duration) (int, string) {
	ts := strconv.FormatInt(time.Now().Add(-age).UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	var out []byte
	res := c.do("POST", path, body, map[string]string{
		"Content-Type": "application/json", pocket.TimestampHeader: ts, pocket.SignatureHeader: hex.EncodeToString(mac.Sum(nil)),
	}, &out)
	return res.StatusCode, string(out)
}

func TestPocketPerUser(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	var bobPocket, adminPocket service.PocketView
	bob.do("GET", "/api/v1/me/pocket", nil, nil, &bobPocket)
	admin.do("GET", "/api/v1/me/pocket", nil, nil, &adminPocket)
	if bobPocket.WebhookPath == adminPocket.WebhookPath || bobPocket.WebhookSecretConfigured {
		t.Fatalf("pocket settings: bob=%+v admin=%+v", bobPocket, adminPocket)
	}

	body := []byte(`{"event":"recording.created","recording":{"id":"rec_abc123","title":"Team Standup","recordingAt":"2026-02-17T15:02:00.000Z"}}`)
	anon := f.browser()
	if code, _ := signedWebhook(anon, bobPocket.WebhookPath, "whsec_bob", body, 0); code != 503 {
		t.Fatalf("before setup: %d", code)
	}
	if code, _ := signedWebhook(anon, "/api/v1/webhooks/pocket/unknown", "x", body, 0); code != 404 {
		t.Fatalf("unknown webhook: %d", code)
	}

	var v service.PocketView
	if res := bob.do("PUT", "/api/v1/me/pocket", map[string]string{"webhookSecret": "whsec_bob", "apiKey": "pk_bob_12345678"}, nil, &v); res.StatusCode != 200 ||
		!v.WebhookSecretConfigured || !v.APIKeyConfigured || v.APIKeyHint != "…5678" {
		t.Fatalf("save settings: %d %+v", res.StatusCode, v)
	}
	var raw []byte
	bob.do("GET", "/api/v1/me/pocket", nil, nil, &raw)
	if bytes.Contains(raw, []byte("whsec_bob")) || bytes.Contains(raw, []byte("pk_bob")) {
		t.Fatal("secrets returned to the browser")
	}

	if code, _ := signedWebhook(anon, bobPocket.WebhookPath, "whsec_other", body, 0); code != 401 {
		t.Fatalf("wrong secret: %d", code)
	}
	if code, _ := signedWebhook(anon, bobPocket.WebhookPath, "whsec_bob", body, time.Hour); code != 401 {
		t.Fatalf("stale: %d", code)
	}
	if code, out := signedWebhook(anon, bobPocket.WebhookPath, "whsec_bob", body, 0); code != 200 || !bytes.Contains([]byte(out), []byte("queued")) {
		t.Fatalf("webhook: %d %s", code, out)
	}
	if code, out := signedWebhook(anon, bobPocket.WebhookPath, "whsec_bob", body, 0); code != 200 || !bytes.Contains([]byte(out), []byte("duplicate")) {
		t.Fatalf("redelivery: %d %s", code, out)
	}

	bobUser, _ := f.users.GetByEmail(context.Background(), "bob@example.com")
	rec, err := f.recs.GetByClientID(context.Background(), recording.PocketDeviceID(bobUser.ID), "rec_abc123")
	if err != nil || rec.OwnerID != bobUser.ID || rec.Status != recording.StatusRemote {
		t.Fatalf("recording: %+v, %v", rec, err)
	}
}
