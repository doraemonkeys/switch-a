package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

func TestWebSocketContextCaptureReasonPreservesCancellationOwnership(t *testing.T) {
	t.Parallel()

	timedOutContext, cancelTimeout := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelTimeout()
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name     string
		ctx      context.Context
		err      error
		fallback requestcapture.TerminationReason
		want     requestcapture.TerminationReason
	}{
		{
			name:     "request deadline owns termination",
			ctx:      timedOutContext,
			err:      errors.New("transport also failed"),
			fallback: requestcapture.TerminationReasonTransportError,
			want:     requestcapture.TerminationReasonTimeout,
		},
		{
			name:     "request cancellation is a client disconnect",
			ctx:      canceledContext,
			err:      errors.New("transport also failed"),
			fallback: requestcapture.TerminationReasonTransportError,
			want:     requestcapture.TerminationReasonClientDisconnect,
		},
		{
			name:     "standalone operation deadline remains timeout",
			ctx:      context.Background(),
			err:      context.DeadlineExceeded,
			fallback: requestcapture.TerminationReasonTransportError,
			want:     requestcapture.TerminationReasonTimeout,
		},
		{
			name:     "standalone operation cancellation is not reclassified as disconnect",
			ctx:      context.Background(),
			err:      context.Canceled,
			fallback: requestcapture.TerminationReasonTransportError,
			want:     requestcapture.TerminationReasonCanceled,
		},
		{
			name:     "ordinary transport failure uses caller fallback",
			ctx:      context.Background(),
			err:      errors.New("connection reset"),
			fallback: requestcapture.TerminationReasonReadError,
			want:     requestcapture.TerminationReasonReadError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := webSocketContextCaptureReason(test.ctx, test.err, test.fallback); got != test.want {
				t.Fatalf("webSocketContextCaptureReason() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWebSocketRelayCaptureOutcomeMapsFailureBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		relay  *webSocketRelaySessionResult
		result *WebSocketResult
		want   requestcapture.TerminationReason
	}{
		{
			name: "missing result remains a partial relay failure",
			want: requestcapture.TerminationReasonWebSocketRelayError,
		},
		{
			name:   "client disconnect",
			result: &WebSocketResult{TerminalCause: model.TerminalClientDisconnect},
			want:   requestcapture.TerminationReasonClientDisconnect,
		},
		{
			name:   "client upgrade rejection",
			result: &WebSocketResult{TerminalCause: model.TerminalClientUpgradeRejected},
			want:   requestcapture.TerminationReasonClientDisconnect,
		},
		{
			name:   "upstream transport without relay detail",
			result: &WebSocketResult{TerminalCause: model.TerminalUpstreamTransportError},
			want:   requestcapture.TerminationReasonWebSocketRelayError,
		},
		{
			name:   "upstream read failure",
			relay:  &webSocketRelaySessionResult{FailureOperation: webSocketRelayFailureOperationRead},
			result: &WebSocketResult{TerminalCause: model.TerminalUpstreamTransportError},
			want:   requestcapture.TerminationReasonReadError,
		},
		{
			name:   "upstream write failure",
			relay:  &webSocketRelaySessionResult{FailureOperation: webSocketRelayFailureOperationWrite},
			result: &WebSocketResult{TerminalCause: model.TerminalUpstreamTransportError},
			want:   requestcapture.TerminationReasonWriteError,
		},
		{
			name:   "upstream transport with unknown operation",
			relay:  &webSocketRelaySessionResult{},
			result: &WebSocketResult{TerminalCause: model.TerminalUpstreamTransportError},
			want:   requestcapture.TerminationReasonWebSocketRelayError,
		},
		{
			name:   "semantic failure",
			result: &WebSocketResult{TerminalCause: model.TerminalUpstreamSemanticError},
			want:   requestcapture.TerminationReasonWebSocketRelayError,
		},
		{
			name:   "unclassified relay read",
			relay:  &webSocketRelaySessionResult{FailureOperation: webSocketRelayFailureOperationRead},
			result: &WebSocketResult{TerminalCause: model.TerminalInternalError},
			want:   requestcapture.TerminationReasonReadError,
		},
		{
			name:   "unclassified relay write",
			relay:  &webSocketRelaySessionResult{FailureOperation: webSocketRelayFailureOperationWrite},
			result: &WebSocketResult{TerminalCause: model.TerminalInternalError},
			want:   requestcapture.TerminationReasonWriteError,
		},
		{
			name:   "unclassified client peer",
			relay:  &webSocketRelaySessionResult{FailurePeer: webSocketPeerClient},
			result: &WebSocketResult{TerminalCause: model.TerminalInternalError},
			want:   requestcapture.TerminationReasonClientDisconnect,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome := webSocketRelayCaptureOutcome(context.Background(), test.relay, test.result)
			if outcome.TerminationReason != test.want {
				t.Fatalf("TerminationReason = %q, want %q", outcome.TerminationReason, test.want)
			}
			if test.result == nil && outcome.SourceCompletion != requestcapture.SourceCompletionPartial {
				t.Fatalf("SourceCompletion = %q, want partial", outcome.SourceCompletion)
			}
		})
	}
}

func TestWebSocketRelayFailureObservationAttributesOperationAndPeer(t *testing.T) {
	t.Parallel()

	if got := webSocketRelayFailureObservation(nil, nil); got.Primary != (requestcapture.FailureFact{}) {
		t.Fatalf("nil result observation = %#v, want empty", got)
	}
	if got := webSocketRelayFailureObservation(nil, &WebSocketResult{}); got.Primary != (requestcapture.FailureFact{}) {
		t.Fatalf("nil error observation = %#v, want empty", got)
	}

	tests := []struct {
		name      string
		relay     *webSocketRelaySessionResult
		wantPeer  requestcapture.FailurePeer
		wantClass requestcapture.FailureClass
		wantCode  requestcapture.FailureCode
	}{
		{
			name:      "unknown transport boundary",
			wantPeer:  requestcapture.FailurePeerUnknown,
			wantClass: requestcapture.FailureClassTransport,
			wantCode:  requestcapture.FailureCodeUnknown,
		},
		{
			name:      "client read",
			relay:     &webSocketRelaySessionResult{FailurePeer: webSocketPeerClient, FailureOperation: webSocketRelayFailureOperationRead},
			wantPeer:  requestcapture.FailurePeerClient,
			wantClass: requestcapture.FailureClassRead,
			wantCode:  requestcapture.FailureCodeRelayRead,
		},
		{
			name:      "upstream write",
			relay:     &webSocketRelaySessionResult{FailurePeer: webSocketPeerUpstream, FailureOperation: webSocketRelayFailureOperationWrite},
			wantPeer:  requestcapture.FailurePeerUpstream,
			wantClass: requestcapture.FailureClassWrite,
			wantCode:  requestcapture.FailureCodeRelayWrite,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation := webSocketRelayFailureObservation(test.relay, &WebSocketResult{Err: errors.New("relay failed")})
			if observation.Primary.Peer != test.wantPeer || observation.Primary.Class != test.wantClass || observation.Primary.Code != test.wantCode {
				t.Fatalf("failure fact = %#v, want peer=%q class=%q code=%q", observation.Primary, test.wantPeer, test.wantClass, test.wantCode)
			}
		})
	}
}

