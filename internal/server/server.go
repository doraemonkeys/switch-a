// Package server provides the HTTP server implementation.
package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/admin"
	admindebugcapture "github.com/doraemonkeys/switch-a/internal/admin/debugcapture"
	adminerrorruleapi "github.com/doraemonkeys/switch-a/internal/admin/errorruleapi"
	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"
	"github.com/doraemonkeys/switch-a/web"

	"go.uber.org/zap"
)

// ReadHeaderTimeout is the timeout for reading request headers.
// 10s is generous for legitimate clients; excessively slow header
// transmission may indicate slowloris attacks or severely degraded networks.
const ReadHeaderTimeout = 10 * time.Second

// IdleTimeout is the timeout for keep-alive connections.
// Prevents idle connections from occupying resources indefinitely.
const IdleTimeout = 120 * time.Second

// HTTP response constants.
const (
	HealthStatusOK = "ok"
)

// store defines the minimal storage interface needed by the server.
// This is a subset of internal.Store, defined at the consumer side per Go idiom
// "Accept Interfaces, Return Structs". The server requires provider/group CRUD,
// config access, and logging capabilities but not health state mutations or cleanup.
type store interface {
	// Provider operations
	ListProviders(ctx context.Context) ([]model.Provider, error)
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetProvider(ctx context.Context, id string) (*model.Provider, error)
	CreateProvider(ctx context.Context, p *model.Provider, options ...model.ProviderWriteOptions) error
	UpdateProvider(ctx context.Context, p *model.Provider, options ...model.ProviderWriteOptions) error
	DeleteProvider(ctx context.Context, id string) error

	// Routing policy operations
	ListRoutingPolicies(ctx context.Context) ([]model.RoutingPolicy, error)
	GetRoutingPolicy(ctx context.Context, id uint) (*model.RoutingPolicy, error)
	CreateRoutingPolicy(ctx context.Context, policy *model.RoutingPolicy) error
	UpdateRoutingPolicy(ctx context.Context, policy *model.RoutingPolicy) error
	DeleteRoutingPolicy(ctx context.Context, id uint) error

	// Group operations
	ListGroups(ctx context.Context) ([]model.Group, error)
	GetGroup(ctx context.Context, id string) (*model.Group, error)
	CreateGroup(ctx context.Context, g *model.Group) error
	UpdateGroup(ctx context.Context, g *model.Group) error
	DeleteGroup(ctx context.Context, id string) error

	// Health state operations
	GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error)
	GetHealthStatesByProviderIDs(ctx context.Context, providerIDs []string) (map[string]*model.HealthState, error)
	ListHealthStates(ctx context.Context) ([]model.HealthState, error)

	// Config operations
	GetConfig(ctx context.Context, key string) (string, error)
	GetAllConfig(ctx context.Context) (map[string]string, error)
	SetConfig(ctx context.Context, key, value string) error
	SetConfigs(ctx context.Context, configs map[string]string) error
	ApplyConfigImport(ctx context.Context, bundle *storepkg.ConfigImportBundle) error

	// Log operations
	InsertLog(ctx context.Context, log *model.RequestLog) error
	ListLogs(ctx context.Context, filter model.LogFilter) ([]model.RequestLog, error)
	CountLogs(ctx context.Context, filter model.LogFilter) (int64, error)
	GetLogStats(ctx context.Context, startTime, endTime time.Time) (*model.LogStats, error)
	GetLogTimeSeries(ctx context.Context, startTime, endTime time.Time, granularity time.Duration) ([]model.TimeSeriesPoint, error)
	GetLogByID(ctx context.Context, id uint) (*model.RequestLog, error)

	// Attempt operations
	InsertAttempts(ctx context.Context, attempts []model.RequestAttempt) error
	GetAttemptsByRequestID(ctx context.Context, requestID string) ([]model.RequestAttempt, error)
}

// Server represents the HTTP server (proxy only).
type Server struct {
	server       *http.Server
	logger       *zap.Logger
	store        store
	mu           sync.RWMutex // protects listener
	listener     net.Listener
	proxyHandler *proxy.Handler
}

// AdminServer represents the admin HTTP server (separate port for security).
type AdminServer struct {
	server   *http.Server
	logger   *zap.Logger
	mu       sync.RWMutex // protects listener
	listener net.Listener
}

// ConcurrencyTracker is an alias to avoid duplicating the interface definition.
type ConcurrencyTracker = admin.ConcurrencyTracker

// Selector is the proxy's lease-aware routing boundary. The server does not
// expose a provider-only selector because dispatch must retain slot ownership.
type Selector = proxy.Selector

