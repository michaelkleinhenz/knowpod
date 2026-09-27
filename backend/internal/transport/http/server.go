package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"

	"github.com/michaelkleinhenz/knowpod-service/backend/api"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/web"
)

// serviceName is reported by the health and info endpoints.
const serviceName = "knowpod-service"

// Pinger reports whether a backing dependency (the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server bundles the services and configuration the handlers need.
type Server struct {
	cfg     config.Config
	log     *slog.Logger
	db      Pinger
	auth    *service.AuthService
	users   *service.UserService
	devices *service.DeviceService
	uploads *service.UploadService
	manual  *service.ManualUploadService
	actions *service.RecordingService
	objects ports.ObjectStore
	pocket  *service.PocketService
	ai      *service.AIService
	themes  *service.ThemeService
	labels  *service.LabelService
	folders *service.FolderService
	// remarkable reads documents from users' reMarkable clouds.
	remarkable *service.RemarkableService
	now        func() time.Time
}

// Deps are the server's constructor dependencies. Handlers of missing services are still
// routed (so the API description matches), but only the provided ones may be called.
type Deps struct {
	Cfg     config.Config
	Log     *slog.Logger
	DB      Pinger // optional; when set, /healthz also checks database connectivity
	Auth    *service.AuthService
	Users   *service.UserService
	Devices *service.DeviceService
	Uploads *service.UploadService
	Manual  *service.ManualUploadService
	Actions *service.RecordingService
	Objects ports.ObjectStore
	Pocket  *service.PocketService
	AI      *service.AIService
	Themes  *service.ThemeService
	Labels  *service.LabelService
	Folders *service.FolderService
	// Remarkable is optional in tests that don't use it.
	Remarkable *service.RemarkableService
}

// NewServer builds the server.
func NewServer(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg: d.Cfg, log: log, db: d.DB, auth: d.Auth, users: d.Users, devices: d.Devices, uploads: d.Uploads,
		manual: d.Manual, actions: d.Actions, objects: d.Objects, pocket: d.Pocket, ai: d.AI, themes: d.Themes,
		labels: d.Labels, folders: d.Folders, remarkable: d.Remarkable, now: time.Now,
	}
}

// Router builds the fully-wired chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{s.cfg.FrontendURL, "http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", uploadOffsetHeader},
		ExposedHeaders:   []string{uploadOffsetHeader},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", s.handleHealth)

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/info", s.handleInfo)
		api.Get("/openapi.yaml", s.handleOpenAPIYAML)
		api.Get("/openapi.json", s.handleOpenAPIJSON)

		// --- web UI sign-in (session cookie) ---
		api.With(httprate.LimitByIP(10, time.Minute)).Post("/auth/login", s.handleLogin)
		api.Post("/auth/logout", s.handleLogout)

		// --- device API: recorders push recordings (device token) ---
		api.Group(func(d chi.Router) {
			d.Use(s.requireDevice)
			d.Post("/uploads", s.handleCreateUpload)
			d.Get("/uploads/{id}", s.handleGetUpload)
			d.Patch("/uploads/{id}", s.handleAppendUpload)
			d.Put("/uploads/{id}/highlights", s.handleSetHighlights)
		})

		// --- webhooks from external services (authenticated by their signatures) ---
		api.Post("/webhooks/pocket/{webhookId}", s.handlePocketWebhook)

		// --- the signed-in user's own data (session, or ADMIN_TOKEN for everyone's) ---
		api.Group(func(u chi.Router) {
			u.Use(s.requireUser)
			u.Get("/auth/me", s.handleMe)
			u.Put("/auth/password", s.handleChangePassword)
			u.Get("/me/pocket", s.handleGetPocketSettings)
			u.Put("/me/pocket", s.handleUpdatePocketSettings)
			u.Get("/me/remarkable", s.handleGetRemarkable)
			u.Delete("/me/remarkable", s.handleUnpairRemarkable)
			u.Post("/me/remarkable/pair", s.handlePairRemarkable)
			u.Post("/me/remarkable/pull", s.handlePullRemarkable)
			u.Put("/me/preferences", s.handleUpdatePreferences)
			u.Get("/ai/status", s.handleAIStatus)
			u.Get("/ai/models", s.handleOpenRouterModels)
			u.Get("/ai/languages", s.handleSummaryLanguages)

			u.Get("/themes", s.handleListThemes)
			u.Post("/themes", s.handleCreateTheme)
			u.Put("/themes/{id}", s.handleUpdateTheme)
			u.Delete("/themes/{id}", s.handleDeleteTheme)

			u.Get("/labels", s.handleListLabels)
			u.Post("/labels", s.handleCreateLabel)
			u.Put("/labels/{id}", s.handleUpdateLabel)
			u.Delete("/labels/{id}", s.handleDeleteLabel)

			u.Get("/folders", s.handleListFolders)
			u.Post("/folders", s.handleCreateFolder)
			u.Put("/folders/{id}", s.handleUpdateFolder)
			u.Delete("/folders/{id}", s.handleDeleteFolder)

			u.Get("/devices", s.handleListDevices)
			u.Post("/devices", s.handleRegisterDevice)
			u.Delete("/devices/{id}", s.handleRevokeDevice)
			u.Post("/devices/{id}/token", s.handleRotateDeviceToken)

			u.Get("/recordings", s.handleListRecordings)
			u.Post("/recordings", s.handleUploadRecording)
			u.Post("/recordings/text", s.handleCreateTextNote)
			u.Post("/recordings/board", s.handleCreateBoard)
			u.Get("/recordings/{id}", s.handleGetRecording)
			u.Delete("/recordings/{id}", s.handleDeleteRecording)
			u.Get("/recordings/{id}/audio", s.handleRecordingAudio)
			u.Get("/recordings/{id}/file", s.handleRecordingFile)
			u.Post("/recordings/{id}/retranscribe", s.handleRetranscribe)
			u.Post("/recordings/{id}/resummarize", s.handleResummarize)
			u.Put("/recordings/{id}/summary", s.handleEditSummary)
			u.Put("/recordings/{id}/labels", s.handleSetNoteLabels)
			u.Put("/recordings/{id}/done", s.handleSetNoteDone)
			u.Put("/recordings/{id}/folder", s.handleSetNoteFolder)
			u.Put("/recordings/{id}/board", s.handleSetBoard)
			u.Get("/recordings/{id}/summary", s.handleDownloadSummary)
			u.Get("/recordings/{id}/transcript", s.handleDownloadTranscript)
		})

		// --- administration (admins, or ADMIN_TOKEN) ---
		api.Route("/admin", func(a chi.Router) {
			a.Use(s.requireAdmin)
			a.Get("/users", s.handleListUsers)
			a.Post("/users", s.handleCreateUser)
			a.Put("/users/{id}", s.handleUpdateUser)
			a.Delete("/users/{id}", s.handleDeleteUser)
			a.Put("/users/{id}/password", s.handleSetUserPassword)
			a.Get("/settings/openrouter", s.handleGetOpenRouterSettings)
			a.Put("/settings/openrouter", s.handleUpdateOpenRouterSettings)
		})
	})

	// The embedded single-page app serves everything else. Unknown paths fall back to
	// index.html so client-side routing works. API 404s are kept as JSON.
	spa := web.Handler()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeCode(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		spa.ServeHTTP(w, r)
	})

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "service": serviceName})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": serviceName})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": serviceName, "apiVersion": "v1"})
}

func (s *Server) handleOpenAPIYAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(api.OpenAPIYAML)
}

func (s *Server) handleOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	doc, err := api.OpenAPIJSON()
	if err != nil {
		s.writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(doc)
}
