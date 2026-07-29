package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler handles proxy requests.
type Handler struct {
	store                      Store
	selector                   Selector
	health                     internal.HealthManager
	activeRegistry             *ActiveRequestRegistry
	visibleContinuitySeedStore model.VisibleContinuitySeedStore
	logger                     *zap.Logger
	mu                         sync.RWMutex
	transport                  *Transport
	lastCfg                    *transportCacheKey
	fallbackCounter            atomic.Int64 // Counter for true round-robin in fallback mode
	wsForwarder                *WebSocketForwarder
	auth                       *providerauth.Service
}

// transportCacheKey is used to detect if Transport config changed.
type transportCacheKey struct {
	connectTimeout   time.Duration
	firstByteTimeout time.Duration
	readTimeout      time.Duration
	sseIdleTimeout   time.Duration
}

// Equals returns true if the two cache keys have identical configuration.
func (k *transportCacheKey) Equals(other *transportCacheKey) bool {
	return k.connectTimeout == other.connectTimeout &&
		k.firstByteTimeout == other.firstByteTimeout &&
		k.readTimeout == other.readTimeout &&
		k.sseIdleTimeout == other.sseIdleTimeout
}

// firstWriteResponseWriter tracks first data write to enable sticky session fallback.
// When an SSE stream outlives the sticky TTL, we use the active provider registry
// to maintain session affinity. This wrapper signals when actual data starts flowing
// so the registry can mark the request as having received data.
// It also tracks the time of first write for TTFT (Time To First Token) metrics,
// and counts total bytes written for transfer statistics.
type firstWriteResponseWriter struct {
	http.ResponseWriter
	onFirstWrite   func()
	onCommit       func()
	onWrite        func(int, time.Time)
	written        bool
	committed      bool
	firstWriteTime time.Time // Time of first data write (for TTFT calculation)
	bytesWritten   int64     // Total bytes written to client
}

func (w *firstWriteResponseWriter) Write(p []byte) (int, error) {
	writeTime := time.Now()
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.commit()
		if !w.written {
			w.firstWriteTime = writeTime
			if w.onFirstWrite != nil {
				w.onFirstWrite()
			}
			w.written = true
		}
		w.bytesWritten += int64(n)
		if w.onWrite != nil {
			w.onWrite(n, writeTime)
		}
	}
	return n, err
}

func (w *firstWriteResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
	w.commit()
}

func (w *firstWriteResponseWriter) commit() {
	if w == nil || w.committed {
		return
	}
	w.committed = true
	if w.onCommit != nil {
		w.onCommit()
	}
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
	EvictProviderContinuity(providerID string)
	ReleaseConcurrency(providerID string)
	// ClearConcurrency removes the concurrency counter for a deleted provider.
	// This should be called when a provider is deleted to prevent unbounded memory growth.
	ClearConcurrency(providerID string)
}

