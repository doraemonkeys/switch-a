package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

const (
	webSocketGatewayErrorEventType = "error"
	webSocketGatewayErrorType      = "gateway_error"
)

type webSocketGatewayErrorEnvelope struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Error  struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

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

type webSocketObserverFactory func(modelName string) WebSocketMessageObserver

// handleWebSocket processes a WebSocket upgrade request.
// Unlike HTTP retries, WebSocket failover has to stop at the client-upgrade
// boundary, so the handler delegates attempt control to a session orchestrator
// instead of reusing the HTTP execution loop.
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
	newObserver, tracker, applyObservation, onClientVisible := h.newWebSocketObserverPipeline(apiType, requestID)
	session := newWebSocketSessionOrchestrator(h, webSocketSessionOrchestratorConfig{
		info:             info,
		selectReq:        selectReq,
		apiType:          apiType,
		requestID:        requestID,
		startTime:        startTime,
		maxAttempts:      cfg.globalMaxAttempts,
		globalAuthMode:   cfg.globalAuthMode,
		probeClientModel: cfg.websocketProbeClientModel,
		newObserver:      newObserver,
		applyObservation: applyObservation,
		onClientVisible:  onClientVisible,
		tracker:          tracker,
	}).Run(ctx, w, r)
	if session == nil {
		return
	}

	if session.ResolvedModel != "" {
		info.Model = session.ResolvedModel
	}

	if session.ClientAccepted &&
		session.FinalResult != nil &&
		session.FinalResult.SessionCommitted &&
		cfg.stickyMode != model.StickyModeOff &&
		h.selector != nil &&
		session.FinalProvider != nil {
		h.selector.UpdateStickyWithTTL(selectReq, session.FinalProvider.ID, cfg.stickyTTL)
		session.StickyWritten = true
	}

	if !session.ClientAccepted && session.GatewayStatusCode > 0 {
		h.writeGatewayError(w, session.GatewayStatusCode, session.GatewayErrorCode, session.GatewayMessage)
	}

	applyWebSocketSessionHealthOutcomes(ctx, h, session)
	go h.logWebSocketSession(info, session, time.Since(startTime))
}

func (h *Handler) newWebSocketObserverPipeline(
	apiType,
	requestID string,
) (webSocketObserverFactory, *LiveBytesTracker, func(WebSocketObservation), func(webSocketVisibleWriteContext)) {
	var tracker *LiveBytesTracker
	if h.activeRegistry != nil {
		tracker = &LiveBytesTracker{}
	}

	applyObservation := func(observation WebSocketObservation) {
		if h.activeRegistry == nil {
			return
		}
		if observation.Model != "" && observation.Model != ModelUnknown {
			h.activeRegistry.UpdateModel(requestID, observation.Model)
		}
	}

	newObserver := func(modelName string) WebSocketMessageObserver {
		observer := newWebSocketMessageObserver(
			apiType,
			modelName,
			NewZapLoggerAdapter(h.logger.Sugar()),
			applyObservation,
			applyObservation,
		)
		if tracker == nil {
			return observer
		}
		return newBytesTrackingObserver(observer, tracker)
	}

	onClientVisible := func(_ webSocketVisibleWriteContext) {
		if h.activeRegistry == nil {
			return
		}
		h.activeRegistry.MarkDataReceived(requestID)
	}

	return newObserver, tracker, applyObservation, onClientVisible
}

type webSocketProviderConfigError struct {
	missingField string
	err          error
}

