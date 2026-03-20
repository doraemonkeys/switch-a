package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// isWebSocketUpgrade checks if the request is a WebSocket upgrade.
// Uses case-insensitive comparison per RFC 6455 Section 4.2.1.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		headerContains(r.Header, "Connection", "upgrade")
}

// headerContains checks if the header contains the given value (case-insensitive).
// Connection headers can contain multiple comma-separated values (e.g., "keep-alive, Upgrade").
func headerContains(h http.Header, key, value string) bool {
	for _, v := range h[http.CanonicalHeaderKey(key)] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(s), value) {
				return true
			}
		}
	}
	return false
}

// handleWebSocket processes a WebSocket upgrade request.
// This is the WebSocket equivalent of executeProxy, but without the retry loop:
// WebSocket connections are stateful, so provider selection happens once.
func (h *Handler) handleWebSocket(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg *runtimeConfig, apiType, requestID string, startTime time.Time) {
	info := RequestInfo{
		ClientIP:  ExtractClientIP(r, cfg.trustProxy),
		UserID:    ExtractUserID(r, cfg.userHeader),
		Model:     extractWebSocketModel(r),
		APIType:   apiType,
		Path:      r.URL.Path,
		Method:    r.Method,
		UserAgent: ExtractUserAgent(r),
		RequestID: ExtractRequestIDHeader(r),
	}

	selectReq := &model.SelectRequest{
		ClientIP:   info.ClientIP,
		User:       info.UserID,
		APIType:    apiType,
		Model:      info.Model,
		StickyMode: cfg.stickyMode,
	}

	// Single-attempt provider selection (no retries for stateful connections).
	provider, fromSticky, ok := h.selectWebSocketProvider(ctx, w, selectReq, info, apiType, requestID, startTime)
	if !ok {
		return
	}

	defer h.beginWebSocketTracking(provider.ID, requestID, apiType, info, startTime)()

	// Build upstream WebSocket URL.
	baseURL, ok := h.validateWebSocketProvider(ctx, w, provider, fromSticky, info, apiType, requestID, startTime)
	if !ok {
		return
	}

	upstreamPath := BuildUpstreamPath(r.URL.Path, apiType)
	upstreamURL := httpToWSURL(h.buildFullURL(baseURL, upstreamPath, r.URL.RawQuery))

	// Build headers for the upstream handshake:
	// auth + non-hop-by-hop headers from the original request (e.g., OpenAI-Beta).
	dialHeaders := buildWebSocketDialHeaders(r, provider, apiType, cfg.globalAuthMode)
	applyObservation := func(observation WebSocketObservation) {
		if h.activeRegistry == nil {
			return
		}
		if observation.Model != "" && observation.Model != ModelUnknown {
			h.activeRegistry.UpdateModel(requestID, observation.Model)
		}
		if observation.SessionCommitted {
			h.activeRegistry.MarkDataReceived(requestID)
		}
	}
	observer := newWebSocketMessageObserver(
		apiType,
		info.Model,
		NewZapLoggerAdapter(h.logger.Sugar()),
		applyObservation,
		applyObservation,
	)

	// Wrap the semantic observer with byte/message/idle tracking so the
	// active registry can expose live data-flow metrics without touching
	// the transport layer.
	var tracker *LiveBytesTracker
	if h.activeRegistry != nil {
		tracker = &LiveBytesTracker{}
		h.activeRegistry.RegisterLiveBytes(requestID, tracker)
		observer = newBytesTrackingObserver(observer, tracker)
	}

	// Forward the WebSocket connection.
	result, fwdErr := h.wsForwarder.ForwardObserved(ctx, w, r, upstreamURL, dialHeaders, observer, applyObservation)
	if fwdErr != nil {
		// Accept failed — client never upgraded. This is a client-side issue (protocol
		// mismatch, malformed request), NOT a provider failure. Do not call markFailure
		// because polluting provider health with client errors can trigger false circuit breaks.
		h.logger.Warn("websocket client accept failed",
			zap.String("provider_id", provider.ID),
			zap.Error(fwdErr),
		)
		go h.logWebSocketRequest(requestID, info, provider, fromSticky, false, result, fwdErr, time.Since(startTime))
		return
	}
	if result != nil && result.Model != "" {
		info.Model = result.Model
		// Keep selectReq in sync so the sticky cache key uses the resolved model,
		// not the stale pre-upgrade value (often ModelUnknown for WebSocket).
		selectReq.Model = result.Model
	}

	stickyWritten := false
	if result.SessionCommitted && cfg.stickyMode != model.StickyModeOff && h.selector != nil {
		h.selector.UpdateStickyWithTTL(selectReq, provider.ID, cfg.stickyTTL)
		stickyWritten = true
	}
	applyWebSocketHealthOutcome(ctx, h, provider.ID, result)

	go h.logWebSocketRequest(requestID, info, provider, fromSticky, stickyWritten, result, result.Err, time.Since(startTime))
}

