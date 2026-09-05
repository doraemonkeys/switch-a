package websocketproxy

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
)

func httpToWSURL(rawURL string) string {
	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "https://") {
		return "wss://" + rawURL[len("https://"):]
	}
	if strings.HasPrefix(lower, "http://") {
		return "ws://" + rawURL[len("http://"):]
	}
	return rawURL
}

func isNormalClose(err error) bool {
	var closeErr websocket.CloseError
	if !errors.As(err, &closeErr) {
		return false
	}
	return isCleanWebSocketCloseCode(closeErr.Code)
}

func isCleanWebSocketCloseCode(code websocket.StatusCode) bool {
	return code == websocket.StatusNormalClosure || code == websocket.StatusGoingAway
}

func isCloseWithoutStatus(err error) bool {
	return websocket.CloseStatus(err) == websocket.StatusNoStatusRcvd
}

func isUnexpectedPeerDisconnect(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isCloseWithoutStatus(err)
}

func newWebSocketRelayResult(bytes int64, err error, failurePeer webSocketPeer, errorOrder *atomic.Uint32) webSocketRelayResult {
	return newWebSocketRelayResultForOperation(
		bytes,
		err,
		failurePeer,
		webSocketRelayFailureOperationUnknown,
		errorOrder,
	)
}

func newWebSocketRelayResultForOperation(
	bytes int64,
	err error,
	failurePeer webSocketPeer,
	failureOperation webSocketRelayFailureOperation,
	errorOrder *atomic.Uint32,
) webSocketRelayResult {
	result := webSocketRelayResult{
		bytes:            bytes,
		err:              err,
		failurePeer:      failurePeer,
		failureOperation: failureOperation,
	}
	var suppressedErr *webSocketSuppressedUpstreamError
	if errors.As(err, &suppressedErr) {
		result.suppressedUpstreamError = suppressedErr.UpstreamError()
	}
	// A close frame is peer evidence only when the relay's Read surfaced it.
	// Write failures can carry close-shaped errors from local propagation, which
	// must not be persisted as though another endpoint sent a frame.
	var closeErr websocket.CloseError
	if failureOperation == webSocketRelayFailureOperationRead && err != nil && errors.As(err, &closeErr) {
		frame := closeErr
		result.closeError = &frame
	}
	if err != nil {
		// Capture order before canceling the sibling relay leg so reduction can preserve
		// the actual trigger instead of whichever struct happens to be examined first.
		result.errorOrder = errorOrder.Add(1)
	}
	return result
}

func newSinglePeerRelaySessionResult(
	err error,
	failurePeer webSocketPeer,
	fallbackCommit *webSocketCommitState,
	lifecycle *webSocketLifecycleState,
	bytesClientToUpstream, bytesUpstreamToClient int64,
) *webSocketRelaySessionResult {
	return newSinglePeerRelaySessionResultForOperation(
		err,
		failurePeer,
		webSocketRelayFailureOperationUnknown,
		fallbackCommit,
		lifecycle,
		bytesClientToUpstream,
		bytesUpstreamToClient,
	)
}

func newSinglePeerRelaySessionResultForOperation(
	err error,
	failurePeer webSocketPeer,
	failureOperation webSocketRelayFailureOperation,
	fallbackCommit *webSocketCommitState,
	lifecycle *webSocketLifecycleState,
	bytesClientToUpstream, bytesUpstreamToClient int64,
) *webSocketRelaySessionResult {
	var errorOrder atomic.Uint32
	return newWebSocketRelaySessionResultFromOutcome(
		reduceOrderedWebSocketRelayResults(
			newWebSocketRelayResultForOperation(0, err, failurePeer, failureOperation, &errorOrder),
			webSocketRelayResult{},
		),
		fallbackCommit,
		lifecycle,
		bytesClientToUpstream,
		bytesUpstreamToClient,
	)
}

func reduceWebSocketRelayErrors(clientToUpstream, upstreamToClient webSocketRelayResult) webSocketRelayOutcome {
	primary, secondary := orderWebSocketRelayResults(clientToUpstream, upstreamToClient)
	return reduceOrderedWebSocketRelayResults(primary, secondary)
}

