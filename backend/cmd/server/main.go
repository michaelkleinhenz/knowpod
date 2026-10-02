// Command server is the knowpod-service backend entrypoint. It wires configuration, MongoDB,
// S3 object storage, the upload and processing services and the HTTP API (with the embedded
// web UI), then serves until interrupted.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // users' time zones, also in images without a zoneinfo database

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
	repo "github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/mongo"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
	s3store "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/s3"
	httpx "github.com/michaelkleinhenz/knowpod-service/backend/internal/transport/http"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/webpush"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/worker"
)

// version is the app version, set at build time from the VERSION file
// (-ldflags "-X main.version=…").
var version = "dev"

// reminderInterval is how often due task reminders are looked for; briefingInterval how
// often due briefings are.
const (
	reminderInterval = 30 * time.Second
	briefingInterval = time.Minute
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	log.Info("starting knowpod", "version", version)
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		fatal(log, "invalid configuration", err)
	}
	ctx := context.Background()

	// Database.
	store, err := repo.Connect(ctx, cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		fatal(log, "mongo connect failed", err)
	}
	defer store.Disconnect(ctx)
	if err := repo.Setup(ctx, store.DB()); err != nil {
		fatal(log, "mongo setup (collections/indexes) failed", err)
	}
	log.Info("database ready", "db", cfg.MongoDB)
	recordings := repo.NewRecordingRepo(store)
	devices := repo.NewDeviceRepo(store)
	users := repo.NewUserRepo(store)
	sessions := repo.NewSessionRepo(store)
	settingsRepo := repo.NewSettingsRepo(store)
	themeRepo := repo.NewThemeRepo(store)
	labelRepo := repo.NewLabelRepo(store)
	folderRepo := repo.NewFolderRepo(store)
	tabletRepo := repo.NewTabletLinkRepo(store)
	pushRepo := repo.NewPushSubscriptionRepo(store)
	filterRepo := repo.NewFilterRepo(store)
	timeRepo := repo.NewTimeEntryRepo(store)
	oauthRepo := repo.NewOAuthRepo(store)
	backupRepo := repo.NewBackupRepo(store)
	versionRepo := repo.NewNoteVersionRepo(store)

	// Object storage.
	objects, err := s3store.New(ctx, s3store.Options{
		Bucket: cfg.S3Bucket, Prefix: cfg.S3Prefix,
	})
	if err != nil {
		fatal(log, "s3 setup failed", err)
	}
	if err := objects.Check(ctx); err != nil {
		fatal(log, "s3 check failed", err)
	}
	log.Info("object storage ready", "bucket", cfg.S3Bucket)

	// Upload spool, services and the processing pipeline.
	spool, err := service.NewSpool(cfg.UploadDir)
	if err != nil {
		fatal(log, "upload dir setup failed", err)
	}
	authSvc := service.NewAuthService(users, sessions, cfg.AdminEmail, cfg.AdminPassword, cfg.SessionTTL)
	if cfg.AdminEmail == "" || cfg.AdminPassword == "" {
		log.Warn("ADMIN_EMAIL/ADMIN_PASSWORD not set: only accounts with a stored password can sign in")
	}
	if err := setupBuiltInAdmin(ctx, authSvc, recordings, devices, log); err != nil {
		fatal(log, "built-in admin setup failed", err)
	}
	// Notes from before note numbers get theirs (after ownerless notes got an owner).
	if n, err := recordings.NumberNotes(ctx); err != nil {
		fatal(log, "numbering notes failed", err)
	} else if n > 0 {
		log.Info("numbered existing notes", "notes", n)
	}
	// Every change of a note reaches the open apps of everyone who sees it.
	events := service.NewNoteEvents()
	var recs ports.RecordingRepository = events.Watch(recordings)
	deviceSvc := service.NewDeviceService(devices)
	uploadSvc := service.NewUploadService(recs, spool, cfg.MaxUploadBytes)
	archiver := service.NewArchiver(spool, objects, cfg.KeepOriginalWAV, log)
	pocketSvc := service.NewPocketService(recs, users, folderRepo, pocket.NewClient(cfg.PocketAPIURL), spool, cfg.MaxUploadBytes, log)
	manualSvc := service.NewManualUploadService(recs, spool, cfg.MaxUploadBytes)
	remarkableSvc := service.NewRemarkableService(tabletRepo, recs, folderRepo, objects,
		remarkable.NewClient(cfg.RemarkableAuthURL, cfg.RemarkableSyncURL), spool, cfg.MaxUploadBytes, log)
	var wakeAI func() // set below, once the AI worker exists
	pipeline := worker.New(recs, []worker.Stage{
		// Audio from Pocket and documents from the reMarkable cloud are fetched first.
		{Name: "fetch", From: recording.StatusRemote, To: recording.StatusReceived,
			Run: func(ctx context.Context, rec *recording.Recording) error {
				if rec.Source == recording.SourceRemarkable {
					return remarkableSvc.Fetch(ctx, rec)
				}
				return pocketSvc.Fetch(ctx, rec)
			}},
		{Name: "archive", From: recording.StatusReceived, To: recording.StatusStored,
			Run: func(ctx context.Context, rec *recording.Recording) error {
				if rec.IsDocument() && rec.Source == recording.SourceRemarkable {
					return remarkableSvc.Store(ctx, rec)
				}
				return archiver.Run(ctx, rec)
			},
			Cleanup: func(rec *recording.Recording) { archiver.Cleanup(rec); wakeAI() }},
	}, worker.Options{PollInterval: cfg.WorkerPollInterval, MaxAttempts: cfg.WorkerMaxAttempts}, log)
	uploadSvc.OnReceived = pipeline.Wake
	pocketSvc.OnQueued = pipeline.Wake
	manualSvc.OnReceived = pipeline.Wake
	pocketSvc.Uploads = manualSvc
	remarkableSvc.OnQueued = pipeline.Wake
	// Text notes in the reMarkable folder go to the tablet shortly after they change.
	events.OwnerChanged = remarkableSvc.Nudge

	// AI processing (transcription, summaries) runs in its own worker so that slow model
	// calls never delay archiving. Its stages wait until OpenRouter is configured.
	themeSvc := service.NewThemeService(themeRepo)
	aiSvc := service.NewAIService(settingsRepo, themeSvc, objects, openrouter.NewClient(cfg.OpenRouterAPIURL, cfg.FrontendURL), cfg.UploadDir, log)
	aiPipeline := worker.New(recs, []worker.Stage{
		{Name: "transcribe", From: recording.StatusStored, To: recording.StatusTranscribed,
			Run: aiSvc.Transcribe, Enabled: aiSvc.CanTranscribe, Lease: time.Hour},
		{Name: "summarize", From: recording.StatusTranscribed, To: recording.StatusSummarized,
			Run: aiSvc.Summarize, Enabled: aiSvc.CanSummarize},
	}, worker.Options{PollInterval: cfg.WorkerPollInterval, MaxAttempts: cfg.WorkerMaxAttempts}, log)
	aiSvc.OnSettingsChanged = aiPipeline.Wake
	aiSvc.Users = users
	actions := service.NewRecordingService(recs, objects, spool, themeSvc)
	userSvc := service.NewUserService(users, sessions, devices, recs, themeRepo, authSvc, actions)
	labelSvc := service.NewLabelService(labelRepo, recs)
	actions.Labels = labelSvc
	userSvc.Labels = labelRepo
	folderSvc := service.NewFolderService(folderRepo, recs)
	actions.Folders = folderSvc
	folderSvc.Notes = actions
	folderSvc.Tablets = tabletRepo
	userSvc.Folders = folderRepo
	userSvc.Remarkable = remarkableSvc
	filterSvc := service.NewFilterService(filterRepo, recs)
	actions.Filters = filterSvc
	actions.TimeEntries = timeRepo
	userSvc.Filters = filterRepo
	userSvc.TimeEntries = timeRepo
	timeSvc := service.NewTimeService(timeRepo, recs, users)
	calendarSvc := service.NewCalendarService(users, recs)
	mcpSvc := service.NewMCPAccessService(users)
	oauthSvc := service.NewOAuthService(oauthRepo, users)
	mcpSvc.OAuth = oauthSvc
	userSvc.OAuth = oauthRepo
	userSvc.Push = pushRepo
	actions.Users = users
	authSvc.OnTimeZoneChanged = actions.RescheduleReminders
	sender, err := pushSender(ctx, settingsRepo, cfg)
	if err != nil {
		// Everything else works without notifications.
		log.Error("web push setup failed; notifications are off", "err", err)
	}
	var pusher service.Pusher
	if sender != nil {
		pusher = sender
	}
	notifySvc := service.NewNotificationService(pushRepo, users, recs, pusher, log)
	actions.OnRequeued = aiPipeline.Wake
	actions.Events = events
	actions.Versions = versionRepo
	wakeAI = aiPipeline.Wake // archived recordings move on to transcription right away
	askSvc := service.NewAskService(aiSvc, actions, log)
	briefingSvc := service.NewBriefingService(users, actions, folderRepo, aiSvc, log)
	briefingSvc.Notifications = notifySvc
	briefingSvc.TimeEntries = timeRepo

	backupSvc := service.NewBackupService(backupRepo, objects)
	backupSvc.TempDir = cfg.UploadDir
	backupSvc.Version = version
	personalBackupSvc := service.NewPersonalBackupService(recs, folderRepo, labelRepo, themeRepo, filterRepo, timeRepo, objects, actions)
	personalBackupSvc.TempDir = cfg.UploadDir
	personalBackupSvc.Version = version

	jobCtx, jobCancel := context.WithCancel(ctx)
	var jobs sync.WaitGroup
	jobs.Add(7)
	go func() { defer jobs.Done(); pipeline.Start(jobCtx) }()
	go func() { defer jobs.Done(); aiPipeline.Start(jobCtx) }()
	go func() { defer jobs.Done(); purgeStaleUploads(jobCtx, uploadSvc, cfg.UploadTTL, log) }()
	go func() { defer jobs.Done(); pullRemarkable(jobCtx, remarkableSvc, cfg.RemarkablePullInterval, log) }()
	go func() { defer jobs.Done(); notifySvc.Run(jobCtx, reminderInterval) }()
	go func() { defer jobs.Done(); purgeTrash(jobCtx, actions, log) }()
	go func() { defer jobs.Done(); briefingSvc.Run(jobCtx, briefingInterval) }()

	// HTTP server.
	srv := httpx.NewServer(httpx.Deps{
		Cfg: cfg, Log: log, DB: store, Auth: authSvc, Users: userSvc, Devices: deviceSvc, Uploads: uploadSvc,
		Manual: manualSvc, Actions: actions, Objects: objects, Pocket: pocketSvc, AI: aiSvc, Themes: themeSvc,
		Labels: labelSvc, Folders: folderSvc, Remarkable: remarkableSvc, Notifications: notifySvc,
		Filters: filterSvc, Times: timeSvc, Calendar: calendarSvc, MCP: mcpSvc, OAuth: oauthSvc, Events: events,
		Ask: askSvc, Briefings: briefingSvc, Backup: backupSvc, PersonalBackup: personalBackupSvc, Version: version,
	})
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	// Shutdown waits for requests to finish; live notification streams never would.
	httpServer.RegisterOnShutdown(notifySvc.Shutdown)
	httpServer.RegisterOnShutdown(events.Shutdown)

	go func() {
		log.Info("server listening", "port", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(log, "server error", err)
		}
	}()

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	jobCancel()
	// Sends still waiting happen with the next pull after the restart.
	remarkableSvc.StopNudges()
	jobs.Wait()
}