func (h *Handler) selectWebSocketProvider(ctx context.Context, w http.ResponseWriter, selectReq *model.SelectRequest, info RequestInfo, apiType, requestID string, startTime time.Time) (*model.Provider, bool, bool) {
	provider, fromSticky, err := h.selectProviderWithTracking(ctx, selectReq, 0, nil)
	if err == nil {
		return provider, fromSticky, true
	}

	statusCode := http.StatusInternalServerError
	terminalCause := model.TerminalInternalError
	errorCode := ErrCodeInternalError
	message := "Provider selection failed"
	if errors.Is(err, internal.ErrNoProvider) {
		statusCode = http.StatusServiceUnavailable
		terminalCause = model.TerminalProviderUnavailable
		errorCode = ErrCodeProviderUnavailable
		message = fmt.Sprintf("No available provider for api_type: %s", apiType)
		h.logger.Warn("no providers available for websocket", zap.String("api_type", apiType))
	} else {
		h.logger.Error("provider selection failed for websocket", zap.Error(err))
	}
	h.writeGatewayError(w, statusCode, errorCode, message)
	go h.logWebSocketRequest(
		requestID,
		info,
		nil,
		fromSticky,
		false,
		newWebSocketGatewayFailureResult(statusCode, terminalCause, err),
		err,
		time.Since(startTime),
	)
	return nil, fromSticky, false
}

// beginWebSocketTracking keeps registry cleanup and concurrency release in one
// place so the LIFO ordering cannot drift as handleWebSocket evolves.
func (h *Handler) beginWebSocketTracking(providerID, requestID, apiType string, info RequestInfo, startTime time.Time) func() {
	if h.activeRegistry == nil {
		return func() {
			h.releaseConcurrency(providerID)
		}
	}

	h.activeRegistry.Register(&ActiveRequest{
		RequestID:       requestID,
		ProviderID:      providerID,
		Model:           info.Model,
		APIType:         apiType,
		UserID:          info.UserID,
		ClientIP:        info.ClientIP,
		IsWebSocket:     true,
		StartedAt:       startTime,
		HasReceivedData: false,
	})
	return func() {
		h.activeRegistry.Unregister(requestID)
		h.releaseConcurrency(providerID)
	}
}

func (h *Handler) validateWebSocketProvider(ctx context.Context, w http.ResponseWriter, provider *model.Provider, fromSticky bool, info RequestInfo, apiType, requestID string, startTime time.Time) (string, bool) {
	baseURL := provider.BaseURLForAPIType(apiType)
	if baseURL == "" {
		h.failWebSocketProviderConfiguration(ctx, w, provider, fromSticky, info, apiType, requestID, startTime, "missing base_url for websocket", "base_url", fmt.Errorf("no base_url for api_type %q", apiType))
		return "", false
	}
	if provider.APIKeyForAPIType(apiType) == "" {
		h.failWebSocketProviderConfiguration(ctx, w, provider, fromSticky, info, apiType, requestID, startTime, "missing api_key for websocket", "api_key", fmt.Errorf("no api_key for api_type %q", apiType))
		return "", false
	}
	return baseURL, true
}

