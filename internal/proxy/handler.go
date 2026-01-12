package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"switch-a/internal"
	"switch-a/internal/defaults"
	"switch-a/internal/model"
	"switch-a/internal/selector"

	"go.uber.org/zap"
)

// Default configuration values - derived from centralized defaults package.
const (
	DefaultMaxBodySizeMB     = defaults.MaxBodySizeMB
	DefaultMaxRetries        = defaults.MaxRetries
	DefaultConnectTimeoutSec = defaults.UpstreamConnectTimeoutSec
	DefaultUserHeader        = defaults.UserHeader
	// DefaultStickyEnabled is the default value for sticky sessions.
	// When enabled, clients are routed to the same provider for consistency.
	DefaultStickyEnabled = defaults.StickyEnabled
)

// Config keys for runtime configuration stored in the database.
// Using constants prevents typos and enables compile-time checking.
const (
	ConfigKeyTrustProxyHeaders      = "trust_proxy_headers"
	ConfigKeyUserHeader             = "user_header"
	ConfigKeyMaxBodySize            = "max_body_size"
	ConfigKeyAuthMode               = "auth_mode"
	ConfigKeyMaxRetries             = "max_retries"
	ConfigKeyUpstreamConnectTimeout = "upstream_connect_timeout"
	ConfigKeyFirstByteTimeout       = "first_byte_timeout"
	ConfigKeyUpstreamReadTimeout    = "upstream_read_timeout"
	ConfigKeySSEIdleTimeout         = "sse_idle_timeout"
	ConfigKeyStickyEnabled          = "sticky_enabled"
	ConfigKeyStickyTTL              = "sticky_ttl"
	ConfigKeyInterGroupStrategy     = selector.ConfigKeyInterGroupStrategy
)

// defaultStickyTTLSeconds is the default sticky session TTL in seconds.
// We use the canonical value from selector package to ensure consistency.
const defaultStickyTTLSeconds = selector.DefaultStickyTTLSeconds

// runtimeConfig holds configuration loaded from the store per-request.
// This struct is immutable once created and passed through the request flow.
type runtimeConfig struct {
	trustProxy       bool
	userHeader       string
	maxBodySizeMB    int64
	globalAuthMode   string
	maxRetries       int
	connectTimeout   time.Duration
	firstByteTimeout time.Duration
	readTimeout      time.Duration
	sseIdleTimeout   time.Duration
	stickyEnabled    bool
	stickyTTL        time.Duration
}

// Handler handles proxy requests.
type Handler struct {
	store           Store
	selector        Selector
	health          internal.HealthManager
	logger          *zap.Logger
	mu              sync.RWMutex
	transport       *Transport
	lastCfg         *transportCacheKey
	fallbackCounter atomic.Int64 // Counter for true round-robin in fallback mode
}

// transportCacheKey is used to detect if Transport config changed.
type transportCacheKey struct {
	connectTimeout   time.Duration
	firstByteTimeout time.Duration
	readTimeout      time.Duration
	sseIdleTimeout   time.Duration
}

// Store defines the minimal storage interface needed by the proxy handler.
type Store interface {
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetConfig(ctx context.Context, key string) (string, error)
	InsertLog(ctx context.Context, log *model.RequestLog) error
}

// Selector defines the provider selection interface.
type Selector interface {
	Select(ctx context.Context, req *model.SelectRequest) (*model.Provider, error)
	SelectExcluding(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error)
	UpdateStickyWithTTL(req *model.SelectRequest, providerID string, ttl time.Duration)
	ReleaseConcurrency(providerID string)
	// ClearConcurrency removes the concurrency counter for a deleted provider.
	// This should be called when a provider is deleted to prevent unbounded memory growth.
	ClearConcurrency(providerID string)
}

// Config holds proxy handler configuration.
type Config struct {
	Store    Store
	Selector Selector
	Health   internal.HealthManager
	Logger   *zap.Logger
}

// NewHandler creates a new proxy handler.
// Panics if Store is nil, as the handler cannot function without it.
func NewHandler(cfg Config) *Handler {
	if cfg.Store == nil {
		panic("proxy: Store is required but was nil")
	}
	return &Handler{
		store:    cfg.Store,
		selector: cfg.Selector,
		health:   cfg.Health,
		logger:   cfg.Logger,
	}
}

