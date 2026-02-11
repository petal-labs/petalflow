// Package ui serves the React SPA from an embedded filesystem.
//
// In production mode, the Go binary embeds ui/dist/ and serves it at /.
// The handler implements SPA fallback: any path that doesn't match a static
// file returns index.html, letting the client-side router handle it.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA.
// It serves static files from dist/ and falls back to index.html
// for paths that don't match a file (SPA client-side routing).
func Handler() http.Handler {
	stripped, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("ui: failed to sub dist: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(stripped))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly.
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Check if the file exists in the embedded FS.
		f, err := stripped.Open(path[1:]) // strip leading /
		if err != nil {
			// File doesn't exist — serve index.html for SPA routing.
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		// File exists — serve it.
		fileServer.ServeHTTP(w, r)
	})
}
