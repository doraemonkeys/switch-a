package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
	"switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
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

	info              RequestInfo
	selectReq         *model.SelectRequest
	apiType           string
	requestID         string
	startTime         time.Time
	maxAttempts       int
	globalAuthMode    string
	newObserver       webSocketObserverFactory
	applyObservation  func(WebSocketObservation)
	onClientVisible   func(webSocketVisibleWriteContext)
	tracker           *LiveBytesTracker
	excludedProviders map[string]bool
	failoverContext   *model.FailoverContext
	attempts          []WebSocketAttemptResult
	isSticky          bool
	currentProvider   *model.Provider
	lifecycle         *webSocketLifecycleState
	clientConn        *websocket.Conn
	replayBuffer      *preVisibleClientMessageBuffer
	suppressedAttempt *webSocketSuppressedAttempt
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

	for attempt := 0; ; attempt++ {
		if o.maxAttempts > 0 && attempt >= o.maxAttempts {
			return o.finalSessionFromLastAttempt(ctx)
		}

		provider, fromSticky, selectionResult := o.selectProvider(ctx, attempt)
		if selectionResult != nil {
			return selectionResult
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

func (o *WebSocketSessionOrchestrator) selectProvider(ctx context.Context, attempt int) (*model.Provider, bool, *WebSocketSessionResult) {
	o.selectReq.FailoverContext = o.failoverContext
	o.selectReq.MaxProviderSwitches = o.maxAttempts

	provider, fromSticky, err := o.handler.selectProviderWithTracking(ctx, o.selectReq, attempt, o.excludedProviders)
	if err == nil {
		if o.failoverContext == nil {
			o.failoverContext = model.NewFailoverContext(provider)
		} else {
			o.failoverContext.Update(provider)
		}
		return provider, fromSticky, nil
	}

	if errors.Is(err, internal.ErrNoProvider) {
		if len(o.attempts) > 0 {
			return nil, false, o.finalSessionFromLastAttempt(ctx)
		}
		o.handler.logger.Warn("no providers available for websocket", zap.String("api_type", o.apiType))
		return nil, false, newWebSocketSelectionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			http.StatusServiceUnavailable,
			model.TerminalProviderUnavailable,
			ErrCodeProviderUnavailable,
			fmt.Sprintf("No available provider for api_type: %s", o.apiType),
			err,
		)
	}

	o.handler.logger.Error("provider selection failed for websocket", zap.Error(err))
	return nil, false, newWebSocketSelectionFailureSession(
		o.requestID,
		o.isSticky,
		o.attempts,
		http.StatusInternalServerError,
		model.TerminalInternalError,
		ErrCodeInternalError,
		"Provider selection failed",
		err,
	)
}

func (o *WebSocketSessionOrchestrator) executeProviderAttempt(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	provider *model.Provider,
	attempt int,
) WebSocketAttemptResult {
	attemptStart := time.Now()

	upstreamURL, dialHeaders, err := o.prepareProviderAttempt(ctx, r, provider)
	if err != nil {
		attemptResult := newWebSocketProviderConfigurationAttempt(provider, o.apiType, attempt, err, time.Since(attemptStart))
		o.applySessionLifecycleToAttempt(&attemptResult)
		return attemptResult
	}

	recoveryAttempted := false
	upstreamConn, dialResult := o.handler.wsForwarder.dialUpstream(ctx, upstreamURL, dialHeaders)
	if dialResult != nil {
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, dialResult, nil, time.Since(attemptStart))
		o.applySessionLifecycleToAttempt(&attemptResult)
		if !o.shouldRecoverUnauthorized(attemptResult) {
			return attemptResult
		}

		recoveryAttempted = true
		recoveredConn, recoveredAttempt, recovered := o.recoverUnauthorizedSameProvider(
			ctx,
			r,
			provider,
			upstreamURL,
			attempt,
			attemptStart,
		)
		if !recovered {
			return attemptResult
		}
		if recoveredConn == nil {
			recoveredAttempt.RecoveryAttempted = true
			return recoveredAttempt
		}
		upstreamConn = recoveredConn
	}
	defer func() {
		if upstreamConn != nil {
			_ = upstreamConn.CloseNow()
		}
	}()

	if err := o.ensureClientAccepted(w, r); err != nil {
		_ = upstreamConn.Close(websocket.StatusGoingAway, "client websocket upgrade rejected")
		upstreamConn = nil
		result := &WebSocketResult{
			HandshakeAccepted: true,
			Err:               err,
			TerminalCause:     model.TerminalClientUpgradeRejected,
			CommitSource:      model.CommitUnknown,
		}
		o.applySessionLifecycleToResult(result)
		return newWebSocketForwardAttemptResult(provider, attempt, result, err, time.Since(attemptStart))
	}

	observer := o.newAttemptObserver()
	replayed, replayErr := o.replayBufferedMessages(ctx, upstreamConn, observer)
	if replayErr != nil {
		result := &WebSocketResult{
			HandshakeAccepted: true,
			Err:               replayErr,
			TerminalCause:     model.TerminalUpstreamTransportError,
			CommitSource:      model.CommitUnknown,
		}
		o.applySessionLifecycleToResult(result)
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, result, replayErr, time.Since(attemptStart))
		attemptResult.ReplayFailed = true
		attemptResult.RecoveryAttempted = recoveryAttempted
		return attemptResult
	}

	relayResult := o.handler.wsForwarder.relay(ctx, o.clientConn, upstreamConn, webSocketRelayOptions{
		Observer:                          observer,
		OnFirstUpstreamMessage:            o.applyObservation,
		OnClientVisible:                   o.onClientVisible,
		PreWriteToClient:                  newAllowlistedProviderScopedSuppressDecision(o.replayBuffer),
		PreVisibleReplayBuffer:            o.replayBuffer,
		Lifecycle:                         o.lifecycle,
		PreserveClientOnSuppress:          true,
		SkipPreVisibleWindow:              replayed,
		PreserveClientOnPreVisibleFailure: o.suppressedAttempt != nil,
	})
	upstreamConn = nil

	result := relayResult.toWebSocketResult()
	result.HandshakeAccepted = true
	if observer != nil {
		mergeWebSocketObservation(result, observer.Snapshot())
	}
	if result.UpstreamError != nil {
		result.TerminalCause = model.TerminalUpstreamSemanticError
	}

	o.captureSuppressedAttempt(provider, relayResult)
	if result.ClientVisible {
		o.clearSuppressedAttempt()
	}

	attemptResult := newWebSocketForwardAttemptResult(provider, attempt, result, result.Err, time.Since(attemptStart))
	attemptResult.RecoveryAttempted = recoveryAttempted
	attemptResult.RecoverySucceeded = recoveryAttempted && attemptResult.clientAccepted()
	return attemptResult
}