// getTransport returns a cached Transport or creates a new one if config changed.
func (h *Handler) getTransport(cfg *runtimeConfig) *Transport {
	key := &transportCacheKey{
		connectTimeout:   cfg.connectTimeout,
		firstByteTimeout: cfg.firstByteTimeout,
		readTimeout:      cfg.readTimeout,
		sseIdleTimeout:   cfg.sseIdleTimeout,
	}

	h.mu.RLock()
	if h.transport != nil && h.lastCfg != nil &&
		h.lastCfg.connectTimeout == key.connectTimeout &&
		h.lastCfg.firstByteTimeout == key.firstByteTimeout &&
		h.lastCfg.readTimeout == key.readTimeout &&
		h.lastCfg.sseIdleTimeout == key.sseIdleTimeout {
		transport := h.transport
		h.mu.RUnlock()
		return transport
	}
	h.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()

	// Double-check after acquiring write lock
	if h.transport != nil && h.lastCfg != nil &&
		h.lastCfg.connectTimeout == key.connectTimeout &&
		h.lastCfg.firstByteTimeout == key.firstByteTimeout &&
		h.lastCfg.readTimeout == key.readTimeout &&
		h.lastCfg.sseIdleTimeout == key.sseIdleTimeout {
		return h.transport
	}

	// Close old transport to prevent connection pool leak
	if h.transport != nil {
		h.transport.CloseIdleConnections()
	}

	h.transport = NewTransport(TransportConfig{
		ConnectTimeout:   cfg.connectTimeout,
		FirstByteTimeout: cfg.firstByteTimeout,
		ReadTimeout:      cfg.readTimeout,
		SSEIdleTimeout:   cfg.sseIdleTimeout,
	})
	h.lastCfg = key
	return h.transport
}

// proxyContext holds all state for a proxy request.
// Note: context.Context is intentionally not stored here; it should be passed
// through the call chain as a function parameter per Go best practices.
type proxyContext struct {
	r         *http.Request
	w         http.ResponseWriter
	cfg       *runtimeConfig
	transport *Transport
	apiType   string
	body      []byte
	info      RequestInfo
	selectReq *model.SelectRequest
	startTime time.Time
}

// ServeHTTP handles proxy requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	startTime := time.Now()

	// Load runtime configuration (immutable per-request)
	cfg, err := h.loadConfig(ctx)
	if err != nil { // coverage-ignore -- config load errors are rare after successful startup
		h.logger.Error("failed to load config", zap.Error(err))
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to load configuration")
		return
	}

	// Parse API type from path
	apiType, ok := ParseAPIType(r.URL.Path)
	if !ok {
		h.logger.Warn("unknown API type", zap.String("path", r.URL.Path))
		h.writeGatewayError(w, http.StatusBadRequest, ErrCodeUnknownAPIType, fmt.Sprintf("Unknown API path: %s", r.URL.Path))
		return
	}

	// Buffer request body for potential retries
	body, err := ConsumeAndReplaceBody(r, cfg.maxBodySizeMB)
	if err != nil {
		h.handleBodyError(w, err, cfg.maxBodySizeMB)
		return
	}

	// Build proxy context
	pctx := &proxyContext{
		r:         r,
		w:         w,
		cfg:       cfg,
		transport: h.getTransport(cfg),
		apiType:   apiType,
		body:      body,
		info: RequestInfo{
			ClientIP: ExtractClientIP(r, cfg.trustProxy),
			UserID:   ExtractUserID(r, cfg.userHeader),
			Model:    ExtractModel(r, apiType, body),
			APIType:  apiType,
		},
		startTime: startTime,
	}
	pctx.selectReq = &model.SelectRequest{
		ClientIP:      pctx.info.ClientIP,
		User:          pctx.info.UserID,
		APIType:       apiType,
		Model:         pctx.info.Model,
		StickyEnabled: cfg.stickyEnabled,
	}

	// Execute proxy with retry logic
	h.executeProxy(ctx, pctx)
}

// handleBodyError handles body read errors.
func (h *Handler) handleBodyError(w http.ResponseWriter, err error, maxSize int64) {
	if errors.Is(err, ErrBodyTooLarge) {
		h.writeGatewayError(w, http.StatusRequestEntityTooLarge, ErrCodeBodyTooLarge, fmt.Sprintf("Request body exceeds %d MB limit", maxSize))
		return
	}
	h.logger.Error("failed to read request body", zap.Error(err)) // coverage-ignore -- body read errors are rare
	h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to read request body")
}

