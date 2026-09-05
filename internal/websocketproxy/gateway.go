package websocketproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
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
	ConversationRecoveryPolicy model.ConversationRecoveryPolicy
	GlobalAuthMode             string
	GlobalMaxAttempts          int
	StickyMode                 model.StickyMode
	StickyTTL                  time.Duration
	TrustProxy                 bool
	UserHeader                 string
	ProbeClientModel           bool
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
	CandidateSnapshot() (codexidentity.CandidateSnapshot, bool)
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
	ApplyProviderCredentials(context.Context, http.Header, codexidentity.CandidateSnapshot, string, string, *http.Request, *url.URL) (codexidentity.AppliedIdentity, error)
	RefreshCredentialSession(context.Context, credentialsession.Snapshot) (bool, error)
}

type ProviderUsageObserver interface {
	ObserveCredentialSessionUsage(context.Context, string, *model.ProviderUsageSnapshot) error
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
	UsageObserver              ProviderUsageObserver
	Capture                    RequestCapture
	Forwarder                  *WebSocketForwarder
	Codex                      *codexws.Runtime
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
	usageObserver              ProviderUsageObserver
	capture                    RequestCapture
	wsForwarder                *WebSocketForwarder
	codex                      *codexws.Runtime
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
	if cfg.Codex == nil {
		panic("websocketproxy: Codex runtime is required but was nil")
	}
	if cfg.Auth == nil {
		panic("websocketproxy: Auth is required but was nil")
	}
	forwarder := cfg.Forwarder
	if forwarder == nil {
		forwarder = NewWebSocketForwarder(WebSocketForwarderConfig{Logger: cfg.Logger})
	}
	usageObserver := cfg.UsageObserver
	if usageObserver == nil {
		usageObserver, _ = cfg.Auth.(ProviderUsageObserver)
	}
	return &Gateway{
		store: cfg.Store, selector: cfg.Selector, health: cfg.Health,
		activeSessions: cfg.ActiveSessions, visibleContinuitySeedStore: cfg.VisibleContinuitySeedStore,
		auth: cfg.Auth, usageObserver: usageObserver,
		capture: cfg.Capture, wsForwarder: forwarder, codex: cfg.Codex, logger: cfg.Logger,
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
		OperationID: requestID,
		ClientIP:    info.ClientIP,
		User:        info.UserID,
		APIType:     apiType,
		Model:       info.Model,
		StickyMode:  cfg.StickyMode,
	}
	var codexOperation *codexws.Operation
	if apiType == APITypeCodex {
		var err error
		codexOperation, err = h.codex.Begin(ctx, r, apiType, requestID, cfg.ConversationRecoveryPolicy)
		if err != nil {
			h.writeCodexWebSocketFailureForOperation(w, requestID, err)
			return
		}
		defer codexOperation.DiscardCookies()
		selectReq.ClientScope = codexOperation.ClientScope()
		if setCookie := codexOperation.GatewaySetCookie(); setCookie != "" {
			w.Header().Add("Set-Cookie", setCookie)
		}
		applyCodexWebSocketRouteConstraint(selectReq, codexOperation)
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
		codexOperation:      codexOperation,
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
	if shouldWriteWebSocketSticky(session, codexOperation != nil && codexOperation.AllowsAccountSwitch()) &&
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

func applyCodexWebSocketRouteConstraint(request *model.SelectRequest, operation *codexws.Operation) {
	if request == nil || operation == nil {
		return
	}
	authority, routeTargetID := operation.RequiredAuthority()
	request.RequiredAuthority = authority
	request.PreferredRouteTargetID = routeTargetID
}

func (h *Gateway) writeCodexWebSocketFailure(w http.ResponseWriter, err error) {
	h.writeCodexWebSocketFailureForOperation(w, "", err)
}

func (h *Gateway) writeCodexWebSocketFailureForOperation(w http.ResponseWriter, operationID string, err error) {
	decision := codexWebSocketRecoveryDecision(err, codexrecovery.PhaseWebSocketPreUpgrade)
	message := "WebSocket protocol state was rejected"
	if decision.RecoveryAction() == codexrecovery.RecoveryActionRetry {
		message = "WebSocket state service is unavailable"
	}
	h.logger.Warn("websocket.codex_boundary_rejected",
		zap.String("operation_id", operationID),
		zap.String("session_id", operationID),
		zap.String("failure_class", string(codexws.Classify(err))),
		zap.String("recovery_condition", string(decision.Condition())),
		zap.String("error_code", string(decision.ErrorCode())),
		zap.String("recovery_action", string(decision.RecoveryAction())),
		zap.Int("http_status", decision.HTTPStatus()),
		zap.Error(err),
	)
	h.writeGatewayError(w, decision.HTTPStatus(), string(decision.ErrorCode()), message)
}

func websocketCloseStatusForCodexFailure(err error) websocket.StatusCode {
	return codexWebSocketRecoveryDecision(err, codexrecovery.PhaseWebSocketAccepted).WebSocketCloseCode()
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