// Config holds proxy handler configuration.
type Config struct {
	Store                      Store
	Selector                   Selector
	Health                     internal.HealthManager
	ActiveRegistry             *ActiveRequestRegistry
	VisibleContinuitySeedStore model.VisibleContinuitySeedStore
	Auth                       *providerauth.Service
	Logger                     *zap.Logger
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
	visibleContinuitySeedStore := cfg.VisibleContinuitySeedStore
	if visibleContinuitySeedStore == nil {
		visibleContinuitySeedStore = NewVisibleContinuitySeedStore()
	}
	return &Handler{
		store:                      cfg.Store,
		selector:                   cfg.Selector,
		health:                     cfg.Health,
		activeRegistry:             cfg.ActiveRegistry,
		visibleContinuitySeedStore: visibleContinuitySeedStore,
		logger:                     cfg.Logger,
		wsForwarder:                NewWebSocketForwarder(WebSocketForwarderConfig{Logger: cfg.Logger}),
		auth:                       cfg.Auth,
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
	if h.transport != nil && h.lastCfg != nil && h.lastCfg.Equals(key) {
		transport := h.transport
		h.mu.RUnlock()
		return transport
	}
	h.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()

	// Double-check pattern: another goroutine may have updated transport between our read unlock and write lock
	if h.transport != nil && h.lastCfg != nil && h.lastCfg.Equals(key) {
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
	liveBytes *LiveBytesTracker      // Logical-request traffic shared across provider attempts
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
	if err != nil {
		h.logger.Error("failed to load config", zap.Error(err))
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to load configuration")
		return
	}

	// Resolve the API contract from the same method/path catalog used by the
	// server mux so direct Handler callers observe identical route semantics.
	apiType, ok := ResolveAPIType(r.Method, r.URL.Path)
	if !ok {
		h.logger.Warn("unknown API type", zap.String("path", r.URL.Path))
		h.writeGatewayError(w, http.StatusBadRequest, ErrCodeUnknownAPIType, fmt.Sprintf("Unknown API path: %s", r.URL.Path))
		return
	}

	// WebSocket upgrade is only valid for Codex (OpenAI Realtime API).
	// Reject upgrades on other API types to prevent health metric pollution
	// from failed WS dials to non-WebSocket backends.
	if isWebSocketUpgrade(r) {
		if apiType != APITypeCodex {
			h.logger.Warn("websocket upgrade rejected for unsupported API type",
				zap.String("api_type", apiType),
				zap.String("remote_addr", r.RemoteAddr),
			)
			h.writeGatewayError(w, http.StatusBadRequest, ErrCodeWebSocketUpgrade,
				fmt.Sprintf("WebSocket upgrade is not supported for API type %q", apiType))
			return
		}
		h.handleWebSocket(ctx, w, r, cfg, apiType, requestID, startTime)
		return
	}

	// GET /responses exists solely for WebSocket (OpenAI Realtime API).
	// A plain GET without Upgrade is meaningless there — reject early.
	// Matching the normalized upstream path keeps the rule scoped to that
	// endpoint, so other codex GET surfaces (e.g. /codex/v1/models discovery)
	// proxy through like any other API type.
	if r.Method == http.MethodGet && apiType == APITypeCodex &&
		BuildUpstreamPath(r.URL.Path, apiType) == RouteCodexResponses {
		h.writeGatewayError(w, http.StatusUpgradeRequired, ErrCodeWebSocketUpgrade, "This endpoint requires a WebSocket upgrade")
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
			ClientIP:    ExtractClientIP(r, cfg.trustProxy),
			UserID:      ExtractUserID(r, cfg.userHeader),
			Model:       ExtractModel(r, apiType, body),
			APIType:     apiType,
			Path:        r.URL.Path,
			Method:      r.Method,
			UserAgent:   ExtractUserAgent(r),
			RequestID:   ExtractRequestIDHeader(r),
			ContentType: ExtractContentType(r),
			Reasoning:   ExtractRequestedReasoning(apiType, r.URL.Path, body),
		},
		startTime: startTime,
		requestID: requestID,
		liveBytes: &LiveBytesTracker{},
		attempts:  make([]model.RequestAttempt, 0),
	}
	pctx.selectReq = &model.SelectRequest{
		ClientIP:   pctx.info.ClientIP,
		User:       pctx.info.UserID,
		APIType:    apiType,
		Model:      pctx.info.Model,
		StickyMode: cfg.stickyMode,
	}

	// Execute proxy with retry logic
	h.executeProxy(ctx, pctx)
}

// handleBodyError handles body read errors.
func (h *Handler) handleBodyError(w http.ResponseWriter, err error, maxSize int64) {
	if errors.Is(err, ErrBodyTooLarge) {
		h.logger.Warn("request body too large", zap.Int64("max_size_mb", maxSize))
		h.writeGatewayError(w, http.StatusRequestEntityTooLarge, ErrCodeBodyTooLarge, fmt.Sprintf("Request body exceeds %d MB limit", maxSize))
		return
	}
	h.logger.Error("failed to read request body", zap.Error(err))
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
	responseCommitted bool
	clientCanceled    bool
	// Transport observation plumbing — mirrored from forwardResult so the
	// final logRequest/evidence path can reconstruct the observation without
	// re-running forwarding logic. Only the last attempt's values survive
	// here (exhausted retries flow through finalizeProxy), matching how the
	// existing err/statusCode/... fields behave.
	firstByteVisible   bool
	isStatusFailover   bool
	isClientWriteError bool
	excludedProviders  map[string]bool
	currentProvider    *model.Provider
	// providerAttempt is 0-based within a single provider. A provider with MaxRetries=N
	// can be attempted (N+1) times: providerAttempt 0..N.
	providerAttempt  int
	activeRegistered bool
	// switchTracker keeps pre-visible replacement separate from visible failover
	// so selector isolation only activates after the upstream becomes observable.
	switchTracker     providerSwitchTracker
	selectionMode     model.SwitchMode
	selectionMetadata selector.SelectionMetadata
	// firstTokenMs tracks Time To First Token for SSE requests (in milliseconds).
	// Only set for successful SSE responses when data starts flowing.
	firstTokenMs *int64
	// responseBytes tracks total bytes written to client for transfer statistics.
	responseBytes int64
	// tokenUsage tracks token usage extracted from response (Phase 4a).
	// Only set for successful 2xx responses that contain usage data.
	tokenUsage *TokenUsage
	// failureDisposition carries provider-scoped retry semantics inferred from the
	// last upstream failure, such as "switch now" or "suspend until reset".
	failureDisposition providerFailureDisposition
}

// selectAndRegisterProvider selects a provider and registers the active request.
// Returns true if selection succeeded, false if the loop should break or return early.
// The earlyReturn flag indicates whether to return immediately from executeProxy.
func (h *Handler) selectAndRegisterProvider(ctx context.Context, pctx *proxyContext, state *retryState, attempt int) (continueLoop, earlyReturn bool) {
	state.selectionMode = state.switchTracker.prepareSelection()

	provider, selectionMetadata, err := h.selectProviderWithTracking(ctx, pctx.selectReq, attempt, state.excludedProviders)
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
	state.selectionMode = state.switchTracker.recordSelection(provider, selectionMetadata)
	state.selectionMetadata = selectionMetadata

	// Track sticky cache hit on first attempt
	if attempt == 0 {
		pctx.isSticky = selectionMetadata.UsesContinuity()
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
	h.activeRegistry.RegisterWithDone(&ActiveRequest{
		RequestID:                     pctx.requestID,
		ProviderID:                    provider.ID,
		Model:                         pctx.info.Model,
		APIType:                       pctx.apiType,
		UserID:                        pctx.info.UserID,
		ClientIP:                      pctx.info.ClientIP,
		StickyMode:                    pctx.selectReq.StickyMode,
		ContinuityKey:                 selector.BuildContinuityKey(pctx.selectReq),
		IsSSE:                         false, // Updated after response type is known
		StartedAt:                     pctx.startTime,
		RequestedReasoningObservation: pctx.info.Reasoning,
	}, pctx.r.Context().Done())
	h.activeRegistry.RegisterLiveBytes(pctx.requestID, pctx.liveBytes)
	state.activeRegistered = true
}

// recordAttempt records a request attempt in the proxy context.
func (h *Handler) recordAttempt(pctx *proxyContext, state *retryState, result forwardResult, attempt int, attemptStart time.Time, switchReason string) {
	createdAt := time.Now()
	attemptRecord := newNormalizedRequestAttempt(pctx.requestID, state.currentProvider.ID, createdAt)
	attemptRecord.Attempt = attempt
	attemptRecord.SwitchMode = requestAttemptSwitchMode(state.selectionMode)
	attemptRecord.ProviderAttempt = state.providerAttempt + 1
	attemptRecord.ProviderSwitchCount = state.switchTracker.providerSwitchCount()
	attemptRecord.StatusCode = result.statusCode
	attemptRecord.BodySnippet = result.bodySnippet
	attemptRecord.LatencyMs = time.Since(attemptStart).Milliseconds()
	attemptRecord.SwitchReason = switchReason
	attemptRecord.ContinuitySeeded = state.selectionMetadata.ContinuitySeeded
	attemptRecord.ContinuityOriginProviderID = state.selectionMetadata.ContinuityOriginProviderID
	attemptRecord.ContinuitySeedAgeMs = selectionMetadataContinuitySeedAgeMs(state.selectionMetadata)
	if result.err != nil {
		attemptRecord.Error = result.err.Error()
		// Include request body snippet for error attempts to help diagnose issues.
		// SECURITY NOTE: This may expose sensitive data (API keys, tokens, PII) in the
		// request_attempts table. Administrators should be aware that error diagnostics
		// include partial request content. The snippet is truncated to maxSnippetBytes to limit
		// exposure. Consider the security implications when granting access to logs/attempts data.
		attemptRecord.ReqBodySnippet = GetReqBodySnippet(pctx.body)
	}
	pctx.attempts = append(pctx.attempts, attemptRecord)
}

// tryIncrementAndExhaustProvider attempts to increment the provider retry counter.
// Returns (exhausted bool, switchReason string):
//   - exhausted=true means we should switch to a different provider
//   - switchReason is non-empty only when exhausted=true, explaining why the switch occurred
//
// Note: Since markFailure is called in forwardToProvider, and IsAvailable check happens
// here (after markFailure), if this failure triggers the circuit breaker, switchReason
// will correctly record "circuit_breaker_triggered".
func (h *Handler) tryIncrementAndExhaustProvider(ctx context.Context, state *retryState) (bool, string) {
	maxRetries := max(0, state.currentProvider.MaxRetries)

	// Check for permanent errors that should force immediate provider switch
	if state.failureDisposition.forcesProviderSwitch() {
		return true, state.failureDisposition.switchReason
	}
	if shouldForceProviderSwitch(state.statusCode) {
		return true, formatPermanentErrorReason(state.statusCode)
	}

	// Check if circuit breaker has been triggered for this provider
	if h.health != nil && !h.health.IsAvailable(ctx, state.currentProvider.ID) {
		return true, SwitchReasonCircuitBreakerTriggered
	}

	// Normal retry logic: check if there are retries remaining
	if state.providerAttempt < maxRetries {
		state.providerAttempt++
		return false, ""
	}

	return true, SwitchReasonMaxRetriesExhausted
}

// excludeCurrentProvider marks the current provider as excluded and tears down
// the active attempt before the next selection so concurrency is released on the
// same lifecycle edge that removes the request from monitoring.
func (h *Handler) excludeCurrentProvider(pctx *proxyContext, state *retryState) {
	state.excludedProviders[state.currentProvider.ID] = true
	if state.activeRegistered && h.activeRegistry != nil {
		h.activeRegistry.Unregister(pctx.requestID)
		state.activeRegistered = false
	} else {
		h.releaseConcurrency(state.currentProvider.ID)
	}
	state.currentProvider = nil
}

// applyBackoffDelay waits for the configured backoff delay before retrying the same provider.
// Returns true if the context was cancelled during the delay, false otherwise.
func (h *Handler) applyBackoffDelay(ctx context.Context, provider *model.Provider, retryIndex int) bool {
	if provider.Backoff.IsZero() {
		return false
	}
	delay := provider.Backoff.DelayForRetry(retryIndex)
	if delay <= 0 {
		return false
	}
	select {
	case <-time.After(delay):
		return false
	case <-ctx.Done():
		return true
	}
}

// forwardResult holds the result of forwarding to a provider.
//
// Transport observation fields (firstByteVisible / isStatusFailover /
// isClientWriteError) carry **runtime facts only** — they feed the SSE
// transport diagnostic derivation one layer up. Per the transport
// observability plan, this struct intentionally does NOT carry a derived
// `TransportDiagnostic`: evidence is built by the evidence layer from
// observation facts, not reverse-engineered from a logged conclusion.
type forwardResult struct {
	headersWritten    bool
	responseCommitted bool
	clientCanceled    bool
	// firstByteVisible is true once a body byte has actually been committed
	// to the client. It is the sole discriminator between the SSE stages
	// `pre_payload_visible` and `post_payload_visible`; the idle watchdog
	// can fire before any byte is written (newIdleWatchdog starts at body
	// open, pre-Read()), so this flag is load-bearing for stage accuracy.
	firstByteVisible bool
	// isStatusFailover flags the synthetic `upstream returned status %d`
	// error produced by failoverForwardResponse. That error is a status
	// classification, not a transport failure, so the derivation function
	// must bypass it explicitly — we carry a flag instead of matching the
	// synthetic message text, which is fragile under localization or copy
	// refactors.
	isStatusFailover bool
	// isClientWriteError is set by handleWriteError when the failure arose
	// from writing to the client rather than reading from upstream or a
	// pure ctx cancel. The derivation function treats this as authoritative
	// rather than sniffing error text.
	isClientWriteError bool
	statusCode         int
	success            bool
	err                error
	done               bool        // whether to stop retrying
	isSSE              bool        // whether the response was SSE
	bodySnippet        string      // first ~500 bytes of error response (failover scenarios only)
	firstTokenMs       *int64      // Time To First Token for SSE requests (ms from request start)
	responseBytes      int64       // Total bytes written to client (for transfer statistics)
	tokenUsage         *TokenUsage // Token usage extracted from response (Phase 4a)
	// failureDisposition records retry semantics derived from the upstream failure.
	failureDisposition providerFailureDisposition
}

// setupTokenInterceptor creates and configures a token capture interceptor for successful responses.
// Returns the interceptor (for Result() call) and sseInterceptor (for Wait() call if SSE).
func (h *Handler) setupTokenInterceptor(statusCode int, isSSE bool, upstreamResp *UpstreamResponse) (ResponseInterceptor, *sseTokenInterceptor) {
	if statusCode < defaults.StatusSuccessMin || statusCode >= defaults.StatusSuccessMax {
		return nil, nil
	}

	if isSSE {
		// Pass Content-Encoding for stream decompression support
		contentEncoding := upstreamResp.Header.Get("Content-Encoding")
		sseInterceptor := newSSETokenInterceptor(
			NewZapLoggerAdapter(h.logger.Sugar()),
			contentEncoding,
		)
		return sseInterceptor, sseInterceptor
	}

	return newTokenCaptureInterceptor(upstreamResp.ContentLength, NewZapLoggerAdapter(h.logger.Sugar())), nil
}

// buildProviderRequest validates the provider's endpoint/auth config and
// constructs the upstream HTTP request.
func (h *Handler) buildProviderRequest(ctx context.Context, pctx *proxyContext, provider *model.Provider) (*http.Request, error) {
	upstreamPath := BuildUpstreamPath(pctx.r.URL.Path, pctx.apiType)
	baseURL := provider.BaseURLForAPIType(pctx.apiType)

	// Fail fast if provider has no BaseURL configured for this API type.
	// This prevents forwarding requests to invalid URLs (just the path with no host),
	// which would produce cryptic errors instead of a clear diagnostic message.
	if baseURL == "" {
		h.logger.Error("missing base_url for api_type",
			zap.String("provider_id", provider.ID),
			zap.String("api_type", pctx.apiType),
		)
		return nil, fmt.Errorf("provider %q has no base_url configured for api_type %q", provider.ID, pctx.apiType)
	}

	if model.NormalizeProviderCredentialType(provider.CredentialType) == model.ProviderCredentialTypeAPIKey && provider.APIKeyForAPIType(pctx.apiType) == "" {
		h.logger.Error("missing api_key for api_type",
			zap.String("provider_id", provider.ID),
			zap.String("api_type", pctx.apiType),
		)
		return nil, fmt.Errorf("provider %q has no api_key configured for api_type %q", provider.ID, pctx.apiType)
	}

	upstreamURL := h.buildFullURL(baseURL, upstreamPath, pctx.r.URL.RawQuery)

	req, err := BuildUpstreamRequest(ctx, pctx.r.Method, upstreamURL, pctx.body, pctx.r)
	if err != nil {
		h.logger.Error("failed to build upstream request", zap.Error(err))
		return nil, err
	}

	return req, nil
}

// extractTokenUsage waits for interceptors to finish and returns parsed token usage, if available.
func (h *Handler) extractTokenUsage(statusCode int, interceptor ResponseInterceptor, sseInterceptor *sseTokenInterceptor) *TokenUsage {
	if interceptor == nil {
		return nil
	}

	// For SSE with gzip passthrough, wait for background goroutine to complete parsing
	if sseInterceptor != nil {
		sseInterceptor.Wait()
	}

	usage, complete := interceptor.Result()
	if usage != nil {
		return usage
	}
	if !complete {
		h.logger.Debug("response not fully read, token usage may be incomplete",
			zap.Int("status", statusCode),
		)
	}
	return nil
}

// forwardToProvider forwards the request to a single provider.
// Note: Retry orchestration (per-provider retries and provider switching) is handled in executeProxy.
func (h *Handler) forwardToProvider(ctx context.Context, pctx *proxyContext, provider *model.Provider) forwardResult {
	upstreamReq, result, ok := h.prepareForwardRequest(ctx, pctx, provider)
	if !ok {
		return result
	}

	upstreamResp, result, ok := h.fetchForwardResponse(ctx, pctx, provider, upstreamReq)
	if !ok {
		return result
	}

	return h.commitForwardResponse(ctx, pctx, provider, upstreamResp)
}

// handleWriteError classifies a write error and updates the result accordingly.
func (h *Handler) handleWriteError(ctx context.Context, writeErr error, providerID string, result *forwardResult) {
	// Distinguish client disconnect from real errors.
	// Client disconnect (context.Canceled) is normal — the upstream succeeded,
	// so we should not log it as a warning or record it as an error.
	clientDisconnect := ctx.Err() != nil

	if clientDisconnect {
		h.logger.Debug("client disconnected during response streaming",
			zap.String("provider_id", providerID),
		)
		result.clientCanceled = true
	} else {
		h.logger.Warn("failed to write response to client",
			zap.String("provider_id", providerID),
			zap.Error(writeErr),
		)
		result.err = writeErr
	}

	// Upstream errors indicate problems with the provider and should
	// trigger circuit breaker to avoid routing to problematic providers.
	isUpstreamErr := errors.Is(writeErr, ErrReadTimeout) ||
		errors.Is(writeErr, ErrSSEIdleTimeout) ||
		IsUpstreamReadError(writeErr)
	switch {
	case isUpstreamErr:
		result.success = false
		h.markFailure(ctx, providerID, writeErr)
	case clientDisconnect && result.statusCode < defaults.StatusClientError:
		// Client disconnected but upstream returned 2xx — count as success.
		result.success = true
		h.markSuccess(ctx, providerID)
	default:
		// Other write errors (e.g., client disconnected with non-2xx) — don't markFailure
		// as the upstream itself succeeded; only the client write failed.
		result.success = false
	}

	// Flag a genuine client-side write failure so the transport diagnostic
	// derivation can classify `signal=client_write_error` without sniffing
	// error text. A pure ctx cancel has no real transport signal and is
	// excluded (clientDisconnect with no writeErr classification); an
	// upstream-origin error wrapped as a write failure stays upstream.
	if !clientDisconnect && !isUpstreamErr && writeErr != nil {
		result.isClientWriteError = true
	}
}
