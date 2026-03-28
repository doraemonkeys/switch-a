package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"github.com/coder/websocket"
)

func (o *WebSocketSessionOrchestrator) finalSessionFromLastAttempt(ctx context.Context) *WebSocketSessionResult {
	if o.suppressedAttempt != nil {
		return o.sessionFromSuppressedPayload(ctx)
	}
	if len(o.attempts) == 0 {
		session := newWebSocketSelectionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			http.StatusServiceUnavailable,
			model.TerminalProviderUnavailable,
			ErrCodeProviderUnavailable,
			fmt.Sprintf("No available provider for api_type: %s", o.apiType),
			internal.ErrNoProvider,
		)
		return o.finalizeSelectionFailureSession(session)
	}
	return o.sessionFromAttempt(o.attempts[len(o.attempts)-1])
}

func (o *WebSocketSessionOrchestrator) finalizeSelectionFailureSession(session *WebSocketSessionResult) *WebSocketSessionResult {
	if o == nil || session == nil {
		return session
	}
	if session.ProbeOutcome == webSocketSelectionProbeOutcomeUnknown {
		session.ProbeOutcome = o.probeOutcome
	}
	o.applySessionLifecycleToResult(session.FinalResult)
	if session.FinalResult != nil {
		session.ClientAccepted = session.FinalResult.ClientAccepted
	}
	if err := o.emitTerminalGatewayErrorIfNeeded(
		session.FinalResult,
		session.GatewayStatusCode,
		session.GatewayErrorCode,
		session.GatewayMessage,
	); err != nil && session.FinalErr == nil {
		session.FinalErr = err
	}
	return session
}

//nolint:nestif // The fallback payload handoff is intentionally linear because it mirrors the terminal recovery contract.
func (o *WebSocketSessionOrchestrator) sessionFromSuppressedPayload(ctx context.Context) *WebSocketSessionResult {
	finalProvider := (*model.Provider)(nil)
	finalErr := error(nil)
	result := &WebSocketResult{CommitSource: model.CommitUnknown}
	if o.suppressedAttempt != nil {
		finalProvider = o.suppressedAttempt.provider
		result = &WebSocketResult{
			HandshakeAccepted: true,
			ClientAccepted:    true,
			TerminalCause:     model.TerminalUpstreamSemanticError,
			CommitSource:      model.CommitUnknown,
			UpstreamError:     o.suppressedAttempt.upstreamError.Clone(),
		}
		o.applySessionLifecycleToResult(result)

		if o.clientConn != nil {
			writeCtx, cancel := context.WithTimeout(context.Background(), webSocketFallbackWriteTimeout)
			defer cancel()
			if err := o.clientConn.Write(writeCtx, o.suppressedAttempt.messageType, o.suppressedAttempt.payload); err != nil {
				result.Err = err
				result.TerminalCause = classifyRelayTerminalCause(err, webSocketPeerClient)
				finalErr = err
			} else {
				result.BytesUpstreamToClient = int64(len(o.suppressedAttempt.payload))
				result.ClientVisible = true
				o.lifecycle.MarkClientVisible()
				if o.replayBuffer != nil {
					snapshot := o.replayBuffer.Snapshot()
					result.BytesClientToUpstream = int64(snapshot.TotalBytes)
					o.replayBuffer.Disable()
				}
				if o.onClientVisible != nil {
					o.onClientVisible(webSocketVisibleWriteContext{
						MessageType: o.suppressedAttempt.messageType,
						Data:        append([]byte(nil), o.suppressedAttempt.payload...),
						Observation: WebSocketObservation{UpstreamError: o.suppressedAttempt.upstreamError.Clone()},
					})
				}
				// Once the suppressed payload becomes the terminal fallback there is no
				// upstream left to own the session, so the gateway must finish the socket
				// after delivery instead of leaving clients parked on a dead provider.
				closeTerminalSuppressedClientConn(o.clientConn)
				o.clientConn = nil
			}
			if o.clientConn != nil {
				_ = o.clientConn.Close(websocket.StatusNormalClosure, "")
				o.clientConn = nil
			}
		}
	}
	o.clearSuppressedAttempt()

	session := &WebSocketSessionResult{
		RequestID:      o.requestID,
		FinalProvider:  finalProvider,
		FinalResult:    result,
		FinalErr:       finalErr,
		Attempts:       append([]WebSocketAttemptResult(nil), o.attempts...),
		IsSticky:       o.isSticky,
		ClientAccepted: result.ClientAccepted,
		ProbeOutcome:   o.probeOutcome,
	}
	if result.Model != "" {
		session.ResolvedModel = result.Model
	}
	return session
}

