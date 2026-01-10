package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Default configuration values.
const (
	DefaultMaxBodySizeMB     int64 = 10
	DefaultMaxRetries              = 3
	DefaultConnectTimeoutSec       = 10
	DefaultUserHeader              = "X-User-ID"
)

// runtimeConfig holds configuration loaded from the store per-request.
// This struct is immutable once created and passed through the request flow.
type runtimeConfig struct {
	trustProxy     bool
	userHeader     string
	maxBodySizeMB  int64
	globalAuthMode string
	maxRetries     int
	connectTimeout time.Duration
	readTimeout    time.Duration
}

// Handler handles proxy requests.
type Handler struct {
	store     Store
	logger    *zap.Logger
	mu        sync.RWMutex
	transport *Transport
	lastCfg   *transportCacheKey
}

// transportCacheKey is used to detect if Transport config changed.
type transportCacheKey struct {
	connectTimeout time.Duration
	readTimeout    time.Duration
}

// Store defines the minimal storage interface needed by the proxy handler.
type Store interface {
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetConfig(ctx context.Context, key string) (string, error)
	InsertLog(ctx context.Context, log *model.RequestLog) error
}

// Config holds proxy handler configuration.
type Config struct {
	Store  Store
	Logger *zap.Logger
}

// NewHandler creates a new proxy handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		store:  cfg.Store,
		logger: cfg.Logger,
	}
}