func (e *webSocketProviderConfigError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *webSocketProviderConfigError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (h *Handler) validateWebSocketProviderReady(provider *model.Provider, apiType string) (string, error) {
	baseURL := provider.BaseURLForAPIType(apiType)
	if baseURL == "" {
		return "", &webSocketProviderConfigError{
			missingField: "base_url",
			err:          fmt.Errorf("no base_url for api_type %q", apiType),
		}
	}
	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case model.ProviderCredentialTypeChatGPT:
		if h.auth == nil {
			return "", &webSocketProviderConfigError{
				missingField: "credentials",
				err:          fmt.Errorf("provider %q requires managed credentials for websocket", provider.ID),
			}
		}
	default:
		if provider.APIKeyForAPIType(apiType) == "" {
			return "", &webSocketProviderConfigError{
				missingField: "api_key",
				err:          fmt.Errorf("no api_key for api_type %q", apiType),
			}
		}
	}
	return baseURL, nil
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

func hasUsableWebSocketSelectionModel(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	return trimmed != "" && !strings.EqualFold(trimmed, ModelUnknown)
}

func (h *Handler) webSocketSelectionConsumesHiddenModel(ctx context.Context, req *model.SelectRequest) bool {
	if req == nil || hasUsableWebSocketSelectionModel(req.Model) {
		return false
	}
	eligibility, err := selector.NewProviderSelectionEligibility(ctx, h.store, nil, req)
	if err != nil {
		h.logger.Warn(
			"failed to evaluate websocket hidden-model selection demand",
			zap.String("api_type", req.APIType),
			zap.Error(err),
		)
		return false
	}
	return eligibility.WouldConsumeHiddenModel()
}

func marshalWebSocketGatewayError(statusCode int, code, message string) []byte {
	envelope := webSocketGatewayErrorEnvelope{
		Type:   webSocketGatewayErrorEventType,
		Status: statusCode,
	}
	envelope.Error.Type = webSocketGatewayErrorType
	envelope.Error.Code = code
	envelope.Error.Message = message

	payload, err := json.Marshal(envelope)
	if err != nil {
		return []byte(fmt.Sprintf(`{"type":"%s","status":%d,"error":{"type":"%s","code":"%s","message":"%s"}}`,
			webSocketGatewayErrorEventType,
			statusCode,
			webSocketGatewayErrorType,
			ErrCodeInternalError,
			"failed to encode websocket gateway error",
		))
	}
	return payload
}

// buildWebSocketPassthroughHeaders copies the client-controlled handshake headers that
// belong to the wire protocol rather than provider authentication.
func buildWebSocketPassthroughHeaders(r *http.Request) http.Header {
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
	return headers
}

// buildWebSocketDialHeaders preserves the legacy static-auth behavior for tests and
// fallback code paths that do not use the managed provider auth service.
func buildWebSocketDialHeaders(r *http.Request, provider *model.Provider, apiType, globalAuthMode string) http.Header {
	headers := buildWebSocketPassthroughHeaders(r)

	// Inject provider auth credentials.
	SetAuthHeader(headers, provider.APIKeyForAPIType(apiType), provider.AuthMode, globalAuthMode, r)

	return headers
}

func (h *Handler) prepareWebSocketDialHeaders(ctx context.Context, r *http.Request, provider *model.Provider, apiType, globalAuthMode string) (http.Header, error) {
	headers := buildWebSocketPassthroughHeaders(r)
	if h.auth != nil {
		if err := h.auth.ApplyProviderCredentials(ctx, headers, provider, apiType, globalAuthMode, r); err != nil {
			return nil, err
		}
		return headers, nil
	}

	SetAuthHeader(headers, provider.APIKeyForAPIType(apiType), provider.AuthMode, globalAuthMode, r)
	return headers, nil
}

func websocketGatewayFailure(result *WebSocketResult) (int, string, string) {
	statusCode := http.StatusBadGateway
	if result != nil && result.HandshakeStatusCode > 0 {
		statusCode = result.HandshakeStatusCode
	}

	message := "Upstream WebSocket handshake failed"
	switch statusCode {
	case http.StatusUnauthorized:
		message = "Upstream WebSocket authentication failed"
	case http.StatusUpgradeRequired:
		message = "Upstream provider requires HTTP fallback for this request"
	}
	if result != nil && result.HandshakeBodySnippet != "" {
		message = result.HandshakeBodySnippet
	}

	return statusCode, ErrCodeWebSocketUpgrade, message
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

// logWebSocketSession persists the session lifecycle in RequestLog while
// keeping RequestAttempt rows scoped to provider attempts only.
func (h *Handler) logWebSocketSession(info RequestInfo, session *WebSocketSessionResult, latency time.Duration) {
	if session == nil {
		return
	}

	result := session.FinalResult
	err := session.FinalErr
	attempts := session.RequestAttempts()
	sessionCommitted := false
	probeOutcome := model.WebSocketProbeOutcomeUnknown
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
	if model.IsValidWebSocketProbeOutcome(session.ProbeOutcome) {
		probeOutcome = session.ProbeOutcome
	}

	log := &model.RequestLog{
		RequestID:        session.RequestID,
		APIType:          info.APIType,
		Model:            info.Model,
		ClientIP:         info.ClientIP,
		UserID:           info.UserID,
		StatusCode:       websocketLogStatusCode(result),
		LatencyMs:        latency.Milliseconds(),
		Success:          websocketLogSuccess(result),
		IsWebSocket:      true,
		IsSticky:         session.IsSticky,
		RetryCount:       session.RetryCount(),
		StickyWritten:    &session.StickyWritten,
		SessionCommitted: &sessionCommitted,
		ProbeOutcome:     &probeOutcome,
		TerminalCause:    &terminalCause,
		CommitSource:     &commitSource,
		CreatedAt:        time.Now(),
		RequestPath:      info.Path,
		RequestMethod:    info.Method,
		UserAgent:        info.UserAgent,
		RequestIDHeader:  info.RequestID,
	}

	if session.FinalProvider != nil {
		log.ProviderID = session.FinalProvider.ID
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
	if len(attempts) > 0 {
		if insertErr := h.store.InsertAttempts(ctx, attempts); insertErr != nil { // coverage-ignore -- attempt insert errors are logged but don't affect response
			h.logger.Error("failed to insert websocket request attempts", zap.Error(insertErr))
		}
	}
}

func applyWebSocketSessionHealthOutcomes(ctx context.Context, h *Handler, session *WebSocketSessionResult) {
	if session == nil {
		return
	}
	finalProviderSawSemanticOutcome := false
	for _, attempt := range session.Attempts {
		if attempt.Provider == nil {
			continue
		}
		if session.FinalProvider != nil &&
			attempt.Provider.ID == session.FinalProvider.ID &&
			attempt.Result != nil &&
			(attempt.Result.UpstreamError != nil || attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError) {
			finalProviderSawSemanticOutcome = true
		}
		applyWebSocketHealthOutcome(ctx, h, attempt.Provider.ID, attempt.Result)
	}
	if session.FinalProvider == nil || session.FinalResult == nil || finalProviderSawSemanticOutcome {
		return
	}
	if session.FinalResult.UpstreamError != nil || session.FinalResult.TerminalCause == model.TerminalUpstreamSemanticError {
		applyWebSocketHealthOutcome(ctx, h, session.FinalProvider.ID, session.FinalResult)
	}
}

func applyWebSocketHealthOutcome(ctx context.Context, h *Handler, providerID string, result *WebSocketResult) {
	if result == nil {
		return
	}
	failureDisposition := classifyWebSocketHandshakeFailure(result)
	if failureDisposition.autoDisableUntil != nil {
		h.markFailure(ctx, providerID, result.Err)
		h.suspendProviderUntil(
			ctx,
			providerID,
			*failureDisposition.autoDisableUntil,
			failureDisposition.autoDisableReason,
		)
		return
	}
	if result.UpstreamError != nil {
		if shouldTrackWebSocketFailureInHealth(result) {
			h.markFailure(ctx, providerID, result.Err)
		}
		return
	}
	if result.HandshakeAccepted &&
		result.ClientVisible &&
		!result.SessionCommitted &&
		result.TerminalCause != model.TerminalClientDisconnect &&
		result.TerminalCause != model.TerminalInternalError {
		if shouldTrackWebSocketFailureInHealth(result) {
			h.markFailure(ctx, providerID, result.Err)
		}
		return
	}
	if result.SessionCommitted {
		if result.TerminalCause == model.TerminalUpstreamSemanticError {
			if shouldTrackWebSocketFailureInHealth(result) {
				h.markFailure(ctx, providerID, result.Err)
			}
			return
		}
		h.markSuccess(ctx, providerID)
		return
	}

	switch result.TerminalCause {
	case model.TerminalProviderConfigurationError:
		return
	case model.TerminalUpstreamHandshakeRejected,
		model.TerminalUpstreamTransportError,
		model.TerminalUpstreamSemanticError:
		if shouldTrackWebSocketFailureInHealth(result) {
			h.markFailure(ctx, providerID, result.Err)
		}
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