func orderWebSocketRelayResults(first, second webSocketRelayResult) (webSocketRelayResult, webSocketRelayResult) {
	switch {
	case first.errorOrder == 0:
		return second, first
	case second.errorOrder == 0:
		return first, second
	case first.errorOrder <= second.errorOrder:
		return first, second
	default:
		return second, first
	}
}

func reduceOrderedWebSocketRelayResults(primary, secondary webSocketRelayResult) webSocketRelayOutcome {
	// Conversion cannot be bypassed by an earlier sibling close or cancellation.
	for _, candidate := range []webSocketRelayResult{primary, secondary} {
		if disguiseFailure(candidate.err) != nil {
			return webSocketRelayOutcome{closeCode: websocket.StatusInternalError, err: candidate.err,
				terminalCause: model.TerminalInternalError, failureOperation: candidate.failureOperation}
		}
	}
	for _, candidate := range []webSocketRelayResult{primary, secondary} {
		if candidate.err == nil {
			continue
		}
		terminalCause := classifyRelayTerminalCause(candidate.err, candidate.failurePeer)
		// The observation-layer fields (observedCloseError, failurePeer) are
		// populated for every candidate-producing branch so evidence derivation
		// has a complete picture; close propagation still reads only closeCode.
		if isNormalClose(candidate.err) {
			return webSocketRelayOutcome{
				closeCode:          extractCloseCode(candidate.err),
				terminalCause:      terminalCause,
				observedCloseError: candidate.closeError,
				failurePeer:        candidate.failurePeer,
				failureOperation:   candidate.failureOperation,
			}
		}
		if isUnexpectedPeerDisconnect(candidate.err) {
			return webSocketRelayOutcome{
				closeCode:          websocket.StatusNoStatusRcvd,
				terminalCause:      terminalCause,
				observedCloseError: candidate.closeError,
				failurePeer:        candidate.failurePeer,
				failureOperation:   candidate.failureOperation,
			}
		}
		return webSocketRelayOutcome{
			closeCode:          extractCloseCode(candidate.err),
			err:                candidate.err,
			terminalCause:      terminalCause,
			observedCloseError: candidate.closeError,
			failurePeer:        candidate.failurePeer,
			failureOperation:   candidate.failureOperation,
		}
	}
	return webSocketRelayOutcome{
		closeCode:     websocket.StatusNormalClosure,
		terminalCause: model.TerminalCleanClose,
	}
}

func mergeWebSocketObservation(result *WebSocketResult, observation WebSocketObservation) {
	result.Model = observation.Model
	result.TokenUsage = observation.TokenUsage
	result.UpstreamError = observation.UpstreamError
	result.CompletionObserved = observation.CompletionObserved
	if observation.SessionCommitted {
		result.SessionCommitted = true
		result.CommitSource = model.CommitSemantic
	}
}

func classifyDialFailure(statusCode int) model.TerminalCause {
	if statusCode > 0 {
		return model.TerminalUpstreamHandshakeRejected
	}
	return model.TerminalUpstreamTransportError
}

func classifyRelayTerminalCause(err error, failurePeer webSocketPeer) model.TerminalCause {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return model.TerminalInternalError
	}
	switch failurePeer {
	case webSocketPeerClient:
		return model.TerminalClientDisconnect
	case webSocketPeerUpstream:
		if isNormalClose(err) {
			return model.TerminalCleanClose
		}
		return model.TerminalUpstreamTransportError
	default:
		return model.TerminalInternalError
	}
}

func sanitizeWebSocketCloseCode(code websocket.StatusCode, sessionErr error) websocket.StatusCode {
	switch code {
	case websocket.StatusNoStatusRcvd, websocket.StatusAbnormalClosure, websocket.StatusTLSHandshake:
		if sessionErr == nil {
			return websocket.StatusNormalClosure
		}
		return websocket.StatusInternalError
	default:
		return code
	}
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func extractCloseCode(err error) websocket.StatusCode {
	var closeErr websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code
	}
	return websocket.StatusNormalClosure
}
