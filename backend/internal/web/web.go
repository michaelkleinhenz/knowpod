// Package web embeds the built frontend single-page application into the backend binary and
// serves it. The frontend is built separately (see the root Dockerfile / Makefile) and its
// output is copied into the dist/ directory before the Go binary is compiled, so a single
// binary contains the entire web UI and its API.
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"strings"
)

func init() {
	// Not in Go's built-in table; browsers expect it for the PWA manifest.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// noCache lists files that must be revalidated on every load: the service worker and its
// registration (so app updates reach installed PWAs) and the manifest. Hashed assets under
// /assets can be cached for good.
var noCache = map[string]bool{"sw.js": true, "registerSW.js": true, "manifest.webmanifest": true}

// dist holds the compiled SPA assets. The all: prefix ensures dot-prefixed files are embedded
// too. A placeholder index.html is committed so the backend builds even without a frontend
// build; the real build overwrites the directory contents.
//
//go:embed all:dist
var dist embed.FS

// assets is the embedded filesystem rooted at dist/.
func assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// dist is a compile-time constant directory; this can only fail if it is missing.
		panic(err)
	}
	return sub
}

// Handler serves the embedded SPA. Existing files (JS/CSS/assets) are served directly; every
// other path falls back to index.html so client-side routing works. API paths are never
// handled here — the caller must route /api and other backend endpoints first.
func Handler() http.Handler {
	fsys := assets()
	fileServer := http.FileServer(http.FS(fsys))

	indexHTML, indexErr := fs.ReadFile(fsys, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}

		// Serve a real asset when it exists.
		if name != "index.html" {
			if f, err := fsys.Open(name); err == nil {
				_ = f.Close()
				switch {
				case noCache[name] || strings.HasPrefix(name, "workbox-"):
					w.Header().Set("Cache-Control", "no-cache")
				case strings.HasPrefix(name, "assets/"):
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: serve index.html for unknown paths (client-side routes).
		if indexErr != nil {
			http.Error(w, "web UI not built", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	})
}