// Config holds proxy server configuration.
type Config struct {
	Port                       string
	Logger                     *zap.Logger
	Store                      store
	Health                     internal.HealthManager
	Selector                   Selector
	ActiveRegistry             *proxy.ActiveRequestRegistry
	VisibleContinuitySeedStore model.VisibleContinuitySeedStore
	Auth                       *providerauth.Service
	Capture                    proxy.RequestCapture
	RuleSetProvider            errorrule.RuleSetProvider
	ResponseAnalyzer           proxy.ResponseAnalyzer
	RuleStatistics             proxy.RuleStatistics
}

// AdminConfig holds admin server configuration.
type AdminConfig struct {
	Port                string
	AdminToken          string
	Logger              *zap.Logger
	Store               store
	Health              internal.HealthManager
	Selector            Selector
	ProviderLifecycles  admin.ProviderLifecycleCoordinator
	Concurrency         ConcurrencyTracker
	ActiveReqList       admin.ActiveRequestLister
	Auth                *providerauth.Service
	ProviderImportStore admin.ProviderImportStore
	InternalErrorRules  *adminerrorruleapi.Handler
	CaptureSessions     admindebugcapture.CaptureSessions
	CaptureQueries      admindebugcapture.CaptureQueries
	CaptureExports      admindebugcapture.CaptureExports
	AnalyticsWindow     *analyticswindow.Resolver
	TokenUsageHandler   http.Handler
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
		Store:                      cfg.Store,
		Selector:                   cfg.Selector,
		Health:                     cfg.Health,
		ActiveRegistry:             cfg.ActiveRegistry,
		VisibleContinuitySeedStore: cfg.VisibleContinuitySeedStore,
		Auth:                       cfg.Auth,
		UsageObserver:              cfg.Auth,
		Capture:                    cfg.Capture,
		RuleSetProvider:            cfg.RuleSetProvider,
		ResponseAnalyzer:           cfg.ResponseAnalyzer,
		RuleStatistics:             cfg.RuleStatistics,
		Logger:                     cfg.Logger,
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

	// Proxy API routes (no auth required). Root-level contracts share their
	// catalog with Handler resolution so adding an endpoint cannot update only
	// one of the two routing layers.
	for _, route := range proxy.BareProxyRoutes() {
		mux.HandleFunc(route.Method+" "+route.Pattern, s.handleProxy)
	}
	// Explicit API namespaces (/claude/*, /codex/*, /grok/*, /gemini/*):
	// clients pin the API type in their base URL when bare contract paths are
	// ambiguous across vendors. GET is registered for model discovery
	// (e.g. /claude/v1/models) and namespaced WebSocket upgrades.
	for _, pattern := range proxy.APINamespaceRoutePatterns() {
		mux.HandleFunc("POST "+pattern, s.handleProxy)
		mux.HandleFunc("GET "+pattern, s.handleProxy)
	}
	// Custom API
	mux.HandleFunc("POST "+proxy.RouteCustomPrefix, s.handleProxy)
	mux.HandleFunc("GET "+proxy.RouteCustomPrefix, s.handleProxy)

	// Catch-all for unregistered routes — log and return 404.
	// Without this, Go's default ServeMux silently returns 404 with no visibility.
	mux.HandleFunc("/", s.handleNotFound)

	return s
}

// NewAdmin creates a new admin HTTP server (separate port for security).
func NewAdmin(cfg AdminConfig) *AdminServer {
	mux := http.NewServeMux()

	s := &AdminServer{
		server: &http.Server{
			Addr:              net.JoinHostPort("", cfg.Port),
			Handler:           secureDebugCaptureBoundary(mux),
			ReadHeaderTimeout: ReadHeaderTimeout,
			IdleTimeout:       IdleTimeout,
		},
		logger: cfg.Logger,
	}

	// Health check endpoint
	mux.HandleFunc("GET /health", s.handleHealth)

	// Register admin API routes with authentication
	s.registerAdminRoutes(mux, cfg)

	// Catch-all for unregistered routes outside /admin/ — log and return 404.
	mux.HandleFunc("/", s.handleNotFound)

	return s
}

