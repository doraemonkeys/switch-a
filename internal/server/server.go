// Package server provides the HTTP server implementation.
package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"switch-a/internal"
	"switch-a/internal/admin"
	"switch-a/internal/model"
	"switch-a/internal/proxy"

	"go.uber.org/zap"
)

// ReadHeaderTimeout is the timeout for reading request headers.
const ReadHeaderTimeout = 10 * time.Second

// IdleTimeout is the timeout for keep-alive connections.
// Prevents idle connections from occupying resources indefinitely.
const IdleTimeout = 120 * time.Second

// store defines the minimal storage interface needed by the server.
type store interface {
	// Provider operations
	ListProviders(ctx context.Context) ([]model.Provider, error)
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetProvider(ctx context.Context, id string) (*model.Provider, error)
	CreateProvider(ctx context.Context, p *model.Provider) error
	UpdateProvider(ctx context.Context, p *model.Provider) error
	DeleteProvider(ctx context.Context, id string) error

	// Group operations
	ListGroups(ctx context.Context) ([]model.Group, error)
	GetGroup(ctx context.Context, id string) (*model.Group, error)
	CreateGroup(ctx context.Context, g *model.Group) error
	UpdateGroup(ctx context.Context, g *model.Group) error
	DeleteGroup(ctx context.Context, id string) error

	// Health state operations
	GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error)
	ListHealthStates(ctx context.Context) ([]model.HealthState, error)

	// Config operations
	GetConfig(ctx context.Context, key string) (string, error)
	GetAllConfig(ctx context.Context) (map[string]string, error)
	SetConfig(ctx context.Context, key, value string) error
	SetConfigs(ctx context.Context, configs map[string]string) error

	// Log operations
	InsertLog(ctx context.Context, log *model.RequestLog) error
	ListLogs(ctx context.Context, limit, offset int) ([]model.RequestLog, error)
	CountLogs(ctx context.Context) (int64, error)
}

// Server represents the HTTP server (proxy only).
type Server struct {
	server       *http.Server
	logger       *zap.Logger
	store        store
	listener     net.Listener
	proxyHandler *proxy.Handler
}

// AdminServer represents the admin HTTP server (separate port for security).
type AdminServer struct {
	server   *http.Server
	logger   *zap.Logger
	listener net.Listener
}

// ConcurrencyTracker is an alias to avoid duplicating the interface definition.
type ConcurrencyTracker = admin.ConcurrencyTracker

// Selector defines the provider selection interface needed by the server.
// This extends the basic internal.Selector with additional methods for
// concurrency management and sticky sessions.
type Selector = proxy.Selector

// Config holds proxy server configuration.
type Config struct {
	Port     string
	Logger   *zap.Logger
	Store    store
	Health   internal.HealthManager
	Selector Selector
}

// AdminConfig holds admin server configuration.
type AdminConfig struct {
	Port        string
	AdminToken  string
	Logger      *zap.Logger
	Store       store
	Health      internal.HealthManager
	Selector    Selector
	Concurrency ConcurrencyTracker
}

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// New creates a new proxy HTTP server.
func New(cfg Config) *Server {
	mux := http.NewServeMux()

	// Create proxy handler with all dependencies for full functionality.
	// Without Selector and Health, the proxy falls back to degraded mode:
	// - No health checks (MarkSuccess/MarkFailure are no-ops)
	// - No concurrency limits
	// - No sticky sessions
	// - Simple round-robin provider selection
	proxyHandler := proxy.NewHandler(proxy.Config{
		Store:    cfg.Store,
		Selector: cfg.Selector,
		Health:   cfg.Health,
		Logger:   cfg.Logger,
	})

	s := &Server{
		server: &http.Server{
			Addr:              net.JoinHostPort("", cfg.Port),
			Handler:           mux,
			ReadHeaderTimeout: ReadHeaderTimeout,
			IdleTimeout:       IdleTimeout,
		},
		logger:       cfg.Logger,
		store:        cfg.Store,
		proxyHandler: proxyHandler,
	}

	// Register routes
	mux.HandleFunc("GET /health", s.handleHealth)

	// Proxy API routes (no auth required)
	// Claude API
	mux.HandleFunc("POST "+proxy.RouteClaudeMessages, s.handleProxy)
	mux.HandleFunc("GET "+proxy.RouteClaudeModels, s.handleProxy)
	// Codex API
	mux.HandleFunc("POST "+proxy.RouteCodexResponses, s.handleProxy)
	// Gemini API
	mux.HandleFunc("POST "+proxy.RouteGeminiPrefix, s.handleProxy)
	// Custom API
	mux.HandleFunc("POST "+proxy.RouteCustomPrefix, s.handleProxy)
	mux.HandleFunc("GET "+proxy.RouteCustomPrefix, s.handleProxy)

	return s
}