func (h *Handler) failWebSocketProviderConfiguration(ctx context.Context, w http.ResponseWriter, provider *model.Provider, fromSticky bool, info RequestInfo, apiType, requestID string, startTime time.Time, logMessage, missingField string, err error) {
	h.logger.Error(logMessage,
		zap.String("provider_id", provider.ID),
		zap.String("api_type", apiType),
	)
	h.writeGatewayError(w, http.StatusBadGateway, ErrCodeWebSocketUpgrade, fmt.Sprintf("Provider %q has no %s for api_type %q", provider.ID, missingField, apiType))
	h.markFailure(ctx, provider.ID, err)
	go h.logWebSocketRequest(
		requestID,
		info,
		provider,
		fromSticky,
		false,
		newWebSocketGatewayFailureResult(http.StatusBadGateway, model.TerminalProviderConfigurationError, err),
		err,
		time.Since(startTime),
	)
}

// extractWebSocketModel extracts the model identifier from a WebSocket request.
// WebSocket requests carry the model in query parameters (e.g., ?model=gpt-4o-realtime)
// because the request body is empty during the WebSocket handshake.
func extractWebSocketModel(r *http.Request) string {
	if m := r.URL.Query().Get("model"); m != "" {
		return m
	}
	return ModelUnknown
}

// buildWebSocketDialHeaders builds HTTP headers for the upstream WebSocket handshake.
// Includes auth headers and passes through non-hop-by-hop, non-auth, non-handshake
// original request headers (e.g., OpenAI-Beta: realtime=v1) that the upstream may require.
func buildWebSocketDialHeaders(r *http.Request, provider *model.Provider, apiType, globalAuthMode string) http.Header {
	headers := make(http.Header)

	// Copy non-hop-by-hop, non-auth, non-WebSocket-handshake headers from the original request.
	for key, values := range r.Header {
		if hopByHopHeaders[key] || isAuthHeader(key) || isWebSocketHandshakeHeader(key) {
			continue
		}
		for _, v := range values {
			headers.Add(key, v)
		}
	}

	// Inject provider auth credentials.
	SetAuthHeader(headers, provider.APIKeyForAPIType(apiType), provider.AuthMode, globalAuthMode, r)

	return headers
}

func websocketLogStatusCode(result *WebSocketResult) int {
	if result == nil {
		return StatusCodeNoResponse
	}
	if !result.HandshakeAccepted {
		if result.HandshakeStatusCode > 0 {
			return result.HandshakeStatusCode
		}
		return StatusCodeNoResponse
	}
	if result.UpstreamError != nil && result.UpstreamError.StatusCode > 0 {
		return result.UpstreamError.StatusCode
	}
	return http.StatusSwitchingProtocols
}

func websocketLogErrorMessage(result *WebSocketResult, fallback error) string {
	if result != nil && result.HandshakeBodySnippet != "" {
		return result.HandshakeBodySnippet
	}
	if result != nil && result.UpstreamError != nil {
		if result.UpstreamError.Raw != "" {
			return result.UpstreamError.Raw
		}
		if result.UpstreamError.Message != "" {
			return result.UpstreamError.Message
		}
	}
	if fallback != nil {
		return fallback.Error()
	}
	return ""
}

func websocketLogSuccess(result *WebSocketResult) bool {
	if result == nil {
		return false
	}
	return result.SessionCommitted && result.TerminalCause != model.TerminalUpstreamSemanticError
}

// newWebSocketGatewayFailureResult keeps live pre-upgrade failures out of the
// historical "unknown" bucket by recording an explicit lifecycle cause.
func newWebSocketGatewayFailureResult(statusCode int, terminalCause model.TerminalCause, err error) *WebSocketResult {
	return &WebSocketResult{
		HandshakeStatusCode: statusCode,
		TerminalCause:       terminalCause,
		CommitSource:        model.CommitUnknown,
		Err:                 err,
	}
}

