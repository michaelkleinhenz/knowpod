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
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
	rt "github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable/remarkabletest"
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
	// cloud is the fake reMarkable cloud; remarkable reads from it.
	cloud      *rt.Cloud
	remarkable *service.RemarkableService
	// notifications has no Web Push; it only reaches live connections.
	notifications *service.NotificationService
	// events tells the web app about note changes.
	events *service.NoteEvents
	// ai answers the AI requests once the models are set up (see withAI).
	ai     *service.AIService
	writer *fakeWriter
}

// fakeWriter answers every AI request with a fixed text.
type fakeWriter struct {
	answer   string
	requests []openrouter.Request
}

func (w *fakeWriter) Complete(_ context.Context, _ string, r openrouter.Request) (string, error) {
	w.requests = append(w.requests, r)
	return w.answer, nil
}

func (w *fakeWriter) Models(context.Context) ([]openrouter.Model, error) { return nil, nil }

// withAI sets up the AI models, answered by the fake writer.
func (f *apiFixture) withAI() {
	f.t.Helper()
	key, model := "sk-test", "test/model"
	if _, err := f.ai.UpdateSettings(context.Background(), service.OpenRouterUpdate{APIKey: &key, TranscriptionModel: &model, SummaryModel: &model}); err != nil {
		f.t.Fatal(err)
	}
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
	themeRepo := memory.NewThemes()
	themes := service.NewThemeService(themeRepo)
	events := service.NewNoteEvents()
	actions := service.NewRecordingService(events.Watch(recs), objects, spool, themes)
	actions.Users, actions.Events = users, events
	actions.Versions = memory.NewNoteVersions()
	labelRepo := memory.NewLabels()
	labels := service.NewLabelService(labelRepo, recs)
	actions.Labels = labels
	userSvc := service.NewUserService(users, sessions, devs, recs, memory.NewThemes(), auth, actions)
	userSvc.Labels = labelRepo
	folderRepo := memory.NewFolders()
	folders := service.NewFolderService(folderRepo, recs)
	actions.Folders = folders
	folders.Notes = actions
	userSvc.Folders = folderRepo
	filterRepo := memory.NewFilters()
	filters := service.NewFilterService(filterRepo, recs)
	actions.Filters = filters
	timeRepo := memory.NewTimeEntries()
	actions.TimeEntries = timeRepo
	userSvc.TimeEntries = timeRepo
	archiver := service.NewArchiver(spool, objects, false, log)
	cloud := rt.New()
	t.Cleanup(cloud.Close)
	rm := service.NewRemarkableService(memory.NewTabletLinks(), recs, folderRepo, objects, remarkable.NewClient(cloud.URL, cloud.URL), spool, 1<<20, log)
	userSvc.Remarkable = rm
	w := worker.New(recs, []worker.Stage{
		{Name: "fetch", From: recording.StatusRemote, To: recording.StatusReceived, Run: rm.Fetch},
		{Name: "archive", From: recording.StatusReceived, To: recording.StatusStored,
			Run: func(ctx context.Context, rec *recording.Recording) error {
				if rec.IsDocument() && rec.Source == recording.SourceRemarkable {
					return rm.Store(ctx, rec)
				}
				return archiver.Run(ctx, rec)
			}, Cleanup: archiver.Cleanup},
	}, worker.Options{}, log)

	notifications := service.NewNotificationService(memory.NewPushSubscriptions(), users, recs, nil, log)
	oauthRepo := memory.NewOAuth()
	userSvc.OAuth = oauthRepo
	oauthSvc := service.NewOAuthService(oauthRepo, users)
	mcp := service.NewMCPAccessService(users)
	mcp.OAuth = oauthSvc
	writer := &fakeWriter{answer: "Written"}
	ai := service.NewAIService(memory.NewSettings(), themes, objects, writer, t.TempDir(), log)
	briefings := service.NewBriefingService(users, actions, folderRepo, ai, log)
	briefings.Notifications, briefings.TimeEntries = notifications, timeRepo
	backupDB := memory.NewBackup("users", "recordings")
	backupDB.Docs["users"] = [][]byte{{5, 0, 0, 0, 0}}
	_ = objects.Put(ctx, "seed.txt", bytes.NewReader([]byte("seed")), 4, "text/plain")
	backup := service.NewBackupService(backupDB, objects)
	backup.TempDir = t.TempDir()
	personal := service.NewPersonalBackupService(recs, folderRepo, labelRepo, themeRepo, filterRepo, timeRepo, objects, actions)
	personal.TempDir = t.TempDir()
	s := NewServer(Deps{
		Backup: backup, PersonalBackup: personal,
		Cfg: config.Config{AdminToken: adminToken}, Log: log, Auth: auth,
		Users:   userSvc,
		Devices: service.NewDeviceService(devs), Uploads: service.NewUploadService(recs, spool, 1<<30),
		Manual: service.NewManualUploadService(recs, spool, 1<<30), Actions: actions, Objects: objects,
		Pocket: service.NewPocketService(recs, users, nil, spool, 1<<20, log),
		AI:     ai,
		Themes: themes, Labels: labels, Folders: folders, Remarkable: rm, Notifications: notifications,
		Filters: filters, Times: service.NewTimeService(timeRepo, recs, users), Calendar: service.NewCalendarService(users, recs),
		MCP: mcp, OAuth: oauthSvc, Events: events,
		Ask: service.NewAskService(ai, actions, log), Briefings: briefings,
	})
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	t.Cleanup(notifications.Shutdown) // before srv.Close, which waits for open streams
	t.Cleanup(events.Shutdown)
	return &apiFixture{t: t, srv: srv, recs: recs, users: users, worker: w, cloud: cloud, remarkable: rm, notifications: notifications, events: events, ai: ai, writer: writer}
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
