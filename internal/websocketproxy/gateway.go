package websocketproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

const (
	webSocketGatewayErrorEventType = "error"
	webSocketGatewayErrorType      = "gateway_error"

	ErrCodeProviderUnavailable = "PROVIDER_UNAVAILABLE"
	ErrCodeWebSocketUpgrade    = "WEBSOCKET_UPGRADE_FAILED"
	ErrCodeWebSocketReconnect  = "WEBSOCKET_RECONNECT_REQUIRED"
	ErrCodeInternalError       = "INTERNAL_ERROR"
	ModelUnknown               = "unknown"
	APITypeCodex               = string(apicontract.APITypeCodex)
	StatusCodeNoResponse       = 0
	logInsertTimeout           = 5 * time.Second
	maxUserAgentLength         = 512
)

// RequestConfig is the immutable WebSocket projection of proxy runtime
// configuration. Keeping this value-only prevents the subsystem from depending
// on the HTTP handler's mutable configuration representation.
type RequestConfig struct {
	GlobalAuthMode    string
	GlobalMaxAttempts int
	StickyMode        model.StickyMode
	StickyTTL         time.Duration
	TrustProxy        bool
	UserHeader        string
	ProbeClientModel  bool
}

type RequestInfo struct {
	ClientIP  string
	UserID    string
	Model     string
	APIType   string
	Path      string
	Method    string
	UserAgent string
	RequestID string
	Reasoning model.RequestedReasoningObservation
}

// Store is the persistence and routing-policy surface consumed by WebSocket
// sessions. Interfaces live at the consumer so tests can isolate the subsystem.
type Store interface {
	ListProvidersByAPIType(context.Context, string) ([]model.Provider, error)
	GetConfig(context.Context, string) (string, error)
	InsertLog(context.Context, *model.RequestLog) error
	InsertAttempts(context.Context, []model.RequestAttempt) error
}

// ProviderLease is the exact generation-bound slot capability owned by one
// WebSocket provider attempt. Cleanup receives the capability itself because a
// provider ID cannot identify the lifecycle generation that was acquired.
type ProviderLease interface {
	Provider() *model.Provider
	ProviderID() string
	Generation() uint64
	// CapabilityIdentity returns a process-local opaque identity. Copies of one
	// lease share it; separately acquired slots never do. Zero is invalid.
	CapabilityIdentity() uintptr
	Held() bool
	Release() bool
}

// ProviderSelection keeps routing facts and slot ownership inseparable at the
// WebSocket boundary. This prevents an attempt from being dispatched with a
// provider snapshot whose concurrency capability was lost by an adapter.
type ProviderSelection struct {
	Lease    ProviderLease
	Metadata selector.SelectionMetadata
}

func (s ProviderSelection) Provider() *model.Provider {
	if s.Lease == nil {
		return nil
	}
	return s.Lease.Provider()
}

type Selector interface {
	SelectInitial(context.Context, *model.SelectRequest) (ProviderSelection, error)
	SelectActive(context.Context, *model.SelectRequest, ProviderLease) (ProviderSelection, error)
	SelectAlternate(context.Context, *model.SelectRequest, map[string]bool) (ProviderSelection, error)
	UpdateStickyWithTTL(*model.SelectRequest, string, time.Duration)
	EvictProviderContinuity(string)
}

type ProviderAuthenticator interface {
	ApplyProviderCredentials(context.Context, http.Header, *model.Provider, string, string, *http.Request) error
	RefreshProviderCredentials(context.Context, *model.Provider) (bool, error)
}

type RequestCapture interface {
	Enabled() bool
	BeginGateway(requestcapture.GatewayStart) requestcapture.GatewayRecorder
}

// LiveTraffic hides the HTTP registry's counter representation while preserving
// the single shared tracker used by monitoring and the relay observer.
type LiveTraffic interface {
	ObserveClientToUpstream(int64)
	ObserveUpstreamToClient(int64)
}

type ActiveSession struct {
	RequestID     string
	Lease         ProviderLease
	Model         string
	APIType       string
	UserID        string
	ClientIP      string
	StickyMode    model.StickyMode
	ContinuityKey model.StickyKey
	StartedAt     time.Time
	Reasoning     model.RequestedReasoningObservation
}

type ActiveSessions interface {
	NewLiveTraffic() LiveTraffic
	// Register transfers cleanup ownership of session.Lease to the registry.
	Register(ActiveSession, <-chan struct{}, LiveTraffic) bool
	Unregister(string) bool
	UpdateModel(string, string)
	MarkDataReceived(string)
	FindActiveLeaseForRequest(*model.SelectRequest) (ProviderLease, bool)
}

