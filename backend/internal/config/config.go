// Package config loads all runtime configuration from environment variables only. AWS
// credentials and region are read by the AWS SDK itself from the standard AWS_* variables.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved application configuration.
type Config struct {
	Port        string
	MongoURI    string
	MongoDB     string
	FrontendURL string // used for CORS when the SPA is served by the Vite dev server

	// Web UI sign-in. AdminEmail/AdminPassword are the default login; once the password is
	// changed in the UI, the stored one replaces AdminPassword.
	AdminEmail    string
	AdminPassword string
	SessionTTL    time.Duration

	// AdminToken lets scripts use the admin API without signing in; disabled when empty.
	AdminToken string

	// Object storage (S3). Credentials and region (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
	// AWS_SESSION_TOKEN, AWS_DEFAULT_REGION, AWS_PROFILE, …) are read by the AWS SDK's
	// default chain, not here.
	S3Bucket string
	S3Prefix string

	// PocketAPIURL is the Pocket (heypocketai.com) API; each user sets their own webhook
	// secret and API key in the web UI.
	PocketAPIURL string

	// reMarkable cloud endpoints (empty: the public cloud) and how often paired accounts are
	// checked for new documents (0 disables the automatic pull).
	RemarkableAuthURL      string
	RemarkableSyncURL      string
	RemarkablePullInterval time.Duration

	// OpenRouterAPIURL is the OpenRouter endpoint (the API key and models are set in the UI).
	OpenRouterAPIURL string

	// WebPushSubject identifies the service to browser push services (a mailto: or https:
	// URL); empty uses mailto:ADMIN_EMAIL.
	WebPushSubject string

	// Uploads.
	UploadDir       string        // local spool for in-flight and not yet archived WAV files
	MaxUploadBytes  int64         // largest accepted WAV file
	UploadTTL       time.Duration // incomplete uploads idle longer than this are purged
	KeepOriginalWAV bool          // also archive the original WAV next to the FLAC

	// Background processing.
	WorkerPollInterval time.Duration
	WorkerMaxAttempts  int
}

// Load reads configuration from the environment, applying sensible development defaults.
func Load() Config {
	return Config{
		Port:                   env("PORT", "8080"),
		MongoURI:               env("MONGO_URI", "mongodb://localhost:27017/?replicaSet=rs0"),
		MongoDB:                env("MONGO_DATABASE", "knowpod"),
		FrontendURL:            env("FRONTEND_URL", "http://localhost:5173"),
		AdminEmail:             env("ADMIN_EMAIL", ""),
		AdminPassword:          env("ADMIN_PASSWORD", ""),
		SessionTTL:             envDuration("SESSION_TTL", 7*24*time.Hour),
		AdminToken:             env("ADMIN_TOKEN", ""),
		S3Bucket:               env("AWS_S3_BUCKET_NAME", ""),
		S3Prefix:               env("AWS_S3_PREFIX", ""),
		PocketAPIURL:           env("POCKET_API_URL", "https://public.heypocketai.com/api/v1"),
		OpenRouterAPIURL:       env("OPENROUTER_API_URL", "https://openrouter.ai/api/v1"),
		WebPushSubject:         env("WEBPUSH_SUBJECT", ""),
		RemarkableAuthURL:      env("REMARKABLE_AUTH_URL", ""),
		RemarkableSyncURL:      env("REMARKABLE_SYNC_URL", ""),
		RemarkablePullInterval: envDuration("REMARKABLE_PULL_INTERVAL", 15*time.Minute),
		UploadDir:              env("UPLOAD_DIR", "./data/uploads"),
		MaxUploadBytes:         envInt64("MAX_UPLOAD_BYTES", 4<<30), // WAV's 32-bit sizes cap files at 4 GiB
		UploadTTL:              envDuration("UPLOAD_TTL", 48*time.Hour),
		KeepOriginalWAV:        envBool("KEEP_ORIGINAL_WAV", false),
		WorkerPollInterval:     envDuration("WORKER_POLL_INTERVAL", 10*time.Second),
		WorkerMaxAttempts:      int(envInt64("WORKER_MAX_ATTEMPTS", 5)),
	}
}

// Validate reports missing required settings.
func (c Config) Validate() error {
	var errs []error
	if c.S3Bucket == "" {
		errs = append(errs, errors.New("AWS_S3_BUCKET_NAME is required"))
	}
	if c.MaxUploadBytes <= 0 {
		errs = append(errs, errors.New("MAX_UPLOAD_BYTES must be positive"))
	}
	return errors.Join(errs...)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(key)), 10, 64); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return def
}