// registerAdminRoutes registers admin API routes with authentication.
func (s *AdminServer) registerAdminRoutes(mux *http.ServeMux, cfg AdminConfig) {
	// Durable eligibility writes share the selector's generation boundary so an
	// in-flight permit cannot activate from the pre-mutation snapshot.
	adminHandler := admin.NewHandler(admin.Config{
		Store:               cfg.Store,
		Health:              cfg.Health,
		Concurrency:         cfg.Concurrency,
		ProviderLifecycles:  cfg.ProviderLifecycles,
		ActiveReqList:       cfg.ActiveReqList,
		Auth:                cfg.Auth,
		ProviderImports:     cfg.Auth,
		ProviderImportStore: cfg.ProviderImportStore,
		InternalErrorRules:  cfg.InternalErrorRules,
		StatsWindowResolver: cfg.AnalyticsWindow,
		Logger:              cfg.Logger,
	})

	// Create auth middleware
	auth := admin.NewAuthMiddleware(cfg.AdminToken)
	s.registerDebugCaptureRoutes(mux, cfg, auth)

	// The frontend derives all built-in API presentation and capability state
	// from this authenticated projection; no static UI fallback is registered.
	mux.Handle("GET /admin/api/api-catalog", auth.WrapFunc(adminHandler.GetAPICatalog))

	// Collection actions are intentionally registered before the ID template.
	// This keeps reserved action names out of the rule-ID namespace as that
	// namespace evolves, most importantly for the Test Message contract.
	mux.Handle("GET /admin/api/internal-error-rules", auth.WrapFunc(adminHandler.ListInternalErrorRules))
	mux.Handle("POST /admin/api/internal-error-rules", auth.WrapFunc(adminHandler.CreateInternalErrorRule))
	mux.Handle("POST /admin/api/internal-error-rules/reorder", auth.WrapFunc(adminHandler.ReorderInternalErrorRules))
	mux.Handle("POST /admin/api/internal-error-rules/test-message", auth.WrapFunc(adminHandler.TestInternalErrorMessage))
	mux.Handle("GET /admin/api/internal-error-rules/{id}", auth.WrapFunc(adminHandler.GetInternalErrorRule))
	mux.Handle("PUT /admin/api/internal-error-rules/{id}", auth.WrapFunc(adminHandler.UpdateInternalErrorRule))
	mux.Handle("DELETE /admin/api/internal-error-rules/{id}", auth.WrapFunc(adminHandler.DeleteInternalErrorRule))
	mux.Handle("GET /admin/api/internal-error-rule-stats", auth.WrapFunc(adminHandler.GetInternalErrorRuleStats))

	// Provider routes
	mux.Handle("GET /admin/api/providers", auth.WrapFunc(adminHandler.ListProviders))
	mux.Handle("POST /admin/api/providers", auth.WrapFunc(adminHandler.CreateProvider))
	mux.Handle("POST /admin/api/provider-auth/chatgpt/start", auth.WrapFunc(adminHandler.StartChatGPTProviderLogin))
	mux.Handle("POST /admin/api/provider-auth/chatgpt/import", auth.WrapFunc(adminHandler.ImportChatGPTProviderCredential))
	mux.Handle("POST /admin/api/provider-imports", auth.WrapFunc(adminHandler.PreviewProviderImport))
	mux.Handle("POST /admin/api/provider-imports/{import_id}/commit", auth.WrapFunc(adminHandler.CommitProviderImport))
	mux.Handle("DELETE /admin/api/provider-imports/{import_id}", auth.WrapFunc(adminHandler.CancelProviderImport))
	mux.Handle("GET /admin/api/provider-auth/chatgpt/sessions/{login_id}", auth.WrapFunc(adminHandler.GetChatGPTProviderLoginStatus))
	mux.Handle("POST /admin/api/providers/batch", auth.WrapFunc(adminHandler.BatchProviderAction))
	mux.Handle("GET /admin/api/providers/{id}", auth.WrapFunc(adminHandler.GetProvider))
	mux.Handle("PUT /admin/api/providers/{id}", auth.WrapFunc(adminHandler.UpdateProvider))
	mux.Handle("DELETE /admin/api/providers/{id}", auth.WrapFunc(adminHandler.DeleteProvider))
	mux.Handle("GET /admin/api/providers/{id}/codex-auth", auth.WrapFunc(adminHandler.ExportProviderCodexAuth))
	mux.Handle("POST /admin/api/providers/{id}/refresh-credential", auth.WrapFunc(adminHandler.RefreshProviderCredential))
	mux.Handle("POST /admin/api/providers/{id}/refresh-usage", auth.WrapFunc(adminHandler.RefreshProviderUsage))
	mux.Handle("POST /admin/api/providers/{id}/enable", auth.WrapFunc(adminHandler.EnableProvider))
	mux.Handle("POST /admin/api/providers/{id}/disable", auth.WrapFunc(adminHandler.DisableProvider))
	mux.Handle("POST /admin/api/providers/{id}/reset", auth.WrapFunc(adminHandler.ResetProvider))

	// Group routes
	mux.Handle("GET /admin/api/groups", auth.WrapFunc(adminHandler.ListGroups))
	mux.Handle("POST /admin/api/groups", auth.WrapFunc(adminHandler.CreateGroup))
	mux.Handle("GET /admin/api/groups/{id}", auth.WrapFunc(adminHandler.GetGroup))
	mux.Handle("PUT /admin/api/groups/{id}", auth.WrapFunc(adminHandler.UpdateGroup))
	mux.Handle("DELETE /admin/api/groups/{id}", auth.WrapFunc(adminHandler.DeleteGroup))
	mux.Handle("POST /admin/api/groups/{id}/enable", auth.WrapFunc(adminHandler.EnableGroup))
	mux.Handle("POST /admin/api/groups/{id}/disable", auth.WrapFunc(adminHandler.DisableGroup))

	// Routing policy routes
	mux.Handle("GET /admin/api/routing-policies", auth.WrapFunc(adminHandler.ListRoutingPolicies))
	mux.Handle("POST /admin/api/routing-policies", auth.WrapFunc(adminHandler.CreateRoutingPolicy))
	mux.Handle("GET /admin/api/routing-policies/{id}", auth.WrapFunc(adminHandler.GetRoutingPolicy))
	mux.Handle("PUT /admin/api/routing-policies/{id}", auth.WrapFunc(adminHandler.UpdateRoutingPolicy))
	mux.Handle("DELETE /admin/api/routing-policies/{id}", auth.WrapFunc(adminHandler.DeleteRoutingPolicy))

	// Config routes
	mux.Handle("GET /admin/api/config", auth.WrapFunc(adminHandler.GetConfig))
	mux.Handle("PUT /admin/api/config", auth.WrapFunc(adminHandler.UpdateConfig))
	mux.Handle("GET /admin/api/config/export", auth.WrapFunc(adminHandler.ExportConfig))
	mux.Handle("POST /admin/api/config/import", auth.WrapFunc(adminHandler.ImportConfig))

	// Health and status routes
	mux.Handle("GET /admin/api/health", auth.WrapFunc(adminHandler.GetHealth))
	mux.Handle("GET /admin/api/status", auth.WrapFunc(adminHandler.GetStatus))

	// Logs route
	mux.Handle("GET /admin/api/logs", auth.WrapFunc(adminHandler.GetLogs))
	mux.Handle("GET /admin/api/logs/{id}", auth.WrapFunc(adminHandler.GetLog))

	// Active requests route
	mux.Handle("GET /admin/api/requests/active", auth.WrapFunc(adminHandler.GetActiveRequests))

	// Stats route
	mux.Handle("GET /admin/api/stats", auth.WrapFunc(adminHandler.GetStats))
	if cfg.TokenUsageHandler != nil {
		mux.Handle("GET /admin/api/token-usage", auth.Wrap(cfg.TokenUsageHandler))
	}

	// Unknown admin API paths must not fall through into the SPA handler.
	mux.Handle("/admin/api/", auth.WrapFunc(s.handleAdminAPINotFound))

	// Frontend static files (no auth required)
	// Serves the embedded React SPA with history fallback for client-side routing.
	// The frontend is built with base path "/admin/" so all assets are correctly prefixed.
	mux.Handle("/admin/", http.StripPrefix("/admin", web.Handler()))
}

