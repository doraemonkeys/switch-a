package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const serverCodexTestAuthorization = "Bearer server-test-client"

func TestHandleProxy(t *testing.T) {
	s := testServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleProxy(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// TestProxyRouteRegistration_Grok drives the registered mux (not the handler
// directly) so a missing route registration surfaces as the catch-all 404
// instead of the proxy's 503 for an empty provider store.
func TestProxyRouteRegistration_Grok(t *testing.T) {
	s := testServer(t)

	for _, path := range []string{"/chat/completions", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok-4"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

// TestProxyRouteRegistration_CodexWebSearch pins both Codex base-URL shapes:
// clients append alpha/search to either the gateway root or an optional /v1
// base. GET remains unregistered because the upstream contract is POST-only.
func TestProxyRouteRegistration_CodexWebSearch(t *testing.T) {
	s := testServer(t)

	for _, path := range []string{"/alpha/search", "/v1/alpha/search"} {
		t.Run("POST "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", serverCodexTestAuthorization)
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
		})

		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

// TestProxyRouteRegistration_CodexResponsesCompact pins the two base-URL
// shapes used by Responses clients. Compaction is an HTTP-only POST contract,
// unlike the GET /responses route reserved for WebSocket upgrades.
func TestProxyRouteRegistration_CodexResponsesCompact(t *testing.T) {
	s := testServer(t)

	for _, path := range []string{"/responses/compact", "/v1/responses/compact"} {
		t.Run("POST "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", serverCodexTestAuthorization)
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
		})

		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

// TestProxyRouteRegistration_APINamespaces verifies the explicit namespace
// routes reach the proxy handler for both methods, again via the real mux.
func TestProxyRouteRegistration_APINamespaces(t *testing.T) {
	s := testServer(t)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/claude/v1/messages"},
		{http.MethodGet, "/claude/v1/models"},
		{http.MethodPost, "/codex/responses"},
		{http.MethodPost, "/codex/responses/compact"},
		{http.MethodPost, "/codex/alpha/search"},
		// GET model discovery must proxy through (503 on the empty store), not
		// hit the 426 rule reserved for the WebSocket-only /responses endpoint.
		{http.MethodGet, "/codex/v1/models"},
		{http.MethodPost, "/grok/chat/completions"},
		{http.MethodPost, "/grok/v1/chat/completions"},
		{http.MethodGet, "/grok/v1/models"},
		{http.MethodPost, "/gemini/v1beta/models/gemini-pro:generateContent"},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"model":"test"}`))
			req.Header.Set("Content-Type", "application/json")
			if strings.HasPrefix(test.path, "/codex/") {
				req.Header.Set("Authorization", serverCodexTestAuthorization)
			}
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

// TestProxyRouteRegistration_GrokWebSocketUpgrade pins the websocket-upgrade
// contract on grok paths through the real mux: bare contract paths register
// POST only, so a genuine RFC 6455 upgrade (always GET) dies at the mux
// catch-all with 404, while the namespaced form reaches the proxy handler and
// is rejected there with the diagnostic 400.
func TestProxyRouteRegistration_GrokWebSocketUpgrade(t *testing.T) {
	s := testServer(t)

	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/chat/completions", wantStatus: http.StatusNotFound},
		{path: "/v1/chat/completions", wantStatus: http.StatusNotFound},
		{path: "/grok/chat/completions", wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, test.wantStatus)
			}
		})
	}
}
