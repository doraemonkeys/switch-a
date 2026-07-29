package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
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
	tracker                   *LiveBytesTracker
}

type webSocketSelectionProbeObserverFactory func(apiType, initialModel string) WebSocketMessageObserver

// WebSocketSessionOrchestrator owns the provider-attempt loop because WebSocket
// failover has different commitment boundaries from HTTP retries even though the
// selector and failover constraints are shared.
type WebSocketSessionOrchestrator struct {
	handler *Handler

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
	tracker                   *LiveBytesTracker
	excludedProviders         map[string]bool
	// switchTracker keeps lifecycle-driven replacement vs failover semantics in
	// one place so retries do not depend on handshake-only milestones.
	switchTracker       providerSwitchTracker
	attempts            []WebSocketAttemptResult
	isSticky            bool
	currentProvider     *model.Provider
	activeRegistered    bool
	lifecycle           *webSocketLifecycleState
	clientConn          *websocket.Conn
	initialClientReadCh <-chan webSocketInitialReadResult
	replayBuffer        *preVisibleClientMessageBuffer
	suppressedAttempt   *webSocketSuppressedAttempt
	probeOutcome        webSocketSelectionProbeOutcome
}

func newWebSocketSessionOrchestrator(handler *Handler, cfg webSocketSessionOrchestratorConfig) *WebSocketSessionOrchestrator {
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
		excludedProviders:         make(map[string]bool),
		switchTracker:             newProviderSwitchTracker(cfg.selectReq, cfg.maxAttempts, handler.visibleContinuitySeedStore),
		attempts:                  make([]WebSocketAttemptResult, 0),
		lifecycle:                 newWebSocketLifecycleState(),
		replayBuffer:              newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
	}
}

func (o *WebSocketSessionOrchestrator) newAttemptObserver() WebSocketMessageObserver {
	if o.newObserver == nil {
		return nil
	}
	return o.newObserver(o.info.Model)
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

		provider, selectionMode, selectionMetadata, selectionResult := o.selectProvider(ctx, attempt)
		if selectionResult != nil {
			return o.finalizeSelectionFailureSession(selectionResult)
		}

		if attempt == 0 {
			o.isSticky = selectionMetadata.UsesContinuity()
		}

		o.currentProvider = provider
		o.trackCurrentAttempt(provider.ID)

		attemptResult := o.executeProviderAttempt(ctx, w, r, provider, attempt, selectionMode, selectionMetadata)
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

func (o *WebSocketSessionOrchestrator) trackCurrentAttempt(providerID string) {
	if o.handler.activeRegistry == nil {
		return
	}

	o.handler.activeRegistry.RegisterWithDone(&ActiveRequest{
		RequestID:                     o.requestID,
		ProviderID:                    providerID,
		Model:                         o.info.Model,
		APIType:                       o.apiType,
		UserID:                        o.info.UserID,
		ClientIP:                      o.info.ClientIP,
		StickyMode:                    o.selectReq.StickyMode,
		ContinuityKey:                 selector.BuildContinuityKey(o.selectReq),
		IsWebSocket:                   true,
		StartedAt:                     o.startTime,
		HasReceivedData:               false,
		RequestedReasoningObservation: o.info.Reasoning,
	}, o.requestDone)
	if o.tracker != nil {
		o.handler.activeRegistry.RegisterLiveBytes(o.requestID, o.tracker)
	}
	o.activeRegistered = true
}

func (o *WebSocketSessionOrchestrator) excludeCurrentProvider() {
	if o.currentProvider == nil {
		return
	}
	o.excludedProviders[o.currentProvider.ID] = true
	if o.activeRegistered && o.handler.activeRegistry != nil {
		o.handler.activeRegistry.Unregister(o.requestID)
		o.activeRegistered = false
	} else {
		o.handler.releaseConcurrency(o.currentProvider.ID)
	}
	o.currentProvider = nil
}

func (o *WebSocketSessionOrchestrator) cleanup() {
	if o.activeRegistered && o.handler.activeRegistry != nil {
		o.handler.activeRegistry.Unregister(o.requestID)
		o.activeRegistered = false
	} else if o.currentProvider != nil {
		o.handler.releaseConcurrency(o.currentProvider.ID)
	}
	if o.clientConn != nil {
		_ = o.clientConn.CloseNow()
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
