// Package config loads all runtime configuration from environment variables only.
package config

import (
	"os"
)

// Config is the fully-resolved application configuration.
type Config struct {
	Port        string
	MongoURI    string
	MongoDB     string
	FrontendURL string // used for CORS when the SPA is served by the Vite dev server
}

// Load reads configuration from the environment, applying sensible development defaults.
func Load() Config {
	return Config{
		Port:        env("PORT", "8080"),
		MongoURI:    env("MONGO_URI", "mongodb://localhost:27017/?replicaSet=rs0"),
		MongoDB:     env("MONGO_DB", "knowpod"),
		FrontendURL: env("FRONTEND_URL", "http://localhost:5173"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
