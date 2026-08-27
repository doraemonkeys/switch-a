package websocketproxy

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/websocketprotocol"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

type webSocketSessionOrchestratorConfig struct {
	info                      RequestInfo
	selectReq                 *model.SelectRequest
	apiType                   string
	requestID                 string
	requestDone               <-chan struct{}
	startTime                 time.Time
	maxAttempts               int
	globalAuthMode            string
	probeClientModel          bool
	newObserver               webSocketObserverFactory
	newSelectionProbeObserver webSocketSelectionProbeObserverFactory
	applyObservation          func(WebSocketObservation)
	onClientVisible           func(webSocketVisibleWriteContext)
	tracker                   LiveTraffic
	capture                   requestcapture.GatewayRecorder
	captureParticipates       bool
	codexOperation            *codexws.Operation
}

type webSocketSelectionProbeObserverFactory func(apiType, initialModel string) WebSocketMessageObserver

// WebSocketSessionOrchestrator owns the provider-attempt loop because WebSocket
// failover has different commitment boundaries from HTTP retries even though the
// selector and failover constraints are shared.
type WebSocketSessionOrchestrator struct {
	handler *Gateway

	info                      RequestInfo
	selectReq                 *model.SelectRequest
	apiType                   string
	requestID                 string
	requestDone               <-chan struct{}
	startTime                 time.Time
	maxAttempts               int
	globalAuthMode            string
	probeClientModel          bool
	newObserver               webSocketObserverFactory
	newSelectionProbeObserver webSocketSelectionProbeObserverFactory
	applyObservation          func(WebSocketObservation)
	onClientVisible           func(webSocketVisibleWriteContext)
	tracker                   LiveTraffic
	capture                   requestcapture.GatewayRecorder
	captureParticipates       bool
	excludedProviders         map[string]bool
	// switchTracker keeps lifecycle-driven replacement vs failover semantics in
	// one place so retries do not depend on handshake-only milestones.
	switchTracker       providerSwitchTracker
	attempts            []WebSocketAttemptResult
	isSticky            bool
	currentProvider     *model.Provider
	currentLease        ProviderLease
	activeRegistered    bool
	lifecycle           *webSocketLifecycleState
	clientConn          *websocket.Conn
	initialClientReadCh <-chan webSocketInitialReadResult
	replayBuffer        *preVisibleClientMessageBuffer
	suppressedAttempt   *webSocketSuppressedAttempt
	probeOutcome        webSocketSelectionProbeOutcome
	captureCompletions  []webSocketDialCaptureCompletion
	subprotocol         websocketprotocol.Negotiation
	codexOperation      *codexws.Operation
}

func newWebSocketSessionOrchestrator(handler *Gateway, cfg webSocketSessionOrchestratorConfig) *WebSocketSessionOrchestrator {
	selectionProbeObserverFactory := cfg.newSelectionProbeObserver
	if selectionProbeObserverFactory == nil {
		selectionProbeObserverFactory = func(apiType, initialModel string) WebSocketMessageObserver {
			return newWebSocketMessageObserver(apiType, initialModel, nil, nil, nil)
		}
	}
	orchestrator := &WebSocketSessionOrchestrator{
		handler:                   handler,
		info:                      cfg.info,
		selectReq:                 cfg.selectReq,
		apiType:                   cfg.apiType,
		requestID:                 cfg.requestID,
		requestDone:               cfg.requestDone,
		startTime:                 cfg.startTime,
		maxAttempts:               cfg.maxAttempts,
		globalAuthMode:            cfg.globalAuthMode,
		probeClientModel:          cfg.probeClientModel,
		newObserver:               cfg.newObserver,
		newSelectionProbeObserver: selectionProbeObserverFactory,
		applyObservation:          cfg.applyObservation,
		tracker:                   cfg.tracker,
		capture:                   cfg.capture,
		captureParticipates:       cfg.captureParticipates,
		codexOperation:            cfg.codexOperation,
		excludedProviders:         make(map[string]bool),
		switchTracker:             newProviderSwitchTracker(cfg.selectReq, cfg.maxAttempts, handler.visibleContinuitySeedStore),
		attempts:                  make([]WebSocketAttemptResult, 0),
		lifecycle:                 newWebSocketLifecycleState(),
		replayBuffer:              newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
	}
	orchestrator.onClientVisible = orchestrator.codexVisibleCallback(cfg.onClientVisible)
	return orchestrator
}

