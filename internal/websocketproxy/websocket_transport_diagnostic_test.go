package websocketproxy

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestBuildWebSocketTransportDiagnostic_SignalClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		result     *WebSocketResult
		fallback   error
		wantSignal string
		wantKind   string
		wantSource string
	}{
		{name: "fallback unknown", fallback: errors.New("dial failed"), wantSignal: transportSignalUnknownTransport, wantKind: transportKindLocalError, wantSource: transportSourceUpstream},
		{name: "timeout remains upstream", result: &WebSocketResult{Err: context.DeadlineExceeded, TransportObservation: WebSocketTransportObservation{FailurePeer: webSocketPeerClient}}, wantSignal: transportSignalTimeout, wantKind: transportKindTimeout, wantSource: transportSourceUpstream},
		{name: "cancel remains client", result: &WebSocketResult{Err: context.Canceled, TransportObservation: WebSocketTransportObservation{FailurePeer: webSocketPeerUpstream}}, wantSignal: transportSignalCanceled, wantKind: transportKindLocalError, wantSource: transportSourceClient},
		{name: "unexpected eof", result: &WebSocketResult{Err: io.ErrUnexpectedEOF}, wantSignal: transportSignalUnexpectedEOF, wantKind: transportKindDisconnect, wantSource: transportSourceUpstream},
		{name: "eof", result: &WebSocketResult{Err: io.EOF}, wantSignal: transportSignalEOF, wantKind: transportKindDisconnect, wantSource: transportSourceUpstream},
		{name: "close without status", result: &WebSocketResult{CloseCode: websocket.StatusNoStatusRcvd}, wantSignal: transportSignalCloseWithoutStatus, wantKind: transportKindDisconnect, wantSource: transportSourceUpstream},
		{name: "client disconnect", result: &WebSocketResult{Err: io.EOF, TransportObservation: WebSocketTransportObservation{FailurePeer: webSocketPeerClient}}, wantSignal: transportSignalEOF, wantKind: transportKindDisconnect, wantSource: transportSourceClient},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostic := buildWebSocketTransportDiagnostic(test.result, test.fallback, false)
			if diagnostic == nil {
				t.Fatal("expected diagnostic")
			}
			if diagnostic.Signal != test.wantSignal || diagnostic.Kind != test.wantKind || diagnostic.Source != test.wantSource {
				t.Fatalf("diagnostic = %+v, want signal=%q kind=%q source=%q", diagnostic, test.wantSignal, test.wantKind, test.wantSource)
			}
		})
	}
}

func TestBuildWebSocketTransportDiagnostic_CloseErrorPreservesPresenceAndPeer(t *testing.T) {
	t.Parallel()
	closeError := &websocket.CloseError{Code: 0, Reason: "peer left"}
	diagnostic := buildWebSocketTransportDiagnostic(&WebSocketResult{
		HandshakeAccepted: true,
		ClientVisible:     true,
		TransportObservation: WebSocketTransportObservation{
			CloseError: closeError, FailurePeer: webSocketPeerClient,
		},
	}, nil, false)
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	if diagnostic.Signal != transportSignalCloseError || diagnostic.Kind != transportKindDisconnect {
		t.Fatalf("diagnostic = %+v, want close_error/disconnect", diagnostic)
	}
	if diagnostic.Source != transportSourceClient || diagnostic.Stage != transportStagePostPayloadVisible {
		t.Fatalf("diagnostic = %+v, want client/post_payload_visible", diagnostic)
	}
	if diagnostic.CloseCode == nil || *diagnostic.CloseCode != 0 {
		t.Fatalf("CloseCode = %v, want present zero", diagnostic.CloseCode)
	}
	if diagnostic.CloseReasonSnippet != "peer left" {
		t.Fatalf("CloseReasonSnippet = %q, want %q", diagnostic.CloseReasonSnippet, "peer left")
	}
}

func TestBuildWebSocketTransportDiagnostic_StageTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result *WebSocketResult
		want   string
	}{
		{name: "before handshake", result: &WebSocketResult{Err: errors.New("dial failed")}, want: transportStagePreConnectionVisible},
		{name: "after handshake", result: &WebSocketResult{Err: errors.New("read failed"), HandshakeAccepted: true}, want: transportStagePrePayloadVisible},
		{name: "after payload", result: &WebSocketResult{Err: errors.New("read failed"), HandshakeAccepted: true, BytesUpstreamToClient: 1}, want: transportStagePostPayloadVisible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostic := buildWebSocketTransportDiagnostic(test.result, nil, false)
			if diagnostic == nil || diagnostic.Stage != test.want {
				t.Fatalf("diagnostic = %+v, want stage %q", diagnostic, test.want)
			}
		})
	}
}

func TestBuildWebSocketTransportDiagnostic_NoSignalAndSyntheticFinalReturnNil(t *testing.T) {
	t.Parallel()
	if diagnostic := buildWebSocketTransportDiagnostic(nil, nil, false); diagnostic != nil {
		t.Fatalf("nil observation diagnostic = %+v, want nil", diagnostic)
	}
	if diagnostic := buildWebSocketTransportDiagnostic(&WebSocketResult{Err: errors.New("ignored")}, nil, true); diagnostic != nil {
		t.Fatalf("synthetic final diagnostic = %+v, want nil", diagnostic)
	}
}

func TestBuildWebSocketTransportDiagnostic_TruncatesUTF8ByRune(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("𝕏", transportRawErrorSnippetLimitRunes+50)
	diagnostic := buildWebSocketTransportDiagnostic(&WebSocketResult{Err: errors.New(long)}, nil, false)
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	if got := len([]rune(diagnostic.RawErrorSnippet)); got != transportRawErrorSnippetLimitRunes {
		t.Fatalf("RawErrorSnippet rune count = %d, want %d", got, transportRawErrorSnippetLimitRunes)
	}
}
