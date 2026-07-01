// Package web provides the embedded frontend static files.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist contains the built frontend static files.
// The files are embedded at compile time using go:embed directive.
// Build the frontend first: cd web && pnpm build
//
// Using "all:" prefix to include hidden files (like .gitkeep) and ensure
// recursive embedding of all files in subdirectories (like assets/).
//
//go:embed all:dist
var dist embed.FS

// DistFS returns the embedded dist filesystem, rooted at "dist".
// Returns nil if dist directory doesn't exist (frontend not built).
func DistFS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	return sub
}

// Handler returns an http.Handler that serves the embedded frontend files.
// It implements SPA (Single Page Application) fallback: any path that doesn't
// match a static file will serve index.html, allowing client-side routing.
//
// The handler expects to be mounted at a specific prefix (e.g., "/admin").
// The stripPrefix parameter should match the mount point to correctly serve files.
//
// Example:
//
//	mux.Handle("/admin/", http.StripPrefix("/admin", web.Handler()))
func Handler() http.Handler {
	fsys := DistFS()
	if fsys == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Frontend not built. Run: cd web && pnpm build", http.StatusServiceUnavailable)
		})
	}
	return &spaHandler{fs: fsys}
}

// spaHandler serves static files with SPA fallback support.
type spaHandler struct {
	fs fs.FS
}

// ServeHTTP implements http.Handler.
// It serves static files from the embedded filesystem.
// For paths that don't exist (likely client-side routes), it serves index.html.
func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean and normalize the path
	urlPath := path.Clean(r.URL.Path)
	if urlPath == "/" {
		urlPath = "/index.html"
	}

	// Remove leading slash for fs operations
	filePath := strings.TrimPrefix(urlPath, "/")

	// Try to open the file
	f, err := h.fs.Open(filePath)
	if err != nil {
		// File doesn't exist, serve index.html for SPA routing
		h.serveIndex(w, r)
		return
	}
	_ = f.Close()

	// Check if it's a directory
	stat, err := fs.Stat(h.fs, filePath)
	if err != nil {
		h.serveIndex(w, r)
		return
	}

	if stat.IsDir() {
		// Try to serve index.html in the directory
		indexPath := path.Join(filePath, "index.html")
		if _, err := fs.Stat(h.fs, indexPath); err != nil {
			h.serveIndex(w, r)
			return
		}
	}

	// Serve the static file
	http.FileServer(http.FS(h.fs)).ServeHTTP(w, r)
}

// serveIndex serves the root index.html for SPA fallback.
func (h *spaHandler) serveIndex(w http.ResponseWriter, _ *http.Request) {
	content, err := fs.ReadFile(h.fs, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