func (o *WebSocketSessionOrchestrator) sessionFromAttempt(attempt WebSocketAttemptResult) *WebSocketSessionResult {
	session := &WebSocketSessionResult{
		RequestID:         o.requestID,
		FinalProvider:     attempt.Provider,
		FinalResult:       attempt.Result.Clone(),
		FinalErr:          attempt.terminalErr(),
		Attempts:          append([]WebSocketAttemptResult(nil), o.attempts...),
		IsSticky:          o.isSticky,
		ClientAccepted:    attempt.clientAccepted(),
		ProbeOutcome:      o.probeOutcome,
		GatewayStatusCode: attempt.GatewayStatusCode,
		GatewayErrorCode:  attempt.GatewayErrorCode,
		GatewayMessage:    attempt.GatewayMessage,
	}
	if attempt.Result != nil {
		session.ResolvedModel = attempt.Result.Model
	}
	if err := o.emitTerminalGatewayErrorIfNeeded(
		session.FinalResult,
		session.GatewayStatusCode,
		session.GatewayErrorCode,
		session.GatewayMessage,
	); err != nil && session.FinalErr == nil {
		session.FinalErr = err
	}
	return session
}

func (o *WebSocketSessionOrchestrator) emitTerminalGatewayErrorIfNeeded(
	result *WebSocketResult,
	statusCode int,
	errorCode,
	message string,
) error {
	if o == nil || o.clientConn == nil || result == nil || result.ClientVisible || statusCode <= 0 {
		return nil
	}

	payload := marshalWebSocketGatewayError(statusCode, errorCode, message)
	writeCtx, cancel := context.WithTimeout(context.Background(), webSocketFallbackWriteTimeout)
	defer cancel()

	if err := o.clientConn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		_ = o.clientConn.Close(websocket.StatusInternalError, "")
		o.clientConn = nil
		return err
	}

	closeTerminalSuppressedClientConn(o.clientConn)
	o.clientConn = nil
	return nil
}

func newWebSocketSelectionFailureSession(
	requestID string,
	isSticky bool,
	attempts []WebSocketAttemptResult,
	statusCode int,
	terminalCause model.TerminalCause,
	errorCode, message string,
	err error,
) *WebSocketSessionResult {
	return &WebSocketSessionResult{
		RequestID:         requestID,
		FinalResult:       newWebSocketGatewayFailureResult(statusCode, terminalCause, err),
		FinalErr:          err,
		Attempts:          append([]WebSocketAttemptResult(nil), attempts...),
		IsSticky:          isSticky,
		ProbeOutcome:      webSocketSelectionProbeOutcomeUnknown,
		GatewayStatusCode: statusCode,
		GatewayErrorCode:  errorCode,
		GatewayMessage:    message,
	}
}

func newWebSocketForwardAttemptResult(
	provider *model.Provider,
	attempt int,
	result *WebSocketResult,
	forwardErr error,
	latency time.Duration,
) WebSocketAttemptResult {
	attemptResult := WebSocketAttemptResult{
		Provider:   provider,
		Attempt:    attempt,
		Result:     result,
		ForwardErr: forwardErr,
		LatencyMs:  latency.Milliseconds(),
		CreatedAt:  time.Now(),
	}
	if forwardErr == nil && result != nil && !result.HandshakeAccepted {
		attemptResult.GatewayStatusCode, attemptResult.GatewayErrorCode, attemptResult.GatewayMessage = websocketGatewayFailure(result)
	}
	return attemptResult
}

func newWebSocketProviderConfigurationAttempt(
	provider *model.Provider,
	apiType string,
	attempt int,
	err error,
	latency time.Duration,
) WebSocketAttemptResult {
	missingField := "configuration"
	var configErr *webSocketProviderConfigError
	if errors.As(err, &configErr) && configErr.missingField != "" {
		missingField = configErr.missingField
	}

	message := fmt.Sprintf(
		"Provider %q is not ready for websocket %q: %s",
		provider.ID,
		apiType,
		missingField,
	)
	return WebSocketAttemptResult{
		Provider:          provider,
		Attempt:           attempt,
		Result:            newWebSocketGatewayFailureResult(http.StatusBadGateway, model.TerminalProviderConfigurationError, err),
		ForwardErr:        err,
		LatencyMs:         latency.Milliseconds(),
		CreatedAt:         time.Now(),
		GatewayStatusCode: http.StatusBadGateway,
		GatewayErrorCode:  ErrCodeWebSocketUpgrade,
		GatewayMessage:    message,
	}
}

func websocketSwitchReason(attempt WebSocketAttemptResult) string {
	if attempt.Result != nil &&
		!attempt.Result.ClientVisible &&
		attempt.Result.UpstreamError != nil &&
		attempt.Result.UpstreamError.IsAllowlistedProviderScoped() {
		return model.RequestAttemptSwitchReasonProviderScopedSemanticError
	}
	if failureDisposition := classifyWebSocketHandshakeFailure(attempt.Result); failureDisposition.forcesProviderSwitch() {
		return failureDisposition.switchReason
	}
	if attempt.Result != nil && attempt.Result.TerminalCause != "" {
		return string(attempt.Result.TerminalCause)
	}
	return ""
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
