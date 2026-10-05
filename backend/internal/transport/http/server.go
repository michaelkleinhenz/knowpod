package http

import (
	"cmp"
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
	// notifications sends task reminders to users' browsers.
	notifications *service.NotificationService
	filters       *service.FilterService
	times         *service.TimeService
	calendar      *service.CalendarService
	// mcp signs in the AI assistants using the MCP server.
	mcp *service.MCPAccessService
	// oauth lets AI assistants connect to the MCP server through OAuth.
	oauth *service.OAuthService
	// events tells the web app about note changes as they happen.
	events *service.NoteEvents
	// ask answers questions about the user's notes.
	ask *service.AskService
	// briefings makes the users' daily and weekly briefings.
	briefings *service.BriefingService
	// backup makes and restores full backups.
	backup *service.BackupService
	// personalBackup makes and restores a user's own backup.
	personalBackup *service.PersonalBackupService
	version        string
	now            func() time.Time
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
	// Notifications is optional in tests that don't use it.
	Notifications *service.NotificationService
	// Filters, Times and Calendar are optional in tests that don't use them.
	Filters  *service.FilterService
	Times    *service.TimeService
	Calendar *service.CalendarService
	// MCP is optional in tests that don't use it.
	MCP *service.MCPAccessService
	// OAuth is optional in tests that don't use it; without it the OAuth endpoints are off.
	OAuth *service.OAuthService
	// Events is optional in tests that don't use it; without it live updates are off.
	Events *service.NoteEvents
	// Ask and Briefings are optional in tests that don't use them.
	Ask       *service.AskService
	Briefings *service.BriefingService
	// Backup is optional in tests that don't use it.
	Backup *service.BackupService
	// PersonalBackup is optional in tests that don't use it.
	PersonalBackup *service.PersonalBackupService
	// Version is the app version, reported by the MCP server.
	Version string
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
		labels: d.Labels, folders: d.Folders, remarkable: d.Remarkable, notifications: d.Notifications,
		filters: d.Filters, times: d.Times, calendar: d.Calendar, mcp: d.MCP, oauth: d.OAuth, events: d.Events,
		ask: d.Ask, briefings: d.Briefings, backup: d.Backup, personalBackup: d.PersonalBackup, version: cmp.Or(d.Version, "dev"),
		now: time.Now,
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
		// The web app's origins; AI assistants running in a browser may call the MCP server
		// and the OAuth endpoints from anywhere.
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			return origin == s.cfg.FrontendURL || origin == "http://localhost:5173" || oauthPublicPath(r.URL.Path)
		},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", uploadOffsetHeader, "Mcp-Protocol-Version"},
		ExposedHeaders:   []string{uploadOffsetHeader, "WWW-Authenticate"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", s.handleHealth)

	// Things shared with the installed app are posted here. The app's service worker takes
	// them; a post that reaches the server (the service worker wasn't running) goes to the
	// share page, which says so. The page itself is the app's, like every other page.
	spa := web.Handler()
	r.Post("/share", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/share?missed=1", http.StatusSeeOther)
	})
	r.Get("/share", spa.ServeHTTP)

	// --- MCP server for AI assistants (the user's MCP access token, or OAuth) ---
	r.Post(service.MCPPath, s.handleMCP)
	r.Get(service.MCPPath, s.handleMCPNotAllowed)
	r.Delete(service.MCPPath, s.handleMCPNotAllowed)

	// --- OAuth for the MCP server (the consent page at /oauth/authorize is the web app's) ---
	r.Get(oauthProtectedResourcePath, s.handleProtectedResourceMetadata)
	r.Get(oauthProtectedResourcePath+service.MCPPath, s.handleProtectedResourceMetadata)
	r.Get(oauthServerMetadataPath, s.handleAuthServerMetadata)
	r.With(httprate.LimitByIP(20, time.Hour)).Post(oauthRegisterPath, s.handleOAuthRegister)
	r.With(httprate.LimitByIP(60, time.Minute)).Post(oauthTokenPath, s.handleOAuthToken)
	r.With(httprate.LimitByIP(60, time.Minute)).Post(oauthRevokePath, s.handleOAuthRevoke)

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

		// --- calendar feeds (authenticated by the secret in the link) ---
		api.With(httprate.LimitByIP(60, time.Minute)).Get("/calendar/{token}.ics", s.handleCalendarFeed)

		// --- published notes (authenticated by the secret in the link) ---
		api.With(httprate.LimitByIP(120, time.Minute)).Get("/public/{token}", s.handlePublicNote)
		api.With(httprate.LimitByIP(600, time.Minute)).Get("/public/{token}/images/{imageId}", s.handlePublicImage)

		// --- the signed-in user's own data (session, or ADMIN_TOKEN for everyone's) ---
		api.Group(func(u chi.Router) {
			u.Use(s.requireUser)
			u.Get("/auth/me", s.handleMe)
			u.Put("/auth/password", s.handleChangePassword)
			u.Get("/me/pocket", s.handleGetPocketSettings)
			u.Put("/me/pocket", s.handleUpdatePocketSettings)
			u.Post("/me/pocket/device/check", s.handleNewPocketDeviceFiles)
			u.Post("/me/pocket/device/files", s.handleImportPocketDeviceFile)
			u.Get("/me/remarkable", s.handleGetRemarkable)
			u.Patch("/me/remarkable", s.handleUpdateRemarkable)
			u.Delete("/me/remarkable", s.handleUnpairRemarkable)
			u.Post("/me/remarkable/pair", s.handlePairRemarkable)
			u.Post("/me/remarkable/pull", s.handlePullRemarkable)
			u.Put("/me/preferences", s.handleUpdatePreferences)
			u.Get("/me/notifications", s.handleNotificationStatus)
			u.Post("/me/notifications/subscriptions", s.handleSubscribePush)
			u.Delete("/me/notifications/subscriptions/{id}", s.handleUnsubscribePush)
			u.Post("/me/notifications/test", s.handleTestNotification)
			u.Get("/me/notifications/stream", s.handleNotificationStream)
			u.Get("/me/events", s.handleNoteEvents)
			u.Get("/me/people", s.handlePeople)
			u.With(httprate.LimitByIP(6, time.Hour)).Get("/me/backup", s.handlePersonalBackup)
			u.With(httprate.LimitByIP(6, time.Hour)).Post("/me/restore", s.handlePersonalRestore)
			u.Get("/me/calendar", s.handleGetCalendar)
			u.Post("/me/calendar", s.handleEnableCalendar)
			u.Delete("/me/calendar", s.handleDisableCalendar)
			u.Get("/me/briefing", s.handleGetBriefing)
			u.Put("/me/briefing", s.handleUpdateBriefing)
			u.With(httprate.LimitByIP(10, time.Minute)).Post("/me/briefing/run", s.handleMakeBriefing)
			u.Get("/me/briefing/today", s.handleTodayBriefing)
			u.With(httprate.LimitByIP(10, time.Minute)).Post("/me/briefing/today", s.handleRemakeTodayBriefing)
			u.Get("/me/mcp", s.handleGetMCP)
			u.Post("/me/mcp", s.handleEnableMCP)
			u.Delete("/me/mcp", s.handleDisableMCP)
			u.Get("/me/mcp/apps", s.handleListMCPApps)
			u.Delete("/me/mcp/apps/{id}", s.handleDisconnectMCPApp)
			u.Get("/oauth/authorize", s.handleOAuthAuthorizeInfo)
			u.Post("/oauth/authorize", s.handleOAuthAuthorize)
			u.Get("/ai/status", s.handleAIStatus)
			u.Get("/ai/models", s.handleOpenRouterModels)
			u.Get("/ai/languages", s.handleSummaryLanguages)
			u.With(httprate.LimitByIP(30, time.Minute)).Post("/ask", s.handleAsk)
			u.With(httprate.LimitByIP(30, time.Minute)).Post("/ai/write", s.handleAIWrite)

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
			u.Put("/folders/order", s.handleReorderFolders)
			u.Put("/folders/{id}", s.handleUpdateFolder)
			u.Post("/folders/{id}/duplicate", s.handleDuplicateFolder)
			u.Delete("/folders/{id}", s.handleDeleteFolder)
			u.Get("/folders/{id}/shares", s.handleGetFolderSharing)
			u.Post("/folders/{id}/shares", s.handleShareFolder)
			u.Put("/folders/{id}/shares/{userId}", s.handleSetFolderShareRole)
			u.Delete("/folders/{id}/shares/{userId}", s.handleUnshareFolder)

			u.Get("/filters", s.handleListFilters)
			u.Post("/filters", s.handleCreateFilter)
			u.Put("/filters/{id}", s.handleUpdateFilter)
			u.Delete("/filters/{id}", s.handleDeleteFilter)

			u.Get("/timer", s.handleGetTimer)
			u.Post("/timer", s.handleStartTimer)
			u.Delete("/timer", s.handleStopTimer)
			u.Get("/time-entries", s.handleListTimeEntries)
			u.Post("/time-entries", s.handleCreateTimeEntry)
			u.Get("/time-entries/export", s.handleExportTimeEntries)
			u.Delete("/time-entries/{id}", s.handleDeleteTimeEntry)

			u.Get("/devices", s.handleListDevices)
			u.Post("/devices", s.handleRegisterDevice)
			u.Delete("/devices/{id}", s.handleRevokeDevice)
			u.Post("/devices/{id}/token", s.handleRotateDeviceToken)

			u.Get("/recordings", s.handleListRecordings)
			u.Post("/recordings", s.handleUploadRecording)
			u.Post("/recordings/text", s.handleCreateTextNote)
			u.Post("/recordings/board", s.handleCreateBoard)
			u.Delete("/recordings/trash", s.handleEmptyTrash)
			u.Put("/recordings/order", s.handleReorderNotes)
			u.Get("/recordings/{id}", s.handleGetRecording)
			u.Delete("/recordings/{id}", s.handleDeleteRecording)
			u.Post("/recordings/{id}/restore", s.handleRestoreRecording)
			u.Post("/recordings/{id}/duplicate", s.handleDuplicateRecording)
			u.Get("/recordings/{id}/audio", s.handleRecordingAudio)
			u.Get("/recordings/{id}/file", s.handleRecordingFile)
			u.Post("/recordings/{id}/images", s.handleAddImage)
			u.Get("/recordings/{id}/images/{imageId}", s.handleGetImage)
			u.Post("/recordings/{id}/attachments", s.handleAddAttachment)
			u.Get("/recordings/{id}/attachments/{attachmentId}", s.handleGetAttachment)
			u.Delete("/recordings/{id}/attachments/{attachmentId}", s.handleDeleteAttachment)
			u.Post("/recordings/{id}/retranscribe", s.handleRetranscribe)
			u.Post("/recordings/{id}/resummarize", s.handleResummarize)
			u.Put("/recordings/{id}/summary", s.handleEditSummary)
			u.Put("/recordings/{id}/labels", s.handleSetNoteLabels)
			u.Put("/recordings/{id}/done", s.handleSetNoteDone)
			u.Put("/recordings/{id}/due", s.handleSetNoteDue)
			u.Put("/recordings/{id}/priority", s.handleSetNotePriority)
			u.Put("/recordings/{id}/assignee", s.handleSetNoteAssignee)
			u.Put("/recordings/{id}/estimate", s.handleSetNoteEstimate)
			u.Post("/recordings/{id}/action-items/{itemId}/task", s.handleCreateActionItemTask)
			u.Put("/recordings/{id}/action-items/{itemId}/dismissed", s.handleDismissActionItem)
			u.Put("/recordings/{id}/folder", s.handleSetNoteFolder)
			u.Put("/recordings/{id}/parent", s.handleSetNoteParent)
			u.Put("/recordings/{id}/board", s.handleSetBoard)
			u.Get("/recordings/{id}/shares", s.handleGetSharing)
			u.Post("/recordings/{id}/shares", s.handleShareNote)
			u.Put("/recordings/{id}/shares/{userId}", s.handleSetShareRole)
			u.Delete("/recordings/{id}/shares/{userId}", s.handleUnshareNote)
			u.Get("/recordings/{id}/summary", s.handleDownloadSummary)
			u.Get("/recordings/{id}/transcript", s.handleDownloadTranscript)
			u.Post("/recordings/{id}/speakers/rename", s.handleRenameSpeaker)
			u.Put("/recordings/{id}/speakers", s.handleNameSpeakers)
			u.Get("/recordings/{id}/versions", s.handleListVersions)
			u.Get("/recordings/{id}/versions/{versionId}", s.handleGetVersion)
			u.Post("/recordings/{id}/versions/{versionId}/restore", s.handleRestoreVersion)
			u.Put("/recordings/{id}/template", s.handleSetTemplate)
			u.Put("/recordings/{id}/public", s.handlePublishNote)
			u.Delete("/recordings/{id}/public", s.handleUnpublishNote)
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
			a.With(httprate.LimitByIP(20, time.Hour)).Post("/settings/elevenlabs/test", s.handleTestElevenLabs)
			a.With(httprate.LimitByIP(6, time.Hour)).Get("/backup", s.handleBackup)
			a.With(httprate.LimitByIP(6, time.Hour)).Post("/restore", s.handleRestore)
		})
	})

	// The embedded single-page app serves everything else. Unknown paths fall back to
	// index.html so client-side routing works. API 404s are kept as JSON.
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
