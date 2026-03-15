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
	provider, fromSticky, err := h.selectProviderWithTracking(ctx, selectReq, 0, nil)
	if err != nil {
		if errors.Is(err, internal.ErrNoProvider) {
			h.logger.Warn("no providers available for websocket", zap.String("api_type", apiType))
			h.writeGatewayError(w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", apiType))
		} else {
			h.logger.Error("provider selection failed for websocket", zap.Error(err))
			h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Provider selection failed")
		}
		go h.logWebSocketRequest(requestID, info, nil, fromSticky, nil, err, time.Since(startTime))
		return
	}

	// Track active WebSocket connection for monitoring.
	// HasReceivedData is set immediately because the WebSocket upgrade handshake
	// itself constitutes a successful connection — unlike HTTP where we wait for
	// the first response byte. This ensures sticky fallback can find active WS providers.
	// Release concurrency AFTER unregistering from active registry (LIFO ordering).
	// This prevents a window where tryActiveProviderFallback finds the entry
	// but the concurrency slot is already released.
	defer h.releaseConcurrency(provider.ID)
	if h.activeRegistry != nil {
		h.activeRegistry.Register(&ActiveRequest{
			RequestID:       requestID,
			ProviderID:      provider.ID,
			Model:           info.Model,
			APIType:         apiType,
			UserID:          info.UserID,
			ClientIP:        info.ClientIP,
			IsWebSocket:     true,
			StartedAt:       startTime,
			HasReceivedData: true,
		})
		defer h.activeRegistry.Unregister(requestID)
	}

	// Build upstream WebSocket URL.
	baseURL := provider.BaseURLForAPIType(apiType)
	if baseURL == "" {
		h.logger.Error("missing base_url for websocket",
			zap.String("provider_id", provider.ID),
			zap.String("api_type", apiType),
		)
		h.writeGatewayError(w, http.StatusBadGateway, ErrCodeWebSocketUpgrade, fmt.Sprintf("Provider %q has no base_url for api_type %q", provider.ID, apiType))
		h.markFailure(ctx, provider.ID, fmt.Errorf("no base_url for api_type %q", apiType))
		go h.logWebSocketRequest(requestID, info, provider, fromSticky, nil, fmt.Errorf("no base_url"), time.Since(startTime))
		return
	}
	apiKey := provider.APIKeyForAPIType(apiType)
	if apiKey == "" {
		h.logger.Error("missing api_key for websocket",
			zap.String("provider_id", provider.ID),
			zap.String("api_type", apiType),
		)
		h.writeGatewayError(w, http.StatusBadGateway, ErrCodeWebSocketUpgrade, fmt.Sprintf("Provider %q has no api_key for api_type %q", provider.ID, apiType))
		h.markFailure(ctx, provider.ID, fmt.Errorf("no api_key for api_type %q", apiType))
		go h.logWebSocketRequest(requestID, info, provider, fromSticky, nil, fmt.Errorf("no api_key"), time.Since(startTime))
		return
	}

	upstreamPath := BuildUpstreamPath(r.URL.Path, apiType)
	upstreamURL := httpToWSURL(h.buildFullURL(baseURL, upstreamPath, r.URL.RawQuery))

	// Build headers for the upstream handshake:
	// auth + non-hop-by-hop headers from the original request (e.g., OpenAI-Beta).
	dialHeaders := buildWebSocketDialHeaders(r, provider, apiType, cfg.globalAuthMode)
	observer := newWebSocketMessageObserver(apiType, info.Model, NewZapLoggerAdapter(h.logger.Sugar()), func(observation WebSocketObservation) {
		if h.activeRegistry == nil || observation.Model == "" || observation.Model == ModelUnknown {
			return
		}
		h.activeRegistry.UpdateModel(requestID, observation.Model)
	})

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
	result, fwdErr := h.wsForwarder.ForwardObserved(ctx, w, r, upstreamURL, dialHeaders, observer)
	if fwdErr != nil {
		// Accept failed — client never upgraded. This is a client-side issue (protocol
		// mismatch, malformed request), NOT a provider failure. Do not call markFailure
		// because polluting provider health with client errors can trigger false circuit breaks.
		h.logger.Warn("websocket client accept failed",
			zap.String("provider_id", provider.ID),
			zap.Error(fwdErr),
		)
		go h.logWebSocketRequest(requestID, info, provider, fromSticky, result, fwdErr, time.Since(startTime))
		return
	}
	if result != nil && result.Model != "" {
		info.Model = result.Model
		// Keep selectReq in sync so the sticky cache key uses the resolved model,
		// not the stale pre-upgrade value (often ModelUnknown for WebSocket).
		selectReq.Model = result.Model
	}

	// Health and sticky reporting based on outcome.
	if result.ConnectSuccess {
		h.markSuccess(ctx, provider.ID)
		if cfg.stickyMode != model.StickyModeOff && h.selector != nil {
			h.selector.UpdateStickyWithTTL(selectReq, provider.ID, cfg.stickyTTL)
		}
	} else {
		h.markFailure(ctx, provider.ID, result.Err)
	}

	go h.logWebSocketRequest(requestID, info, provider, fromSticky, result, result.Err, time.Since(startTime))
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
	if !result.ConnectSuccess {
		if result.HandshakeStatusCode > 0 {
			return result.HandshakeStatusCode
		}
		return StatusCodeNoResponse
	}
	if result.UpstreamError != nil && result.UpstreamError.StatusCode > 0 {
		return result.UpstreamError.StatusCode
	}
	if result.ConnectSuccess {
		return http.StatusSwitchingProtocols
	}
	return StatusCodeNoResponse
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
	if result == nil || !result.ConnectSuccess {
		return false
	}
	return result.Err == nil && result.UpstreamError == nil
}

// logWebSocketRequest logs a WebSocket connection lifecycle event asynchronously.
func (h *Handler) logWebSocketRequest(requestID string, info RequestInfo, provider *model.Provider, isSticky bool, result *WebSocketResult, err error, latency time.Duration) {
	log := &model.RequestLog{
		RequestID:       requestID,
		APIType:         info.APIType,
		Model:           info.Model,
		ClientIP:        info.ClientIP,
		UserID:          info.UserID,
		StatusCode:      websocketLogStatusCode(result),
		LatencyMs:       latency.Milliseconds(),
		IsWebSocket:     true,
		IsSticky:        isSticky,
		CreatedAt:       time.Now(),
		RequestPath:     info.Path,
		RequestMethod:   info.Method,
		UserAgent:       info.UserAgent,
		RequestIDHeader: info.RequestID,
	}

	if provider != nil {
		log.ProviderID = provider.ID
	}

	if result != nil && result.ConnectSuccess {
		log.Success = websocketLogSuccess(result)
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
