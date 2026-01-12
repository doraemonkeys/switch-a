package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDistFS(t *testing.T) {
	fsys := DistFS()
	if fsys == nil {
		t.Fatal("DistFS() returned nil")
	}

	// Verify we can read index.html
	content, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}

	if len(content) == 0 {
		t.Error("index.html is empty")
	}

	if !strings.Contains(string(content), "<!doctype html>") {
		t.Error("index.html does not contain expected doctype")
	}
}

func TestDistFS_AssetsExist(t *testing.T) {
	fsys := DistFS()
	if fsys == nil {
		t.Fatal("DistFS() returned nil")
	}

	// Verify assets directory exists
	entries, err := fs.ReadDir(fsys, "assets")
	if err != nil {
		t.Fatalf("failed to read assets directory: %v", err)
	}

	if len(entries) == 0 {
		t.Error("assets directory is empty")
	}

	// Check for CSS and JS files
	var hasCSS, hasJS bool
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".css") {
			hasCSS = true
		}
		if strings.HasSuffix(entry.Name(), ".js") {
			hasJS = true
		}
	}

	if !hasCSS {
		t.Error("no CSS files found in assets")
	}
	if !hasJS {
		t.Error("no JS files found in assets")
	}
}

func TestHandler(t *testing.T) {
	h := Handler()
	if h == nil {
		t.Fatal("Handler() returned nil")
	}

	// Handler should be a *spaHandler (wrapped from DistFS)
	_, ok := h.(*spaHandler)
	if !ok {
		// Could be the error handler if frontend not built
		// Let's test it responds
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// Should get some response
		if w.Code == 0 {
			t.Error("handler did not set status code")
		}
	}
}

func TestHandler_ServeIndex(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!doctype html>") {
		t.Error("response does not contain expected HTML doctype")
	}
}

func TestHandler_ServeStaticFile(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/vite.svg", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "svg") && !strings.Contains(contentType, "xml") {
		t.Errorf("Content-Type = %q, want svg or xml", contentType)
	}
}

func TestHandler_ServeAssets(t *testing.T) {
	h := Handler()

	// Get the actual asset filenames from DistFS
	fsys := DistFS()
	entries, err := fs.ReadDir(fsys, "assets")
	if err != nil {
		t.Fatalf("failed to read assets: %v", err)
	}

	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/assets/"+entry.Name(), nil)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for %s", w.Code, http.StatusOK, entry.Name())
			}

			if w.Body.Len() == 0 {
				t.Errorf("empty response body for %s", entry.Name())
			}
		})
	}
}

func TestHandler_SPAFallback(t *testing.T) {
	h := Handler()

	// These paths don't exist as static files, should fallback to index.html
	testPaths := []string{
		"/dashboard",
		"/providers",
		"/groups",
		"/config",
		"/login",
		"/some/deep/nested/route",
	}

	for _, path := range testPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}

			contentType := w.Header().Get("Content-Type")
			if !strings.Contains(contentType, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", contentType)
			}

			body := w.Body.String()
			if !strings.Contains(body, "<!doctype html>") {
				t.Error("SPA fallback did not serve index.html")
			}
		})
	}
}

func TestHandler_CleanPath(t *testing.T) {
	h := Handler()

	// Test path cleaning (e.g., double slashes, dot segments)
	testCases := []struct {
		name string
		path string
	}{
		{"double slash", "//vite.svg"},
		{"trailing slash root", "/"},
		{"dot segment", "/./vite.svg"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			// Should handle gracefully (either serve file or SPA fallback)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for path %q", w.Code, http.StatusOK, tc.path)
			}
		})
	}
}

// TestSPAHandler_WithMockFS tests the spaHandler with a mock filesystem
func TestSPAHandler_WithMockFS(t *testing.T) {
	mockFS := fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><html><body>Mock Index</body></html>")},
		"style.css":        {Data: []byte("body { margin: 0; }")},
		"app.js":           {Data: []byte("console.log('app');")},
		"subdir/page.html": {Data: []byte("<html>subpage</html>")},
	}

	h := &spaHandler{fs: mockFS}

	t.Run("serve root returns index.html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}

		if !strings.Contains(w.Body.String(), "Mock Index") {
			t.Error("expected mock index.html content")
		}
	})

	t.Run("serve existing file", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("serve file in subdirectory", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/subdir/page.html", nil)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("non-existent file falls back to index.html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}

		if !strings.Contains(w.Body.String(), "Mock Index") {
			t.Error("expected SPA fallback to index.html")
		}
	})
}

// TestSPAHandler_DirectoryHandling tests directory access behavior
func TestSPAHandler_DirectoryHandling(t *testing.T) {
	mockFS := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html>root index")},
		"subdir/index.html":    {Data: []byte("<!doctype html>subdir index")},
		"emptydir/placeholder": {Data: []byte("")},
	}

	h := &spaHandler{fs: mockFS}

	t.Run("directory with index.html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/subdir", nil)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		// http.FileServer returns 301 redirect for directories without trailing slash
		// Accept either redirect or OK
		if w.Code != http.StatusOK && w.Code != http.StatusMovedPermanently {
			t.Errorf("status = %d, want %d or %d", w.Code, http.StatusOK, http.StatusMovedPermanently)
		}
	})

	t.Run("directory without index.html falls back", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/emptydir", nil)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}

		// Should fall back to root index.html
		if !strings.Contains(w.Body.String(), "root index") {
			t.Error("expected fallback to root index.html")
		}
	})
}

// TestSPAHandler_ServeIndexError tests error handling when index.html is missing
func TestSPAHandler_ServeIndexError(t *testing.T) {
	// Create a filesystem without index.html
	mockFS := fstest.MapFS{
		"other.html": {Data: []byte("<html>other</html>")},
	}

	h := &spaHandler{fs: mockFS}

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	// Should return 500 when index.html is not found
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	if !strings.Contains(w.Body.String(), "index.html not found") {
		t.Error("expected error message about missing index.html")
	}
}

// TestHandler_NilFS tests Handler behavior when DistFS returns nil
// This simulates the case when frontend is not built
func TestSPAHandler_ContentTypeForIndex(t *testing.T) {
	mockFS := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html></html>")},
	}

	h := &spaHandler{fs: mockFS}

	req := httptest.NewRequest(http.MethodGet, "/some-spa-route", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", contentType)
	}
}