func (o *WebSocketSessionOrchestrator) codexVisibleCallback(
	next func(webSocketVisibleWriteContext),
) func(webSocketVisibleWriteContext) {
	return func(visible webSocketVisibleWriteContext) {
		if o != nil && o.codexOperation != nil {
			o.codexOperation.PinClientVisible()
		}
		if next != nil {
			next(visible)
		}
	}
}

func (o *WebSocketSessionOrchestrator) newAttemptObserver() WebSocketMessageObserver {
	if o.newObserver == nil {
		return nil
	}
	return o.newObserver(o.info.Model)
}

func (o *WebSocketSessionOrchestrator) takeInitialClientReadChannel() <-chan webSocketInitialReadResult {
	initialClientReadCh := o.initialClientReadCh
	o.initialClientReadCh = nil
	return initialClientReadCh
}

func (o *WebSocketSessionOrchestrator) learnResolvedModel(modelName string) {
	if o == nil || !hasUsableWebSocketSelectionModel(modelName) {
		return
	}
	o.info.Model = modelName
	if o.selectReq != nil {
		o.selectReq.Model = modelName
	}
}

func (o *WebSocketSessionOrchestrator) Run(ctx context.Context, w http.ResponseWriter, r *http.Request) *WebSocketSessionResult {
	defer o.cleanup()
	if session := o.initializeSubprotocol(r); session != nil {
		return session
	}

	if bootstrapSession := o.bootstrapSelectionContext(ctx, w, r); bootstrapSession != nil {
		return bootstrapSession
	}
	o.handler.maybeLookupVisibleContinuityCandidate(ctx, &o.switchTracker)

	for attempt := 0; ; attempt++ {
		if o.maxAttempts > 0 && attempt >= o.maxAttempts {
			return o.finalSessionFromLastAttempt(ctx)
		}

		selection, selectionMode, selectionResult := o.selectProvider(ctx, attempt)
		if selectionResult != nil {
			return o.finalizeSelectionFailureSession(selectionResult)
		}

		if attempt == 0 {
			o.isSticky = selection.Metadata.UsesContinuity()
		}

		o.currentProvider = selection.Provider()
		o.currentLease = selection.Lease
		o.trackCurrentAttempt(selection)

		attemptResult := o.executeProviderAttempt(ctx, w, r, selection.Provider(), selection.Lease, attempt, selectionMode, selection.Metadata)
		o.attempts = append(o.attempts, attemptResult)

		if attemptResult.Result != nil {
			o.learnResolvedModel(attemptResult.Result.Model)
		}

		if o.shouldSwitchProvider(attemptResult) {
			o.attempts[len(o.attempts)-1].SwitchReason = websocketSwitchReason(attemptResult)
			o.switchTracker.prepareProviderSwitch()
			o.excludeCurrentProvider()
			continue
		}
		if o.shouldFallbackToSuppressedPayload(attemptResult) {
			return o.sessionFromSuppressedPayload(ctx)
		}
		return o.sessionFromAttempt(attemptResult)
	}
}

func (o *WebSocketSessionOrchestrator) applySessionLifecycleToAttempt(attempt *WebSocketAttemptResult) {
	if attempt != nil {
		o.applySessionLifecycleToResult(attempt.Result)
	}
}