func (o *WebSocketSessionOrchestrator) prepareProviderAttempt(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
) (string, http.Header, error) {
	baseURL, err := o.handler.validateWebSocketProviderReady(provider, o.apiType)
	if err != nil {
		return "", nil, err
	}

	upstreamPath := BuildUpstreamPath(r.URL.Path, o.apiType)
	upstreamURL := httpToWSURL(o.handler.buildFullURL(baseURL, upstreamPath, r.URL.RawQuery))

	dialHeaders, err := o.handler.prepareWebSocketDialHeaders(ctx, r, provider, o.apiType, o.globalAuthMode)
	if err != nil {
		return "", nil, &webSocketProviderConfigError{
			missingField: "credentials",
			err:          err,
		}
	}

	return upstreamURL, dialHeaders, nil
}

func (o *WebSocketSessionOrchestrator) shouldRecoverUnauthorized(attempt WebSocketAttemptResult) bool {
	return o.handler.auth != nil &&
		attempt.ForwardErr == nil &&
		attempt.Result != nil &&
		!attempt.Result.HandshakeAccepted &&
		attempt.Result.HandshakeStatusCode == http.StatusUnauthorized
}

func (o *WebSocketSessionOrchestrator) recoverUnauthorizedSameProvider(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
	upstreamURL string,
	attempt int,
	attemptStart time.Time,
) (*websocket.Conn, WebSocketAttemptResult, bool) {
	refreshed, err := o.handler.auth.RefreshProviderCredentials(ctx, provider)
	if !refreshed {
		return nil, WebSocketAttemptResult{}, false
	}
	if err != nil {
		o.handler.logger.Warn(
			"websocket provider credential refresh failed",
			zap.String("provider_id", provider.ID),
			zap.Error(err),
		)
		return nil, WebSocketAttemptResult{}, false
	}

	dialHeaders, err := o.handler.prepareWebSocketDialHeaders(ctx, r, provider, o.apiType, o.globalAuthMode)
	if err != nil {
		configAttempt := newWebSocketProviderConfigurationAttempt(provider, o.apiType, attempt, &webSocketProviderConfigError{
			missingField: "credentials",
			err:          err,
		}, time.Since(attemptStart))
		configAttempt.RecoveryAttempted = true
		o.applySessionLifecycleToAttempt(&configAttempt)
		return nil, configAttempt, true
	}

	upstreamConn, dialResult := o.handler.wsForwarder.dialUpstream(ctx, upstreamURL, dialHeaders)
	if dialResult == nil {
		return upstreamConn, WebSocketAttemptResult{RecoveryAttempted: true}, true
	}

	recoveredAttempt := newWebSocketForwardAttemptResult(provider, attempt, dialResult, nil, time.Since(attemptStart))
	recoveredAttempt.RecoveryAttempted = true
	o.applySessionLifecycleToAttempt(&recoveredAttempt)
	return nil, recoveredAttempt, true
}

