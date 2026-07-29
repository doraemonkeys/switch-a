// Package web provides the embedded frontend static files.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const (
	distDirectory              = "dist"
	frontendIndexPath          = "index.html"
	frontendUnavailableMessage = "Frontend not built. Run: cd web && pnpm build"
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
// The source-controlled sentinel keeps the embed valid in clean checkouts; the
// handler separately verifies that a real frontend entry point was built.
func DistFS() fs.FS {
	sub, err := fs.Sub(dist, distDirectory)
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
	return newSPAHandler(DistFS())
}

// spaHandler serves static files with SPA fallback support.
type spaHandler struct {
	assets fs.FS
}

func newSPAHandler(assets fs.FS) *spaHandler {
	if assets == nil {
		return &spaHandler{}
	}

	index, err := fs.Stat(assets, frontendIndexPath)
	if err != nil || index.IsDir() {
		// A sentinel-only tree is intentional in source checkouts, but it must
		// not masquerade as a successfully built frontend at runtime.
		return &spaHandler{}
	}

	return &spaHandler{assets: assets}
}

// ServeHTTP implements http.Handler.
// It serves static files from the embedded filesystem.
// For paths that don't exist (likely client-side routes), it serves index.html.
func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.assets == nil {
		http.Error(w, frontendUnavailableMessage, http.StatusServiceUnavailable)
		return
	}

	// Clean and normalize the path
	urlPath := path.Clean(r.URL.Path)
	if urlPath == "/" {
		urlPath = "/" + frontendIndexPath
	}

	// Remove leading slash for fs operations
	filePath := strings.TrimPrefix(urlPath, "/")

	// Try to open the file
	f, err := h.assets.Open(filePath)
	if err != nil {
		// File doesn't exist, serve index.html for SPA routing
		h.serveIndex(w, r)
		return
	}
	_ = f.Close()

	// Check if it's a directory
	stat, err := fs.Stat(h.assets, filePath)
	if err != nil {
		h.serveIndex(w, r)
		return
	}

	if stat.IsDir() {
		// Try to serve index.html in the directory
		indexPath := path.Join(filePath, frontendIndexPath)
		if _, err := fs.Stat(h.assets, indexPath); err != nil {
			h.serveIndex(w, r)
			return
		}
	}

	// Serve the static file
	http.FileServer(http.FS(h.assets)).ServeHTTP(w, r)
}

// serveIndex serves the root index.html for SPA fallback.
func (h *spaHandler) serveIndex(w http.ResponseWriter, _ *http.Request) {
	content, err := fs.ReadFile(h.assets, frontendIndexPath)
	if err != nil {
		http.Error(w, "index.html not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
