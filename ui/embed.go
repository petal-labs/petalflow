// Package ui provides embedded static assets for the PetalFlow Designer UI.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var distFS embed.FS

// DistFS returns a filesystem containing the built UI assets.
// The returned filesystem has the dist/ prefix stripped, so files
// can be accessed directly (e.g., "index.html" instead of "dist/index.html").
func DistFS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
