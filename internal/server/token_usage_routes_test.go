package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/admin"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

type tokenUsageRouteSpy struct {
	calls int
}

func (s *tokenUsageRouteSpy) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.calls++
	w.Header().Set("Content-Type", admin.ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"summary":{}}`))
}

func TestTokenUsageRouteAuthenticatedExactGetDispatches(t *testing.T) {
	spy := &tokenUsageRouteSpy{}
	server := NewAdmin(AdminConfig{
		Port:              "0",
		AdminToken:        "route-token",
		Logger:            zap.NewNop(),
		Store:             &mockStore{},
		TokenUsageHandler: spy,
	})
	request := httptest.NewRequest(http.MethodGet, "/admin/api/token-usage", nil)
	request.Header.Set("Authorization", "Bearer route-token")
	recorder := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || spy.calls != 1 || recorder.Header().Get("Content-Type") != admin.ContentTypeJSON {
		t.Fatalf("status/calls/content type = %d/%d/%q", recorder.Code, spy.calls, recorder.Header().Get("Content-Type"))
	}
}

func TestTokenUsageRouteRequiresAuthentication(t *testing.T) {
	tests := []string{"", "Bearer wrong-token"}
	for _, authorization := range tests {
		t.Run(authorization, func(t *testing.T) {
			spy := &tokenUsageRouteSpy{}
			server := NewAdmin(AdminConfig{
				Port: "0", AdminToken: "route-token", Logger: zap.NewNop(), Store: &mockStore{}, TokenUsageHandler: spy,
			})
			request := httptest.NewRequest(http.MethodGet, "/admin/api/token-usage", nil)
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}
			recorder := httptest.NewRecorder()

			server.server.Handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized || spy.calls != 0 || recorder.Header().Get("Content-Type") != admin.ContentTypeJSON {
				t.Fatalf("status/calls/content type = %d/%d/%q", recorder.Code, spy.calls, recorder.Header().Get("Content-Type"))
			}
		})
	}
}

func TestTokenUsageRouteWrongMethodAndSubpathReachAuthenticatedJSONBoundary(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/admin/api/token-usage"},
		{method: http.MethodGet, path: "/admin/api/token-usage/extra"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			spy := &tokenUsageRouteSpy{}
			server := NewAdmin(AdminConfig{
				Port: "0", AdminToken: "route-token", Logger: zap.NewNop(), Store: &mockStore{}, TokenUsageHandler: spy,
			})
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer route-token")
			recorder := httptest.NewRecorder()

			server.server.Handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound || spy.calls != 0 || recorder.Header().Get("Content-Type") != admin.ContentTypeJSON {
				t.Fatalf("status/calls/content type = %d/%d/%q", recorder.Code, spy.calls, recorder.Header().Get("Content-Type"))
			}
			var response model.ErrorResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Code != admin.ErrCodeNotFound {
				t.Fatalf("error code = %q, want %q", response.Code, admin.ErrCodeNotFound)
			}
		})
	}
}