// NewAdmin creates a new admin HTTP server (separate port for security).
func NewAdmin(cfg AdminConfig) *AdminServer {
	mux := http.NewServeMux()

	s := &AdminServer{
		server: &http.Server{
			Addr:              net.JoinHostPort("", cfg.Port),
			Handler:           mux,
			ReadHeaderTimeout: ReadHeaderTimeout,
			IdleTimeout:       IdleTimeout,
		},
		logger: cfg.Logger,
	}

	// Health check endpoint
	mux.HandleFunc("GET /health", s.handleHealth)

	// Register admin API routes with authentication
	s.registerAdminRoutes(mux, cfg)

	return s
}

// registerAdminRoutes registers admin API routes with authentication.
func (s *AdminServer) registerAdminRoutes(mux *http.ServeMux, cfg AdminConfig) {
	// Create admin handler with cleaner for proper resource cleanup.
	// The Selector implements ConcurrencyCleaner for clearing concurrency
	// counters when providers are deleted.
	adminHandler := admin.NewHandler(admin.Config{
		Store:       cfg.Store,
		Health:      cfg.Health,
		Concurrency: cfg.Concurrency,
		Cleaner:     cfg.Selector,
		Logger:      cfg.Logger,
	})

	// Create auth middleware
	auth := admin.NewAuthMiddleware(cfg.AdminToken)

	// Provider routes
	mux.Handle("GET /admin/api/providers", auth.WrapFunc(adminHandler.ListProviders))
	mux.Handle("POST /admin/api/providers", auth.WrapFunc(adminHandler.CreateProvider))
	mux.Handle("GET /admin/api/providers/{id}", auth.WrapFunc(adminHandler.GetProvider))
	mux.Handle("PUT /admin/api/providers/{id}", auth.WrapFunc(adminHandler.UpdateProvider))
	mux.Handle("DELETE /admin/api/providers/{id}", auth.WrapFunc(adminHandler.DeleteProvider))
	mux.Handle("POST /admin/api/providers/{id}/enable", auth.WrapFunc(adminHandler.EnableProvider))
	mux.Handle("POST /admin/api/providers/{id}/disable", auth.WrapFunc(adminHandler.DisableProvider))
	mux.Handle("POST /admin/api/providers/{id}/reset", auth.WrapFunc(adminHandler.ResetProvider))

	// Group routes
	mux.Handle("GET /admin/api/groups", auth.WrapFunc(adminHandler.ListGroups))
	mux.Handle("POST /admin/api/groups", auth.WrapFunc(adminHandler.CreateGroup))
	mux.Handle("GET /admin/api/groups/{id}", auth.WrapFunc(adminHandler.GetGroup))
	mux.Handle("PUT /admin/api/groups/{id}", auth.WrapFunc(adminHandler.UpdateGroup))
	mux.Handle("DELETE /admin/api/groups/{id}", auth.WrapFunc(adminHandler.DeleteGroup))

	// Config routes
	mux.Handle("GET /admin/api/config", auth.WrapFunc(adminHandler.GetConfig))
	mux.Handle("PUT /admin/api/config", auth.WrapFunc(adminHandler.UpdateConfig))

	// Health and status routes
	mux.Handle("GET /admin/api/health", auth.WrapFunc(adminHandler.GetHealth))
	mux.Handle("GET /admin/api/status", auth.WrapFunc(adminHandler.GetStatus))

	// Logs route
	mux.Handle("GET /admin/api/logs", auth.WrapFunc(adminHandler.GetLogs))
}

// handleProxy forwards requests to the proxy handler.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHandler.ServeHTTP(w, r)
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil { // coverage-ignore -- port binding errors require specific conditions
		return err
	}
	s.listener = ln
	s.logger.Info("starting HTTP server", zap.String("addr", ln.Addr().String()))
	if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed { // coverage-ignore -- serve errors after successful listen are rare
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down HTTP server")
	return s.server.Shutdown(ctx)
}

// writeHealthResponse writes a JSON health check response.
// This is a shared implementation used by both Server and AdminServer to avoid duplication.
func writeHealthResponse(w http.ResponseWriter, logger *zap.Logger) {
	resp := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil { // coverage-ignore -- JSON encoding of simple struct rarely fails
		logger.Error("failed to encode health response", zap.Error(err))
	}
}

// handleHealth handles the /health endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeHealthResponse(w, s.logger)
}

// Addr returns the server's address.
// If the server is listening, returns the actual address (useful when port 0 is used).
// Otherwise, returns the configured address.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.server.Addr
}

// Start starts the admin HTTP server.
func (s *AdminServer) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil { // coverage-ignore -- port binding errors require specific conditions
		return err
	}
	s.listener = ln
	s.logger.Info("starting admin HTTP server", zap.String("addr", ln.Addr().String()))
	if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed { // coverage-ignore -- serve errors after successful listen are rare
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the admin server.
func (s *AdminServer) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down admin HTTP server")
	return s.server.Shutdown(ctx)
}

// handleHealth handles the /health endpoint for admin server.
func (s *AdminServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeHealthResponse(w, s.logger)
}

// Addr returns the admin server's address.
// If the server is listening, returns the actual address (useful when port 0 is used).
// Otherwise, returns the configured address.
func (s *AdminServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.server.Addr
}