type Config struct {
	Store                      Store
	Selector                   Selector
	Health                     internal.HealthManager
	ActiveSessions             ActiveSessions
	VisibleContinuitySeedStore model.VisibleContinuitySeedStore
	Auth                       ProviderAuthenticator
	Capture                    RequestCapture
	Forwarder                  *WebSocketForwarder
	Logger                     *zap.Logger
}

// Gateway contains the WebSocket subsystem. All dependencies are immutable and
// injected; request contexts stay request-local and are never stored here.
type Gateway struct {
	store                      Store
	selector                   Selector
	health                     internal.HealthManager
	activeSessions             ActiveSessions
	visibleContinuitySeedStore model.VisibleContinuitySeedStore
	auth                       ProviderAuthenticator
	capture                    RequestCapture
	wsForwarder                *WebSocketForwarder
	logger                     *zap.Logger
	fallbackCounter            atomic.Int64
	fallbackLeaseGeneration    atomic.Uint64
}

func NewGateway(cfg Config) *Gateway {
	if cfg.Store == nil {
		panic("websocketproxy: Store is required but was nil")
	}
	if cfg.Logger == nil {
		panic("websocketproxy: Logger is required but was nil")
	}
	forwarder := cfg.Forwarder
	if forwarder == nil {
		forwarder = NewWebSocketForwarder(WebSocketForwarderConfig{Logger: cfg.Logger})
	}
	return &Gateway{
		store: cfg.Store, selector: cfg.Selector, health: cfg.Health,
		activeSessions: cfg.ActiveSessions, visibleContinuitySeedStore: cfg.VisibleContinuitySeedStore,
		auth: cfg.Auth, capture: cfg.Capture, wsForwarder: forwarder, logger: cfg.Logger,
	}
}

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
func IsUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		headerContains(r.Header, "Connection", "upgrade")
}