// executeProxy runs the proxy logic with retry.
func (h *Handler) executeProxy(ctx context.Context, pctx *proxyContext) {
	var lastErr error
	var providerUsed *model.Provider
	var statusCode int
	var success bool
	excludedProviders := make(map[string]bool)
	headersWritten := false

	for attempt := 0; attempt <= pctx.cfg.maxRetries && !headersWritten; attempt++ {
		provider, err := h.selectProvider(ctx, pctx, attempt, excludedProviders)
		if err != nil {
			if errors.Is(err, internal.ErrNoProvider) {
				h.handleNoProvider(pctx)
				return
			}
			lastErr = err
			continue
		}
		if provider == nil {
			break
		}

		// Check provider-specific max retries BEFORE adding to excluded.
		// This ensures providers are only skipped if the global attempt exceeds their threshold,
		// but they can still be tried if selected on an earlier attempt.
		providerMaxRetries := pctx.cfg.maxRetries
		if provider.MaxRetries >= 0 {
			providerMaxRetries = provider.MaxRetries
		}
		if attempt > providerMaxRetries {
			// Provider can't be used at this attempt level.
			// Add to excluded to prevent re-selection, release concurrency, and continue.
			excludedProviders[provider.ID] = true
			h.releaseConcurrency(provider.ID)
			continue
		}

		providerUsed = provider
		excludedProviders[provider.ID] = true

		result := h.forwardToProvider(ctx, pctx, provider)
		headersWritten = result.headersWritten
		statusCode = result.statusCode
		lastErr = result.err
		success = result.success

		if result.done {
			break
		}
	}

	// Log request asynchronously.
	// Trade-off: Fire-and-forget logging may lose logs on immediate shutdown,
	// but avoids blocking the response path. For a high-throughput proxy,
	// this is an acceptable trade-off as most logs will complete.
	go h.logRequest(pctx.info, providerUsed, statusCode, success, lastErr, time.Since(pctx.startTime))

	// Handle exhausted retries
	if !success && !headersWritten { // coverage-ignore -- retry exhaustion tested at integration level
		h.handleExhaustedRetries(pctx, lastErr)
	}
}

// forwardResult holds the result of forwarding to a provider.
type forwardResult struct {
	headersWritten bool
	statusCode     int
	success        bool
	err            error
	done           bool // whether to stop retrying
}

