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
	cfg        config.Config
	log        *slog.Logger
	db         Pinger
	auth       *service.AuthService
	devices    *service.DeviceService
	uploads    *service.UploadService
	recordings ports.RecordingRepository
	objects    ports.ObjectStore
}

// Deps are the server's constructor dependencies.
type Deps struct {
	Cfg        config.Config
	Log        *slog.Logger
	DB         Pinger // optional; when set, /healthz also checks database connectivity
	Auth       *service.AuthService
	Devices    *service.DeviceService
	Uploads    *service.UploadService
	Recordings ports.RecordingRepository
	Objects    ports.ObjectStore
}

// NewServer builds the server.
func NewServer(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg: d.Cfg, log: log, db: d.DB, auth: d.Auth, devices: d.Devices, uploads: d.Uploads,
		recordings: d.Recordings, objects: d.Objects,
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

		// --- web UI sign-in (session cookie) ---
		api.With(httprate.LimitByIP(10, time.Minute)).Post("/auth/login", s.handleLogin)
		api.Post("/auth/logout", s.handleLogout)
		api.Group(func(u chi.Router) {
			u.Use(s.requireUser)
			u.Get("/auth/me", s.handleMe)
			u.Put("/auth/password", s.handleChangePassword)
		})

		// --- device API: recorders push recordings (device token) ---
		api.Group(func(d chi.Router) {
			d.Use(s.requireDevice)
			d.Post("/uploads", s.handleCreateUpload)
			d.Get("/uploads/{id}", s.handleGetUpload)
			d.Patch("/uploads/{id}", s.handleAppendUpload)
		})

		// --- admin API: device provisioning and recording access (session or ADMIN_TOKEN) ---
		api.Route("/admin", func(a chi.Router) {
			a.Use(s.requireAdmin)
			a.Post("/devices", s.handleRegisterDevice)
			a.Get("/devices", s.handleListDevices)
			a.Delete("/devices/{id}", s.handleRevokeDevice)
			a.Get("/recordings", s.handleListRecordings)
			a.Get("/recordings/{id}", s.handleGetRecording)
			a.Get("/recordings/{id}/audio", s.handleRecordingAudio)
		})
	})

	// The embedded single-page app serves everything else. Unknown paths fall back to
	// index.html so client-side routing works. API 404s are kept as JSON.
	spa := web.Handler()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, errResponse{Error: "not found"})
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
