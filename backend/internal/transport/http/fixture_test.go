package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/worker"
)

const (
	adminToken    = "test-admin-token"
	adminEmail    = "admin@example.com"
	adminPassword = "env-secret"
)

// apiFixture is a server with every service on in-memory storage, a built-in admin, and
// the archive worker (run on demand with RunOnce).
type apiFixture struct {
	t      *testing.T
	srv    *httptest.Server
	recs   *memory.Recordings
	users  *memory.Users
	worker *worker.Worker
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	recs, users, sessions, devs := memory.NewRecordings(), memory.NewUsers(), memory.NewSessions(), memory.NewDevices()
	objects := memstore.New()
	spool, err := service.NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := service.NewAuthService(users, sessions, adminEmail, adminPassword, time.Hour)
	if _, err := auth.EnsureBuiltInAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	themes := service.NewThemeService(memory.NewThemes())
	actions := service.NewRecordingService(recs, objects, spool, themes)
	archiver := service.NewArchiver(spool, objects, false, log)
	w := worker.New(recs, []worker.Stage{{
		Name: "archive", From: recording.StatusReceived, To: recording.StatusStored, Run: archiver.Run, Cleanup: archiver.Cleanup,
	}}, worker.Options{}, log)

	s := NewServer(Deps{
		Cfg: config.Config{AdminToken: adminToken}, Log: log, Auth: auth,
		Users:   service.NewUserService(users, sessions, devs, recs, memory.NewThemes(), auth, actions),
		Devices: service.NewDeviceService(devs), Uploads: service.NewUploadService(recs, spool, 1<<30),
		Manual: service.NewManualUploadService(recs, spool, 1<<30), Actions: actions, Objects: objects,
		Pocket: service.NewPocketService(recs, users, nil, spool, 1<<20, log),
		AI:     service.NewAIService(memory.NewSettings(), themes, objects, nil, t.TempDir(), log),
		Themes: themes,
	})
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return &apiFixture{t: t, srv: srv, recs: recs, users: users, worker: w}
}

// client is an HTTP client with its own cookie jar, i.e. one browser.
type client struct {
	f      *apiFixture
	http   *http.Client
	bearer string
}

// browser returns a new, signed-out browser.
func (f *apiFixture) browser() *client {
	jar, _ := cookiejar.New(nil)
	return &client{f: f, http: &http.Client{Jar: jar}}
}

// script returns a client that authenticates with a bearer token.
func (f *apiFixture) script(token string) *client {
	return &client{f: f, http: http.DefaultClient, bearer: token}
}

// signedIn returns a browser signed in with the given credentials.
func (f *apiFixture) signedIn(email, password string) *client {
	f.t.Helper()
	c := f.browser()
	if res := c.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password}, nil, nil); res.StatusCode != 200 {
		f.t.Fatalf("login %s: %d", email, res.StatusCode)
	}
	return c
}

// do sends a request. A []byte body is sent raw, anything else as JSON. The response is
// decoded into out when it is not nil (*[]byte receives the raw body).
func (c *client) do(method, path string, body any, headers map[string]string, out any) *http.Response {
	c.f.t.Helper()
	var r io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		r = bytes.NewReader(data)
		contentType = "application/json"
	}
	req, _ := http.NewRequest(method, c.f.srv.URL+path, r)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.f.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil {
		if b, ok := out.(*[]byte); ok {
			*b = data
		} else if err := json.Unmarshal(data, out); err != nil {
			c.f.t.Fatalf("%s %s: decoding %q: %v", method, path, data, err)
		}
	}
	return res
}
