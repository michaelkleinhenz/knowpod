// Command server is the knowpod-service backend entrypoint. It wires configuration, MongoDB
// and the HTTP API (with the embedded web UI), then serves until interrupted.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	repo "github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/mongo"
	httpx "github.com/michaelkleinhenz/knowpod-service/backend/internal/transport/http"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := config.Load()
	ctx := context.Background()

	store, err := repo.Connect(ctx, cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		log.Error("mongo connect failed", "err", err)
		os.Exit(1)
	}
	defer store.Disconnect(ctx)

	if err := repo.Setup(ctx, store.DB()); err != nil {
		log.Error("mongo setup (collections/indexes) failed", "err", err)
		os.Exit(1)
	}
	log.Info("database ready", "db", cfg.MongoDB)

	srv := httpx.NewServer(httpx.Deps{Cfg: cfg, Log: log, DB: store})
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("server listening", "port", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
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
}
