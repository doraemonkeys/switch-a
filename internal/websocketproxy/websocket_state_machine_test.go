package websocketproxy

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

func TestWebSocketProbeBudgetExactBoundaries(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	now := started
	newTracker := func(budget webSocketProbeBudget) webSocketProbeBudgetTracker {
		return newWebSocketProbeBudgetTracker(budget, func() time.Time { return now })
	}

	t.Run("duration", func(t *testing.T) {
		budget := webSocketProbeBudget{Duration: 3 * time.Second, MaxFrames: 2, MaxBytes: 2}
		tracker := newTracker(budget)
		now = started.Add(budget.Duration)
		if err := tracker.Admit(1); err != nil {
			t.Fatalf("exact duration rejected: %v", err)
		}
		now = started.Add(budget.Duration + time.Nanosecond)
		if err := tracker.Admit(1); err == nil {
			t.Fatal("duration overflow admitted")
		}
	})

	t.Run("frames", func(t *testing.T) {
		now = started
		budget := defaultWebSocketProbeBudget()
		tracker := newTracker(budget)
		for frame := 0; frame < budget.MaxFrames; frame++ {
			if err := tracker.Admit(0); err != nil {
				t.Fatalf("frame %d rejected: %v", frame, err)
			}
		}
		if err := tracker.Admit(0); err == nil {
			t.Fatal("frame overflow admitted")
		}
	})

	t.Run("bytes", func(t *testing.T) {
		now = started
		budget := defaultWebSocketProbeBudget()
		tracker := newTracker(budget)
		if err := tracker.Admit(budget.MaxBytes); err != nil {
			t.Fatalf("exact byte budget rejected: %v", err)
		}
		if err := tracker.Admit(1); err == nil {
			t.Fatal("byte overflow admitted")
		}
	})
}

