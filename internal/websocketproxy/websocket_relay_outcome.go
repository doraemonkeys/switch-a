package websocketproxy

import (
	"context"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

func finishWebSocketDialCapture(exchange DialExchange, outcome requestcapture.Outcome) {
	if !exchange.captureMode.Participates() {
		return
	}
	if exchange.captureMode.CapturesPayload() {
		outcome.CredentialEvidence.Merge(exchange.credentialEvidence)
		outcome.CredentialEvidence.Seal()
	}
	exchange.capture.Finish(outcome)
}

func webSocketDialFailureReason(
	exchange DialExchange,
	providerWillSwitch bool,
) requestcapture.TerminationReason {
	if providerWillSwitch && exchange.HandshakeStatusCode > 0 && exchange.HandshakeStatusCode != http.StatusSwitchingProtocols {
		return requestcapture.TerminationReasonStatusFailoverDrain
	}
	return requestcapture.TerminationReasonTransportError
}

func webSocketDialFailureSourceCompletion(exchange DialExchange) requestcapture.SourceCompletion {
	if exchange.HandshakeStatusCode > 0 &&
		sourceEndpointComplete(
			exchange.ObservedFailureBodyBytes,
			exchange.HandshakeContentLength,
			exchange.FailureBodyReachedEOF && !exchange.FailureBodyLimitReached,
			exchange.FailureBodyReadErr != nil,
		) {
		return requestcapture.SourceCompletionComplete
	}
	return requestcapture.SourceCompletionPartial
}

func webSocketDialFailureObservation(exchange DialExchange) requestcapture.FailureObservation {
	return webSocketHandshake(
		exchange.HandshakeStatusCode,
		exchange.Err,
		exchange.FailureBodyReadErr,
	)
}

func webSocketDialFailureCaptureOutcome(
	ctx context.Context,
	exchange DialExchange,
	fallback requestcapture.TerminationReason,
) requestcapture.Outcome {
	return requestcapture.Outcome{
		SourceCompletion:  webSocketDialFailureSourceCompletion(exchange),
		TerminationReason: webSocketContextCaptureReason(ctx, exchange.Err, fallback),
		Failure:           webSocketDialFailureObservation(exchange),
	}
}

func webSocketContextCaptureReason(
	ctx context.Context,
	err error,
	fallback requestcapture.TerminationReason,
) requestcapture.TerminationReason {
	if contextClass, ok := ContextClass(contextError(ctx)); ok {
		switch contextClass {
		case requestcapture.FailureClassTimeout:
			return requestcapture.TerminationReasonTimeout
		case requestcapture.FailureClassCanceled:
			return requestcapture.TerminationReasonClientDisconnect
		}
	}
	switch errorClass, ok := ContextClass(err); {
	case ok && errorClass == requestcapture.FailureClassTimeout:
		return requestcapture.TerminationReasonTimeout
	case ok && errorClass == requestcapture.FailureClassCanceled:
		return requestcapture.TerminationReasonCanceled
	default:
		return fallback
	}
}

// Relay outcome shaping lives beside the relay state model so transport loops
// can focus on byte movement while lifecycle projection stays easy to test.
func newWebSocketRelaySessionResultFromOutcome(
	outcome webSocketRelayOutcome,
	fallbackCommit *webSocketCommitState,
	lifecycle *webSocketLifecycleState,
	bytesClientToUpstream int64,
	bytesUpstreamToClient int64,
) *webSocketRelaySessionResult {
	sessionCommitted := false
	commitSource := model.CommitUnknown
	if fallbackCommit != nil {
		sessionCommitted, commitSource = fallbackCommit.Snapshot()
	}
	lifecycleSnapshot := webSocketLifecycleSnapshot{}
	if lifecycle != nil {
		lifecycleSnapshot = lifecycle.Snapshot()
	}
	return &webSocketRelaySessionResult{
		Disposition:           webSocketRelayDispositionCompleted,
		SessionCommitted:      sessionCommitted,
		TerminalCause:         outcome.terminalCause,
		CommitSource:          commitSource,
		CloseCode:             outcome.closeCode,
		BytesClientToUpstream: bytesClientToUpstream,
		BytesUpstreamToClient: bytesUpstreamToClient,
		Err:                   outcome.err,
		ClientAccepted:        lifecycleSnapshot.ClientAccepted,
		ClientVisible:         lifecycleSnapshot.ClientVisible,
		ObservedCloseError:    outcome.observedCloseError,
		FailurePeer:           outcome.failurePeer,
		FailureOperation:      outcome.failureOperation,
	}
}

func newSuppressedPreVisibleRelayResult(
	fallbackCommit *webSocketCommitState,
	lifecycleSnapshot webSocketLifecycleSnapshot,
	bytesClientToUpstream int64,
	decision webSocketPreWriteDecision,
) *webSocketRelaySessionResult {
	sessionCommitted, commitSource := fallbackCommit.Snapshot()
	return &webSocketRelaySessionResult{
		Disposition:             webSocketRelayDispositionSuppressedUpstreamError,
		SessionCommitted:        sessionCommitted,
		TerminalCause:           model.TerminalUpstreamSemanticError,
		CommitSource:            commitSource,
		BytesClientToUpstream:   bytesClientToUpstream,
		BytesUpstreamToClient:   0,
		ClientAccepted:          lifecycleSnapshot.ClientAccepted,
		ClientVisible:           lifecycleSnapshot.ClientVisible,
		SuppressedUpstreamError: decision.SuppressedUpstreamError,
		SuppressedMessageType:   decision.SuppressedMessageType,
		SuppressedMessageData:   append([]byte(nil), decision.SuppressedMessageData...),
	}
}

func (r *webSocketRelaySessionResult) toWebSocketResult() *WebSocketResult {
	if r == nil {
		return &WebSocketResult{CommitSource: model.CommitUnknown}
	}
	return &WebSocketResult{
		healthOutcomePublished:  r.healthOutcomePublished,
		accountRecoveryNotified: r.accountRecoveryNotified,
		ClientAccepted:          r.ClientAccepted,
		ClientVisible:           r.ClientVisible,
		SessionCommitted:        r.SessionCommitted,
		TerminalCause:           r.TerminalCause,
		CommitSource:            r.CommitSource,
		CloseCode:               r.CloseCode,
		BytesClientToUpstream:   r.BytesClientToUpstream,
		BytesUpstreamToClient:   r.BytesUpstreamToClient,
		Err:                     r.Err,
		UpstreamError:           r.SuppressedUpstreamError,
		// The transport observation is surfaced here so evidence derivation
		// (session + attempt) has a single source of truth. Suppressed-payload
		// relays intentionally pass through nil values — those paths report
		// semantic errors, not transport facts.
		TransportObservation: WebSocketTransportObservation{
			CloseError:  r.ObservedCloseError,
			FailurePeer: r.FailurePeer,
		},
	}
}

func shouldPreserveClientOnPreVisibleFailure(
	options webSocketRelayOptions,
	lifecycleSnapshot webSocketLifecycleSnapshot,
	outcome webSocketRelayOutcome,
) bool {
	if !options.PreserveClientOnPreVisibleFailure || lifecycleSnapshot.ClientVisible {
		return false
	}

	switch outcome.terminalCause {
	case model.TerminalUpstreamTransportError, model.TerminalCleanClose:
		return true
	default:
		return false
	}
}

func firstSuppressedUpstreamError(results ...webSocketRelayResult) *WebSocketUpstreamError {
	for _, result := range results {
		if disguiseFailure(result.err) != nil {
			return nil
		}
	}
	for _, result := range results {
		if result.suppressedUpstreamError != nil {
			return result.suppressedUpstreamError.Clone()
		}
	}
	return nil
}

func webSocketCaptureCloseObservation(
	relay *webSocketRelaySessionResult,
) *requestcapture.WebSocketCloseObservation {
	if relay == nil || relay.ObservedCloseError == nil {
		return nil
	}

	var direction requestcapture.MessageDirection
	switch relay.FailurePeer {
	case webSocketPeerClient:
		direction = requestcapture.MessageDirectionClientToUpstream
	case webSocketPeerUpstream:
		direction = requestcapture.MessageDirectionUpstreamToClient
	default:
		return nil
	}
	return &requestcapture.WebSocketCloseObservation{
		Direction: direction,
		Code:      int(relay.ObservedCloseError.Code),
		Reason:    relay.ObservedCloseError.Reason,
		Clean:     isCleanWebSocketCloseCode(relay.ObservedCloseError.Code),
	}
}

func webSocketRelayCaptureOutcome(
	ctx context.Context,
	relay *webSocketRelaySessionResult,
	result *WebSocketResult,
) requestcapture.Outcome {
	outcome := requestcapture.Outcome{
		SourceCompletion:  requestcapture.SourceCompletionPartial,
		TerminationReason: requestcapture.TerminationReasonWebSocketRelayError,
	}
	if result == nil {
		return outcome
	}
	outcome.WebSocketClose = webSocketCaptureCloseObservation(relay)
	if result.TerminalCause == model.TerminalCleanClose ||
		(outcome.WebSocketClose != nil && outcome.WebSocketClose.Clean) {
		// A physical clean close is terminal wire evidence. A parent cancellation
		// observed while the sibling relay goroutine unwinds must not rewrite it as
		// an incomplete timeout/cancel outcome.
		outcome.SourceCompletion = requestcapture.SourceCompletionComplete
		outcome.TerminationReason = requestcapture.TerminationReasonWebSocketClose
		return outcome
	}
	outcome.Failure = webSocketRelayFailureObservation(relay, result)
	if reason := webSocketContextCaptureReason(ctx, result.Err, ""); reason != "" {
		outcome.TerminationReason = reason
		return outcome
	}

	switch result.TerminalCause {
	case model.TerminalClientDisconnect, model.TerminalClientUpgradeRejected:
		outcome.TerminationReason = requestcapture.TerminationReasonClientDisconnect
	case model.TerminalUpstreamTransportError:
		switch {
		case relay == nil:
			outcome.TerminationReason = requestcapture.TerminationReasonWebSocketRelayError
		case relay.FailureOperation == webSocketRelayFailureOperationRead:
			outcome.TerminationReason = requestcapture.TerminationReasonReadError
		case relay.FailureOperation == webSocketRelayFailureOperationWrite:
			outcome.TerminationReason = requestcapture.TerminationReasonWriteError
		default:
			outcome.TerminationReason = requestcapture.TerminationReasonWebSocketRelayError
		}
	case model.TerminalUpstreamSemanticError:
		outcome.TerminationReason = requestcapture.TerminationReasonWebSocketRelayError
	default:
		if relay != nil {
			switch {
			case relay.FailureOperation == webSocketRelayFailureOperationRead:
				outcome.TerminationReason = requestcapture.TerminationReasonReadError
			case relay.FailureOperation == webSocketRelayFailureOperationWrite:
				outcome.TerminationReason = requestcapture.TerminationReasonWriteError
			case relay.FailurePeer == webSocketPeerClient:
				outcome.TerminationReason = requestcapture.TerminationReasonClientDisconnect
			}
		}
	}
	return outcome
}

func webSocketRelayFailureObservation(
	relay *webSocketRelaySessionResult,
	result *WebSocketResult,
) requestcapture.FailureObservation {
	if result == nil {
		return requestcapture.FailureObservation{}
	}
	if result.UpstreamError != nil {
		fact, truncated := ProviderSemantic(
			requestcapture.FailureSiteWebSocketMessage,
			requestcapture.FailurePeerProvider,
			result.UpstreamError.StatusCode,
			result.UpstreamError.ProviderErrorType,
			result.UpstreamError.Code,
			result.UpstreamError.Message,
		)
		observation := Observation(fact, requestcapture.FailureFact{})
		observation.Truncated = truncated
		return observation
	}
	if relay != nil && relay.ObservedCloseError != nil &&
		!isCleanWebSocketCloseCode(relay.ObservedCloseError.Code) {
		fact, truncated := WebSocketClose(
			requestcapture.FailureSiteWebSocketClose,
			captureFailurePeer(relay.FailurePeer),
			relay.ObservedCloseError,
		)
		observation := Observation(fact, requestcapture.FailureFact{})
		observation.Truncated = truncated
		return observation
	}
	if result.Err == nil {
		return requestcapture.FailureObservation{}
	}

	peer := requestcapture.FailurePeerUnknown
	class := requestcapture.FailureClassTransport
	code := requestcapture.FailureCodeUnknown
	if relay != nil {
		peer = captureFailurePeer(relay.FailurePeer)
		switch relay.FailureOperation {
		case webSocketRelayFailureOperationRead:
			class = requestcapture.FailureClassRead
			code = requestcapture.FailureCodeRelayRead
		case webSocketRelayFailureOperationWrite:
			class = requestcapture.FailureClassWrite
			code = requestcapture.FailureCodeRelayWrite
		}
	}
	fact := FromError(
		requestcapture.FailureSiteWebSocketRelay,
		peer,
		class,
		code,
		result.Err,
	)
	return Observation(fact, requestcapture.FailureFact{})
}

func captureFailurePeer(peer webSocketPeer) requestcapture.FailurePeer {
	switch peer {
	case webSocketPeerClient:
		return requestcapture.FailurePeerClient
	case webSocketPeerUpstream:
		return requestcapture.FailurePeerUpstream
	default:
		return requestcapture.FailurePeerUnknown
	}
}
