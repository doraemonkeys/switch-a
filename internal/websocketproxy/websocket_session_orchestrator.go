package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/websocketprotocol"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"
	wsdisguise "github.com/doraemonkeys/switch-a/internal/websocketproxy/disguise"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

type webSocketSessionOrchestratorConfig struct {
	disguise                  *wsdisguise.Session
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

type webSocketSubprotocolPhase string

const (
	webSocketSubprotocolPhaseProbeFixed           webSocketSubprotocolPhase = "probe_fixed"
	webSocketSubprotocolPhaseUpstreamSelection    webSocketSubprotocolPhase = "upstream_selection"
	webSocketSubprotocolPhaseDownstreamValidation webSocketSubprotocolPhase = "downstream_validation"
)

type webSocketSubprotocolValueState string

const (
	webSocketSubprotocolValueEmpty   webSocketSubprotocolValueState = "empty"
	webSocketSubprotocolValuePresent webSocketSubprotocolValueState = "present"
)

type webSocketSubprotocolOutcome string

const (
	webSocketSubprotocolOutcomeAccepted webSocketSubprotocolOutcome = "accepted"
	webSocketSubprotocolOutcomeMismatch webSocketSubprotocolOutcome = "mismatch"
)

const webSocketSubprotocolMismatchUnclassified = "unclassified_mismatch"

// WebSocketSessionOrchestrator owns the provider-attempt loop because WebSocket
// failover has different commitment boundaries from HTTP retries even though the
// selector and failover constraints are shared.
type WebSocketSessionOrchestrator struct {
	disguise *wsdisguise.Session
	handler  *Gateway

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
	clientReadHandoff   *webSocketClientReadHandoff
	pendingDelivery     []webSocketPendingDelivery
	replayBuffer        *preVisibleClientMessageBuffer
	suppressedAttempt   *webSocketSuppressedAttempt
	probeOutcome        webSocketSelectionProbeOutcome
	accountRecoveryKey  model.StickyKey
	captureCompletions  []webSocketDialCaptureCompletion
	subprotocol         websocketprotocol.Negotiation
	codexOperation      *codexws.Operation
	probeBudget         webSocketProbeBudget
	probeNow            func() time.Time
}

func newWebSocketSessionOrchestrator(handler *Gateway, cfg webSocketSessionOrchestratorConfig) *WebSocketSessionOrchestrator {
	if cfg.apiType == APITypeCodex && cfg.codexOperation == nil {
		panic("websocketproxy: Codex operation is required for Codex sessions")
	}
	selectionProbeObserverFactory := cfg.newSelectionProbeObserver
	if selectionProbeObserverFactory == nil {
		selectionProbeObserverFactory = func(apiType, initialModel string) WebSocketMessageObserver {
			return newWebSocketMessageObserver(apiType, initialModel, nil, nil, nil)
		}
	}
	orchestrator := &WebSocketSessionOrchestrator{
		disguise:                  cfg.disguise,
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
		probeBudget:               defaultWebSocketProbeBudget(),
		probeNow:                  time.Now,
	}
	orchestrator.replayBuffer.onTransition = orchestrator.logReplayTransition
	orchestrator.logReplayTransition(orchestrator.replayBuffer.Status())
	orchestrator.onClientVisible = orchestrator.codexVisibleCallback(cfg.onClientVisible)
	return orchestrator
}

func (o *WebSocketSessionOrchestrator) codexVisibleCallback(
	next func(webSocketVisibleWriteContext),
) func(webSocketVisibleWriteContext) {
	return func(visible webSocketVisibleWriteContext) {
		o.switchTracker.markClientVisible(o.currentProvider, time.Now())
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

func (o *WebSocketSessionOrchestrator) sessionClientReadHandoff() *webSocketClientReadHandoff {
	if o.clientReadHandoff == nil {
		o.clientReadHandoff = newWebSocketClientReadHandoff(o.takeInitialClientReadChannel())
	}
	return o.clientReadHandoff
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

func (o *WebSocketSessionOrchestrator) Run(ctx context.Context, w http.ResponseWriter, r *http.Request) (session *WebSocketSessionResult) {
	defer func() { o.finishDisguiseSelection(session) }()
	defer o.cleanup()
	if session := o.initializeSubprotocol(r); session != nil {
		return session
	}

	if bootstrapSession := o.bootstrapSelectionContext(ctx, w, r); bootstrapSession != nil {
		return bootstrapSession
	}
	o.prepareSelectionContinuity(ctx)

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
		if disguiseFailure(attemptResult.terminalErr()) != nil {
			return o.sessionFromAttempt(attemptResult)
		}

		if o.shouldSwitchProvider(attemptResult) {
			if o.codexOperation != nil {
				if err := o.codexOperation.ReplacePhysicalAttempt(); err != nil {
					return o.sessionFromAttempt(attemptResult)
				}
				applyCodexWebSocketRouteConstraint(o.selectReq, o.codexOperation)
			}
			switchReason := websocketSwitchReason(attemptResult)
			o.attempts[len(o.attempts)-1].SwitchReason = switchReason
			nextSelectionMode := o.switchTracker.prepareProviderSwitch()
			o.logProviderSwitch(attemptResult, switchReason, nextSelectionMode)
			o.excludeCurrentProvider()
			continue
		}
		if o.shouldFallbackToSuppressedPayload(attemptResult) {
			return o.sessionFromSuppressedPayload(ctx)
		}
		o.commitFinalCodexAttempt(ctx, &attemptResult)
		o.attempts[len(o.attempts)-1] = attemptResult
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

	result.ReplayStatus = o.replayBuffer.Status()
	snapshot := o.lifecycle.Snapshot()
	if snapshot.ClientAccepted {
		result.ClientAccepted = true
	}
	if snapshot.ClientVisible {
		result.ClientVisible = true
	}
}

func (o *WebSocketSessionOrchestrator) ensureClientAccepted(
	w http.ResponseWriter,
	r *http.Request,
	negotiation websocketprotocol.Negotiation,
) error {
	if o.clientConn != nil {
		return nil
	}
	if !negotiation.Fixed() && len(negotiation.ClientOffer()) == 0 {
		// A protocol-free session has only one possible result. Fixing that result
		// here keeps the downstream accept boundary explicit even without an offer.
		next, err := negotiation.BindUpstream("")
		if err != nil {
			return err
		}
		negotiation = next
	}

	downstreamOffer, err := negotiation.DownstreamOffer()
	if err != nil {
		return err
	}
	clientConn, err := o.handler.wsForwarder.acceptClient(w, r, downstreamOffer...)
	if err != nil {
		return err
	}
	if err := negotiation.ValidateDownstream(clientConn.Subprotocol()); err != nil {
		closeWebSocketSubprotocolViolation(clientConn)
		return err
	}
	// The downstream 101 is the first point where an ordinary selection becomes
	// session state. Rejected upstream attempts retain the original full offer.
	o.subprotocol = negotiation
	o.clientConn = clientConn
	o.lifecycle.MarkClientAccepted()
	o.logSubprotocolDecision(
		"websocket.subprotocol_downstream_accepted",
		webSocketSubprotocolPhaseDownstreamValidation,
		websocketprotocol.PeerDownstream,
		clientConn.Subprotocol(),
		nil,
	)
	return nil
}

func (o *WebSocketSessionOrchestrator) initializeSubprotocol(r *http.Request) *WebSocketSessionResult {
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

func (o *WebSocketSessionOrchestrator) acceptedSubprotocolNegotiation(
	exchange DialExchange,
) (websocketprotocol.Negotiation, error) {
	next, err := o.subprotocol.BindUpstream(exchange.NegotiatedSubprotocol)
	if err != nil {
		o.logSubprotocolDecision(
			"websocket.subprotocol_mismatch",
			webSocketSubprotocolPhaseUpstreamSelection,
			websocketprotocol.PeerUpstream,
			exchange.NegotiatedSubprotocol,
			err,
		)
		return o.subprotocol, err
	}
	o.logSubprotocolDecisionForNegotiation(
		"websocket.subprotocol_upstream_selected",
		webSocketSubprotocolPhaseUpstreamSelection,
		websocketprotocol.PeerUpstream,
		exchange.NegotiatedSubprotocol,
		nil,
		next,
	)
	return next, nil
}

func (o *WebSocketSessionOrchestrator) logSubprotocolDecision(
	event string,
	phase webSocketSubprotocolPhase,
	peer websocketprotocol.Peer,
	actual string,
	decisionErr error,
) {
	if o == nil {
		return
	}
	o.logSubprotocolDecisionForNegotiation(
		event, phase, peer, actual, decisionErr, o.subprotocol,
	)
}

func (o *WebSocketSessionOrchestrator) logSubprotocolDecisionForNegotiation(
	event string,
	phase webSocketSubprotocolPhase,
	peer websocketprotocol.Peer,
	actual string,
	decisionErr error,
	negotiation websocketprotocol.Negotiation,
) {
	if o == nil || o.handler == nil || o.handler.logger == nil {
		return
	}
	outcome := webSocketSubprotocolOutcomeAccepted
	mismatchReason := ""
	peer = observableSubprotocolPeer(peer)
	if decisionErr != nil {
		outcome = webSocketSubprotocolOutcomeMismatch
		mismatchReason = webSocketSubprotocolMismatchUnclassified
		var mismatch *websocketprotocol.MismatchError
		if errors.As(decisionErr, &mismatch) {
			peer = observableSubprotocolPeer(mismatch.Peer)
			mismatchReason = observableSubprotocolMismatchReason(mismatch.Reason)
		}
	}
	fields := []zap.Field{
		zap.String("request_id", o.requestID),
		zap.String("session_id", o.requestID),
		zap.Int("attempt_index", len(o.attempts)),
		zap.Bool("attempt_active", o.currentLease != nil),
		zap.String("negotiation_phase", string(phase)),
		zap.String("negotiation_outcome", string(outcome)),
		zap.String("peer", string(peer)),
		zap.Bool("probe", o.probeOutcome != webSocketSelectionProbeOutcomeBypassed),
		zap.Int("client_offer_count", len(negotiation.ClientOffer())),
		zap.Strings("client_offered_subprotocols", negotiation.ClientOffer()),
		zap.Bool("selection_fixed", negotiation.Fixed()),
		zap.String("selected_state", string(subprotocolValueState(negotiation.Selected()))),
		zap.String("actual_state", string(subprotocolValueState(actual))),
		zap.String("selected_subprotocol", negotiation.Selected()),
		zap.String("actual_subprotocol", actual),
	}
	if o.currentLease != nil {
		fields = append(fields,
			zap.String("provider_id", o.currentLease.ProviderID()),
			zap.Uint64("provider_generation", o.currentLease.Generation()),
		)
	}
	if mismatchReason != "" {
		fields = append(fields, zap.String("mismatch_reason", mismatchReason))
		o.handler.logger.Warn(event, fields...)
		return
	}
	o.handler.logger.Debug(event, fields...)
}

func subprotocolValueState(value string) webSocketSubprotocolValueState {
	if value == "" {
		return webSocketSubprotocolValueEmpty
	}
	return webSocketSubprotocolValuePresent
}

func observableSubprotocolPeer(peer websocketprotocol.Peer) websocketprotocol.Peer {
	switch peer {
	case websocketprotocol.PeerUpstream, websocketprotocol.PeerDownstream:
		return peer
	default:
		return ""
	}
}

func observableSubprotocolMismatchReason(reason websocketprotocol.MismatchReason) string {
	switch reason {
	case websocketprotocol.MismatchReasonUnexpectedSelection,
		websocketprotocol.MismatchReasonMissingSelection,
		websocketprotocol.MismatchReasonSelectionChanged:
		return string(reason)
	default:
		return webSocketSubprotocolMismatchUnclassified
	}
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

func (o *WebSocketSessionOrchestrator) commitFinalCodexAttempt(ctx context.Context, attempt *WebSocketAttemptResult) {
	if o == nil || o.codexOperation == nil || attempt == nil || attempt.Result == nil {
		return
	}
	if !attempt.Result.HandshakeAccepted || !attempt.Result.ClientAccepted ||
		errors.Is(attempt.Result.Err, websocketprotocol.ErrSubprotocolMismatch) {
		return
	}
	if err := o.codexOperation.CommitCookies(ctx); err != nil {
		attempt.ForwardErr = err
		attempt.Result.Err = err
		attempt.Result.TerminalCause = model.TerminalInternalError
		attempt.Result.CloseCode = websocketCloseStatusForCodexFailure(err)
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
