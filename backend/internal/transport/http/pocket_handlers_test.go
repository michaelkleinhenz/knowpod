package http

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func pocketServer(t *testing.T, cfg config.Config) (http.Handler, *memory.Recordings) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	recs := memory.NewRecordings()
	spool, _ := service.NewSpool(t.TempDir())
	cfg.AdminToken = adminToken
	s := NewServer(Deps{Cfg: cfg, Log: log, Recordings: recs, Pocket: service.NewPocketService(recs, nil, spool, 1<<20, log)})
	return s.Router(), recs
}

func postWebhook(h http.Handler, secret string, body []byte, age time.Duration) *httptest.ResponseRecorder {
	ts := strconv.FormatInt(time.Now().Add(-age).UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, pocketWebhookPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HeyPocket-Webhook/1.0")
	req.Header.Set(pocket.TimestampHeader, ts)
	req.Header.Set(pocket.SignatureHeader, hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPocketWebhook(t *testing.T) {
	h, recs := pocketServer(t, config.Config{PocketWebhookSecret: "whsec", PocketAPIKey: "pk_test"})
	body := []byte(`{"event":"recording.created","timestamp":"2026-02-18T12:00:00.000Z","recording":{"id":"rec_abc123","title":"Team Standup","duration":1800,"recordingAt":"2026-02-17T15:02:00.000Z"}}`)

	res := postWebhook(h, "whsec", body, 0)
	var out map[string]string
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if res.Code != 200 || out["status"] != "queued" {
		t.Fatalf("webhook: %d %s", res.Code, res.Body)
	}
	if rec, err := recs.GetByClientID(t.Context(), recording.PocketDeviceID, "rec_abc123"); err != nil || rec.Status != recording.StatusRemote {
		t.Fatalf("recording: %+v, %v", rec, err)
	}
	if res := postWebhook(h, "whsec", body, 0); res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte("duplicate")) {
		t.Fatalf("redelivery: %d %s", res.Code, res.Body)
	}

	if res := postWebhook(h, "wrong", body, 0); res.Code != 401 {
		t.Fatalf("wrong secret: %d", res.Code)
	}
	if res := postWebhook(h, "whsec", body, time.Hour); res.Code != 401 {
		t.Fatalf("stale timestamp: %d", res.Code)
	}
	if res := postWebhook(h, "whsec", []byte("not json"), 0); res.Code != 400 {
		t.Fatalf("invalid JSON: %d", res.Code)
	}
}

func TestPocketWebhookRequiresConfiguration(t *testing.T) {
	h, _ := pocketServer(t, config.Config{PocketWebhookSecret: "whsec"}) // no API key
	if res := postWebhook(h, "whsec", []byte(`{}`), 0); res.Code != 503 {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestIntegrationsEndpoint(t *testing.T) {
	h, _ := pocketServer(t, config.Config{PocketWebhookSecret: "whsec"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/integrations", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out integrationsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out.Pocket.WebhookPath != pocketWebhookPath || !out.Pocket.WebhookSecretConfigured || out.Pocket.APIKeyConfigured {
		t.Fatalf("integrations: %d %s", rec.Code, rec.Body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("whsec")) {
		t.Fatal("secret leaked")
	}
}
