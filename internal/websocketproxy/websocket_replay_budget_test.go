package websocketproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

func TestReplayRetentionCapacityAndSnapshotLifetime(t *testing.T) {
	b := newPreVisibleClientMessageBuffer(64 + 4*webSocketReplayDescriptorBytes)
	input := []byte("original")
	if b.Record(websocket.MessageText, input, false) != 0 {
		t.Fatal("record failed")
	}
	input[0] = 'X'
	initial := b.Status()
	if initial.PayloadBytes != 8 || initial.RetainedBytes != 8+webSocketReplayDescriptorBytes {
		t.Fatalf("status=%+v", initial)
	}
	snapshot := b.Snapshot()
	if !snapshot.Enabled || string(snapshot.Messages[0].Data) != "original" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if &snapshot.Messages[0].Data[0] != &b.messages[0].Data[0] {
		t.Fatal("snapshot copied immutable payload")
	}
	if b.Status().SnapshotBytes != webSocketReplayDescriptorBytes {
		t.Fatal("snapshot descriptors not counted")
	}
	b.MarkDelivered(0, requestcapture.MessageLineage{})
	if snapshot.Messages[0].Delivered {
		t.Fatal("snapshot descriptors changed")
	}
	b.CloseReplay(webSocketReplayParseDegraded)
	if b.Status().RetainedBytes != 8+webSocketReplayDescriptorBytes {
		t.Fatalf("leased payload lost accounting: %+v", b.Status())
	}
	if string(snapshot.Messages[0].Data) != "original" {
		t.Fatal("closure corrupted leased bytes")
	}
	snapshot.Release()
	snapshot.Release()
	if status := b.Status(); status.RetainedBytes != 0 || status.State != webSocketReplayParseDegraded {
		t.Fatalf("release=%+v", status)
	}
	b.CloseReplay(webSocketReplayVisibilityClosed)
	if b.Status().State != webSocketReplayParseDegraded {
		t.Fatal("original terminal reason overwritten")
	}
	if allocations := testing.AllocsPerRun(100, func() { b.Status() }); allocations != 0 {
		t.Fatalf("status allocated %f", allocations)
	}
}

func TestReplayBudgetKeepsLegacyEnvelopeAndAllowsManySmallFrames(t *testing.T) {
	t.Run("legacy full payload", func(t *testing.T) {
		b := newPreVisibleClientMessageBuffer(0)
		for index := range 128 {
			if b.Record(websocket.MessageBinary, make([]byte, 4*1024*1024/128), false) != index {
				t.Fatalf("legacy frame %d rejected", index)
			}
		}
		snapshot := b.Snapshot()
		defer snapshot.Release()
		if !snapshot.Enabled || len(snapshot.Messages) != 128 {
			t.Fatal("legacy snapshot unavailable")
		}
		if b.Status().RetainedBytes > b.limitBytes {
			t.Fatal("retention exceeded budget")
		}
	})
	t.Run("more than 128 frames", func(t *testing.T) {
		b := newPreVisibleClientMessageBuffer(0)
		for index := range 512 {
			if b.Record(websocket.MessageText, []byte("x"), false) != index {
				t.Fatalf("small frame %d rejected", index)
			}
		}
		snapshot := b.Snapshot()
		defer snapshot.Release()
		if !snapshot.Enabled || len(snapshot.Messages) != 512 {
			t.Fatal("small messages not replayable")
		}
	})
	t.Run("empty descriptors and snapshot reservation", func(t *testing.T) {
		b := newPreVisibleClientMessageBuffer(4 * webSocketReplayDescriptorBytes)
		for index := range 2 {
			if b.Record(websocket.MessageText, nil, false) != index {
				t.Fatal("empty frame rejected")
			}
		}
		snapshot := b.Snapshot()
		if b.Status().RetainedBytes != 4*webSocketReplayDescriptorBytes {
			t.Fatalf("empty cost=%+v", b.Status())
		}
		if b.Record(websocket.MessageText, nil, false) != invalidWebSocketReplayMessageIndex {
			t.Fatal("descriptor exhaustion ignored")
		}
		if b.Status().State != webSocketReplayBudgetExhausted {
			t.Fatal("wrong exhaustion reason")
		}
		if len(snapshot.Messages) != 2 {
			t.Fatal("active snapshot lost prefix")
		}
		snapshot.Release()
		if b.Status().RetainedBytes != 0 {
			t.Fatal("snapshot leaked retention")
		}
		if b.Snapshot().Enabled {
			t.Fatal("partially replayable prefix exposed")
		}
	})
	t.Run("multiple snapshot leases", func(t *testing.T) {
		b := newPreVisibleClientMessageBuffer(3*webSocketReplayDescriptorBytes + 1)
		b.Record(websocket.MessageText, []byte("x"), false)
		first := b.Snapshot()
		defer first.Release()
		second := b.Snapshot()
		defer second.Release()
		if !first.Enabled || !second.Enabled || b.Status().PayloadBytes != 1 {
			t.Fatal("shared payload double counted")
		}
		third := b.Snapshot()
		defer third.Release()
		if third.Enabled || b.Status().State != webSocketReplayBudgetExhausted {
			t.Fatal("snapshot budget not enforced")
		}
	})
}

