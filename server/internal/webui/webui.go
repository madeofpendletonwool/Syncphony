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
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves static assets and falls back to index.html so client-side
// routes work on reload.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
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
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			// Vite fingerprints everything under assets/.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
