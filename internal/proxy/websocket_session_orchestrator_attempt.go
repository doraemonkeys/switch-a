package proxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// executeProviderAttempt keeps upstream dial, replay, and suppression handling
// in one place because they share the same pre-visible commitment boundary.
func (o *WebSocketSessionOrchestrator) executeProviderAttempt(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	provider *model.Provider,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
) WebSocketAttemptResult {
	attemptStart := time.Now()

	upstreamURL, dialHeaders, err := o.prepareProviderAttempt(ctx, r, provider)
	if err != nil {
		attemptResult := newWebSocketProviderConfigurationAttempt(provider, o.apiType, attempt, selectionMode, selectionMetadata, err, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
		o.applySessionLifecycleToAttempt(&attemptResult)
		return attemptResult
	}

	recoveryAttempted := false
	upstreamConn, dialResult := o.handler.wsForwarder.dialUpstream(ctx, upstreamURL, dialHeaders)
	if dialResult != nil {
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, dialResult, nil, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
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
			selectionMode,
			selectionMetadata,
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
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, result, err, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
		return attemptResult
	}

	observer := o.newAttemptObserver()
	replayedBytes, replayed, replayErr := o.replayBufferedMessages(ctx, upstreamConn, observer)
	if replayErr != nil {
		result := &WebSocketResult{
			HandshakeAccepted:     true,
			BytesClientToUpstream: replayedBytes,
			Err:                   replayErr,
			TerminalCause:         model.TerminalUpstreamTransportError,
			CommitSource:          model.CommitUnknown,
		}
		o.applySessionLifecycleToResult(result)
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, result, replayErr, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
		attemptResult.ReplayFailed = true
		attemptResult.RecoveryAttempted = recoveryAttempted
		return attemptResult
	}

	initialClientReadCh := o.initialClientReadCh
	o.initialClientReadCh = nil
	postVisibleFailover := selectionMode == model.SwitchModeFailover && o.lifecycle != nil && o.lifecycle.Snapshot().ClientVisible
	relayResult := o.handler.wsForwarder.relay(ctx, o.clientConn, upstreamConn, webSocketRelayOptions{
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
	result.HandshakeAccepted = true
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
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
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
		configAttempt := newWebSocketProviderConfigurationAttempt(provider, o.apiType, attempt, selectionMode, selectionMetadata, &webSocketProviderConfigError{
			missingField: "credentials",
			err:          err,
		}, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&configAttempt, selectionMode, selectionMetadata)
		configAttempt.RecoveryAttempted = true
		o.applySessionLifecycleToAttempt(&configAttempt)
		return nil, configAttempt, true
	}

	upstreamConn, dialResult := o.handler.wsForwarder.dialUpstream(ctx, upstreamURL, dialHeaders)
	if dialResult == nil {
		return upstreamConn, WebSocketAttemptResult{RecoveryAttempted: true}, true
	}

	recoveredAttempt := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, dialResult, nil, time.Since(attemptStart))
	o.stampAttemptSelectionContext(&recoveredAttempt, selectionMode, selectionMetadata)
	recoveredAttempt.RecoveryAttempted = true
	o.applySessionLifecycleToAttempt(&recoveredAttempt)
	return nil, recoveredAttempt, true
}

func (o *WebSocketSessionOrchestrator) stampAttemptSelectionContext(
	attempt *WebSocketAttemptResult,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
) {
	if attempt == nil {
		return
	}
	attempt.SelectionMode = selectionMode
	attempt.SelectionMetadata = selectionMetadata
	attempt.ProviderAttempt = 1
	attempt.ProviderSwitchCount = o.switchTracker.providerSwitchCount()
}

func (o *WebSocketSessionOrchestrator) replayBufferedMessages(
	ctx context.Context,
	upstreamConn *websocket.Conn,
	observer WebSocketMessageObserver,
) (int64, bool, error) {
	if o.replayBuffer == nil {
		return 0, false, nil
	}

	snapshot := o.replayBuffer.Snapshot()
	if !snapshot.Enabled {
		if o.lifecycle != nil && o.lifecycle.Snapshot().ClientVisible {
			// Once the session is already visible, a disabled pre-visible replay buffer
			// is expected rather than fatal. Post-visible failover reuses the live
			// downstream socket without trying to resurrect the pre-visible window.
			return 0, false, nil
		}
		return 0, false, errors.New("pre-visible replay buffer disabled")
	}
	if len(snapshot.Messages) == 0 {
		// Suppression can happen before the client sends any replayable frame. In that
		// case the replacement provider should continue with a clean socket rather than
		// treating "nothing to replay" as a synthetic transport failure.
		return 0, false, nil
	}

	var replayedBytes int64
	for _, message := range snapshot.Messages {
		o.observeReplayClientMessage(observer, message.MessageType, message.Data)
		if err := upstreamConn.Write(ctx, message.MessageType, message.Data); err != nil {
			return replayedBytes, true, err
		}
		replayedBytes += int64(len(message.Data))
	}
	return replayedBytes, true, nil
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

func (o *WebSocketSessionOrchestrator) clearSuppressedAttempt() { o.suppressedAttempt = nil }

func (o *WebSocketSessionOrchestrator) shouldSwitchProvider(attempt WebSocketAttemptResult) bool {
	if attempt.Result == nil {
		return false
	}
	if attempt.ReplayFailed {
		return false
	}
	if attempt.shouldReplaceBeforeClientVisible() {
		return true
	}
	if attempt.Result.ClientVisible {
		return o.shouldFailoverAfterClientVisible(attempt)
	}
	if attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError {
		return o.suppressedAttempt != nil &&
			attempt.Result.UpstreamError != nil &&
			attempt.Result.UpstreamError.IsSwitchableProviderScoped()
	}
	return false
}

func (o *WebSocketSessionOrchestrator) shouldFailoverAfterClientVisible(attempt WebSocketAttemptResult) bool {
	if o == nil || o.suppressedAttempt == nil || attempt.Result == nil {
		return false
	}
	if attempt.Result.UpstreamError == nil {
		return false
	}
	if !attempt.Result.UpstreamError.IsSwitchableProviderScoped() {
		return false
	}
	return o.switchTracker.continuityContext != nil
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