func (o *WebSocketSessionOrchestrator) applySessionLifecycleToResult(result *WebSocketResult) {
	if result == nil {
		return
	}

	snapshot := o.lifecycle.Snapshot()
	if snapshot.ClientAccepted {
		result.ClientAccepted = true
	}
	if snapshot.ClientVisible {
		result.ClientVisible = true
	}
}

func (o *WebSocketSessionOrchestrator) ensureClientAccepted(w http.ResponseWriter, r *http.Request) error {
	if o.clientConn != nil {
		return nil
	}
	if !o.subprotocol.Fixed() && len(o.subprotocol.ClientOffer()) == 0 {
		// A protocol-free session has only one possible result. Fixing that result
		// here keeps focused transport tests valid without weakening the rule that
		// a non-empty client offer must be resolved by the upstream first.
		next, err := o.subprotocol.BindUpstream("")
		if err != nil {
			return err
		}
		o.subprotocol = next
	}

	downstreamOffer, err := o.subprotocol.DownstreamOffer()
	if err != nil {
		return err
	}
	clientConn, err := o.handler.wsForwarder.acceptClient(w, r, downstreamOffer...)
	if err != nil {
		return err
	}
	if err := o.subprotocol.ValidateDownstream(clientConn.Subprotocol()); err != nil {
		closeWebSocketSubprotocolViolation(clientConn)
		return err
	}
	o.clientConn = clientConn
	o.lifecycle.MarkClientAccepted()
	if o.codexOperation != nil {
		// A non-probe 101 is already observable. Probe acceptance has no physical
		// provider yet, so PinClientVisible intentionally remains a no-op until the
		// first successfully forwarded upstream frame invokes the visible callback.
		o.codexOperation.PinClientVisible()
	}
	o.logSubprotocolDecision("websocket.subprotocol_downstream_accepted", clientConn.Subprotocol(), "")
	return nil
}

func (o *WebSocketSessionOrchestrator) initializeSubprotocol(r *http.Request) *WebSocketSessionResult {
	if o.codexOperation != nil && !o.codexOperation.Features().WebSocketSubprotocol {
		o.subprotocol = websocketprotocol.New(websocketprotocol.Offer{})
		o.logSubprotocolDecision("websocket.subprotocol_disabled", "", "")
		return nil
	}
	negotiation, err := parseWebSocketSubprotocolNegotiation(r.Header)
	if err == nil {
		o.subprotocol = negotiation
		return nil
	}
	return o.finalizeSelectionFailureSession(newWebSocketSelectionFailureSession(
		o.requestID,
		o.isSticky,
		o.attempts,
		http.StatusBadRequest,
		model.TerminalClientUpgradeRejected,
		ErrCodeWebSocketUpgrade,
		"Invalid WebSocket subprotocol offer",
		err,
	))
}

func (o *WebSocketSessionOrchestrator) bindUpstreamSubprotocol(exchange DialExchange) error {
	next, err := o.subprotocol.BindUpstream(exchange.NegotiatedSubprotocol)
	if err != nil {
		o.logSubprotocolDecision("websocket.subprotocol_mismatch", exchange.NegotiatedSubprotocol, err.Error())
		return err
	}
	o.subprotocol = next
	o.logSubprotocolDecision("websocket.subprotocol_upstream_selected", exchange.NegotiatedSubprotocol, "")
	return nil
}

func (o *WebSocketSessionOrchestrator) logSubprotocolDecision(event, actual, mismatchReason string) {
	if o == nil || o.handler == nil || o.handler.logger == nil {
		return
	}
	fields := []zap.Field{
		zap.String("request_id", o.requestID),
		zap.Bool("probe", o.probeOutcome != webSocketSelectionProbeOutcomeBypassed),
		zap.Int("client_offer_count", len(o.subprotocol.ClientOffer())),
		zap.String("selected_subprotocol", o.subprotocol.Selected()),
		zap.String("actual_subprotocol", actual),
	}
	if mismatchReason != "" {
		fields = append(fields, zap.String("mismatch_reason", mismatchReason))
		o.handler.logger.Warn(event, fields...)
		return
	}
	o.handler.logger.Debug(event, fields...)
}