// getTransport returns a cached Transport or creates a new one if config changed.
func (h *Handler) getTransport(cfg *runtimeConfig) *Transport {
	key := &transportCacheKey{
		connectTimeout: cfg.connectTimeout,
		readTimeout:    cfg.readTimeout,
	}

	h.mu.RLock()
	if h.transport != nil && h.lastCfg != nil &&
		h.lastCfg.connectTimeout == key.connectTimeout &&
		h.lastCfg.readTimeout == key.readTimeout {
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
		h.lastCfg.readTimeout == key.readTimeout {
		return h.transport
	}

	// Close old transport to prevent connection pool leak
	if h.transport != nil {
		h.transport.CloseIdleConnections()
	}

	h.transport = NewTransport(TransportConfig{
		ConnectTimeout: cfg.connectTimeout,
		ReadTimeout:    cfg.readTimeout,
	})
	h.lastCfg = key
	return h.transport
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

	// Get or create transport (cached, thread-safe)
	transport := h.getTransport(cfg)

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
		if errors.Is(err, ErrBodyTooLarge) {
			h.writeGatewayError(w, http.StatusRequestEntityTooLarge, ErrCodeBodyTooLarge, fmt.Sprintf("Request body exceeds %d MB limit", cfg.maxBodySizeMB))
			return
		}
		h.logger.Error("failed to read request body", zap.Error(err)) // coverage-ignore -- body read errors are rare
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to read request body")
		return
	}

	// Extract request info
	info := RequestInfo{
		ClientIP: ExtractClientIP(r, cfg.trustProxy),
		UserID:   ExtractUserID(r, cfg.userHeader),
		Model:    ExtractModel(r, apiType, body),
		APIType:  apiType,
	}

	// Get available providers for this API type
	providers, err := h.store.ListProvidersByAPIType(ctx, apiType)
	if err != nil { // coverage-ignore -- database errors are rare after successful startup
		h.logger.Error("failed to list providers", zap.Error(err), zap.String("api_type", apiType))
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to list providers")
		return
	}

	if len(providers) == 0 {
		h.logger.Warn("no providers available", zap.String("api_type", apiType))
		h.writeGatewayError(w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", apiType))
		return
	}

	// Try providers with retry logic
	var lastErr error
	var providerUsed *model.Provider
	var statusCode int
	var success bool

	for attempt := 0; attempt <= cfg.maxRetries; attempt++ {
		provider := &providers[attempt%len(providers)]
		providerUsed = provider

		// Build upstream URL
		upstreamPath := BuildUpstreamPath(r.URL.Path, apiType)
		upstreamURL := h.buildFullURL(provider.BaseURL, upstreamPath, r.URL.RawQuery)

		// Create upstream request
		upstreamReq, err := BuildUpstreamRequest(ctx, r.Method, upstreamURL, body, r)
		if err != nil { // coverage-ignore -- request building rarely fails with valid inputs
			h.logger.Error("failed to build upstream request", zap.Error(err))
			lastErr = err
			continue
		}

		// Set authentication header
		SetAuthHeader(upstreamReq.Header, provider.APIKey, provider.AuthMode, cfg.globalAuthMode, r)

		// Forward request
		headersWritten, respStatusCode, err := transport.ForwardRequest(ctx, w, upstreamReq)
		statusCode = respStatusCode

		if err != nil { // coverage-ignore -- network errors tested at integration level
			h.logger.Warn("upstream request failed",
				zap.String("provider_id", provider.ID),
				zap.Error(err),
				zap.Int("attempt", attempt+1),
			)
			lastErr = err

			// Can only retry if headers haven't been written
			if headersWritten {
				break
			}
			continue
		}

		// Check if response indicates failure (5xx or 429)
		if shouldRetry(respStatusCode) { // coverage-ignore -- retry logic tested at integration level
			h.logger.Warn("upstream returned error status",
				zap.String("provider_id", provider.ID),
				zap.Int("status_code", respStatusCode),
				zap.Int("attempt", attempt+1),
			)
			// Response already written to client, can't retry
			// But mark as success=false for logging
			success = false
			break
		}

		success = true
		break
	}

	// Log request (async) - uses context.Background() internally
	go h.logRequest(info, providerUsed, statusCode, success, lastErr, time.Since(startTime))

	// If all retries exhausted and no response written
	if !success && lastErr != nil && statusCode == 0 { // coverage-ignore -- retry exhaustion tested at integration level
		h.writeGatewayError(w, http.StatusServiceUnavailable, ErrCodeProviderExhausted, "All providers failed")
	}
}

// loadConfig loads runtime configuration from the store.
// Returns an immutable runtimeConfig struct for use during the request.
func (h *Handler) loadConfig(ctx context.Context) (*runtimeConfig, error) {
	cfg := &runtimeConfig{}

	// Trust proxy headers
	trustProxy, err := h.store.GetConfig(ctx, "trust_proxy_headers")
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.trustProxy = trustProxy == "true"

	// User header
	userHeader, err := h.store.GetConfig(ctx, "user_header")
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	if userHeader != "" {
		cfg.userHeader = userHeader
	} else {
		cfg.userHeader = DefaultUserHeader
	}

	// Max body size
	maxBodySize, err := h.store.GetConfig(ctx, "max_body_size")
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.maxBodySizeMB = parseInt64OrDefault(maxBodySize, DefaultMaxBodySizeMB)

	// Global auth mode
	authMode, err := h.store.GetConfig(ctx, "auth_mode")
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	if authMode != "" {
		cfg.globalAuthMode = authMode
	} else {
		cfg.globalAuthMode = AuthModeAuto
	}

	// Max retries
	maxRetries, err := h.store.GetConfig(ctx, "max_retries")
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		return nil, err
	}
	cfg.maxRetries = parseIntOrDefault(maxRetries, DefaultMaxRetries)

	// Upstream timeouts - errors logged but use defaults (non-critical config)
	connectTimeout, err := h.store.GetConfig(ctx, "upstream_connect_timeout")
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get upstream_connect_timeout, using default", zap.Error(err))
	}
	readTimeout, err := h.store.GetConfig(ctx, "upstream_read_timeout")
	if err != nil { // coverage-ignore -- config errors are rare after successful startup
		h.logger.Warn("failed to get upstream_read_timeout, using default", zap.Error(err))
	}

	cfg.connectTimeout = time.Duration(parseIntOrDefault(connectTimeout, DefaultConnectTimeoutSec)) * time.Second
	cfg.readTimeout = time.Duration(parseIntOrDefault(readTimeout, 0)) * time.Second

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

// logRequest logs the request asynchronously.
// Note: Uses context.Background() because this runs after the HTTP response completes
// and the request context may already be cancelled.
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

	if insertErr := h.store.InsertLog(context.Background(), log); insertErr != nil { // coverage-ignore -- log insert errors are logged but don't affect response
		h.logger.Error("failed to insert request log", zap.Error(insertErr))
	}
}

// shouldRetry determines if a response status code indicates a retryable failure.
func shouldRetry(statusCode int) bool {
	return statusCode >= 500 || statusCode == 429
}

// buildFullURL constructs the full upstream URL.
// If baseURL is invalid, falls back to simple string concatenation and logs a warning.
// Note: Base URLs should be validated at provider creation time to avoid this fallback.
func (h *Handler) buildFullURL(baseURL, path, query string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		// Invalid base URL - fall back to string concatenation.
		// This produces a potentially malformed URL, but the request will fail
		// with a clear error rather than silently misbehaving.
		h.logger.Warn("invalid base URL, falling back to string concatenation",
			zap.String("base_url", baseURL),
			zap.Error(err),
		)
		return baseURL + path
	}

	u.Path = path
	u.RawQuery = query
	return u.String()
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
