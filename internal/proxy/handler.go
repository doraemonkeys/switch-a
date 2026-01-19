package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"switch-a/internal"
	"switch-a/internal/defaults"
	"switch-a/internal/model"
	"switch-a/internal/selector"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler handles proxy requests.
type Handler struct {
	store           Store
	selector        Selector
	health          internal.HealthManager
	activeRegistry  *ActiveRequestRegistry
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

// firstWriteResponseWriter tracks first data write to enable sticky session fallback.
// When an SSE stream outlives the sticky TTL, we use the active provider registry
// to maintain session affinity. This wrapper signals when actual data starts flowing
// so the registry can mark the request as having received data.
type firstWriteResponseWriter struct {
	http.ResponseWriter
	onFirstWrite func()
	written      bool
}

func (w *firstWriteResponseWriter) Write(p []byte) (int, error) {
	if !w.written && w.onFirstWrite != nil {
		w.onFirstWrite()
		w.written = true
	}
	return w.ResponseWriter.Write(p)
}

// Flush preserves http.Flusher interface for SSE streaming.
func (w *firstWriteResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Store defines the minimal storage interface needed by the proxy handler.
type Store interface {
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetConfig(ctx context.Context, key string) (string, error)
	InsertLog(ctx context.Context, log *model.RequestLog) error
	InsertAttempts(ctx context.Context, attempts []model.RequestAttempt) error
}

// Selector defines the provider selection interface.
type Selector interface {
	Select(ctx context.Context, req *model.SelectRequest) (*model.Provider, error)
	SelectExcluding(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error)
	SelectWithMetadata(ctx context.Context, req *model.SelectRequest) (*selector.SelectResult, error)
	UpdateStickyWithTTL(req *model.SelectRequest, providerID string, ttl time.Duration)
	ReleaseConcurrency(providerID string)
	// ClearConcurrency removes the concurrency counter for a deleted provider.
	// This should be called when a provider is deleted to prevent unbounded memory growth.
	ClearConcurrency(providerID string)
}

// Config holds proxy handler configuration.
type Config struct {
	Store          Store
	Selector       Selector
	Health         internal.HealthManager
	ActiveRegistry *ActiveRequestRegistry
	Logger         *zap.Logger
}

// NewHandler creates a new proxy handler.
// Panics if Store or Logger is nil, as the handler cannot function without them.
func NewHandler(cfg Config) *Handler {
	if cfg.Store == nil {
		panic("proxy: Store is required but was nil")
	}
	if cfg.Logger == nil {
		panic("proxy: Logger is required but was nil")
	}
	return &Handler{
		store:          cfg.Store,
		selector:       cfg.Selector,
		health:         cfg.Health,
		activeRegistry: cfg.ActiveRegistry,
		logger:         cfg.Logger,
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

	// Double-check pattern: another goroutine may have updated transport between our read unlock and write lock
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

// proxyContext aggregates per-request state to reduce method signature complexity.
// Immutable fields (r, cfg, apiType, body, info, startTime, requestID) are set once during construction.
// Mutable fields (selectReq, isSticky, attempts) are modified during retry orchestration.
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
	requestID string                 // UUID for this request
	isSticky  bool                   // Whether provider came from sticky cache
	attempts  []model.RequestAttempt // Attempts made during this request
}

// ServeHTTP handles proxy requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	startTime := time.Now()
	requestID := uuid.New().String()

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
		requestID: requestID,
		attempts:  make([]model.RequestAttempt, 0),
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

// retryState tracks mutable state across retry attempts in executeProxy.
type retryState struct {
	lastErr           error
	providerUsed      *model.Provider
	statusCode        int
	success           bool
	isSSE             bool
	headersWritten    bool
	excludedProviders map[string]bool
	currentProvider   *model.Provider
	// providerAttempt is 0-based within a single provider. A provider with MaxRetries=N
	// can be attempted (N+1) times: providerAttempt 0..N.
	providerAttempt  int
	activeRegistered bool
}

// selectAndRegisterProvider selects a provider and registers the active request.
// Returns true if selection succeeded, false if the loop should break or return early.
// The earlyReturn flag indicates whether to return immediately from executeProxy.
func (h *Handler) selectAndRegisterProvider(ctx context.Context, pctx *proxyContext, state *retryState, attempt int) (continueLoop, earlyReturn bool) {
	provider, fromSticky, err := h.selectProviderWithTracking(ctx, pctx, attempt, state.excludedProviders)
	if err != nil {
		if errors.Is(err, internal.ErrNoProvider) {
			// No provider available before any upstream attempt -> immediate error.
			if attempt == 0 && len(pctx.attempts) == 0 {
				h.handleNoProvider(pctx)
				return false, true
			}
			// No providers left after previous failures -> treat as exhausted.
			return false, false
		}
		state.lastErr = err
		return false, false
	}
	if provider == nil {
		return false, false
	}

	state.currentProvider = provider
	state.providerAttempt = 0

	// Track sticky cache hit on first attempt
	if attempt == 0 {
		pctx.isSticky = fromSticky
	}

	h.registerActiveRequest(pctx, state, provider)
	return true, false
}

// registerActiveRequest registers or updates the active request in the registry.
func (h *Handler) registerActiveRequest(pctx *proxyContext, state *retryState, provider *model.Provider) {
	if h.activeRegistry == nil {
		return
	}
	// Register (or update) active request.
	// Note: We update ProviderID on provider switch so sticky fallback reflects the
	// actual upstream provider that eventually produces data.
	h.activeRegistry.Register(&ActiveRequest{
		RequestID:  pctx.requestID,
		ProviderID: provider.ID,
		Model:      pctx.info.Model,
		APIType:    pctx.apiType,
		UserID:     pctx.info.UserID,
		ClientIP:   pctx.info.ClientIP,
		IsSSE:      false, // Updated after response type is known
		StartedAt:  pctx.startTime,
	})
	state.activeRegistered = true
}

// recordAttempt records a request attempt in the proxy context.
func (h *Handler) recordAttempt(pctx *proxyContext, state *retryState, result forwardResult, attempt int, attemptStart time.Time) {
	attemptRecord := model.RequestAttempt{
		RequestID:   pctx.requestID,
		ProviderID:  state.currentProvider.ID,
		Attempt:     attempt,
		StatusCode:  result.statusCode,
		BodySnippet: result.bodySnippet,
		LatencyMs:   time.Since(attemptStart).Milliseconds(),
		CreatedAt:   time.Now(),
	}
	if result.err != nil {
		attemptRecord.Error = result.err.Error()
	}
	pctx.attempts = append(pctx.attempts, attemptRecord)
}

// tryIncrementAndExhaustsProvider attempts to increment the provider retry counter.
// Returns true if the provider is exhausted (should switch to a different provider).
func (h *Handler) tryIncrementAndExhaustsProvider(ctx context.Context, state *retryState) bool {
	// Retry decision: by default, retry the SAME provider up to Provider.MaxRetries times.
	// Force immediate provider switch for:
	// 1. Permanent failures (402, 401, 403) - retrying same provider won't help
	// 2. Circuit breaker triggered - provider is marked unavailable
	maxRetries := max(0, state.currentProvider.MaxRetries)
	if shouldForceProviderSwitch(state.statusCode) {
		maxRetries = forceProviderSwitch
	} else if h.health != nil && !h.health.IsAvailable(ctx, state.currentProvider.ID) {
		maxRetries = forceProviderSwitch
	}
	if state.providerAttempt < maxRetries {
		state.providerAttempt++
		return false
	}
	return true
}

// excludeCurrentProvider marks the current provider as excluded and releases its concurrency.
func (h *Handler) excludeCurrentProvider(state *retryState) {
	state.excludedProviders[state.currentProvider.ID] = true
	h.releaseConcurrency(state.currentProvider.ID)
	state.currentProvider = nil
}

// executeProxy runs the proxy logic with retry.
func (h *Handler) executeProxy(ctx context.Context, pctx *proxyContext) {
	state := &retryState{
		excludedProviders: make(map[string]bool),
	}

	for attempt := 0; !state.headersWritten; attempt++ {
		if pctx.cfg.globalMaxAttempts > 0 && attempt >= pctx.cfg.globalMaxAttempts {
			break
		}

		// Early exit on context cancellation - avoid wasted retries
		if err := ctx.Err(); err != nil {
			state.lastErr = err
			break
		}

		attemptStart := time.Now()

		// Select a provider if we don't have one or if we just switched.
		if state.currentProvider == nil {
			continueLoop, earlyReturn := h.selectAndRegisterProvider(ctx, pctx, state, attempt)
			if earlyReturn {
				return
			}
			if !continueLoop {
				break
			}
		}

		state.providerUsed = state.currentProvider

		result := h.forwardToProvider(ctx, pctx, state.currentProvider)
		state.headersWritten = result.headersWritten
		state.statusCode = result.statusCode
		state.lastErr = result.err
		state.success = result.success
		state.isSSE = result.isSSE

		// Note: SSE status is updated in forwardToProvider() BEFORE streaming starts.
		// This ensures long-running SSE streams are visible in the Monitor page with the SSE badge.

		h.recordAttempt(pctx, state, result, attempt, attemptStart)

		if result.done {
			break
		}

		if h.tryIncrementAndExhaustsProvider(ctx, state) {
			h.excludeCurrentProvider(state)
		}
	}

	h.finalizeProxy(pctx, state)
}

// finalizeProxy performs cleanup and logging after the retry loop completes.
func (h *Handler) finalizeProxy(pctx *proxyContext, state *retryState) {
	// Release concurrency for the current provider (if any).
	if state.currentProvider != nil {
		h.releaseConcurrency(state.currentProvider.ID)
	}

	// Unregister active request
	if h.activeRegistry != nil {
		h.activeRegistry.Unregister(pctx.requestID)
	}

	// Log request asynchronously.
	// Trade-off: Fire-and-forget logging may lose logs on immediate shutdown,
	// but avoids blocking the response path. For a high-throughput proxy,
	// this is an acceptable trade-off as most logs will complete.
	go h.logRequest(pctx, state.providerUsed, state.statusCode, state.success, state.isSSE, state.lastErr, time.Since(pctx.startTime))

	// Handle exhausted retries
	if !state.success && !state.headersWritten { // coverage-ignore -- retry exhaustion tested at integration level
		h.handleExhaustedRetries(pctx, state.lastErr)
	}
}

// forwardResult holds the result of forwarding to a provider.
type forwardResult struct {
	headersWritten bool
	statusCode     int
	success        bool
	err            error
	done           bool   // whether to stop retrying
	isSSE          bool   // whether the response was SSE
	bodySnippet    string // first ~500 bytes of error response (failover scenarios only)
}

// forwardToProvider forwards the request to a single provider.
// Note: Retry orchestration (per-provider retries and provider switching) is handled in executeProxy.
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
		result.success = false // Explicitly mark as failure
		// Mark failure for invalid URL configuration so circuit breaker can trigger.
		// This prevents bad configurations from being infinitely retried.
		h.markFailure(ctx, provider.ID, err)
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
		result.success = false // Explicitly mark as failure
		h.markFailure(ctx, provider.ID, err)
		return result // headersWritten is false, so retry is possible
	}

	result.statusCode = upstreamResp.StatusCode
	result.isSSE = upstreamResp.IsSSE()

	// Check if response indicates failover-eligible error BEFORE writing to client.
	// Failover-eligible: 5xx, 402, 429, 401, 403 - these are provider-side issues.
	if shouldFailover(result.statusCode) {
		statusErr := fmt.Errorf("upstream returned status %d", result.statusCode)
		h.logger.Warn("upstream returned error status",
			zap.String("provider_id", provider.ID),
			zap.Int("status_code", result.statusCode),
		)
		result.err = statusErr // Record error for proper "all providers failed" message
		result.success = false // Explicitly mark as failure for logging
		h.markFailure(ctx, provider.ID, statusErr)
		// Capture response body snippet for debugging before draining.
		// This helps diagnose why upstream returned an error (e.g., quota exceeded, invalid key).
		result.bodySnippet = upstreamResp.DrainWithSnippet(0) // 0 = use default size (512 bytes)
		// headersWritten is still false, allowing retry with another provider
		return result
	}

	// Update SSE status in active registry BEFORE starting to stream.
	// This is crucial for SSE requests: WriteToClient blocks until the entire stream completes,
	// so we need to update the SSE flag now while the request is still "active" and visible
	// in monitoring. If we waited until after WriteToClient, the request would be unregistered
	// almost immediately after being marked as SSE, making it invisible in the Monitor page.
	if h.activeRegistry != nil && result.isSSE {
		h.activeRegistry.UpdateSSE(pctx.requestID, true)
	}

	// Wrap ResponseWriter to detect first data write for sticky session fallback
	wrappedWriter := &firstWriteResponseWriter{
		ResponseWriter: pctx.w,
		onFirstWrite: func() {
			if h.activeRegistry != nil {
				h.activeRegistry.MarkDataReceived(pctx.requestID)
			}
		},
	}

	// Commit: write response to client
	writeErr := pctx.transport.WriteToClient(ctx, wrappedWriter, upstreamResp)
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
		return result
	}

	// Mark as success only for 2xx/3xx status codes.
	// Other status codes (4xx like 400 Bad Request) indicate the request was processed
	// but wasn't a true "success" for health tracking purposes.
	// Note: Retryable error codes (401, 403, 429, 5xx) are already handled above
	// and will not reach this point.
	if result.statusCode < defaults.StatusClientError {
		result.success = true
		h.markSuccess(ctx, provider.ID)
	} else {
		// Non-retryable client errors (e.g., 400 Bad Request, 404 Not Found)
		// Set result flag to false but skip health tracking - client errors don't reflect provider health
		result.success = false
		h.logger.Info("upstream returned client error",
			zap.String("provider_id", provider.ID),
			zap.Int("status_code", result.statusCode),
		)
	}

	// Update sticky cache
	if pctx.cfg.stickyEnabled && h.selector != nil {
		h.selector.UpdateStickyWithTTL(pctx.selectReq, provider.ID, pctx.cfg.stickyTTL)
	}

	return result
}
