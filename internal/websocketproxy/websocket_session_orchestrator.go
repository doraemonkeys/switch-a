package websocketproxy

import (
	"context"
	"net/http"
	"time"

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
}

type webSocketDialCaptureCompletion struct {
	exchange DialExchange
	outcome  requestcapture.Outcome
}

func newWebSocketSessionOrchestrator(handler *Gateway, cfg webSocketSessionOrchestratorConfig) *WebSocketSessionOrchestrator {
	selectionProbeObserverFactory := cfg.newSelectionProbeObserver
	if selectionProbeObserverFactory == nil {
		selectionProbeObserverFactory = func(apiType, initialModel string) WebSocketMessageObserver {
			return newWebSocketMessageObserver(apiType, initialModel, nil, nil, nil)
		}
	}
	return &WebSocketSessionOrchestrator{
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
		onClientVisible:           cfg.onClientVisible,
		tracker:                   cfg.tracker,
		capture:                   cfg.capture,
		captureParticipates:       cfg.captureParticipates,
		excludedProviders:         make(map[string]bool),
		switchTracker:             newProviderSwitchTracker(cfg.selectReq, cfg.maxAttempts, handler.visibleContinuitySeedStore),
		attempts:                  make([]WebSocketAttemptResult, 0),
		lifecycle:                 newWebSocketLifecycleState(),
		replayBuffer:              newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
	}
}

func (o *WebSocketSessionOrchestrator) queueCaptureCompletion(exchange DialExchange, outcome requestcapture.Outcome) {
	if o == nil || !exchange.captureMode.Participates() {
		return
	}
	if outcome.CompletedAt.IsZero() {
		outcome.CompletedAt = time.Now()
	}
	o.captureCompletions = append(o.captureCompletions, webSocketDialCaptureCompletion{
		exchange: exchange,
		outcome:  outcome,
	})
}

func (o *WebSocketSessionOrchestrator) finishCaptureCompletions() {
	if o == nil {
		return
	}
	for _, completion := range o.captureCompletions {
		finishWebSocketDialCapture(completion.exchange, completion.outcome)
	}
	o.captureCompletions = nil
}

func (o *WebSocketSessionOrchestrator) newAttemptObserver() WebSocketMessageObserver {
	if o.newObserver == nil {
		return nil
	}
	return o.newObserver(o.info.Model)
}

