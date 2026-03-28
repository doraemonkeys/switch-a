package proxy

import (
	"context"
	"net/http"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/selector"

	"github.com/coder/websocket"
)

type webSocketSessionOrchestratorConfig struct {
	info             RequestInfo
	selectReq        *model.SelectRequest
	apiType          string
	requestID        string
	startTime        time.Time
	maxAttempts      int
	globalAuthMode   string
	newObserver      webSocketObserverFactory
	applyObservation func(WebSocketObservation)
	onClientVisible  func(webSocketVisibleWriteContext)
	tracker          *LiveBytesTracker
}

// WebSocketSessionOrchestrator owns the provider-attempt loop because WebSocket
// failover has different commitment boundaries from HTTP retries even though the
// selector and failover constraints are shared.
type WebSocketSessionOrchestrator struct {
	handler *Handler

	info                RequestInfo
	selectReq           *model.SelectRequest
	apiType             string
	requestID           string
	startTime           time.Time
	maxAttempts         int
	globalAuthMode      string
	newObserver         webSocketObserverFactory
	applyObservation    func(WebSocketObservation)
	onClientVisible     func(webSocketVisibleWriteContext)
	tracker             *LiveBytesTracker
	excludedProviders   map[string]bool
	failoverContext     *model.FailoverContext
	attempts            []WebSocketAttemptResult
	isSticky            bool
	currentProvider     *model.Provider
	lifecycle           *webSocketLifecycleState
	clientConn          *websocket.Conn
	initialClientReadCh <-chan webSocketInitialReadResult
	replayBuffer        *preVisibleClientMessageBuffer
	suppressedAttempt   *webSocketSuppressedAttempt
}

func newWebSocketSessionOrchestrator(handler *Handler, cfg webSocketSessionOrchestratorConfig) *WebSocketSessionOrchestrator {
	return &WebSocketSessionOrchestrator{
		handler:           handler,
		info:              cfg.info,
		selectReq:         cfg.selectReq,
		apiType:           cfg.apiType,
		requestID:         cfg.requestID,
		startTime:         cfg.startTime,
		maxAttempts:       cfg.maxAttempts,
		globalAuthMode:    cfg.globalAuthMode,
		newObserver:       cfg.newObserver,
		applyObservation:  cfg.applyObservation,
		onClientVisible:   cfg.onClientVisible,
		tracker:           cfg.tracker,
		excludedProviders: make(map[string]bool),
		attempts:          make([]WebSocketAttemptResult, 0),
		lifecycle:         newWebSocketLifecycleState(),
		replayBuffer:      newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
	}
}

func (o *WebSocketSessionOrchestrator) newAttemptObserver() WebSocketMessageObserver {
	if o.newObserver == nil {
		return nil
	}
	return o.newObserver(o.info.Model)
}

func (o *WebSocketSessionOrchestrator) Run(ctx context.Context, w http.ResponseWriter, r *http.Request) *WebSocketSessionResult {
	defer o.cleanup()

	if bootstrapSession := o.bootstrapSelectionContext(ctx, w, r); bootstrapSession != nil {
		return bootstrapSession
	}

	for attempt := 0; ; attempt++ {
		if o.maxAttempts > 0 && attempt >= o.maxAttempts {
			return o.finalSessionFromLastAttempt(ctx)
		}

		provider, fromSticky, selectionResult := o.selectProvider(ctx, attempt)
		if selectionResult != nil {
			return o.finalizeSelectionFailureSession(selectionResult)
		}

		if attempt == 0 {
			o.isSticky = fromSticky
		}

		o.currentProvider = provider
		o.trackCurrentAttempt(provider.ID)

		attemptResult := o.executeProviderAttempt(ctx, w, r, provider, attempt)
		o.attempts = append(o.attempts, attemptResult)

		if attemptResult.Result != nil && attemptResult.Result.Model != "" {
			o.info.Model = attemptResult.Result.Model
		}

		if fromSticky {
			return o.sessionFromAttempt(attemptResult)
		}
		if o.shouldSwitchProvider(attemptResult) {
			o.attempts[len(o.attempts)-1].SwitchReason = websocketSwitchReason(attemptResult)
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

	o.handler.activeRegistry.Register(&ActiveRequest{
		RequestID:       o.requestID,
		ProviderID:      providerID,
		Model:           o.info.Model,
		APIType:         o.apiType,
		UserID:          o.info.UserID,
		ClientIP:        o.info.ClientIP,
		StickyMode:      o.selectReq.StickyMode,
		ContinuityKey:   selector.BuildContinuityKey(o.selectReq),
		IsWebSocket:     true,
		StartedAt:       o.startTime,
		HasReceivedData: false,
	})
	if o.tracker != nil {
		o.handler.activeRegistry.RegisterLiveBytes(o.requestID, o.tracker)
	}
}

func (o *WebSocketSessionOrchestrator) excludeCurrentProvider() {
	if o.currentProvider == nil {
		return
	}
	o.excludedProviders[o.currentProvider.ID] = true
	o.handler.releaseConcurrency(o.currentProvider.ID)
	o.currentProvider = nil
}

func (o *WebSocketSessionOrchestrator) cleanup() {
	if o.currentProvider != nil {
		o.handler.releaseConcurrency(o.currentProvider.ID)
	}
	if o.clientConn != nil {
		_ = o.clientConn.CloseNow()
	}
	if o.handler.activeRegistry != nil {
		o.handler.activeRegistry.Unregister(o.requestID)
	}
}

func closeTerminalSuppressedClientConn(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	go func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
}
