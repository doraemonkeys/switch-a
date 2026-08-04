package websocketproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

var disabledWebSocketCaptureReadSink webSocketCapturedRead

func TestDisabledWebSocketCaptureHotPathDoesNotAllocate(t *testing.T) {
	options := (webSocketRelayOptions{}).withCaptureHooks()
	payload := []byte("frame")
	exchange := DialExchange{}

	allocations := testing.AllocsPerRun(1000, func() {
		captured := captureWebSocketMessageRead(
			options,
			requestcapture.MessageDirectionClientToUpstream,
			websocket.MessageText,
			payload,
			requestcapture.MessageSourceLive,
			requestcapture.MessageLineage{},
			requestcapture.MessageLineage{},
		)
		captureWebSocketMessageResult(
			options,
			captured,
			requestcapture.MessageDispositionForwarded,
			true,
			nil,
		)
		finishWebSocketDialCapture(exchange, requestcapture.Outcome{})
		disabledWebSocketCaptureReadSink = captured
	})

	if allocations != 0 {
		t.Fatalf("disabled WebSocket capture hot-path allocations = %v, want 0", allocations)
	}
	if disabledWebSocketCaptureReadSink != (webSocketCapturedRead{}) {
		t.Fatalf("disabled capture read = %#v, want zero", disabledWebSocketCaptureReadSink)
	}
}

func TestDisabledWebSocketDialSkipsCredentialEvidenceAndPreservesFailureBody(t *testing.T) {
	const body = "upstream rejected the handshake"
	forwarder := NewWebSocketForwarder(WebSocketForwarderConfig{
		Dialer: &mockDialer{dialFunc: func(
			context.Context,
			string,
			*websocket.DialOptions,
		) (*websocket.Conn, *http.Response, error) {
			return nil, &http.Response{
				StatusCode: http.StatusBadGateway,
				Proto:      "HTTP/1.1",
				Header: http.Header{
					"Set-Cookie": {"session=response-secret; Secure"},
				},
				Body: io.NopCloser(strings.NewReader(body)),
			}, errors.New("handshake rejected")
		}},
	})

	exchange := forwarder.dialUpstream(context.Background(), WebSocketDialRequest{
		URL: "wss://upstream.example/responses",
		Headers: http.Header{
			"Authorization": {"Bearer request-secret"},
		},
	})

	if exchange.credentialEvidence.Sealed() || exchange.credentialEvidence.Overflowed() {
		t.Fatalf("disabled capture inspected credential evidence: %#v", exchange.credentialEvidence)
	}
	if exchange.HandshakeBodySnippet != body ||
		exchange.ObservedFailureBodyBytes != int64(len(body)) ||
		!exchange.FailureBodyReachedEOF {
		t.Fatalf("disabled capture changed handshake body behavior: %#v", exchange)
	}
}

type captureDrainErrorBody struct {
	err    error
	reads  int
	closed bool
}

func (b *captureDrainErrorBody) Read([]byte) (int, error) {
	b.reads++
	return 0, b.err
}

func (b *captureDrainErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestDrainObservationDoesNotInvokeHostileErrorHooks(t *testing.T) {
	ordinaryError := errors.New("ordinary drain failure")
	ordinaryBody := &captureDrainErrorBody{err: ordinaryError}
	ordinarySnippet, ordinary := drainReadCloserWithSnippetObserved(ordinaryBody, 0)
	if ordinary.readErr != ordinaryError || !ordinaryBody.closed {
		t.Fatalf("ordinary drain observation = %#v, closed:%t", ordinary, ordinaryBody.closed)
	}

	blocked := make(chan struct{})
	hostile := &proxyHostileCaptureError{block: blocked}
	hostileBody := &captureDrainErrorBody{err: hostile}
	type result struct {
		snippet     string
		observation drainObservation
	}
	finished := make(chan result, 1)
	go func() {
		snippet, observation := drainReadCloserWithSnippetObserved(hostileBody, 0)
		finished <- result{snippet: snippet, observation: observation}
	}()

	select {
	case got := <-finished:
		if got.snippet != ordinarySnippet ||
			got.observation.bytesRead != ordinary.bytesRead ||
			got.observation.reachedEOF != ordinary.reachedEOF ||
			got.observation.limitReached != ordinary.limitReached ||
			got.observation.readErr != hostile ||
			hostileBody.reads != ordinaryBody.reads ||
			hostileBody.closed != ordinaryBody.closed {
			t.Fatalf("hostile drain diverged from ordinary drain: ordinary=%#v hostile=%#v", ordinary, got.observation)
		}
	case <-time.After(hostileCaptureDeadline):
		close(blocked)
		t.Fatal("drain observation invoked hostile Is/Unwrap hooks")
	}
	if calls := hostile.calls.Load(); calls != 0 {
		t.Fatalf("hostile drain error method calls = %d, want 0", calls)
	}
}
