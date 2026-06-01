package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestProxyRoutesCodexImageGeneration(t *testing.T) {
	s := testServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-2","prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
