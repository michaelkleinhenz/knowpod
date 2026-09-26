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

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	repo "github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/mongo"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
	s3store "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/s3"
	httpx "github.com/michaelkleinhenz/knowpod-service/backend/internal/transport/http"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

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
	deviceSvc := service.NewDeviceService(devices)
	uploadSvc := service.NewUploadService(recordings, spool, cfg.MaxUploadBytes)
	archiver := service.NewArchiver(spool, objects, cfg.KeepOriginalWAV, log)
	pocketSvc := service.NewPocketService(recordings, users, pocket.NewClient(cfg.PocketAPIURL), spool, cfg.MaxUploadBytes, log)
	manualSvc := service.NewManualUploadService(recordings, spool, cfg.MaxUploadBytes)
	var wakeAI func() // set below, once the AI worker exists
	pipeline := worker.New(recordings, []worker.Stage{
		{Name: "pocket-fetch", From: recording.StatusRemote, To: recording.StatusReceived, Run: pocketSvc.Fetch},
		{Name: "archive", From: recording.StatusReceived, To: recording.StatusStored, Run: archiver.Run,
			Cleanup: func(rec *recording.Recording) { archiver.Cleanup(rec); wakeAI() }},
	}, worker.Options{PollInterval: cfg.WorkerPollInterval, MaxAttempts: cfg.WorkerMaxAttempts}, log)
	uploadSvc.OnReceived = pipeline.Wake
	pocketSvc.OnQueued = pipeline.Wake
	manualSvc.OnReceived = pipeline.Wake

	// AI processing (transcription, summaries) runs in its own worker so that slow model
	// calls never delay archiving. Its stages wait until OpenRouter is configured.
	aiSvc := service.NewAIService(settingsRepo, objects, openrouter.NewClient(cfg.OpenRouterAPIURL, cfg.FrontendURL), cfg.UploadDir, log)
	aiPipeline := worker.New(recordings, []worker.Stage{
		{Name: "transcribe", From: recording.StatusStored, To: recording.StatusTranscribed,
			Run: aiSvc.Transcribe, Enabled: aiSvc.CanTranscribe, Lease: time.Hour},
		{Name: "summarize", From: recording.StatusTranscribed, To: recording.StatusSummarized,
			Run: aiSvc.Summarize, Enabled: aiSvc.CanSummarize},
	}, worker.Options{PollInterval: cfg.WorkerPollInterval, MaxAttempts: cfg.WorkerMaxAttempts}, log)
	aiSvc.OnSettingsChanged = aiPipeline.Wake
	actions := service.NewRecordingService(recordings, objects, spool)
	userSvc := service.NewUserService(users, sessions, devices, recordings, authSvc, actions)
	actions.OnRequeued = aiPipeline.Wake
	wakeAI = aiPipeline.Wake // archived recordings move on to transcription right away

	jobCtx, jobCancel := context.WithCancel(ctx)
	var jobs sync.WaitGroup
	jobs.Add(3)
	go func() { defer jobs.Done(); pipeline.Start(jobCtx) }()
	go func() { defer jobs.Done(); aiPipeline.Start(jobCtx) }()
	go func() { defer jobs.Done(); purgeStaleUploads(jobCtx, uploadSvc, cfg.UploadTTL, log) }()

	// HTTP server.
	srv := httpx.NewServer(httpx.Deps{
		Cfg: cfg, Log: log, DB: store, Auth: authSvc, Users: userSvc, Devices: deviceSvc, Uploads: uploadSvc,
		Manual: manualSvc, Actions: actions, Objects: objects, Pocket: pocketSvc, AI: aiSvc,
	})
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

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

func fatal(log *slog.Logger, msg string, err error) {
	log.Error(msg, "err", err)
	os.Exit(1)
}