func TestWebSocketRelayCloseAndPreservationBoundaries(t *testing.T) {
	t.Parallel()

	unknownPeer := webSocketCaptureCloseObservation(&webSocketRelaySessionResult{
		FailurePeer:        webSocketPeerUnknown,
		ObservedCloseError: &websocket.CloseError{Code: websocket.StatusInternalError},
	})
	if unknownPeer != nil {
		t.Fatalf("unknown-peer close observation = %#v, want nil", unknownPeer)
	}

	for peer, want := range map[webSocketPeer]requestcapture.FailurePeer{
		webSocketPeerClient:   requestcapture.FailurePeerClient,
		webSocketPeerUpstream: requestcapture.FailurePeerUpstream,
		webSocketPeerUnknown:  requestcapture.FailurePeerUnknown,
	} {
		if got := captureFailurePeer(peer); got != want {
			t.Fatalf("captureFailurePeer(%v) = %q, want %q", peer, got, want)
		}
	}

	options := webSocketRelayOptions{PreserveClientOnPreVisibleFailure: true}
	if !shouldPreserveClientOnPreVisibleFailure(options, webSocketLifecycleSnapshot{}, webSocketRelayOutcome{terminalCause: model.TerminalUpstreamTransportError}) {
		t.Fatal("pre-visible upstream transport failure should preserve the downstream client")
	}
	if !shouldPreserveClientOnPreVisibleFailure(options, webSocketLifecycleSnapshot{}, webSocketRelayOutcome{terminalCause: model.TerminalCleanClose}) {
		t.Fatal("pre-visible clean upstream close should preserve the downstream client")
	}
	if shouldPreserveClientOnPreVisibleFailure(options, webSocketLifecycleSnapshot{}, webSocketRelayOutcome{terminalCause: model.TerminalClientDisconnect}) {
		t.Fatal("client disconnect cannot preserve the downstream client")
	}
	if shouldPreserveClientOnPreVisibleFailure(options, webSocketLifecycleSnapshot{ClientVisible: true}, webSocketRelayOutcome{terminalCause: model.TerminalUpstreamTransportError}) {
		t.Fatal("client-visible sessions cannot return to the pre-visible preservation window")
	}
}

