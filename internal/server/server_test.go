package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/admin"
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	codexhttp "github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	codexws "github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

// mockStore implements the store interface for testing.
type mockStore struct{}

func testCodexRuntimes(t *testing.T) (*codexhttp.Runtime, *codexws.Runtime) {
	t.Helper()
	document, err := codexkeyring.GenerateDocument(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := codexkeyring.Parse(document, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	persistence, err := storepkg.NewSQLiteStore(filepath.Join(t.TempDir(), "codex.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistence.Close() })
	repositories, err := persistence.OpenCodexRepositories(context.Background(), keyring)
	if err != nil {
		t.Fatal(err)
	}
	digester, err := codexidentity.NewDigester(keyring)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: 24 * time.Hour, CommittedTTL: 30 * 24 * time.Hour,
		TombstoneTTL: 7 * 24 * time.Hour, MaxBindings: 100,
	}
	policy, err := codexcontinuity.NewPolicy(map[codexcontinuity.Kind]codexcontinuity.Limits{
		codexcontinuity.KindThreadID: limits, codexcontinuity.KindSessionID: limits,
		codexcontinuity.KindConversationID: limits, codexcontinuity.KindWindowID: limits,
		codexcontinuity.KindTurnState: limits, codexcontinuity.KindTurnMetadata: limits,
		codexcontinuity.KindResponseReference: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuity, err := codexcontinuity.NewService(codexcontinuity.Config{
		Store: repositories.Continuity, Digester: &digester, Policy: policy, Clock: internal.RealClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository: repositories.ProviderCookies, HandleDigester: keyring, Random: rand.Reader,
		Clock: internal.RealClock{}, HostCanonicalizer: providercookie.HostCanonicalizerFunc(codexidentity.CanonicalizeCookieHost),
		PublicSuffixList: codexidentity.PublicSuffixList{}, Policy: providercookie.DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	scheme := codexhttp.NewTrustedProxySchemeResolver(nil)
	httpRuntime, err := codexhttp.New(codexhttp.Config{
		ClientScopes: &digester, Continuity: continuity, ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	webSocketRuntime, err := codexws.New(codexws.Config{
		ClientScopes: &digester, Continuity: continuity, ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	return httpRuntime, webSocketRuntime
}

func (m *mockStore) ListProviders(context.Context) ([]model.Provider, error) { return nil, nil }
func (m *mockStore) ListProvidersByAPIType(context.Context, string) ([]model.Provider, error) {
	return nil, nil
}
func (m *mockStore) GetProvider(context.Context, string) (*model.Provider, error) { return nil, nil }
func (m *mockStore) CreateProvider(context.Context, *model.Provider) error {
	return nil
}
func (m *mockStore) UpdateProvider(context.Context, *model.Provider) error {
	return nil
}
func (m *mockStore) DeleteProvider(context.Context, string) error { return nil }

func (m *mockStore) ListRoutingPolicies(context.Context) ([]model.RoutingPolicy, error) {
	return []model.RoutingPolicy{}, nil
}
func (m *mockStore) GetRoutingPolicy(context.Context, uint) (*model.RoutingPolicy, error) {
	return nil, nil
}
func (m *mockStore) CreateRoutingPolicy(context.Context, *model.RoutingPolicy) error {
	return nil
}
func (m *mockStore) UpdateRoutingPolicy(context.Context, *model.RoutingPolicy) error {
	return nil
}
func (m *mockStore) DeleteRoutingPolicy(context.Context, uint) error { return nil }

func (m *mockStore) ListGroups(context.Context) ([]model.Group, error)      { return nil, nil }
func (m *mockStore) GetGroup(context.Context, string) (*model.Group, error) { return nil, nil }
func (m *mockStore) CreateGroup(context.Context, *model.Group) error        { return nil }
func (m *mockStore) UpdateGroup(context.Context, *model.Group) error        { return nil }
func (m *mockStore) DeleteGroup(context.Context, string) error              { return nil }

func (m *mockStore) GetHealthState(context.Context, string) (*model.HealthState, error) {
	return nil, nil
}
func (m *mockStore) GetHealthStatesByProviderIDs(context.Context, []string) (map[string]*model.HealthState, error) {
	return nil, nil
}
func (m *mockStore) ListHealthStates(context.Context) ([]model.HealthState, error) { return nil, nil }

func (m *mockStore) GetConfig(context.Context, string) (string, error)       { return "", nil }
func (m *mockStore) GetAllConfig(context.Context) (map[string]string, error) { return nil, nil }
func (m *mockStore) SetConfig(context.Context, string, string) error         { return nil }
func (m *mockStore) SetConfigs(context.Context, map[string]string) error     { return nil }
func (m *mockStore) ApplyConfigImport(context.Context, *storepkg.ConfigImportBundle) error {
	return nil
}

func (m *mockStore) InsertLog(context.Context, *model.RequestLog) error { return nil }
func (m *mockStore) ListLogs(context.Context, model.LogFilter) ([]model.RequestLog, error) {
	return nil, nil
}
func (m *mockStore) CountLogs(context.Context, model.LogFilter) (int64, error) { return 0, nil }
func (m *mockStore) GetLogStats(context.Context, time.Time, time.Time) (*model.LogStats, error) {
	return &model.LogStats{
		ByAPIType:  make(map[string]int64),
		ByProvider: []model.ProviderLogStats{},
	}, nil
}
func (m *mockStore) GetLogTimeSeries(context.Context, time.Time, time.Time, time.Duration) ([]model.TimeSeriesPoint, error) {
	return nil, nil
}
func (m *mockStore) GetAttemptsByRequestID(context.Context, string) ([]model.RequestAttempt, error) {
	return nil, nil
}
func (m *mockStore) GetLogByID(context.Context, uint) (*model.RequestLog, error) {
	return nil, nil
}
func (m *mockStore) InsertAttempts(context.Context, []model.RequestAttempt) error {
	return nil
}
func (m *mockStore) CleanOldAttempts(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func testServer(t *testing.T) *Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	httpRuntime, webSocketRuntime := testCodexRuntimes(t)
	return New(Config{
		Port: "0", Logger: logger, Store: &mockStore{},
		CodexHTTP: httpRuntime, CodexWebSocket: webSocketRuntime,
	})
}

func testAdminServer(t *testing.T) *AdminServer {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	return NewAdmin(AdminConfig{
		Port:       "0",
		AdminToken: "test-token",
		Logger:     logger,
		Store:      &mockStore{},
	})
}

func TestNew(t *testing.T) {
	s := testServer(t)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestHandleHealth(t *testing.T) {
	s := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}

	if resp.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}

	// Verify timestamp is valid RFC3339
	if _, err := time.Parse(time.RFC3339, resp.Timestamp); err != nil {
		t.Errorf("invalid timestamp format: %v", err)
	}
}

func TestServerAddr(t *testing.T) {
	s := testServer(t)
	addr := s.Addr()
	if addr == "" {
		t.Error("expected non-empty address")
	}
}

func TestServerShutdown(t *testing.T) {
	s := testServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown without starting should not error
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown error: %v", err)
	}
}

func TestServerStartAndShutdown(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	httpRuntime, webSocketRuntime := testCodexRuntimes(t)
	s := New(Config{
		Port:   "0", // Use port 0 to get a random available port
		Logger: logger, Store: &mockStore{},
		CodexHTTP: httpRuntime, CodexWebSocket: webSocketRuntime,
	})

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Poll health endpoint until server is ready (max 5 seconds)
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		addr := s.Addr()
		// Wait until the listener is set and we have an actual address
		if addr == ":0" {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		// Extract port from address (handles both IPv4 and IPv6 formats)
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		resp, err := http.Get("http://127.0.0.1:" + port + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not become ready in time")
	}

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown error: %v", err)
	}

	// Start should return nil after shutdown
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Start did not return after shutdown")
	}
}

func TestNewAdmin(t *testing.T) {
	s := testAdminServer(t)
	if s == nil {
		t.Fatal("expected non-nil admin server")
	}
}

func TestAdminServerAddr(t *testing.T) {
	s := testAdminServer(t)
	addr := s.Addr()
	if addr == "" {
		t.Error("expected non-empty address")
	}
}

func TestAdminServerShutdown(t *testing.T) {
	s := testAdminServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown without starting should not error
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown error: %v", err)
	}
}

func TestAdminServerStartAndShutdown(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	s := NewAdmin(AdminConfig{
		Port:       "0", // Use port 0 to get a random available port
		AdminToken: "test-token",
		Logger:     logger,
		Store:      &mockStore{},
	})

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Poll health endpoint until server is ready (max 5 seconds)
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		addr := s.Addr()
		// Wait until the listener is set and we have an actual address
		if addr == ":0" {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		// Extract port from address (handles both IPv4 and IPv6 formats)
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		resp, err := http.Get("http://127.0.0.1:" + port + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("admin server did not become ready in time")
	}

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown error: %v", err)
	}

	// Start should return nil after shutdown
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Start did not return after shutdown")
	}
}

func TestHandleNotFound(t *testing.T) {
	s := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	w := httptest.NewRecorder()

	s.handleNotFound(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAdminHandleNotFound(t *testing.T) {
	s := testAdminServer(t)

	req := httptest.NewRequest(http.MethodPost, "/foo/bar", nil)
	w := httptest.NewRecorder()

	s.handleNotFound(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAdminProviderCredentialRoutesRequireAuth(t *testing.T) {
	s := testAdminServer(t)

	testCases := []struct {
		name   string
		method string
		path   string
	}{
		{
			name:   "export Codex auth",
			method: http.MethodGet,
			path:   "/admin/api/providers/test-provider/codex-auth",
		},
		{
			name:   "refresh credential",
			method: http.MethodPost,
			path:   "/admin/api/providers/test-provider/refresh-credential",
		},
		{
			name:   "refresh usage",
			method: http.MethodPost,
			path:   "/admin/api/providers/test-provider/refresh-usage",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()

			s.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestAdminUnknownAPIRouteRequiresAuth(t *testing.T) {
	s := testAdminServer(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/unknown-route", nil)
	w := httptest.NewRecorder()

	s.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	if got := w.Header().Get("Content-Type"); got != admin.ContentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", got, admin.ContentTypeJSON)
	}
}

func TestAdminUnknownAPIRouteReturnsJSONNotFound(t *testing.T) {
	s := testAdminServer(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/unknown-route", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	s.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	if got := w.Header().Get("Content-Type"); got != admin.ContentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", got, admin.ContentTypeJSON)
	}

	var resp model.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode JSON error response: %v", err)
	}

	if resp.Code != admin.ErrCodeNotFound {
		t.Fatalf("error code = %q, want %q", resp.Code, admin.ErrCodeNotFound)
	}
}

func TestAdminAPICatalogRoute(t *testing.T) {
	t.Parallel()

	server := testAdminServer(t)

	unauthorized := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, "/admin/api/api-catalog", nil),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	request := httptest.NewRequest(http.MethodGet, "/admin/api/api-catalog", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != admin.ContentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", got, admin.ContentTypeJSON)
	}
	var got apicontract.CatalogResponse
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode catalog response: %v", err)
	}
	if want := admin.APICatalogResponse(); !reflect.DeepEqual(got, want) {
		t.Fatalf("routed catalog projection drifted\ngot:  %+v\nwant: %+v", got, want)
	}

	for _, test := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "wrong method", method: http.MethodPost, path: "/admin/api/api-catalog"},
		{name: "subpath", method: http.MethodGet, path: "/admin/api/api-catalog/extra"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer test-token")
			response := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
			}
		})
	}
}

func TestAdminRoutingPoliciesRouteReturnsJSON(t *testing.T) {
	s := testAdminServer(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	s.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Content-Type"); got != admin.ContentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", got, admin.ContentTypeJSON)
	}
	if body := w.Body.String(); body != "[]\n" {
		t.Fatalf("body = %q, want %q", body, "[]\n")
	}
}
