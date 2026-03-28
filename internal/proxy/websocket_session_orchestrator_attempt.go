package proxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"switch-a/internal/model"

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
		attemptResult := newWebSocketForwardAttemptResult(provider, attempt, result, replayErr, time.Since(attemptStart))
		attemptResult.ReplayFailed = true
		attemptResult.RecoveryAttempted = recoveryAttempted
		return attemptResult
	}

	initialClientReadCh := o.initialClientReadCh
	o.initialClientReadCh = nil
	relayResult := o.handler.wsForwarder.relay(ctx, o.clientConn, upstreamConn, webSocketRelayOptions{
		InitialClientReadCh:               initialClientReadCh,
		Observer:                          observer,
		OnFirstUpstreamMessage:            o.applyObservation,
		OnClientVisible:                   o.onClientVisible,
		PreWriteToClient:                  newAllowlistedProviderScopedSuppressDecision(o.replayBuffer),
		PreVisibleReplayBuffer:            o.replayBuffer,
		Lifecycle:                         o.lifecycle,
		PreserveClientOnSuppress:          true,
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
	if attempt.Result.ClientVisible || attempt.ReplayFailed {
		return false
	}
	if attempt.shouldFailoverBeforeClientVisible() {
		return true
	}
	if attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError {
		return o.suppressedAttempt != nil &&
			attempt.Result.UpstreamError != nil &&
			attempt.Result.UpstreamError.IsSwitchableProviderScoped()
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