// forwardToProvider forwards the request to a single provider.
// Note: MaxRetries check is performed in executeProxy before calling this function.
func (h *Handler) forwardToProvider(ctx context.Context, pctx *proxyContext, provider *model.Provider) forwardResult {
	result := forwardResult{}

	// Build upstream URL
	upstreamPath := BuildUpstreamPath(pctx.r.URL.Path, pctx.apiType)
	upstreamURL := h.buildFullURL(provider.BaseURL, upstreamPath, pctx.r.URL.RawQuery)

	// Create upstream request
	upstreamReq, err := BuildUpstreamRequest(ctx, pctx.r.Method, upstreamURL, pctx.body, pctx.r)
	if err != nil { // coverage-ignore -- request building rarely fails with valid inputs
		h.logger.Error("failed to build upstream request", zap.Error(err))
		result.err = err
		// Mark failure for invalid URL configuration so circuit breaker can trigger.
		// This prevents bad configurations from being infinitely retried.
		h.markFailure(ctx, provider.ID, err)
		h.releaseConcurrency(provider.ID)
		return result
	}

	// Set authentication header
	SetAuthHeader(upstreamReq.Header, provider.APIKey, provider.AuthMode, pctx.cfg.globalAuthMode, pctx.r)

	// Fetch upstream response WITHOUT writing to client yet
	// This allows us to check status code and retry if needed
	upstreamResp, err := pctx.transport.FetchUpstream(ctx, upstreamReq)
	if err != nil { // coverage-ignore -- network errors tested at integration level
		h.logger.Warn("upstream request failed",
			zap.String("provider_id", provider.ID),
			zap.Error(err),
		)
		result.err = err
		h.markFailure(ctx, provider.ID, err)
		h.releaseConcurrency(provider.ID)
		return result // headersWritten is false, so retry is possible
	}

	result.statusCode = upstreamResp.StatusCode

	// Check if response indicates failure (5xx or 429) BEFORE writing to client
	if shouldRetry(result.statusCode) {
		statusErr := fmt.Errorf("upstream returned status %d", result.statusCode)
		h.logger.Warn("upstream returned error status",
			zap.String("provider_id", provider.ID),
			zap.Int("status_code", result.statusCode),
		)
		result.err = statusErr // Record error for proper "all providers failed" message
		h.markFailure(ctx, provider.ID, statusErr)
		h.releaseConcurrency(provider.ID)
		upstreamResp.Drain() // Drain and close to enable connection reuse for retries
		// headersWritten is still false, allowing retry with another provider
		return result
	}

	// Commit: write response to client
	writeErr := pctx.transport.WriteToClient(ctx, pctx.w, upstreamResp)
	upstreamResp.Close()
	result.headersWritten = true
	result.done = true // No retry possible after headers are written

	if writeErr != nil { // coverage-ignore -- write errors occur when client disconnects
		h.logger.Warn("failed to write response to client",
			zap.String("provider_id", provider.ID),
			zap.Error(writeErr),
		)
		result.err = writeErr
		result.success = false

		// Check if this is an upstream error (not a client disconnect).
		// These errors indicate problems with the upstream provider and should
		// trigger circuit breaker to avoid routing to problematic providers:
		// - ErrReadTimeout/ErrSSEIdleTimeout: upstream stopped sending data
		// - UpstreamReadError: upstream connection reset, unexpected EOF, etc.
		if errors.Is(writeErr, ErrReadTimeout) || errors.Is(writeErr, ErrSSEIdleTimeout) || IsUpstreamReadError(writeErr) {
			h.markFailure(ctx, provider.ID, writeErr)
		}
		// For other write errors (e.g., client disconnected), we don't markFailure
		// as the upstream itself succeeded; only the client write failed.
		h.releaseConcurrency(provider.ID)
		return result
	}

	// True success: upstream responded and we wrote it to client
	result.success = true
	h.markSuccess(ctx, provider.ID)
	h.releaseConcurrency(provider.ID)

	// Update sticky cache
	if pctx.cfg.stickyEnabled && h.selector != nil {
		h.selector.UpdateStickyWithTTL(pctx.selectReq, provider.ID, pctx.cfg.stickyTTL)
	}

	return result
}

// selectProvider selects a provider for the given attempt.
func (h *Handler) selectProvider(ctx context.Context, pctx *proxyContext, attempt int, excluded map[string]bool) (*model.Provider, error) {
	if h.selector != nil {
		if attempt == 0 {
			return h.selector.Select(ctx, pctx.selectReq)
		}
		return h.selector.SelectExcluding(ctx, pctx.selectReq, excluded)
	}

	// Fallback: direct provider list (no selector configured)
	return h.selectProviderFallback(ctx, pctx, attempt)
}

// selectProviderFallback selects a provider when no Selector is configured.
//
// This fallback exists for minimal deployments where advanced selection features
// (health checks, concurrency limits, sticky sessions, group-based strategies) are not needed.
// It uses simple round-robin selection based on attempt number.
//
// Limitations compared to the full Selector:
//   - No health checks: unhealthy providers may be selected
//   - No concurrency limits: may overload providers
//   - No sticky sessions: same client may hit different providers
//   - No group-based strategies: ignores priority/weight/random settings
//
// For production deployments with multiple providers, configure a Selector for robust behavior.
func (h *Handler) selectProviderFallback(ctx context.Context, pctx *proxyContext, attempt int) (*model.Provider, error) {
	providers, err := h.store.ListProvidersByAPIType(ctx, pctx.apiType)
	if err != nil { // coverage-ignore -- database errors are rare after successful startup
		h.logger.Error("failed to list providers", zap.Error(err), zap.String("api_type", pctx.apiType))
		return nil, err
	}
	if len(providers) == 0 {
		return nil, internal.ErrNoProvider
	}
	// True round-robin: use atomic counter for cross-request distribution,
	// plus attempt offset to ensure retries hit different providers.
	idx := h.fallbackCounter.Add(1)
	provider := providers[(int(idx)-1+attempt)%len(providers)]
	return &provider, nil
}

