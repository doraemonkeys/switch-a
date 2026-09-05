package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/websocketprotocol"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// This module owns the one-way transition from an accepted upstream attempt to
// client-visible traffic. Keeping handshake, replay, and write-confirmed state
// commits together prevents the policy loop from acquiring a second visibility
// state machine.

type webSocketCodexCloseError struct {
	code   websocket.StatusCode
	reason string
	cause  error
}

func (e *webSocketCodexCloseError) Error() string { return e.cause.Error() }
func (e *webSocketCodexCloseError) Unwrap() error { return e.cause }
func (e *webSocketCodexCloseError) As(target any) bool {
	closeError, ok := target.(*websocket.CloseError)
	if !ok {
		return false
	}
	*closeError = websocket.CloseError{Code: e.code, Reason: e.reason}
	return true
}

func newWebSocketCodexCloseError(err error) error {
	if err == nil {
		err = errors.New("websocket state boundary rejected")
	}
	decision := codexWebSocketRecoveryDecision(err, codexrecovery.PhaseWebSocketAccepted)
	return &webSocketCodexCloseError{
		code: decision.WebSocketCloseCode(), reason: string(decision.ErrorCode()), cause: err,
	}
}

type webSocketDialCaptureCompletion struct {
	exchange DialExchange
	outcome  requestcapture.Outcome
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
	injectedCredential string,
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

	negotiation, failure := o.prepareAcceptedSubprotocol(
		provider, dialExchange, attempt, selectionMode, selectionMetadata, attemptStart, injectedCredential,
	)
	if failure != nil {
		upstreamConn = nil
		return *failure
	}

	serverHeaderPermit, boundaryFailure := o.prepareAcceptedCodexHandshake(
		ctx, w, provider, dialExchange, attempt, selectionMode, selectionMetadata, attemptStart, injectedCredential,
	)
	if boundaryFailure != nil {
		return *boundaryFailure
	}

	if err := o.ensureClientAccepted(w, r, negotiation); err != nil {
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
		attemptResult.injectedCredential = injectedCredential
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
		return attemptResult
	}
	if err := o.commitAcceptedCodexHandshake(ctx, serverHeaderPermit); err != nil {
		return o.newCodexBoundaryAttempt(provider, dialExchange, attempt, selectionMode, selectionMetadata, attemptStart, err, injectedCredential)
	}
	if o.codexOperation != nil {
		defer o.codexOperation.CloseConnection()
	}

	observer, captureOptions := o.newAttemptRelayContext(dialExchange)
	captureOptions.PreWriteToUpstream = o.codexClientPreWrite(ctx)
	replayedBytes, replayed, replayErr := o.replayBufferedMessages(ctx, upstreamConn, observer, captureOptions)
	if replayErr != nil {
		attemptResult, outcome := o.newReplayFailureAttempt(
			ctx, provider, dialExchange, attempt, selectionMode, selectionMetadata,
			attemptStart, recoveryAttempted, injectedCredential, replayedBytes, replayErr,
		)
		if outcome != nil {
			dialCaptureOutcome = *outcome
		}
		return attemptResult
	}

	relayResult := o.handler.wsForwarder.relay(ctx, o.clientConn, upstreamConn, webSocketRelayOptions{
		GatewayCapture: captureOptions.GatewayCapture,
		Capture:        captureOptions.Capture,
		CaptureMode:    captureOptions.CaptureMode,

		CredentialEvidence:                captureOptions.CredentialEvidence,
		ClientReadHandoff:                 o.sessionClientReadHandoff(),
		Observer:                          observer,
		OnFirstUpstreamMessage:            o.applyObservation,
		OnClientVisible:                   o.onClientVisible,
		PreWriteToClient:                  o.composeUpstreamPreWrite(ctx, newAllowlistedProviderScopedSuppressDecision(o.replayBuffer)),
		PreWriteToUpstream:                o.codexClientPreWrite(ctx),
		PreVisibleReplayBuffer:            o.replayBuffer,
		Lifecycle:                         o.lifecycle,
		PreserveClientOnSuppress:          true,
		SkipPreVisibleWindow:              replayed && o.suppressedAttempt != nil,
		PreserveClientOnPreVisibleFailure: o.suppressedAttempt != nil,
	})
	upstreamConn = nil

	result := relayResult.toWebSocketResult()
	dialExchange.applyHandshake(result)
	result.BytesClientToUpstream += replayedBytes
	result.ReplayStatus = o.replayBuffer.Status()
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
	attemptResult.injectedCredential = injectedCredential
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

func (o *WebSocketSessionOrchestrator) prepareAcceptedSubprotocol(
	provider *model.Provider,
	dialExchange DialExchange,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
	injectedCredential string,
) (websocketprotocol.Negotiation, *WebSocketAttemptResult) {
	negotiation, err := o.acceptedSubprotocolNegotiation(dialExchange)
	if err != nil {
		closeWebSocketSubprotocolViolation(dialExchange.Conn)
		attemptResult := o.newSubprotocolViolationAttempt(
			provider, dialExchange, attempt, selectionMode, selectionMetadata, attemptStart, err, injectedCredential,
		)
		return websocketprotocol.Negotiation{}, &attemptResult
	}
	return negotiation, nil
}

func (o *WebSocketSessionOrchestrator) prepareAcceptedCodexHandshake(
	ctx context.Context,
	w http.ResponseWriter,
	provider *model.Provider,
	dialExchange DialExchange,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
	injectedCredential string,
) (*codexws.Permit, *WebSocketAttemptResult) {
	if o.codexOperation == nil {
		return nil, nil
	}
	var permit *codexws.Permit
	if o.clientConn == nil {
		var projected http.Header
		var err error
		permit, projected, err = o.codexOperation.PrepareServerHeaders(ctx, dialExchange.HandshakeHeaders)
		if err != nil {
			failure := o.newCodexBoundaryAttempt(
				provider, dialExchange, attempt, selectionMode, selectionMetadata, attemptStart, err, injectedCredential,
			)
			return nil, &failure
		}
		projectWebSocketHandshakeHeaders(w.Header(), projected)
	}
	return permit, nil
}

func projectWebSocketHandshakeHeaders(destination, projected http.Header) {
	for name, values := range projected {
		destination.Del(name)
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func (o *WebSocketSessionOrchestrator) commitAcceptedCodexHandshake(
	ctx context.Context,
	serverHeaderPermit *codexws.Permit,
) error {
	if serverHeaderPermit != nil {
		if err := serverHeaderPermit.Commit(ctx); err != nil {
			_ = o.clientConn.Close(websocketCloseStatusForCodexFailure(err), "websocket state commit failed")
			return err
		}
		if serverHeaderPermit.PinsRouteTarget() {
			if err := o.codexOperation.CommitVisibility(ctx); err != nil {
				_ = o.clientConn.Close(websocketCloseStatusForCodexFailure(err), "websocket visibility state failed")
				return err
			}
		}
	}
	if o.codexOperation == nil {
		return nil
	}
	if err := o.codexOperation.OpenConnection(); err != nil {
		_ = o.clientConn.Close(websocketCloseStatusForCodexFailure(err), "websocket connection state failed")
		return err
	}
	return nil
}

func (o *WebSocketSessionOrchestrator) newReplayFailureAttempt(
	ctx context.Context,
	provider *model.Provider,
	dialExchange DialExchange,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
	recoveryAttempted bool,
	injectedCredential string,
	replayedBytes int64,
	replayErr error,
) (WebSocketAttemptResult, *requestcapture.Outcome) {
	result := &WebSocketResult{
		BytesClientToUpstream: replayedBytes,
		Err:                   replayErr, TerminalCause: model.TerminalUpstreamTransportError, CommitSource: model.CommitUnknown,
	}
	dialExchange.applyHandshake(result)
	o.applySessionLifecycleToResult(result)

	var outcome *requestcapture.Outcome
	if dialExchange.captureMode.Participates() {
		reason, failure := webSocketReplayWrite(contextError(ctx), replayErr)
		captured := requestcapture.Outcome{
			SourceCompletion: requestcapture.SourceCompletionPartial, TerminationReason: reason,
			Failure: failure, CredentialEvidence: dialExchange.credentialEvidence,
		}
		outcome = &captured
	}
	attemptResult := newWebSocketForwardAttemptResult(
		provider, attempt, selectionMode, selectionMetadata, result, replayErr, time.Since(attemptStart),
	)
	attemptResult.injectedCredential = injectedCredential
	o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
	attemptResult.ReplayFailed = true
	attemptResult.RecoveryAttempted = recoveryAttempted
	return attemptResult, outcome
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

func (o *WebSocketSessionOrchestrator) codexClientPreWrite(
	ctx context.Context,
) func(webSocketPreWriteContext) webSocketPreWriteDecision {
	if o == nil || o.codexOperation == nil {
		return nil
	}
	return func(write webSocketPreWriteContext) webSocketPreWriteDecision {
		frame := o.codexOperation.ClassifyClientFrame(ctx, write.MessageType == websocket.MessageText, write.Data)
		o.logCodexClientFramePermit(frame)
		return o.codexClientFrameDecision(ctx, frame, true)
	}
}

func (o *WebSocketSessionOrchestrator) logCodexClientFramePermit(frame *codexws.ClientFramePermit) {
	if o == nil || frame == nil || o.handler == nil || o.handler.logger == nil {
		return
	}
	trace := frame.Trace()
	if trace.Kind == codexws.ClientFrameOpaque {
		return
	}
	fields := []zap.Field{
		zap.String("request_id", o.requestID),
		zap.String("session_id", o.requestID),
		zap.String("operation_id", o.requestID),
		zap.String("frame_kind", string(trace.Kind)),
		zap.String("event_type", trace.EventType),
		zap.String("decision", string(frame.Disposition())),
		zap.Bool("replay_eligible", frame.ReplayEligible()),
		zap.Bool("replacement_eligible", frame.ReplacementEligible()),
		zap.Bool("current_connection_required", frame.CurrentConnectionRequired()),
	}
	if frame.Disposition() == codexws.ClientFrameReject {
		recovery := codexWebSocketRecoveryDecision(frame.Rejection(), codexrecovery.PhaseWebSocketAccepted)
		fields = append(fields,
			zap.String("recovery_condition", string(recovery.Condition())),
			zap.String("error_code", string(recovery.ErrorCode())),
			zap.Int("close_code", int(recovery.WebSocketCloseCode())),
		)
		o.handler.logger.Warn("websocket.codex_client_frame_decision", fields...)
		return
	}
	o.handler.logger.Debug("websocket.codex_client_frame_decision", fields...)
}

func (o *WebSocketSessionOrchestrator) codexClientFrameDecision(
	ctx context.Context,
	frame *codexws.ClientFramePermit,
	prepareDelivery bool,
) webSocketPreWriteDecision {
	if frame == nil {
		return replayableClientFrameDecision()
	}
	if frame.Disposition() == codexws.ClientFrameReject {
		return codexRejectedWrite(frame.Rejection())
	}
	trace := frame.Trace()
	decision := webSocketPreWriteDecision{
		Action:              webSocketPreWriteActionForward,
		ReplayEligible:      frame.ReplayEligible(),
		ReplacementEligible: frame.ReplacementEligible(),
		CurrentConnection:   frame.CurrentConnectionRequired(),
		TraceContext: webSocketClientFrameTrace{
			Kind: string(trace.Kind), EventType: trace.EventType, Decision: string(trace.Decision),
		},
	}
	decision.PrepareReplay = func() webSocketPreWriteDecision {
		return o.codexClientFrameDecision(ctx, frame, true)
	}
	if !prepareDelivery {
		return decision
	}
	permit, err := frame.PrepareDelivery(ctx)
	if err != nil {
		return codexRejectedWrite(err)
	}
	if permit != nil {
		decision.OnWriteConfirmed = func() error {
			if err := permit.Commit(ctx); err != nil {
				return newWebSocketCodexCloseError(err)
			}
			return nil
		}
	}
	return decision
}

func (o *WebSocketSessionOrchestrator) composeUpstreamPreWrite(
	ctx context.Context,
	semantic func(webSocketPreWriteContext) webSocketPreWriteDecision,
) func(webSocketPreWriteContext) webSocketPreWriteDecision {
	if o == nil || o.codexOperation == nil {
		return semantic
	}
	return func(write webSocketPreWriteContext) webSocketPreWriteDecision {
		if semantic != nil {
			if decision := semantic(write); decision.Action != webSocketPreWriteActionForward {
				return decision
			}
		}
		permit, err := o.codexOperation.PrepareServerFrame(ctx, write.MessageType == websocket.MessageText, write.Data)
		if err != nil {
			return codexRejectedWrite(err)
		}
		decision := webSocketPreWriteDecision{
			Action: webSocketPreWriteActionForward, ReplayEligible: true, ReplacementEligible: true,
		}
		decision.OnWriteConfirmed = func() error {
			if permit != nil {
				if err := permit.Commit(ctx); err != nil {
					return newWebSocketCodexCloseError(err)
				}
			}
			if err := o.codexOperation.CommitVisibility(ctx); err != nil {
				return newWebSocketCodexCloseError(err)
			}
			return nil
		}
		return decision
	}
}

func codexRejectedWrite(err error) webSocketPreWriteDecision {
	disposition := requestcapture.MessageDispositionProtocolRejected
	switch codexws.Classify(err) {
	case codexws.FailureIdentity:
		disposition = requestcapture.MessageDispositionIdentityRejected
	case codexws.FailureStorage:
		disposition = requestcapture.MessageDispositionStorageRejected
	}
	return webSocketPreWriteDecision{
		Action: webSocketPreWriteActionReject, Err: newWebSocketCodexCloseError(err),
		RejectionDisposition: disposition,
	}
}