func (o *WebSocketSessionOrchestrator) relayAcceptedProviderAttempt(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	provider *model.Provider,
	dialExchange DialExchange,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
	recoveryAttempted bool,
) WebSocketAttemptResult {
	dialCaptureOutcome := requestcapture.Outcome{
		SourceCompletion:  requestcapture.SourceCompletionPartial,
		TerminationReason: requestcapture.TerminationReasonWebSocketRelayError,
	}
	if dialExchange.captureMode.Participates() {
		defer func() {
			o.queueCaptureCompletion(dialExchange, dialCaptureOutcome)
		}()
	}

	upstreamConn := dialExchange.Conn
	defer func() {
		if upstreamConn != nil {
			_ = upstreamConn.CloseNow()
		}
	}()

	if err := o.ensureClientAccepted(w, r); err != nil {
		_ = upstreamConn.Close(websocket.StatusGoingAway, "client websocket upgrade rejected")
		upstreamConn = nil
		result := &WebSocketResult{
			Err:           err,
			TerminalCause: model.TerminalClientUpgradeRejected,
			CommitSource:  model.CommitUnknown,
		}
		dialExchange.applyHandshake(result)
		o.applySessionLifecycleToResult(result)
		if dialExchange.captureMode.Participates() {
			reason, failure := webSocketClientAccept(contextError(ctx), err)
			dialCaptureOutcome = requestcapture.Outcome{
				SourceCompletion:   requestcapture.SourceCompletionPartial,
				TerminationReason:  reason,
				Failure:            failure,
				CredentialEvidence: dialExchange.credentialEvidence,
			}
		}
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, result, err, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
		return attemptResult
	}

	observer, captureOptions := o.newAttemptRelayContext(dialExchange)
	replayedBytes, replayed, replayErr := o.replayBufferedMessages(ctx, upstreamConn, observer, captureOptions)
	if replayErr != nil {
		result := &WebSocketResult{
			BytesClientToUpstream: replayedBytes,
			Err:                   replayErr,
			TerminalCause:         model.TerminalUpstreamTransportError,
			CommitSource:          model.CommitUnknown,
		}
		dialExchange.applyHandshake(result)
		o.applySessionLifecycleToResult(result)
		if dialExchange.captureMode.Participates() {
			reason, failure := webSocketReplayWrite(contextError(ctx), replayErr)
			dialCaptureOutcome = requestcapture.Outcome{
				SourceCompletion:   requestcapture.SourceCompletionPartial,
				TerminationReason:  reason,
				Failure:            failure,
				CredentialEvidence: dialExchange.credentialEvidence,
			}
		}
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, result, replayErr, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
		attemptResult.ReplayFailed = true
		attemptResult.RecoveryAttempted = recoveryAttempted
		return attemptResult
	}

	initialClientReadCh := o.takeInitialClientReadChannel()
	postVisibleFailover := selectionMode == model.SwitchModeFailover && o.lifecycle != nil && o.lifecycle.Snapshot().ClientVisible
	relayResult := o.handler.wsForwarder.relay(ctx, o.clientConn, upstreamConn, webSocketRelayOptions{
		GatewayCapture: captureOptions.GatewayCapture,
		Capture:        captureOptions.Capture,
		CaptureMode:    captureOptions.CaptureMode,

		CredentialEvidence:                captureOptions.CredentialEvidence,
		InitialClientReadCh:               initialClientReadCh,
		Observer:                          observer,
		OnFirstUpstreamMessage:            o.applyObservation,
		OnClientVisible:                   o.onClientVisible,
		PreWriteToClient:                  newAllowlistedProviderScopedSuppressDecision(o.replayBuffer),
		PreVisibleReplayBuffer:            o.replayBuffer,
		Lifecycle:                         o.lifecycle,
		PreserveClientOnSuppress:          true,
		SkipClientToUpstream:              postVisibleFailover,
		SkipPreVisibleWindow:              replayed && o.suppressedAttempt != nil,
		PreserveClientOnPreVisibleFailure: o.suppressedAttempt != nil,
	})
	upstreamConn = nil

	result := relayResult.toWebSocketResult()
	dialExchange.applyHandshake(result)
	result.BytesClientToUpstream += replayedBytes
	if observer != nil {
		mergeWebSocketObservation(result, observer.Snapshot())
	}
	if result.UpstreamError != nil {
		result.TerminalCause = model.TerminalUpstreamSemanticError
	}
	o.captureSuppressedAttempt(provider, relayResult)
	if result.ClientVisible && relayResult.SuppressedUpstreamError == nil {
		o.clearSuppressedAttempt()
	}

	attemptResult := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, result, result.Err, time.Since(attemptStart))
	o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
	attemptResult.RecoveryAttempted = recoveryAttempted
	attemptResult.RecoverySucceeded = recoveryAttempted && attemptResult.clientAccepted()
	if result.ClientVisible {
		o.switchTracker.markClientVisible(provider, time.Now())
	}
	if dialExchange.captureMode.Participates() {
		dialCaptureOutcome = webSocketRelayCaptureOutcome(ctx, relayResult, result)
		dialCaptureOutcome.CredentialEvidence = dialExchange.credentialEvidence
	}
	return attemptResult
}

func (o *WebSocketSessionOrchestrator) newAttemptRelayContext(
	dialExchange DialExchange,
) (WebSocketMessageObserver, webSocketRelayOptions) {
	return o.newAttemptObserver(), webSocketRelayOptions{
		GatewayCapture:     o.capture,
		Capture:            dialExchange.capture,
		CaptureMode:        dialExchange.captureMode,
		CredentialEvidence: dialExchange.credentialEvidence,
	}
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

		attemptResult := o.executeProviderAttempt(ctx, w, r, selection.Provider(), attempt, selectionMode, selection.Metadata)
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

	clientConn, err := o.handler.wsForwarder.acceptClient(w, r)
	if err != nil {
		return err
	}
	o.clientConn = clientConn
	o.lifecycle.MarkClientAccepted()
	return nil
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
