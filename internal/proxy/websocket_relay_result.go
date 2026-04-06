package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"switch-a/internal/model"

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
	return closeErr.Code == websocket.StatusNormalClosure ||
		closeErr.Code == websocket.StatusGoingAway
}

func isCloseWithoutStatus(err error) bool {
	return websocket.CloseStatus(err) == websocket.StatusNoStatusRcvd
}

func isUnexpectedPeerDisconnect(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isCloseWithoutStatus(err)
}

func newWebSocketRelayResult(bytes int64, err error, failurePeer webSocketPeer, errorOrder *atomic.Uint32) webSocketRelayResult {
	result := webSocketRelayResult{
		bytes:       bytes,
		err:         err,
		failurePeer: failurePeer,
	}
	var suppressedErr *webSocketSuppressedUpstreamError
	if errors.As(err, &suppressedErr) {
		result.suppressedUpstreamError = suppressedErr.UpstreamError()
	}
	if err != nil {
		// Capture order before canceling the sibling relay leg so reduction can preserve
		// the actual trigger instead of whichever struct happens to be examined first.
		result.errorOrder = errorOrder.Add(1)
	}
	return result
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
	for _, candidate := range []webSocketRelayResult{primary, secondary} {
		if candidate.err == nil {
			continue
		}
		terminalCause := classifyRelayTerminalCause(candidate.err, candidate.failurePeer)
		if isNormalClose(candidate.err) {
			return webSocketRelayOutcome{
				closeCode:     extractCloseCode(candidate.err),
				terminalCause: terminalCause,
			}
		}
		if isUnexpectedPeerDisconnect(candidate.err) {
			return webSocketRelayOutcome{
				closeCode:     websocket.StatusNoStatusRcvd,
				terminalCause: terminalCause,
			}
		}
		return webSocketRelayOutcome{
			closeCode:     extractCloseCode(candidate.err),
			err:           candidate.err,
			terminalCause: terminalCause,
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

func classifyDialFailure(resp *http.Response) model.TerminalCause {
	if resp != nil && resp.StatusCode > 0 {
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
