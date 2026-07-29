package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const testIndexHTML = "<!doctype html><html><body>Test Index</body></html>"

func testFrontendFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte(testIndexHTML)},
		"vite.svg":       {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		"assets/app.css": {Data: []byte("body { margin: 0; }")},
		"assets/app.js":  {Data: []byte("console.log('app');")},
	}
}

func newTestSPAHandler(t *testing.T) *spaHandler {
	t.Helper()

	handler := newSPAHandler(testFrontendFS())
	if handler.assets == nil {
		t.Fatal("test frontend fixture is missing its entry point")
	}
	return handler
}

func TestDistFSIncludesBuildSentinel(t *testing.T) {
	content, err := fs.ReadFile(DistFS(), "PLACEHOLDER.txt")
	if err != nil {
		t.Fatalf("read build sentinel: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("build sentinel is empty")
	}
}

func TestHandler(t *testing.T) {
	h := Handler()
	if h == nil {
		t.Fatal("Handler() returned nil")
	}

	if _, ok := h.(*spaHandler); !ok {
		t.Fatalf("Handler() type = %T, want *spaHandler", h)
	}
}

func TestSPAHandler_FrontendUnavailable(t *testing.T) {
	tests := map[string]fs.FS{
		"nil filesystem":  nil,
		"missing index":   fstest.MapFS{"PLACEHOLDER.txt": {Data: []byte("sentinel")}},
		"index directory": fstest.MapFS{"index.html": {Mode: fs.ModeDir}},
	}

	for name, assets := range tests {
		t.Run(name, func(t *testing.T) {
			handler := newSPAHandler(assets)
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
			}
			if !strings.Contains(response.Body.String(), frontendUnavailableMessage) {
				t.Fatalf("body = %q, want unavailable message", response.Body.String())
			}
		})
	}
}

func TestHandler_ServeIndex(t *testing.T) {
	h := newTestSPAHandler(t)

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
	h := newTestSPAHandler(t)

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
	h := newTestSPAHandler(t)

	for _, assetPath := range []string{"/assets/app.css", "/assets/app.js"} {
		t.Run(assetPath, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, assetPath, nil)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for %s", w.Code, http.StatusOK, assetPath)
			}

			if w.Body.Len() == 0 {
				t.Errorf("empty response body for %s", assetPath)
			}
		})
	}
}

func TestHandler_SPAFallback(t *testing.T) {
	h := newTestSPAHandler(t)

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
	h := newTestSPAHandler(t)

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

	h := newSPAHandler(mockFS)

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

	h := newSPAHandler(mockFS)

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
	mockFS := fstest.MapFS{
		"index.html": {Data: []byte(testIndexHTML)},
		"other.html": {Data: []byte("<html>other</html>")},
	}

	h := newSPAHandler(mockFS)
	// Removing the entry point after initialization simulates a corrupted or
	// partially replaced asset tree without weakening constructor validation.
	delete(mockFS, frontendIndexPath)

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

func TestSPAHandler_ContentTypeForIndex(t *testing.T) {
	mockFS := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html></html>")},
	}

	h := newSPAHandler(mockFS)

	req := httptest.NewRequest(http.MethodGet, "/some-spa-route", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", contentType)
	}
}
