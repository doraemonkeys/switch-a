package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	adminerrorruleapi "github.com/doraemonkeys/switch-a/internal/admin/errorruleapi"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

func TestAdminInternalErrorRoutesDispatchToFocusedHandler(t *testing.T) {
	adminServer := newInternalErrorRouteServer(t)
	validTestMessage := `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":"application/json","content_encoding":"identity","body":{"encoding":"utf8","value":"{}"}}`
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{name: "list", method: http.MethodGet, path: "/admin/api/internal-error-rules", wantStatus: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/admin/api/internal-error-rules", body: `{}`, wantStatus: http.StatusPreconditionRequired},
		{name: "reorder", method: http.MethodPost, path: "/admin/api/internal-error-rules/reorder", body: `{}`, wantStatus: http.StatusPreconditionRequired},
		// A successful analysis proves this reserved action path did not fall
		// through to the rule-ID template or the admin catch-all.
		{name: "test message", method: http.MethodPost, path: "/admin/api/internal-error-rules/test-message", body: validTestMessage, wantStatus: http.StatusOK},
		{name: "get by id", method: http.MethodGet, path: "/admin/api/internal-error-rules/not-a-uuid", wantStatus: http.StatusBadRequest},
		{name: "update by id", method: http.MethodPut, path: "/admin/api/internal-error-rules/not-a-uuid", body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "delete by id", method: http.MethodDelete, path: "/admin/api/internal-error-rules/not-a-uuid", wantStatus: http.StatusBadRequest},
		{name: "statistics", method: http.MethodGet, path: "/admin/api/internal-error-rule-stats", wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer test-token")
			response := httptest.NewRecorder()

			adminServer.server.Handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
		})
	}
}

func TestAdminInternalErrorRoutesRequireAuthentication(t *testing.T) {
	adminServer := newInternalErrorRouteServer(t)
	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/admin/api/internal-error-rules"},
		{method: http.MethodPost, path: "/admin/api/internal-error-rules"},
		{method: http.MethodPost, path: "/admin/api/internal-error-rules/reorder"},
		{method: http.MethodPost, path: "/admin/api/internal-error-rules/test-message"},
		{method: http.MethodGet, path: "/admin/api/internal-error-rules/not-a-uuid"},
		{method: http.MethodPut, path: "/admin/api/internal-error-rules/not-a-uuid"},
		{method: http.MethodDelete, path: "/admin/api/internal-error-rules/not-a-uuid"},
		{method: http.MethodGet, path: "/admin/api/internal-error-rule-stats"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		response := httptest.NewRecorder()
		adminServer.server.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d", route.method, route.path, response.Code, http.StatusUnauthorized)
		}
	}
}

func TestAdminInternalErrorRouteBoundariesStayExact(t *testing.T) {
	adminServer := newInternalErrorRouteServer(t)
	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/admin/api/internal-error-rules/not-a-uuid"},
		{method: http.MethodGet, path: "/admin/api/internal-error-rules/test-message/extra"},
		{method: http.MethodPost, path: "/admin/api/internal-error-rule-stats"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		adminServer.server.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want %d", route.method, route.path, response.Code, http.StatusNotFound)
		}
	}
}

func newInternalErrorRouteServer(t *testing.T) *AdminServer {
	t.Helper()
	sqlStore, err := storepkg.NewSQLiteStore(filepath.Join(t.TempDir(), "routes.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	repository := sqlStore.InternalErrorRuleRepository()
	accumulator, err := statistics.New(repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); err != nil {
		t.Fatal(err)
	}
	internalErrorHandler, err := adminerrorruleapi.NewHandler(adminerrorruleapi.Config{
		Rules:        repository,
		Stats:        repository,
		StatsOverlay: accumulator,
		Providers:    &mockStore{},
		Analyzer:     adminerrorruleapi.NewRegistryAnalyzer(),
		Logger:       zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewAdmin(AdminConfig{
		Port:               "0",
		AdminToken:         "test-token",
		Logger:             zap.NewNop(),
		Store:              &mockStore{},
		InternalErrorRules: internalErrorHandler,
	})
}