func TestProbeRetainsFirstDeliveryAfterReplayExhaustion(t *testing.T) {
	gateway, client := newCodexRelayPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	frames := [][]byte{[]byte(`{"type":"future.event","sequence":1}`), []byte(`{"type":"future.event","sequence":2}`), []byte(`{"type":"response.create","response":{"model":"gpt-test"}}`)}
	sent := make(chan error, 1)
	go func() {
		for _, frame := range frames {
			if err := client.Write(ctx, websocket.MessageText, frame); err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
	}()
	o := &WebSocketSessionOrchestrator{apiType: APITypeCodex, info: RequestInfo{Model: ModelUnknown}, clientConn: gateway,
		replayBuffer: newPreVisibleClientMessageBuffer(1), lifecycle: newWebSocketLifecycleState()}
	result, modelName, outcome := o.probeClientSelectionContext(ctx)
	if result != nil || modelName != "gpt-test" || outcome != webSocketSelectionProbeOutcomeObservedUsableModel {
		t.Fatalf("probe: %v %q %q", result, modelName, outcome)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if o.replayBuffer.Status().State != webSocketReplayBudgetExhausted || len(o.pendingDelivery) != len(frames) {
		t.Fatal("queue coupled to replay")
	}
	upstream, peer := newCodexRelayPair(t)
	if _, _, err := o.replayBufferedMessages(ctx, upstream, nil, webSocketRelayOptions{}); err != nil {
		t.Fatal(err)
	}
	for index, want := range frames {
		kind, got, err := peer.Read(ctx)
		if err != nil || kind != websocket.MessageText || !bytes.Equal(got, want) {
			t.Fatalf("delivery %d: %v %q %v", index, kind, got, err)
		}
	}
	if len(o.pendingDelivery) != 0 {
		t.Fatal("first-delivery queue not released")
	}
	if _, _, err := o.replayBufferedMessages(ctx, upstream, nil, webSocketRelayOptions{}); err == nil || !strings.Contains(err.Error(), "budget_exhausted") {
		t.Fatalf("partial replay attempted: %v", err)
	}
	if err := client.Write(ctx, websocket.MessageBinary, []byte("live")); err != nil {
		t.Fatal(err)
	}
	kind, got, err := o.sessionClientReadHandoff().Read(ctx, ctx, gateway)
	if err != nil || kind != websocket.MessageBinary || string(got) != "live" {
		t.Fatalf("live handoff: %v %q %v", kind, got, err)
	}
}

func TestProbeBudgetExhaustionPreservesHealthyRead(t *testing.T) {
	for _, test := range []struct {
		name    string
		budget  webSocketProbeBudget
		initial []byte
	}{
		{"duration", webSocketProbeBudget{Duration: time.Millisecond, MaxWorkUnits: 2, MaxDecodedBytes: 1024}, nil},
		{"decoded bytes", webSocketProbeBudget{Duration: time.Second, MaxWorkUnits: 2, MaxDecodedBytes: 1}, []byte("opaque")},
		{"work units", webSocketProbeBudget{Duration: time.Second, MaxWorkUnits: 1, MaxDecodedBytes: 1024}, []byte(`{"type":"future.event"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			gateway, client := newCodexRelayPair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if test.initial != nil {
				if err := client.Write(ctx, websocket.MessageText, test.initial); err != nil {
					t.Fatal(err)
				}
			}
			o := &WebSocketSessionOrchestrator{apiType: APITypeCodex, info: RequestInfo{Model: ModelUnknown}, clientConn: gateway,
				replayBuffer: newPreVisibleClientMessageBuffer(0), lifecycle: newWebSocketLifecycleState(), probeBudget: test.budget}
			result, _, outcome := o.probeClientSelectionContext(ctx)
			if result != nil || outcome != webSocketSelectionProbeOutcomeCompletedWithoutUsableModel {
				t.Fatalf("budget ended connection: %v %s", result, outcome)
			}
			if test.name == "work units" {
				if len(o.pendingDelivery) != 1 {
					t.Fatal("work-budget frame lost")
				}
			}
			if test.name == "decoded bytes" {
				_, got, err := o.sessionClientReadHandoff().Read(ctx, ctx, gateway)
				if err != nil || !bytes.Equal(got, test.initial) {
					t.Fatalf("crossing frame lost: %q %v", got, err)
				}
			}
			if err := client.Write(ctx, websocket.MessageText, []byte("after-probe")); err != nil {
				t.Fatal(err)
			}
			_, got, err := o.sessionClientReadHandoff().Read(ctx, ctx, gateway)
			if err != nil || string(got) != "after-probe" {
				t.Fatalf("read destroyed by probe: %q %v", got, err)
			}
		})
	}
}

func TestReplayUnavailableReasonSurvivesEvidence(t *testing.T) {
	result := &WebSocketResult{ReplayStatus: webSocketReplayStatus{State: webSocketReplayBudgetExhausted}}
	evidence := buildWebSocketEvidence(webSocketGatewayEvidenceInput{}, result, nil, false, "")
	if evidence == nil {
		t.Fatal("replay diagnostic discarded")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(*evidence), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["replay"].(map[string]any)["state"] != "budget_exhausted" {
		t.Fatal(*evidence)
	}
}
func TestReplayCoverageUsesObservedMessageTimes(t *testing.T) {
	b := newPreVisibleClientMessageBuffer(0)
	observed := time.Unix(100, 0)
	b.now = func() time.Time { return observed }
	b.Record(websocket.MessageText, nil, false)
	observed = observed.Add(275 * time.Millisecond)
	b.Record(websocket.MessageBinary, nil, false)
	b.CloseReplay(webSocketReplayBudgetExhausted)
	if status := b.Status(); status.CoverageDurationMs != 275 || status.MessageCount != 2 {
		t.Fatalf("retained coverage=%+v", status)
	}
}

func TestPendingDeliveryKeepsSharedPayloadAccountedAfterReplayCloses(t *testing.T) {
	b := newPreVisibleClientMessageBuffer(0)
	o := &WebSocketSessionOrchestrator{replayBuffer: b, lifecycle: newWebSocketLifecycleState()}
	o.recordSelectionProbeFrame(websocket.MessageText, []byte("pending"), replayableClientFrameDecision())
	b.CloseReplay(webSocketReplayBudgetExhausted)
	if status := b.Status(); status.PayloadBytes != 7 {
		t.Fatalf("pending payload not retained: %+v", status)
	}
	upstream, peer := newCodexRelayPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := o.replayBufferedMessages(ctx, upstream, nil, webSocketRelayOptions{}); err != nil {
		t.Fatal(err)
	}
	_, got, err := peer.Read(ctx)
	if err != nil || string(got) != "pending" {
		t.Fatalf("delivery=%q %v", got, err)
	}
	if b.Status().RetainedBytes != 0 {
		t.Fatalf("pending owner leaked: %+v", b.Status())
	}
}

func BenchmarkReplaySnapshot(b *testing.B) {
	buffer := newPreVisibleClientMessageBuffer(0)
	for range legacyWebSocketReplayMessageCount {
		buffer.Record(websocket.MessageBinary, make([]byte, legacyWebSocketReplayPayloadBytes/legacyWebSocketReplayMessageCount), false)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		snapshot := buffer.Snapshot()
		if !snapshot.Enabled {
			b.Fatal("snapshot unavailable")
		}
		snapshot.Release()
	}
}
