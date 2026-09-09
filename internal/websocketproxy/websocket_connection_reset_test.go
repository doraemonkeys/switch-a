package websocketproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responsefacts"
)

func TestWebSocketConnectionResetEvidence(t *testing.T) {
	t.Parallel()
	assertWebSocketConnectionResetEvidence(t, syscall.ECONNRESET)
}

func assertWebSocketConnectionResetEvidence(t *testing.T, cause error) {
	t.Helper()
	err := fmt.Errorf("failed to get reader: failed to read frame header: %w", &net.OpError{
		Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "recv", Err: cause},
	})
	for source, peer := range map[string]webSocketPeer{transportSourceClient: webSocketPeerClient, transportSourceUpstream: webSocketPeerUpstream} {
		t.Run(source, func(t *testing.T) {
			result := &WebSocketResult{
				Err:                  err,
				TransportObservation: WebSocketTransportObservation{FailurePeer: peer},
			}
			for _, stage := range []string{transportStagePreConnectionVisible, transportStagePrePayloadVisible, transportStagePostPayloadVisible} {
				result.HandshakeAccepted = stage != transportStagePreConnectionVisible
				result.ClientVisible = stage == transportStagePostPayloadVisible
				d := buildWebSocketTransportDiagnostic(result, nil, false)
				if d == nil || d.Kind != transportKindDisconnect || d.Signal != "connection_reset" || d.Source != source || d.Stage != stage {
					t.Fatalf("diagnostic = %+v, want %s disconnect/connection_reset at %s", d, source, stage)
				}
				if d.RawErrorSnippet != truncateTransportSnippet(err.Error()) || d.CloseCode != nil {
					t.Fatalf("TCP reset must preserve the error without inventing a WS close frame: %+v", d)
				}
			}
		})
	}
	t.Run("dial fallback", func(t *testing.T) {
		d := buildWebSocketTransportDiagnostic(nil, err, false)
		if d == nil || d.Signal != "connection_reset" || d.Kind != transportKindDisconnect || d.Source != transportSourceUpstream || d.Stage != transportStagePreConnectionVisible {
			t.Fatalf("dial reset diagnostic = %+v", d)
		}
	})
	t.Run("completed response remains completed", func(t *testing.T) {
		observer := newCodexWebSocketMessageObserver("", nil, nil, nil)
		observer.ObserveClientMessage(websocket.MessageText, []byte(`{"type":"response.create"}`))
		observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"done","status":"completed"}}`))
		writes := responsefacts.Write{Calls: 1, SuccessfulCalls: 1, ConfirmedBytes: 123}
		result := &WebSocketResult{
			Err: err, HandshakeAccepted: true, ClientVisible: true,
			TerminalCause:        model.TerminalClientDisconnect,
			TransportObservation: WebSocketTransportObservation{FailurePeer: webSocketPeerClient},
			DownstreamWrite:      writes,
		}
		mergeWebSocketObservation(result, observer.Snapshot())
		assessment := assessWebSocketSession(&WebSocketSessionResult{FinalResult: result, FinalErr: err})
		if assessment.ServiceOutcome != model.ServiceOutcomeCompleted || assessment.CompletionState != model.CompletionStateCompleted ||
			deref(assessment.TerminationReason) != model.TerminationReasonClientDisconnect || deref(assessment.TerminationActor) != model.TerminationActorClient {
			t.Fatalf("reset changed business or termination facts: %+v", assessment)
		}
		var evidence webSocketEvidence
		if assessment.SessionEvidenceJSON == nil {
			t.Fatal("missing session evidence")
		}
		if err := json.Unmarshal([]byte(*assessment.SessionEvidenceJSON), &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.Transport == nil || evidence.Transport.Signal != "connection_reset" || evidence.Transport.Kind != transportKindDisconnect ||
			evidence.UpstreamResponses == nil || !evidence.UpstreamResponses.Current.Completed() ||
			evidence.DownstreamWrite == nil || *evidence.DownstreamWrite != writes {
			t.Fatalf("reset diagnostic lost completion or writer facts: %+v", evidence)
		}
	})
}

func TestWebSocketConnectionResetRequiresSystemError(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"connection reset by peer", "wsarecv: An existing connection was forcibly closed by the remote host."} {
		d := buildWebSocketTransportDiagnostic(&WebSocketResult{Err: errors.New(text)}, nil, false)
		if d == nil || d.Signal != transportSignalUnknownTransport || d.Kind != transportKindLocalError {
			t.Fatalf("untyped text must not be promoted to a system fact: %+v", d)
		}
	}
}
