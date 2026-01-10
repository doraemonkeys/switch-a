// Package server provides the HTTP server implementation.
package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/proxy"

	"go.uber.org/zap"
)

// ReadHeaderTimeout is the timeout for reading request headers.
const ReadHeaderTimeout = 10 * time.Second

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

	// Log operations
	InsertLog(ctx context.Context, log *model.RequestLog) error
	ListLogs(ctx context.Context, limit, offset int) ([]model.RequestLog, error)
}

// Server represents the HTTP server.
type Server struct {
	server       *http.Server
	logger       *zap.Logger
	store        store
	adminToken   string
	listener     net.Listener
	proxyHandler *proxy.Handler
}

// Config holds server configuration.
type Config struct {
	Port       string
	AdminToken string
	Logger     *zap.Logger
	Store      store
}

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// New creates a new HTTP server.
func New(cfg Config) *Server {
	mux := http.NewServeMux()

	// Create proxy handler
	proxyHandler := proxy.NewHandler(proxy.Config{
		Store:  cfg.Store,
		Logger: cfg.Logger,
	})

	s := &Server{
		server: &http.Server{
			Addr:              net.JoinHostPort("", cfg.Port),
			Handler:           mux,
			ReadHeaderTimeout: ReadHeaderTimeout,
		},
		logger:       cfg.Logger,
		store:        cfg.Store,
		adminToken:   cfg.AdminToken,
		proxyHandler: proxyHandler,
	}

	// Register routes
	mux.HandleFunc("GET /health", s.handleHealth)

	// Proxy API routes (no auth required)
	// Claude API
	mux.HandleFunc("POST /v1/messages", s.handleProxy)
	mux.HandleFunc("GET /v1/models", s.handleProxy)
	// Codex API
	mux.HandleFunc("POST /responses", s.handleProxy)
	// Gemini API
	mux.HandleFunc("POST /gemini/", s.handleProxy)
	// Custom API
	mux.HandleFunc("POST /custom/", s.handleProxy)
	mux.HandleFunc("GET /custom/", s.handleProxy)

	return s
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

// handleHealth handles the /health endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil { // coverage-ignore -- JSON encoding of simple struct rarely fails
		s.logger.Error("failed to encode health response", zap.Error(err))
	}
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