// handleNoProvider handles the case when no provider is available.
func (h *Handler) handleNoProvider(pctx *proxyContext) {
	h.logger.Warn("no providers available", zap.String("api_type", pctx.apiType))
	h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", pctx.apiType))
	go h.logRequest(pctx.info, nil, 0, false, internal.ErrNoProvider, time.Since(pctx.startTime))
}

// handleExhaustedRetries handles exhausted retry attempts.
func (h *Handler) handleExhaustedRetries(pctx *proxyContext, lastErr error) {
	if lastErr != nil {
		h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderExhausted, "All providers failed")
	} else {
		h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", pctx.apiType))
	}
}

// markSuccess marks a successful request for the provider.
func (h *Handler) markSuccess(ctx context.Context, providerID string) {
	if h.health != nil {
		h.health.MarkSuccess(ctx, providerID)
	}
}

// markFailure marks a failed request for the provider.
func (h *Handler) markFailure(ctx context.Context, providerID string, err error) {
	if h.health != nil {
		h.health.MarkFailure(ctx, providerID, err)
	}
}

// releaseConcurrency releases the concurrency slot for a provider.
func (h *Handler) releaseConcurrency(providerID string) {
	if h.selector != nil {
		h.selector.ReleaseConcurrency(providerID)
	}
}

// loadConfig loads runtime configuration from the store.
// Returns an immutable runtimeConfig struct for use during the request.
func (h *Handler) loadConfig(ctx context.Context) (*runtimeConfig, error) {
	cfg := &runtimeConfig{}

	// Trust proxy headers
	trustProxy, err := h.store.GetConfig(ctx, ConfigKeyTrustProxyHeaders)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.trustProxy = trustProxy == "true"

	// User header
	userHeader, err := h.store.GetConfig(ctx, ConfigKeyUserHeader)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	if userHeader != "" {
		cfg.userHeader = userHeader
	} else {
		cfg.userHeader = DefaultUserHeader
	}

	// Max body size
	maxBodySize, err := h.store.GetConfig(ctx, ConfigKeyMaxBodySize)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.maxBodySizeMB = parseInt64OrDefault(maxBodySize, DefaultMaxBodySizeMB)

	// Global auth mode
	authMode, err := h.store.GetConfig(ctx, ConfigKeyAuthMode)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	if authMode != "" {
		cfg.globalAuthMode = authMode
	} else {
		cfg.globalAuthMode = AuthModeAuto
	}

	// Max retries
	maxRetries, err := h.store.GetConfig(ctx, ConfigKeyMaxRetries)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.maxRetries = parseIntOrDefault(maxRetries, DefaultMaxRetries)

	// Upstream timeouts - errors logged but use defaults (non-critical config)
	connectTimeout, err := h.store.GetConfig(ctx, ConfigKeyUpstreamConnectTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get upstream_connect_timeout, using default", zap.Error(err))
	}
	firstByteTimeout, err := h.store.GetConfig(ctx, ConfigKeyFirstByteTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get first_byte_timeout, using default", zap.Error(err))
	}
	readTimeout, err := h.store.GetConfig(ctx, ConfigKeyUpstreamReadTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get upstream_read_timeout, using default", zap.Error(err))
	}

	cfg.connectTimeout = time.Duration(parseIntOrDefault(connectTimeout, DefaultConnectTimeoutSec)) * time.Second
	cfg.firstByteTimeout = time.Duration(parseIntOrDefault(firstByteTimeout, defaults.FirstByteTimeoutSec)) * time.Second
	cfg.readTimeout = time.Duration(parseIntOrDefault(readTimeout, 0)) * time.Second

	// SSE idle timeout - protects against silent upstream connections
	sseIdleTimeout, err := h.store.GetConfig(ctx, ConfigKeySSEIdleTimeout)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get sse_idle_timeout, using default", zap.Error(err))
	}
	cfg.sseIdleTimeout = time.Duration(parseIntOrDefault(sseIdleTimeout, defaults.SSEIdleTimeoutSec)) * time.Second

	// Sticky session config
	stickyEnabled, err := h.store.GetConfig(ctx, ConfigKeyStickyEnabled)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get sticky_enabled, using default", zap.Error(err))
	}
	cfg.stickyEnabled = parseBoolOrDefault(stickyEnabled, DefaultStickyEnabled)

	stickyTTL, err := h.store.GetConfig(ctx, ConfigKeyStickyTTL)
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get sticky_ttl, using default", zap.Error(err))
	}
	cfg.stickyTTL = time.Duration(parseIntOrDefault(stickyTTL, defaultStickyTTLSeconds)) * time.Second

	return cfg, nil
}

