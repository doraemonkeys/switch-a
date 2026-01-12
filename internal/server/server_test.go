package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// mockStore implements the store interface for testing.
type mockStore struct{}

func (m *mockStore) ListProviders(context.Context) ([]model.Provider, error) { return nil, nil }
func (m *mockStore) ListProvidersByAPIType(context.Context, string) ([]model.Provider, error) {
	return nil, nil
}
func (m *mockStore) GetProvider(context.Context, string) (*model.Provider, error) { return nil, nil }
func (m *mockStore) CreateProvider(context.Context, *model.Provider) error        { return nil }
func (m *mockStore) UpdateProvider(context.Context, *model.Provider) error        { return nil }
func (m *mockStore) DeleteProvider(context.Context, string) error                 { return nil }

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

func testServer(t *testing.T) *Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	return New(Config{
		Port:   "0",
		Logger: logger,
		Store:  &mockStore{},
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
	s := New(Config{
		Port:   "0", // Use port 0 to get a random available port
		Logger: logger,
		Store:  &mockStore{},
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
