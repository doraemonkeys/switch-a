package websocketproxy

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

func TestPlan10ReviewLegacyUnevenCapacityEnvelope(t *testing.T) {
	for _, count := range []int{1, 3, 65, 127, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			buffer := newPreVisibleClientMessageBuffer(0)
			// A large final frame and spare caller capacity exercise a different
			// allocation shape from the equal-sized legacy-envelope fixture.
			for index := range count {
				size := 0
				if index == count-1 {
					size = legacyWebSocketReplayPayloadBytes
				}
				data := make([]byte, size, size+1024)
				if got := buffer.Record(websocket.MessageBinary, data, false); got != index {
					t.Fatalf("legacy frame %d/%d rejected: %+v", index, count, buffer.Status())
				}
			}
			snapshot := buffer.Snapshot()
			defer snapshot.Release()
			if !snapshot.Enabled || len(snapshot.Messages) != count {
				t.Fatalf("legacy snapshot unavailable: %+v", buffer.Status())
			}
			if got := buffer.Status(); got.PayloadBytes != legacyWebSocketReplayPayloadBytes || got.RetainedBytes > buffer.limitBytes {
				t.Fatalf("capacity accounting: %+v", got)
			}
		})
	}
}

func TestPlan10ReviewRepeatedReplayKeepsOriginalLineageAndLiveTail(t *testing.T) {
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{ID: "provider", Name: "Provider"}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "plan10-independent-review"})
	buffer := newPreVisibleClientMessageBuffer(0)
	o := &WebSocketSessionOrchestrator{
		replayBuffer: buffer, lifecycle: newWebSocketLifecycleState(),
		capture: gateway, captureParticipates: true,
	}
	const initialCount = 130
	frames := make([][]byte, initialCount)
	for index := range initialCount {
		frames[index] = []byte(fmt.Sprintf("frame-%d", index))
		o.recordSelectionProbeFrame(websocket.MessageText, frames[index], replayableClientFrameDecision())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var originalIDs []string
	for attempt := range 3 {
		upstream, peer := newCodexRelayPair(t)
		record := beginWebSocketCaptureTestRecord(gateway, "provider", requestcapture.SelectionModeReplacement)
		options := webSocketRelayOptions{GatewayCapture: gateway, Capture: record, CaptureMode: captureModePayload}
		if _, _, err := o.replayBufferedMessages(ctx, upstream, nil, options); err != nil {
			t.Fatal(err)
		}
		for index, want := range frames {
			kind, got, err := peer.Read(ctx)
			expectedKind := websocket.MessageText
			if index == initialCount {
				expectedKind = websocket.MessageBinary
			}
			if err != nil || kind != expectedKind || !bytes.Equal(got, want) {
				t.Fatalf("attempt %d frame %d: kind=%v payload=%q err=%v", attempt, index, kind, got, err)
			}
		}
		// Physical delivery between replacements must join the retained prefix,
		// without changing the source IDs of frames delivered by older attempts.
		if attempt == 1 {
			tail := []byte{0, 1, 255}
			progress := (&WebSocketForwarder{}).relayPreVisibleClientMessage(ctx, upstream,
				webSocketRelayOptions{GatewayCapture: gateway, Capture: record, CaptureMode: captureModePayload,
					PreVisibleReplayBuffer: buffer}.withCaptureHooks(),
				o.lifecycle, webSocketInitialReadResult{messageType: websocket.MessageBinary, data: tail}, nil, nil)
			if progress.Result != nil {
				t.Fatalf("live tail failed: %+v", progress.Result)
			}
			kind, got, err := peer.Read(ctx)
			if err != nil || kind != websocket.MessageBinary || !bytes.Equal(got, tail) {
				t.Fatalf("live tail: kind=%v data=%v err=%v", kind, got, err)
			}
			frames = append(frames, tail)
		}
		record.Finish(requestcapture.Outcome{SourceCompletion: requestcapture.SourceCompletionComplete,
			TerminationReason: requestcapture.TerminationReasonWebSocketClose})
		detail := getWebSocketCaptureTestDetail(t, manager, session, record.ID())
		if len(detail.WebSocket.Messages) != len(frames) {
			t.Fatalf("attempt %d captured %d frames, want %d", attempt, len(detail.WebSocket.Messages), len(frames))
		}
		for index, message := range detail.WebSocket.Messages {
			firstDelivery := attempt == 0 || (attempt == 1 && index == initialCount)
			if firstDelivery {
				if message.Source != requestcapture.MessageSourceLive {
					t.Fatalf("first delivery marked %q", message.Source)
				}
				originalIDs = append(originalIDs, message.MessageID)
			} else if message.Source != requestcapture.MessageSourceReplay || message.SourceMessageID != originalIDs[index] {
				t.Fatalf("attempt %d frame %d lost original lineage: %+v", attempt, index, message)
			}
		}
		if status := buffer.Status(); status.SnapshotBytes != 0 || status.State != webSocketReplayable {
			t.Fatalf("attempt %d retention: %+v", attempt, status)
		}
	}
	gateway.Finish(requestcapture.GatewayOutcome{})
}
