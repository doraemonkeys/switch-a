package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware_Wrap_ValidToken(t *testing.T) {
	token := "test-token-123"
	m := NewAuthMiddleware(token)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})

	wrapped := m.Wrap(handler)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "success" {
		t.Errorf("body = %q, want %q", w.Body.String(), "success")
	}
}

func TestAuthMiddleware_Wrap_MissingHeader(t *testing.T) {
	m := NewAuthMiddleware("test-token")

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := m.Wrap(handler)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_Wrap_InvalidFormat(t *testing.T) {
	m := NewAuthMiddleware("test-token")

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := m.Wrap(handler)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_Wrap_InvalidToken(t *testing.T) {
	m := NewAuthMiddleware("correct-token")

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := m.Wrap(handler)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_WrapFunc(t *testing.T) {
	token := "test-token"
	m := NewAuthMiddleware(token)

	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	wrapped := m.WrapFunc(handler)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