// setupBuiltInAdmin creates the ADMIN_EMAIL user if needed and gives it the recordings and
// devices that were created before there were users.
func setupBuiltInAdmin(ctx context.Context, auth *service.AuthService, recs *repo.RecordingRepo, devs *repo.DeviceRepo, log *slog.Logger) error {
	admin, err := auth.EnsureBuiltInAdmin(ctx)
	if err != nil || admin == nil {
		return err
	}
	nr, err := recs.AssignOwnerless(ctx, admin.ID)
	if err != nil {
		return err
	}
	nd, err := devs.AssignOwnerless(ctx, admin.ID)
	if err != nil {
		return err
	}
	if nr+nd > 0 {
		log.Info("assigned existing data to the built-in admin", "recordings", nr, "devices", nd)
	}
	return nil
}

// pushSender loads the VAPID keys for Web Push, generating them on the first start.
func pushSender(ctx context.Context, st *repo.SettingsRepo, cfg config.Config) (*webpush.Sender, error) {
	fresh, err := webpush.GenerateKeys()
	if err != nil {
		return nil, err
	}
	keys, err := st.InitWebPush(ctx, &settings.WebPush{PrivateKey: fresh.Private, PublicKey: fresh.Public})
	if err != nil {
		return nil, err
	}
	subject := cfg.WebPushSubject
	if subject == "" && cfg.AdminEmail != "" {
		subject = "mailto:" + cfg.AdminEmail
	}
	if subject == "" {
		subject = "mailto:knowpod@example.com"
	}
	return webpush.NewSender(webpush.Keys{Private: keys.PrivateKey, Public: keys.PublicKey}, subject, nil)
}

// purgeStaleUploads periodically deletes uploads that stopped progressing.
func purgeStaleUploads(ctx context.Context, uploads *service.UploadService, ttl time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := uploads.PurgeStale(ctx, ttl); err != nil {
			log.Error("purging stale uploads failed", "err", err)
		} else if n > 0 {
			log.Info("purged stale uploads", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// purgeTrash periodically deletes the notes that have been in the trash long enough.
func purgeTrash(ctx context.Context, actions *service.RecordingService, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := actions.PurgeTrash(ctx); err != nil {
			log.Error("emptying the trash failed", "err", err)
		} else if n > 0 {
			log.Info("deleted notes from the trash", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pullRemarkable imports new documents from the paired reMarkable accounts periodically.
func pullRemarkable(ctx context.Context, svc *service.RemarkableService, every time.Duration, log *slog.Logger) {
	if every <= 0 {
		log.Info("automatic reMarkable pull disabled")
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			svc.PullAll(ctx)
		}
	}
}

func fatal(log *slog.Logger, msg string, err error) {
	log.Error(msg, "err", err)
	os.Exit(1)
}
