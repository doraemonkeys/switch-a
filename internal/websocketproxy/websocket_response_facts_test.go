package websocketproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

func TestNextRoundWriteFailureCannotReuseEarlierCompletion(t *testing.T) {
	observer := newCodexWebSocketMessageObserver("", nil, nil, nil)
	observer.ObserveClientMessage(websocket.MessageText, []byte(`{"type":"response.create"}`))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"first","status":"completed"}}`))
	if !observer.Snapshot().CompletionObserved {
		t.Fatal("missing first completion")
	}
	server := newRecordingWSServer(t, make(chan webSocketReplayMessage, 1))
	defer server.Close()
	conn := connectWSClient(t, t.Context(), wsURL(server))
	_ = conn.CloseNow()
	processor := webSocketRelayMessageProcessor{
		ctx: t.Context(), dst: conn, dstPeer: webSocketPeerUpstream,
		direction: requestcapture.MessageDirectionClientToUpstream,
		options:   (webSocketRelayOptions{Observer: observer}).withCaptureHooks(),
		observe:   observer.ObserveClientMessage,
	}
	_, _, err := processor.process(websocket.MessageText, []byte(`{"type":"response.create"}`))
	if err == nil {
		t.Fatal("closed upstream unexpectedly accepted message")
	}
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"first","status":"completed"}}`))
	result := &WebSocketResult{HandshakeAccepted: true, ClientVisible: true, TerminalCause: model.TerminalUpstreamTransportError, Err: err}
	mergeWebSocketObservation(result, observer.Snapshot())
	assessment := assessWebSocketSession(&WebSocketSessionResult{FinalResult: result})
	if result.CompletionObserved || assessment.CompletionState == model.CompletionStateCompleted || assessment.ServiceOutcome == model.ServiceOutcomeCompleted {
		t.Fatalf("previous response masked failed upload: %+v", assessment)
	}
	if result.ResponseProgress.Current.Round != 2 || result.ResponseProgress.LastCompleted.ResponseID != "first" {
		t.Fatal(result.ResponseProgress)
	}
}

func TestUpstreamCompletionAndDownstreamWriteFailureRemainIndependent(t *testing.T) {
	observer := newCodexWebSocketMessageObserver("", nil, nil, nil)
	observer.ObserveClientMessage(websocket.MessageText, []byte(`{"type":"response.create"}`))
	server := newRecordingWSServer(t, make(chan webSocketReplayMessage, 1))
	defer server.Close()
	conn := connectWSClient(t, t.Context(), wsURL(server))
	_ = conn.CloseNow()
	lifecycle := newWebSocketLifecycleState()
	processor := webSocketRelayMessageProcessor{
		ctx: t.Context(), dst: conn, dstPeer: webSocketPeerClient,
		direction: requestcapture.MessageDirectionUpstreamToClient,
		options:   (webSocketRelayOptions{Observer: observer, Lifecycle: lifecycle}).withCaptureHooks(),
		observe:   observer.ObserveUpstreamMessage,
	}
	_, _, err := processor.process(websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"done","status":"completed"}}`))
	if err == nil {
		t.Fatal("closed client unexpectedly accepted message")
	}
	result := &WebSocketResult{HandshakeAccepted: true, TerminalCause: model.TerminalClientDisconnect, Err: err, DownstreamWrite: lifecycle.Snapshot().DownstreamWrite}
	mergeWebSocketObservation(result, observer.Snapshot())
	assessment := assessWebSocketSession(&WebSocketSessionResult{FinalResult: result})
	if assessment.ServiceOutcome != model.ServiceOutcomeCompleted || deref(assessment.TerminationReason) != model.TerminationReasonClientDisconnect {
		t.Fatalf("disconnect erased upstream completion: %+v", assessment)
	}
	var evidence webSocketEvidence
	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("missing independent facts")
	}
	if err := json.Unmarshal([]byte(*assessment.SessionEvidenceJSON), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.UpstreamResponses == nil || !evidence.UpstreamResponses.Current.Completed() || evidence.DownstreamWrite == nil || evidence.DownstreamWrite.FailedCalls != 1 || evidence.DownstreamWrite.ConfirmedBytes != 0 {
		t.Fatalf("lost physical write failure or upstream completion: %+v", evidence)
	}
}

func TestGatewayFailureCodeUsesActualConnectionStage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result WebSocketResult
		want   string
	}{
		{"handshake_rejected", WebSocketResult{HandshakeStatusCode: http.StatusForbidden, TerminalCause: model.TerminalUpstreamHandshakeRejected}, ErrCodeWebSocketUpgrade},
		{"dial_transport", WebSocketResult{TerminalCause: model.TerminalUpstreamTransportError}, ErrCodeWebSocketTransport},
		{"accepted_upload_failed", WebSocketResult{HandshakeAccepted: true, HandshakeStatusCode: 101, TerminalCause: model.TerminalUpstreamTransportError}, ErrCodeWebSocketRelay},
		{"local_error_after_upgrade", WebSocketResult{HandshakeAccepted: true, TerminalCause: model.TerminalInternalError}, ErrCodeInternalError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &WebSocketSessionResult{FinalResult: &tc.result}
			populateCanonicalWebSocketGatewayMetadata(session)
			if session.GatewayErrorCode != tc.want {
				t.Fatalf("code=%s want=%s", session.GatewayErrorCode, tc.want)
			}
		})
	}
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, errors.New("write failed")} {
		if got := classifyRelayTerminalCause(err, webSocketPeerUpstream); got != model.TerminalUpstreamTransportError {
			t.Fatalf("upstream failure became %s", got)
		}
		if got := classifyRelayTerminalCause(err, webSocketPeerClient); got != model.TerminalClientDisconnect {
			t.Fatalf("client failure became %s", got)
		}
	}
}