func (o *WebSocketSessionOrchestrator) applySessionLifecycleToAttempt(attempt *WebSocketAttemptResult) {
	if attempt == nil {
		return
	}
	o.applySessionLifecycleToResult(attempt.Result)
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

func (o *WebSocketSessionOrchestrator) replayBufferedMessages(
	ctx context.Context,
	upstreamConn *websocket.Conn,
	observer WebSocketMessageObserver,
) (bool, error) {
	if o.suppressedAttempt == nil || o.replayBuffer == nil {
		return false, nil
	}

	snapshot := o.replayBuffer.Snapshot()
	if !snapshot.Enabled {
		return false, errors.New("pre-visible replay buffer disabled")
	}
	if len(snapshot.Messages) == 0 {
		// Suppression can happen before the client sends any replayable frame. In that
		// case the replacement provider should continue with a clean socket rather than
		// treating "nothing to replay" as a synthetic transport failure.
		return false, nil
	}

	for _, message := range snapshot.Messages {
		o.observeReplayClientMessage(observer, message.MessageType, message.Data)
		if err := upstreamConn.Write(ctx, message.MessageType, message.Data); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (o *WebSocketSessionOrchestrator) observeReplayClientMessage(
	observer WebSocketMessageObserver,
	messageType websocket.MessageType,
	data []byte,
) {
	switch tracked := observer.(type) {
	case *bytesTrackingObserver:
		if tracked.inner != nil {
			tracked.inner.ObserveClientMessage(messageType, data)
		}
	default:
		if observer != nil {
			observer.ObserveClientMessage(messageType, data)
		}
	}
}

func (o *WebSocketSessionOrchestrator) captureSuppressedAttempt(
	provider *model.Provider,
	relayResult *webSocketRelaySessionResult,
) {
	if relayResult == nil || relayResult.SuppressedUpstreamError == nil || provider == nil {
		return
	}

	messageType := relayResult.SuppressedMessageType
	if messageType == 0 {
		// Semantic failover only suppresses JSON application frames, so a missing
		// type means we are reconstructing legacy helper output rather than a real
		// binary payload.
		messageType = websocket.MessageText
	}

	payload := append([]byte(nil), relayResult.SuppressedMessageData...)
	if len(payload) == 0 && relayResult.SuppressedUpstreamError.Raw != "" {
		payload = []byte(relayResult.SuppressedUpstreamError.Raw)
	}

	o.suppressedAttempt = &webSocketSuppressedAttempt{
		provider:      provider,
		messageType:   messageType,
		payload:       payload,
		upstreamError: relayResult.SuppressedUpstreamError.Clone(),
	}
}

func (o *WebSocketSessionOrchestrator) clearSuppressedAttempt() {
	o.suppressedAttempt = nil
}

func (o *WebSocketSessionOrchestrator) shouldSwitchProvider(attempt WebSocketAttemptResult) bool {
	if attempt.Result == nil {
		return false
	}
	if !attempt.clientAccepted() {
		return attempt.shouldFailoverBeforeClientAccept()
	}
	if attempt.Result.ClientVisible || attempt.ReplayFailed {
		return false
	}
	if attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError {
		return o.suppressedAttempt != nil &&
			attempt.Result.UpstreamError != nil &&
			attempt.Result.UpstreamError.IsAllowlistedProviderScoped()
	}
	return false
}

func (o *WebSocketSessionOrchestrator) shouldFallbackToSuppressedPayload(attempt WebSocketAttemptResult) bool {
	if o.suppressedAttempt == nil || attempt.Result == nil || attempt.Result.ClientVisible {
		return false
	}
	if attempt.Result.TerminalCause == model.TerminalClientDisconnect ||
		attempt.Result.TerminalCause == model.TerminalInternalError {
		return false
	}
	if attempt.ReplayFailed {
		return true
	}
	return attempt.clientAccepted() && !o.shouldSwitchProvider(attempt)
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