func TestProbeBuffersOpaqueFramesThroughFirstResponseCreateInOrder(t *testing.T) {
	frames := []struct {
		messageType websocket.MessageType
		data        []byte
	}{
		{websocket.MessageText, []byte(`{"type":"future.event","sequence":1}`)},
		{websocket.MessageBinary, []byte{0x00, 0xff, 0x7f}},
		{websocket.MessageText, []byte(`{"type":"response.create","response":{"model":"gpt-5-realtime"}}`)},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("accept probe client: %v", err)
			return
		}
		defer connection.CloseNow()
		for _, frame := range frames {
			if err := connection.Write(request.Context(), frame.messageType, frame.data); err != nil {
				t.Errorf("write probe frame: %v", err)
				return
			}
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, wsURL(server), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	orchestrator := &WebSocketSessionOrchestrator{
		apiType: APITypeCodex, info: RequestInfo{Model: ModelUnknown}, clientConn: client,
		replayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
		lifecycle:    newWebSocketLifecycleState(),
		probeBudget:  defaultWebSocketProbeBudget(),
		probeNow:     time.Now,
	}

	session, modelName, outcome := orchestrator.probeClientSelectionContext(ctx)
	if session != nil || modelName != "gpt-5-realtime" || outcome != webSocketSelectionProbeOutcomeObservedUsableModel {
		t.Fatalf("probe result session=%#v model=%q outcome=%q", session, modelName, outcome)
	}
	snapshot := orchestrator.replayBuffer.Snapshot()
	if !snapshot.Enabled || len(snapshot.Messages) != len(frames) {
		t.Fatalf("probe buffer = %#v", snapshot)
	}
	for index, frame := range frames {
		buffered := snapshot.Messages[index]
		if buffered.MessageType != frame.messageType || !bytes.Equal(buffered.Data, frame.data) || buffered.Delivered {
			t.Fatalf("buffered frame %d = %#v", index, buffered)
		}
	}
}

func TestReplayBufferStoresPermitDecisionAndNeverReclassifies(t *testing.T) {
	buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
	if index := buffer.RecordDecision(websocket.MessageText, []byte("rejected"), false, requestcapture.MessageLineage{}, webSocketPreWriteDecision{
		Action: webSocketPreWriteActionReject, ReplayEligible: true,
	}); index != invalidWebSocketReplayMessageIndex {
		t.Fatalf("rejected frame entered buffer at %d", index)
	}
	if index := buffer.RecordDecision(websocket.MessageText, []byte("connection-bound"), false, requestcapture.MessageLineage{}, webSocketPreWriteDecision{
		Action: webSocketPreWriteActionForward, ReplayEligible: false, CurrentConnection: true,
	}); index != invalidWebSocketReplayMessageIndex {
		t.Fatalf("connection-bound frame entered buffer at %d", index)
	}

	payloads := [][]byte{[]byte("opaque-one"), []byte("opaque-two")}
	prepareOrder := make([]int, 0, len(payloads))
	commitOrder := make([]int, 0, len(payloads))
	originalCommits := 0
	for index, payload := range payloads {
		index := index
		decision := replayableClientFrameDecision()
		decision.TraceContext = webSocketClientFrameTrace{Kind: "opaque", Decision: "forward"}
		decision.OnWriteConfirmed = func() error {
			originalCommits++
			return nil
		}
		decision.PrepareReplay = func() webSocketPreWriteDecision {
			prepareOrder = append(prepareOrder, index)
			replay := replayableClientFrameDecision()
			replay.OnWriteConfirmed = func() error {
				commitOrder = append(commitOrder, index)
				return nil
			}
			return replay
		}
		if got := buffer.RecordDecision(websocket.MessageText, payload, false, requestcapture.MessageLineage{}, decision); got != index {
			t.Fatalf("recorded frame %d at %d", index, got)
		}
	}

	upstream, peer := newCodexRelayPair(t)
	rawClassifierCalls := 0
	orchestrator := &WebSocketSessionOrchestrator{
		replayBuffer: buffer,
		lifecycle:    newWebSocketLifecycleState(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	replayed, attempted, err := orchestrator.replayBufferedMessages(ctx, upstream, nil, webSocketRelayOptions{
		PreWriteToUpstream: func(webSocketPreWriteContext) webSocketPreWriteDecision {
			rawClassifierCalls++
			return replayableClientFrameDecision()
		},
	})
	if err != nil || !attempted || replayed != int64(len(payloads[0])+len(payloads[1])) {
		t.Fatalf("replay bytes=%d attempted=%v err=%v", replayed, attempted, err)
	}
	if rawClassifierCalls != 0 || originalCommits != 0 || !equalInts(prepareOrder, []int{0, 1}) || !equalInts(commitOrder, []int{0, 1}) {
		t.Fatalf("replay decisions raw=%d original=%d prepare=%v commit=%v", rawClassifierCalls, originalCommits, prepareOrder, commitOrder)
	}
	for index, want := range payloads {
		messageType, got, err := peer.Read(ctx)
		if err != nil || messageType != websocket.MessageText || !bytes.Equal(got, want) {
			t.Fatalf("replayed frame %d type=%d data=%q err=%v", index, messageType, got, err)
		}
	}
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestWebSocketRecoveryAdaptersUseSharedContract(t *testing.T) {
	for _, condition := range []codexrecovery.Condition{
		codexrecovery.ConditionStateConflict,
		codexrecovery.ConditionReconnectRequired,
		codexrecovery.ConditionNewThreadRequired,
		codexrecovery.ConditionStateStoreUnavailable,
		codexrecovery.ConditionProtocolInvalid,
		codexrecovery.ConditionInternalFailure,
	} {
		t.Run(string(condition), func(t *testing.T) {
			root := codexrecovery.Mark(condition, errors.New("root cause"))
			preUpgrade := codexrecovery.Classify(root, codexrecovery.PhaseWebSocketPreUpgrade)
			accepted := codexrecovery.Classify(root, codexrecovery.PhaseWebSocketAccepted)
			if got := websocketCloseStatusForCodexFailure(root); got != accepted.WebSocketCloseCode() {
				t.Fatalf("accepted close = %d, want %d", got, accepted.WebSocketCloseCode())
			}
			recorder := httptest.NewRecorder()
			(&Gateway{logger: zap.NewNop()}).writeCodexWebSocketFailureForOperation(recorder, "stable-operation", root)
			if recorder.Code != preUpgrade.HTTPStatus() || !bytes.Contains(recorder.Body.Bytes(), []byte(preUpgrade.ErrorCode())) {
				t.Fatalf("pre-upgrade response = status:%d body:%q, want status:%d code:%q",
					recorder.Code, recorder.Body.String(), preUpgrade.HTTPStatus(), preUpgrade.ErrorCode())
			}
		})
	}
}

func TestConnectionBoundWriteFailureRequiresReconnectWithoutReplacement(t *testing.T) {
	writeErr := errors.New("physical write failed")
	reconnectErr := clientFrameWriteError(webSocketPreWriteDecision{CurrentConnection: true}, writeErr)
	decision := codexrecovery.Classify(reconnectErr, codexrecovery.PhaseWebSocketAccepted)
	if decision.Condition() != codexrecovery.ConditionReconnectRequired || decision.WebSocketCloseCode() != websocket.StatusServiceRestart {
		t.Fatalf("connection-bound write decision = condition:%q close:%d", decision.Condition(), decision.WebSocketCloseCode())
	}
	orchestrator := &WebSocketSessionOrchestrator{}
	attempt := WebSocketAttemptResult{Result: &WebSocketResult{
		Err: reconnectErr, TerminalCause: model.TerminalUpstreamTransportError,
	}}
	if orchestrator.shouldSwitchProvider(attempt) {
		t.Fatal("connection-bound write failure allowed provider replacement")
	}
	if got := clientFrameWriteError(replayableClientFrameDecision(), writeErr); !errors.Is(got, writeErr) ||
		codexrecovery.Classify(got, codexrecovery.PhaseWebSocketAccepted).Condition() == codexrecovery.ConditionReconnectRequired {
		t.Fatalf("ordinary write was reclassified as reconnect: %v", got)
	}
}
