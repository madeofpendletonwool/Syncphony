// SPDX-License-Identifier: AGPL-3.0-only

// Package webui serves the built web app embedded into the binary.
//
// Production builds copy web/dist into ./dist before `go build`
// (see the Makefile and Dockerfile). In development the Vite dev server
// serves the UI instead, and this handler explains that.
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

func init() {
	// Not in Go's built-in table; the web app's manifest needs it.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// Handler serves static assets and falls back to index.html so client-side
// routes work on reload.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	return handler(root)
}

func handler(root fs.FS) http.Handler {
	if _, err := fs.Stat(root, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "web UI not built into this binary; run `make build`, or use the Vite dev server (`make dev`)", http.StatusNotFound)
		})
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(root, name); err != nil {
			// Unknown path: let the SPA router handle it.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		switch {
		case strings.HasPrefix(name, "assets/"):
			// Vite fingerprints everything under assets/.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case name == "sw.js" || name == "index.html" || name == "manifest.webmanifest":
			// Revalidate every time, so a new build reaches people: the
			// service worker caches the shell itself (see web/sw.js).
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