// handleNotFound handles requests that don't match any registered route.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.logger.Warn("unmatched route",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
	)
	http.NotFound(w, r)
}

// handleProxy forwards requests to the proxy handler.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHandler.ServeHTTP(w, r)
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	s.logger.Info("starting HTTP server", zap.String("addr", ln.Addr().String()))
	if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
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
		Status:    HealthStatusOK,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", admin.ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
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
	s.mu.RLock()
	ln := s.listener
	s.mu.RUnlock()
	if ln != nil {
		return ln.Addr().String()
	}
	return s.server.Addr
}

// Start starts the admin HTTP server.
func (s *AdminServer) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	s.logger.Info("starting admin HTTP server", zap.String("addr", ln.Addr().String()))
	if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
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

// handleNotFound handles requests that don't match any registered admin route.
func (s *AdminServer) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.logger.Warn("unmatched route",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
	)
	http.NotFound(w, r)
}

// handleAdminAPINotFound preserves JSON semantics for unknown admin API routes.
func (s *AdminServer) handleAdminAPINotFound(w http.ResponseWriter, r *http.Request) {
	s.logger.Warn("unmatched admin api route",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
	)

	writeAdminAPINotFound(w)
}

func writeAdminAPINotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", admin.ContentTypeJSON)
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(model.ErrorResponse{
		Code:    admin.ErrCodeNotFound,
		Message: "Admin API endpoint not found",
	})
}

// Addr returns the admin server's address.
// If the server is listening, returns the actual address (useful when port 0 is used).
// Otherwise, returns the configured address.
func (s *AdminServer) Addr() string {
	s.mu.RLock()
	ln := s.listener
	s.mu.RUnlock()
	if ln != nil {
		return ln.Addr().String()
	}
	return s.server.Addr
}