func TestWebSocketClientReadHandoffRetainsPendingFrameAcrossAttemptCancellation(t *testing.T) {
	t.Parallel()

	pending := make(chan webSocketInitialReadResult, 1)
	handoff := newWebSocketClientReadHandoff(pending)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	cancelAttempt()

	if _, _, err := handoff.Read(attemptCtx, context.Background(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled attempt read error = %v, want context canceled", err)
	}

	want := webSocketInitialReadResult{
		messageType: websocket.MessageText,
		data:        []byte(`{"type":"response.create"}`),
	}
	pending <- want
	messageType, data, err := handoff.Read(context.Background(), context.Background(), nil)
	if err != nil {
		t.Fatalf("next attempt read error = %v", err)
	}
	if messageType != want.messageType || string(data) != string(want.data) {
		t.Fatalf("next attempt read = (%v, %q), want (%v, %q)", messageType, data, want.messageType, want.data)
	}
	if got := handoff.pendingRead(context.Background(), nil); got != nil {
		t.Fatal("completed handoff retained a stale read channel")
	}
}

func TestPreVisibleClientMessageBufferRejectsUnsafeReplayState(t *testing.T) {
	t.Parallel()

	var nilBuffer *preVisibleClientMessageBuffer
	if nilBuffer.Enabled() {
		t.Fatal("nil buffer reported enabled")
	}
	nilBuffer.Disable()
	if got := nilBuffer.RecordWithLineage(websocket.MessageText, []byte("ignored"), false, requestcapture.MessageLineage{}); got != invalidWebSocketReplayMessageIndex {
		t.Fatalf("nil buffer record index = %d, want invalid", got)
	}
	nilBuffer.MarkDelivered(0, requestcapture.MessageLineage{})
	if err := nilBuffer.Replay(context.Background(), nil); err != nil {
		t.Fatalf("nil buffer replay error = %v", err)
	}
	if snapshot := nilBuffer.Snapshot(); snapshot.Enabled || len(snapshot.Messages) != 0 {
		t.Fatalf("nil buffer snapshot = %#v, want zero", snapshot)
	}

	defaultSized := newPreVisibleClientMessageBuffer(0)
	if defaultSized.limitBytes != preVisibleClientReplayBufferLimitBytes {
		t.Fatalf("default limit = %d, want %d", defaultSized.limitBytes, preVisibleClientReplayBufferLimitBytes)
	}
	if got := defaultSized.Record(websocket.MessageText, []byte("visible"), true); got != invalidWebSocketReplayMessageIndex || defaultSized.Enabled() {
		t.Fatalf("client-visible record = index %d enabled %v, want disabled", got, defaultSized.Enabled())
	}
	if got := defaultSized.Record(websocket.MessageText, []byte("after-disable"), false); got != invalidWebSocketReplayMessageIndex {
		t.Fatalf("disabled buffer record index = %d, want invalid", got)
	}
	if err := defaultSized.Replay(context.Background(), nil); err == nil {
		t.Fatal("disabled buffer replay unexpectedly succeeded")
	}

	invalidType := newPreVisibleClientMessageBuffer(64)
	if got := invalidType.Record(websocket.MessageType(99), []byte("control"), false); got != invalidWebSocketReplayMessageIndex || invalidType.Enabled() {
		t.Fatalf("non-replayable record = index %d enabled %v, want disabled", got, invalidType.Enabled())
	}

	messageLimited := newPreVisibleClientMessageBuffer(128 + 2*128*webSocketReplayDescriptorBytes)
	for index := range 128 {
		if got := messageLimited.Record(websocket.MessageText, []byte{'x'}, false); got != index {
			t.Fatalf("record %d index = %d", index, got)
		}
	}
	if got := messageLimited.Record(websocket.MessageText, []byte{'x'}, false); got != invalidWebSocketReplayMessageIndex || messageLimited.Enabled() {
		t.Fatalf("message-limit record = index %d enabled %v, want disabled", got, messageLimited.Enabled())
	}

	byteLimited := newPreVisibleClientMessageBuffer(3)
	if got := byteLimited.Record(websocket.MessageBinary, []byte("four"), false); got != invalidWebSocketReplayMessageIndex || byteLimited.Enabled() {
		t.Fatalf("byte-limit record = index %d enabled %v, want disabled", got, byteLimited.Enabled())
	}

	delivered := newPreVisibleClientMessageBuffer(16 + 2*webSocketReplayDescriptorBytes)
	index := delivered.Record(websocket.MessageText, []byte("safe"), false)
	delivered.MarkDelivered(index+1, requestcapture.MessageLineage{})
	if delivered.Snapshot().Messages[index].Delivered {
		t.Fatal("out-of-range delivery marker mutated the buffered message")
	}
}

func TestProviderScopedSuppressionRequiresReplaySafety(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`)
	ctx := webSocketPreWriteContext{MessageType: websocket.MessageText, Data: payload}

	if decision := newAllowlistedProviderScopedSuppressDecision(nil)(ctx); decision.Action != webSocketPreWriteActionForward {
		t.Fatalf("nil replay buffer action = %v, want forward", decision.Action)
	}

	disabled := newPreVisibleClientMessageBuffer(64)
	disabled.Disable()
	if decision := newAllowlistedProviderScopedSuppressDecision(disabled)(ctx); decision.Action != webSocketPreWriteActionForward {
		t.Fatalf("disabled replay buffer action = %v, want forward", decision.Action)
	}

	enabled := newPreVisibleClientMessageBuffer(64)
	decision := newAllowlistedProviderScopedSuppressDecision(enabled)(ctx)
	if decision.Action != webSocketPreWriteActionSuppress {
		t.Fatalf("replay-safe provider error action = %v, want suppress", decision.Action)
	}
	if decision.SuppressedUpstreamError == nil || decision.SuppressedUpstreamError.Raw != string(payload) {
		t.Fatalf("suppressed upstream error = %#v, want raw payload", decision.SuppressedUpstreamError)
	}
	if string(decision.SuppressedMessageData) != string(payload) {
		t.Fatalf("suppressed payload = %q, want %q", decision.SuppressedMessageData, payload)
	}
}

func TestCaptureFailureFacadePreservesStructuredFacts(t *testing.T) {
	t.Parallel()

	fact := Fact(
		requestcapture.FailureSiteWebSocketRelay,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassWrite,
		requestcapture.FailureCodeRelayWrite,
	)
	if fact.Site != requestcapture.FailureSiteWebSocketRelay || fact.Peer != requestcapture.FailurePeerUpstream || fact.Class != requestcapture.FailureClassWrite || fact.Code != requestcapture.FailureCodeRelayWrite {
		t.Fatalf("Fact() = %#v", fact)
	}
	statusFact := HTTPStatus(requestcapture.FailureSiteWebSocketHandshake, requestcapture.FailurePeerProvider, http.StatusBadGateway)
	if statusFact.HTTPStatusCode != http.StatusBadGateway {
		t.Fatalf("HTTPStatus() = %#v", statusFact)
	}
	statusFact = WithHTTPStatus(statusFact, http.StatusServiceUnavailable)
	if statusFact.HTTPStatusCode != http.StatusServiceUnavailable {
		t.Fatalf("WithHTTPStatus() = %#v", statusFact)
	}

	reason, preparationFailure := webSocketPreparation(nil, errors.New("missing credential"), requestcapture.FailureCodeCredentialApply)
	if reason == "" || preparationFailure.Primary.Code != requestcapture.FailureCodeCredentialApply {
		t.Fatalf("webSocketPreparation() = (%q, %#v)", reason, preparationFailure)
	}
	reason, acceptFailure := webSocketClientAccept(context.Canceled, errors.New("upgrade rejected"))
	if reason != requestcapture.TerminationReasonClientDisconnect || acceptFailure.Primary.Site == "" {
		t.Fatalf("webSocketClientAccept() = (%q, %#v)", reason, acceptFailure)
	}
}