// logWebSocketRequest logs a WebSocket connection lifecycle event asynchronously.
func (h *Handler) logWebSocketRequest(requestID string, info RequestInfo, provider *model.Provider, isSticky, stickyWritten bool, result *WebSocketResult, err error, latency time.Duration) {
	sessionCommitted := false
	terminalCause := model.TerminalUnknown
	commitSource := model.CommitUnknown
	if result != nil {
		sessionCommitted = result.SessionCommitted
		if result.TerminalCause != "" {
			terminalCause = result.TerminalCause
		}
		if result.CommitSource != "" {
			commitSource = result.CommitSource
		}
	}

	log := &model.RequestLog{
		RequestID:        requestID,
		APIType:          info.APIType,
		Model:            info.Model,
		ClientIP:         info.ClientIP,
		UserID:           info.UserID,
		StatusCode:       websocketLogStatusCode(result),
		LatencyMs:        latency.Milliseconds(),
		Success:          websocketLogSuccess(result),
		IsWebSocket:      true,
		IsSticky:         isSticky,
		StickyWritten:    &stickyWritten,
		SessionCommitted: &sessionCommitted,
		TerminalCause:    &terminalCause,
		CommitSource:     &commitSource,
		CreatedAt:        time.Now(),
		RequestPath:      info.Path,
		RequestMethod:    info.Method,
		UserAgent:        info.UserAgent,
		RequestIDHeader:  info.RequestID,
	}

	if provider != nil {
		log.ProviderID = provider.ID
	}

	if result != nil {
		log.ResponseBytes = result.BytesUpstreamToClient
		log.RequestBytes = result.BytesClientToUpstream
	}

	log.ErrorMsg = websocketLogErrorMessage(result, err)
	if result != nil && result.TokenUsage != nil {
		log.PromptTokens, log.CompletionTokens, log.TotalTokens,
			log.CacheReadInputTokens, log.CacheCreationInputTokens, log.UsageDetails = result.TokenUsage.ToModelFields()
	}

	ctx, cancel := context.WithTimeout(context.Background(), logInsertTimeout)
	defer cancel()

	if insertErr := h.store.InsertLog(ctx, log); insertErr != nil { // coverage-ignore // store error path only reachable with a failing database
		h.logger.Error("failed to insert websocket request log", zap.Error(insertErr))
		return
	}
}

func applyWebSocketHealthOutcome(ctx context.Context, h *Handler, providerID string, result *WebSocketResult) {
	if result == nil {
		return
	}
	if result.SessionCommitted {
		if result.TerminalCause == model.TerminalUpstreamSemanticError {
			h.markFailure(ctx, providerID, result.Err)
			return
		}
		h.markSuccess(ctx, providerID)
		return
	}

	switch result.TerminalCause {
	case model.TerminalUpstreamHandshakeRejected, model.TerminalUpstreamTransportError, model.TerminalUpstreamSemanticError:
		h.markFailure(ctx, providerID, result.Err)
	}
}

// bytesTrackingObserver decorates a WebSocketMessageObserver, recording byte
// counts, message counts, and last-activity timestamps into a LiveBytesTracker.
// This piggybacks on the existing observer pipeline — zero transport-layer changes.
type bytesTrackingObserver struct {
	inner   WebSocketMessageObserver
	tracker *LiveBytesTracker
}

func newBytesTrackingObserver(inner WebSocketMessageObserver, tracker *LiveBytesTracker) *bytesTrackingObserver {
	return &bytesTrackingObserver{inner: inner, tracker: tracker}
}

func (o *bytesTrackingObserver) ObserveClientMessage(messageType websocket.MessageType, data []byte) {
	n := int64(len(data))
	o.tracker.BytesSent.Add(n)
	o.tracker.MsgsSent.Add(1)
	o.tracker.LastActivityAt.Store(time.Now().UnixMilli())
	if o.inner != nil {
		o.inner.ObserveClientMessage(messageType, data)
	}
}

func (o *bytesTrackingObserver) ObserveUpstreamMessage(messageType websocket.MessageType, data []byte) {
	n := int64(len(data))
	o.tracker.BytesReceived.Add(n)
	o.tracker.MsgsReceived.Add(1)
	o.tracker.LastActivityAt.Store(time.Now().UnixMilli())
	if o.inner != nil {
		o.inner.ObserveUpstreamMessage(messageType, data)
	}
}

func (o *bytesTrackingObserver) Snapshot() WebSocketObservation {
	if o.inner != nil {
		return o.inner.Snapshot()
	}
	return WebSocketObservation{}
}

func (o *bytesTrackingObserver) ParseDegraded() bool {
	if o.inner != nil {
		return o.inner.ParseDegraded()
	}
	return false
}

func (o *bytesTrackingObserver) HasSemanticObservation() bool {
	if o.inner != nil {
		return o.inner.HasSemanticObservation()
	}
	return false
}
