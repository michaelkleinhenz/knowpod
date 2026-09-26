// Package web embeds the built frontend single-page application into the backend binary and
// serves it. The frontend is built separately (see the root Dockerfile / Makefile) and its
// output is copied into the dist/ directory before the Go binary is compiled, so a single
// binary contains the entire web UI and its API.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

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