func (o *WebSocketSessionOrchestrator) trackCurrentAttempt(selection ProviderSelection) {
	if o.handler.activeSessions == nil {
		return
	}

	registered := o.handler.activeSessions.Register(ActiveSession{
		RequestID: o.requestID, Lease: selection.Lease, Model: o.info.Model,
		APIType: o.apiType, UserID: o.info.UserID, ClientIP: o.info.ClientIP,
		StickyMode: o.selectReq.StickyMode, ContinuityKey: selector.BuildContinuityKey(o.selectReq),
		StartedAt: o.startTime, Reasoning: o.info.Reasoning,
	}, o.requestDone, o.tracker)
	o.activeRegistered = registered
	if !registered {
		o.handler.logger.Warn("websocket.active_session_registration_rejected",
			zap.String("request_id", o.requestID),
			zap.String("provider_id", selection.Lease.ProviderID()),
			zap.Uint64("provider_generation", selection.Lease.Generation()),
		)
	}
}

func (o *WebSocketSessionOrchestrator) excludeCurrentProvider() {
	if o.currentProvider == nil || o.currentLease == nil {
		return
	}
	o.excludedProviders[o.currentProvider.ID] = true
	if o.activeRegistered && o.handler.activeSessions != nil {
		o.unregisterCurrentLease("provider_switch")
		o.activeRegistered = false
	} else {
		o.releaseCurrentLease("provider_switch")
	}
	o.currentProvider = nil
	o.currentLease = nil
}

func (o *WebSocketSessionOrchestrator) cleanup() {
	if o.activeRegistered && o.handler.activeSessions != nil {
		o.unregisterCurrentLease("session_cleanup")
		o.activeRegistered = false
	} else {
		o.releaseCurrentLease("session_cleanup")
	}
	o.currentLease = nil
	if o.clientConn != nil {
		_ = o.clientConn.CloseNow()
	}
}

func (o *WebSocketSessionOrchestrator) releaseCurrentLease(reason string) {
	if o == nil || o.currentLease == nil {
		return
	}
	released := o.currentLease.Release()
	if o.handler != nil && o.handler.logger != nil {
		o.handler.logger.Debug("websocket.provider_lease_released",
			zap.String("request_id", o.requestID),
			zap.String("provider_id", o.currentLease.ProviderID()),
			zap.Uint64("provider_generation", o.currentLease.Generation()),
			zap.String("reason", reason),
			zap.String("release_owner", "orchestrator"),
			zap.Bool("released", released),
		)
	}
}

func (o *WebSocketSessionOrchestrator) unregisterCurrentLease(reason string) {
	if o == nil || o.currentLease == nil || o.handler == nil || o.handler.activeSessions == nil {
		return
	}
	entryRemoved := o.handler.activeSessions.Unregister(o.requestID)
	if o.handler.logger != nil {
		o.handler.logger.Debug("websocket.provider_lease_released",
			zap.String("request_id", o.requestID),
			zap.String("provider_id", o.currentLease.ProviderID()),
			zap.Uint64("provider_generation", o.currentLease.Generation()),
			zap.String("reason", reason),
			zap.String("release_owner", "active_registry"),
			zap.Bool("registry_entry_removed", entryRemoved),
			zap.Bool("lease_held_after", o.currentLease.Held()),
		)
	}
}

func closeTerminalSuppressedClientConn(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	// Post-terminal gateway ownership is only protocol-stable if the close frame is
	// queued before the handler returns, but waiting for the full close handshake
	// on the main goroutine would wedge terminal session finalization. CloseRead
	// keeps the control-plane handshake moving while the bounded wait preserves the
	// canonical close frame in the common case.
	conn.CloseRead(context.Background())
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	select {
	case <-closed:
	case <-time.After(webSocketTerminalCloseFlushTimeout):
	}
}
