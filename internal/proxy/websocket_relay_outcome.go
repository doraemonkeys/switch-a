package proxy

import (
	"github.com/doraemonkeys/switch-a/internal/model"
)

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
		ClientAccepted:        r.ClientAccepted,
		ClientVisible:         r.ClientVisible,
		SessionCommitted:      r.SessionCommitted,
		TerminalCause:         r.TerminalCause,
		CommitSource:          r.CommitSource,
		CloseCode:             r.CloseCode,
		BytesClientToUpstream: r.BytesClientToUpstream,
		BytesUpstreamToClient: r.BytesUpstreamToClient,
		Err:                   r.Err,
		UpstreamError:         r.SuppressedUpstreamError,
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
		if result.suppressedUpstreamError != nil {
			return result.suppressedUpstreamError.Clone()
		}
	}
	return nil
}