// headerContains checks if the header contains the given value (case-insensitive).
// Connection headers can contain multiple comma-separated values (e.g., "keep-alive, Upgrade").
func headerContains(h http.Header, key, value string) bool {
	for _, v := range h[http.CanonicalHeaderKey(key)] {
		for s := range strings.SplitSeq(v, ",") {
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
func (h *Gateway) Handle(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg RequestConfig, apiType, requestID string, startTime time.Time) {
	capture := h.beginGatewayCapture(requestID, startTime)
	captureParticipates := capture.Valid()
	if captureParticipates {
		defer func() {
			capture.Finish(gatewayCaptureOutcome(ctx))
		}()
	}

	reasoningState := model.ReasoningObservationUnsupported
	info := RequestInfo{
		ClientIP:  extractClientIP(r, cfg.TrustProxy),
		UserID:    extractUserID(r, cfg.UserHeader),
		Model:     extractWebSocketModel(r),
		APIType:   apiType,
		Path:      r.URL.Path,
		Method:    r.Method,
		UserAgent: extractUserAgent(r),
		RequestID: extractRequestIDHeader(r),
		Reasoning: model.RequestedReasoningObservation{State: &reasoningState},
	}

	selectReq := &model.SelectRequest{
		ClientIP:   info.ClientIP,
		User:       info.UserID,
		APIType:    apiType,
		Model:      info.Model,
		StickyMode: cfg.StickyMode,
	}
	newObserver, tracker, applyObservation, onClientVisible := h.newWebSocketObserverPipeline(apiType, requestID)
	orchestrator := newWebSocketSessionOrchestrator(h, webSocketSessionOrchestratorConfig{
		info:                info,
		selectReq:           selectReq,
		apiType:             apiType,
		requestID:           requestID,
		requestDone:         ctx.Done(),
		startTime:           startTime,
		maxAttempts:         cfg.GlobalMaxAttempts,
		globalAuthMode:      cfg.GlobalAuthMode,
		probeClientModel:    cfg.ProbeClientModel,
		newObserver:         newObserver,
		applyObservation:    applyObservation,
		onClientVisible:     onClientVisible,
		tracker:             tracker,
		capture:             capture,
		captureParticipates: captureParticipates,
	})
	if captureParticipates {
		// Exchange records must close before their gateway, but only after sticky,
		// health, and duration state has been frozen by the behavior path below.
		defer orchestrator.finishCaptureCompletions()
	}
	session := orchestrator.Run(ctx, w, r)
	if session == nil {
		return
	}

	if session.ResolvedModel != "" {
		info.Model = session.ResolvedModel
	}

	// Commit-based sticky records continuity after real upstream service starts.
	// Later reuse still flows through selector eligibility and health checks, so
	// this write does not bypass a subsequent suspension such as usage-limit handling.
	if session.ClientAccepted &&
		session.FinalResult != nil &&
		session.FinalResult.SessionCommitted &&
		cfg.StickyMode != model.StickyModeOff &&
		h.selector != nil &&
		session.FinalProvider != nil {
		h.selector.UpdateStickyWithTTL(selectReq, session.FinalProvider.ID, cfg.StickyTTL)
		session.StickyWritten = true
	}

	if shouldStoreWebSocketVisibleContinuitySeed(session) {
		h.storeVisibleContinuitySeedFromContext(
			selectReq,
			selectReq.ProviderContinuityContext,
			time.Now(),
			nil,
		)
	}

	if !session.ClientAccepted && session.GatewayStatusCode > 0 {
		h.writeGatewayError(w, session.GatewayStatusCode, session.GatewayErrorCode, session.GatewayMessage)
	}

	applyWebSocketSessionHealthOutcomes(ctx, h, session)
	go h.logWebSocketSession(info, session, time.Since(startTime))
}

func (h *Gateway) newWebSocketObserverPipeline(
	apiType,
	requestID string,
) (webSocketObserverFactory, LiveTraffic, func(WebSocketObservation), func(webSocketVisibleWriteContext)) {
	var tracker LiveTraffic
	if h.activeSessions != nil {
		tracker = h.activeSessions.NewLiveTraffic()
	}

	applyObservation := func(observation WebSocketObservation) {
		if h.activeSessions == nil {
			return
		}
		if observation.Model != "" && observation.Model != ModelUnknown {
			h.activeSessions.UpdateModel(requestID, observation.Model)
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
		if h.activeSessions == nil {
			return
		}
		h.activeSessions.MarkDataReceived(requestID)
	}

	return newObserver, tracker, applyObservation, onClientVisible
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
		return fmt.Appendf(nil, `{"type":"%s","status":%d,"error":{"type":"%s","code":"%s","message":"%s"}}`,
			webSocketGatewayErrorEventType,
			statusCode,
			webSocketGatewayErrorType,
			ErrCodeInternalError,
			"failed to encode websocket gateway error",
		)
	}
	return payload
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
func (h *Gateway) logWebSocketSession(info RequestInfo, session *WebSocketSessionResult, latency time.Duration) {
	if session == nil {
		return
	}

	result := session.FinalResult
	attempts := session.RequestAttempts()
	assessment := assessWebSocketSession(session)
	sessionCommitted := assessment.SessionCommitted
	clientVisible := assessment.ClientVisible
	commitSource := model.CommitUnknown
	if result != nil {
		if result.CommitSource != "" {
			commitSource = result.CommitSource
		}
	}

	log := &model.RequestLog{
		RequestID:                     session.RequestID,
		APIType:                       info.APIType,
		Model:                         info.Model,
		ClientIP:                      info.ClientIP,
		UserID:                        info.UserID,
		SemanticsVersion:              assessment.SemanticsVersion,
		ClientTransportStatusCode:     ptr(assessment.ClientTransportStatusCode),
		CompletionState:               ptr(assessment.CompletionState),
		ServiceOutcome:                ptr(assessment.ServiceOutcome),
		TerminationActor:              assessment.TerminationActor,
		TerminationReason:             assessment.TerminationReason,
		ClientAction:                  ptr(assessment.ClientAction),
		SessionEvidenceJSON:           assessment.SessionEvidenceJSON,
		LatencyMs:                     latency.Milliseconds(),
		IsWebSocket:                   true,
		IsSticky:                      session.IsSticky,
		RetryCount:                    session.RetryCount(),
		SessionCommitted:              &sessionCommitted,
		ClientVisible:                 &clientVisible,
		CommitSource:                  &commitSource,
		CreatedAt:                     time.Now(),
		RequestPath:                   info.Path,
		RequestMethod:                 info.Method,
		UserAgent:                     info.UserAgent,
		RequestIDHeader:               info.RequestID,
		RequestedReasoningObservation: info.Reasoning,
	}

	if session.FinalProvider != nil {
		log.ProviderID = session.FinalProvider.ID
	}

	if result != nil {
		log.ResponseBytes = result.BytesUpstreamToClient
		log.RequestBytes = result.BytesClientToUpstream
	}

	if result != nil && result.TokenUsage != nil {
		log.PromptTokens, log.CompletionTokens, log.TotalTokens,
			log.ReasoningTokens, log.CacheReadInputTokens, log.CacheCreationInputTokens, log.UsageDetails = result.TokenUsage.ToModelFields()
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

func applyWebSocketSessionHealthOutcomes(ctx context.Context, h *Gateway, session *WebSocketSessionResult) {
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
		applyWebSocketHealthOutcome(ctx, h, attempt.Provider, attempt.Result)
	}
	if session.FinalProvider == nil || session.FinalResult == nil || finalProviderSawSemanticOutcome {
		return
	}
	if session.FinalResult.UpstreamError != nil || session.FinalResult.TerminalCause == model.TerminalUpstreamSemanticError {
		applyWebSocketHealthOutcome(ctx, h, session.FinalProvider, session.FinalResult)
	}
}

func applyWebSocketHealthOutcome(
	ctx context.Context,
	h *Gateway,
	provider *model.Provider,
	result *WebSocketResult,
) {
	if provider == nil || result == nil {
		return
	}
	healthAssessment := assessWebSocketHealth(provider, result)
	if healthAssessment.markFailure {
		h.markFailure(ctx, provider.ID, result.Err)
	}
	if healthAssessment.suspendUntil != nil {
		h.suspendProviderUntil(
			ctx,
			provider.ID,
			*healthAssessment.suspendUntil,
			healthAssessment.suspendReason,
		)
		return
	}
	if healthAssessment.markSuccess {
		h.markSuccess(ctx, provider.ID)
	}
}

// bytesTrackingObserver decorates a WebSocketMessageObserver, recording byte
// counts, message counts, and last-activity timestamps into a LiveBytesTracker.
// This piggybacks on the existing observer pipeline — zero transport-layer changes.
type bytesTrackingObserver struct {
	inner   WebSocketMessageObserver
	tracker LiveTraffic
}

func newBytesTrackingObserver(inner WebSocketMessageObserver, tracker LiveTraffic) *bytesTrackingObserver {
	return &bytesTrackingObserver{inner: inner, tracker: tracker}
}

func (o *bytesTrackingObserver) ObserveClientMessage(messageType websocket.MessageType, data []byte) {
	n := int64(len(data))
	o.tracker.ObserveClientToUpstream(n)
	if o.inner != nil {
		o.inner.ObserveClientMessage(messageType, data)
	}
}

func (o *bytesTrackingObserver) ObserveUpstreamMessage(messageType websocket.MessageType, data []byte) {
	n := int64(len(data))
	o.tracker.ObserveUpstreamToClient(n)
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

func (h *Gateway) beginGatewayCapture(requestID string, startedAt time.Time) requestcapture.GatewayRecorder {
	if h.capture == nil || !h.capture.Enabled() {
		return requestcapture.GatewayRecorder{}
	}
	return h.capture.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: requestID, StartedAt: startedAt})
}

func gatewayCaptureOutcome(ctx context.Context) requestcapture.GatewayOutcome {
	err := ctx.Err()
	if err == nil {
		return requestcapture.GatewayOutcome{}
	}
	peer := requestcapture.FailurePeerGateway
	class := requestcapture.FailureClassCanceled
	reason := requestcapture.TerminationReasonCanceled
	if contextClass, known := ContextClass(err); known {
		class = contextClass
		switch contextClass {
		case requestcapture.FailureClassTimeout:
			reason = requestcapture.TerminationReasonTimeout
		case requestcapture.FailureClassCanceled:
			peer = requestcapture.FailurePeerClient
			reason = requestcapture.TerminationReasonClientDisconnect
		}
	}
	return requestcapture.GatewayOutcome{
		TerminationReason: reason,
		Failure: Observation(FromError(
			requestcapture.FailureSiteGateway, peer, class,
			requestcapture.FailureCodeGatewayContext, err,
		), requestcapture.FailureFact{}),
	}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func extractClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			if first, _, _ := strings.Cut(forwarded, ","); strings.TrimSpace(first) != "" {
				return strings.TrimSpace(first)
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func extractUserID(r *http.Request, header string) string { return r.Header.Get(header) }

func extractUserAgent(r *http.Request) string {
	value := r.Header.Get("User-Agent")
	if len(value) > maxUserAgentLength {
		return value[:maxUserAgentLength]
	}
	return value
}

func extractRequestIDHeader(r *http.Request) string { return r.Header.Get("X-Request-ID") }

func (h *Gateway) writeGatewayError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(model.NewGatewayError(code, message)); err != nil {
		h.logger.Error("failed to encode websocket gateway error", zap.Error(err))
	}
}

func (h *Gateway) markSuccess(ctx context.Context, providerID string) {
	if h.health != nil {
		h.health.MarkSuccess(ctx, providerID)
	}
}

func (h *Gateway) markFailure(ctx context.Context, providerID string, err error) {
	if h.health != nil {
		h.health.MarkFailure(ctx, providerID, err)
	}
}

func (h *Gateway) suspendProviderUntil(ctx context.Context, providerID string, until time.Time, reason string) {
	if h.health == nil {
		return
	}
	if err := h.health.SuspendUntil(ctx, providerID, until, reason); err != nil {
		h.logger.Warn("failed to suspend provider after websocket failure", zap.String("provider_id", providerID), zap.Time("disabled_until", until), zap.Error(err))
		return
	}
	if h.selector != nil {
		h.selector.EvictProviderContinuity(providerID)
	}
}
