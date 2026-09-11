package websocketproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
)

func TestWebSocketRelayPreservesConsumedTransportErrorInEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		peer       webSocketPeer
		wantSignal string
		wantSource string
	}{
		{"upstream EOF", io.EOF, webSocketPeerUpstream, transportSignalEOF, transportSourceUpstream},
		{"upstream wrapped EOF", fmt.Errorf("failed to read frame header: %w", io.EOF), webSocketPeerUpstream, transportSignalEOF, transportSourceUpstream},
		{"upstream unexpected EOF", fmt.Errorf("failed to read frame payload: %w", io.ErrUnexpectedEOF), webSocketPeerUpstream, transportSignalUnexpectedEOF, transportSourceUpstream},
		{"client EOF", io.EOF, webSocketPeerClient, transportSignalEOF, transportSourceClient},
		{"close without status", websocket.CloseError{Code: websocket.StatusNoStatusRcvd}, webSocketPeerUpstream, transportSignalCloseError, transportSourceUpstream},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var order atomic.Uint32
			primary := newWebSocketRelayResultForOperation(0, test.err, test.peer, webSocketRelayFailureOperationRead, &order)
			secondary := newWebSocketRelayResultForOperation(0, context.Canceled, webSocketPeerClient, webSocketRelayFailureOperationRead, &order)
			// Feed the sibling first to exercise the terminal-event ordering boundary.
			outcome := reduceWebSocketRelayErrors(secondary, primary)
			result := newWebSocketRelaySessionResultFromOutcome(outcome, nil, nil, 0, 1).toWebSocketResult()
			if result.Err != nil || sanitizeWebSocketCloseCode(result.CloseCode, result.Err) != websocket.StatusNormalClosure {
				t.Fatalf("diagnostic retention changed client close policy: %+v", result)
			}
			if result.TransportObservation.Err != test.err {
				t.Fatalf("observed error = %v, want original %v", result.TransportObservation.Err, test.err)
			}
			encoded := map[string]*string{
				"session": buildWebSocketEvidence(webSocketGatewayEvidenceInput{}, result, context.Canceled, false, ""),
				"attempt": buildWebSocketAttemptEvidence(WebSocketAttemptResult{Result: result}),
			}
			for scope, raw := range encoded {
				if raw == nil {
					t.Fatalf("%s evidence missing", scope)
				}
				var evidence webSocketEvidence
				if err := json.Unmarshal([]byte(*raw), &evidence); err != nil {
					t.Fatal(err)
				}
				transport := evidence.Transport
				if transport == nil || transport.Signal != test.wantSignal || transport.Source != test.wantSource ||
					transport.RawErrorSnippet != test.err.Error() || transport.Stage != transportStagePostPayloadVisible {
					t.Fatalf("%s transport = %+v, want original %v from %s", scope, transport, test.err, test.wantSource)
				}
				if test.wantSignal != transportSignalCloseError && transport.CloseCode != nil {
					t.Fatalf("%s EOF synthesized a peer close frame: %+v", scope, transport)
				}
			}
		})
	}
}