// writeGatewayError writes a gateway error response.
func (h *Handler) writeGatewayError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	err := model.NewGatewayError(code, message)
	if encErr := json.NewEncoder(w).Encode(err); encErr != nil { // coverage-ignore -- JSON encoding of simple struct rarely fails
		h.logger.Error("failed to encode error response", zap.Error(encErr))
	}
}

// logInsertTimeout is the maximum time allowed for inserting a request log.
// This prevents goroutine accumulation if the database is slow or blocked.
const logInsertTimeout = 2 * time.Second

// logRequest logs the request asynchronously.
// Note: Uses context.Background() with timeout because this runs after the HTTP response
// completes and the request context may already be cancelled.
func (h *Handler) logRequest(info RequestInfo, provider *model.Provider, statusCode int, success bool, err error, latency time.Duration) {
	log := &model.RequestLog{
		APIType:    info.APIType,
		Model:      info.Model,
		ClientIP:   info.ClientIP,
		UserID:     info.UserID,
		StatusCode: statusCode,
		LatencyMs:  latency.Milliseconds(),
		Success:    success,
		CreatedAt:  time.Now(),
	}

	if provider != nil {
		log.ProviderID = provider.ID
	}

	if err != nil { // coverage-ignore -- error logging path
		log.ErrorMsg = err.Error()
	}

	// Use timeout to prevent goroutine accumulation if database is slow or blocked
	ctx, cancel := context.WithTimeout(context.Background(), logInsertTimeout)
	defer cancel()

	if insertErr := h.store.InsertLog(ctx, log); insertErr != nil { // coverage-ignore -- log insert errors are logged but don't affect response
		h.logger.Error("failed to insert request log", zap.Error(insertErr))
	}
}

// shouldRetry determines if a response status code indicates a retryable failure.
// Retries on server errors (5xx) and rate limiting (429 Too Many Requests).
func shouldRetry(statusCode int) bool {
	return statusCode >= defaults.StatusServerError || statusCode == defaults.StatusTooManyRequests
}

// buildFullURL constructs the full upstream URL.
// It properly joins the baseURL's existing path (if any) with the given path.
// For example: baseURL="https://api.openai.com/v1", path="/chat/completions"
// yields "https://api.openai.com/v1/chat/completions".
func (h *Handler) buildFullURL(baseURL, path, query string) string {
	// url.JoinPath handles path joining correctly:
	// - Preserves the base URL's scheme, host, and existing path
	// - Properly joins paths (handles slashes, dots, etc.)
	joined, err := url.JoinPath(baseURL, path)
	if err != nil {
		// Invalid base URL - fall back to string concatenation with proper slash handling.
		h.logger.Warn("invalid base URL, falling back to string concatenation",
			zap.String("base_url", baseURL),
			zap.Error(err),
		)
		if !strings.HasSuffix(baseURL, "/") && !strings.HasPrefix(path, "/") {
			return baseURL + "/" + path
		}
		return baseURL + path
	}

	if query == "" {
		return joined
	}

	// Append query string if present
	return joined + "?" + query
}

// parseIntOrDefault parses a string to int, returning defaultVal on error.
func parseIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// parseInt64OrDefault parses a string to int64, returning defaultVal on error.
func parseInt64OrDefault(s string, defaultVal int64) int64 {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

// parseBoolOrDefault parses a string to bool, returning defaultVal if empty or invalid.
// Accepts "true", "1" as true, and "false", "0" as false (case-insensitive).
// Invalid values (e.g., "xyz") return defaultVal instead of false.
func parseBoolOrDefault(s string, defaultVal bool) bool {
	if s == "" {
		return defaultVal
	}
	lower := strings.ToLower(s)
	switch lower {
	case "true", "1":
		return true
	case "false", "0":
		return false
	default:
		return defaultVal
	}
}
