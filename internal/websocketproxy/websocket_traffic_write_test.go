package websocketproxy

import (
	"bytes"
	"errors"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

func TestRelayTrafficCountsOnlyConfirmedPhysicalPayload(t *testing.T) {
	for _, direction := range []requestcapture.MessageDirection{
		requestcapture.MessageDirectionClientToUpstream, requestcapture.MessageDirectionUpstreamToClient,
	} {
		for _, failed := range []bool{false, true} {
			name := string(direction)
			if failed {
				name += "/write_failure"
			} else {
				name += "/confirmed_then_storage_failure"
			}
			t.Run(name, func(t *testing.T) {
				received := make(chan webSocketReplayMessage, 1)
				server := newRecordingWSServer(t, received)
				defer server.Close()
				connection := connectWSClient(t, t.Context(), wsURL(server))
				defer connection.CloseNow()
				if failed {
					_ = connection.CloseNow()
				}
				tracker := &observerTestLiveTraffic{}
				observer := newBytesTrackingObserver(nil, tracker)
				payload := []byte("transformed payload")
				storageErr := errors.New("storage rejected after physical write")
				processor := webSocketRelayMessageProcessor{
					ctx: t.Context(), dst: connection, dstPeer: webSocketPeerUpstream,
					direction: direction,
					options:   (webSocketRelayOptions{Observer: observer}).withCaptureHooks(),
					observe: func(typ websocket.MessageType, data []byte) {
						if direction == requestcapture.MessageDirectionClientToUpstream {
							observer.ObserveClientMessage(typ, data)
						} else {
							observer.ObserveUpstreamMessage(typ, data)
						}
						if tracker.BytesSent.Load() != 0 || tracker.BytesReceived.Load() != 0 {
							t.Error("read observation prematurely reported a transfer")
						}
					},
					preWrite: func(websocket.MessageType, []byte) webSocketPreWriteDecision {
						return webSocketPreWriteDecision{
							Action: webSocketPreWriteActionForward, PreparedPayload: payload,
							OnWriteConfirmed: func() error { return storageErr },
						}
					},
				}
				_, _, err := processor.process(websocket.MessageText, []byte("original"))
				if err == nil {
					t.Fatal("expected write or post-write storage failure")
				}
				wantBytes, wantMessages := int64(len(payload)), int64(1)
				if failed {
					wantBytes, wantMessages = 0, 0
				} else {
					if !errors.Is(err, storageErr) {
						t.Fatal(err)
					}
					message := <-received
					if !bytes.Equal(message.Data, payload) {
						t.Fatal("physical payload changed")
					}
				}
				gotBytes, gotMessages := tracker.BytesSent.Load(), tracker.MsgsSent.Load()
				if direction == requestcapture.MessageDirectionUpstreamToClient {
					gotBytes, gotMessages = tracker.BytesReceived.Load(), tracker.MsgsReceived.Load()
				}
				if gotBytes != wantBytes || gotMessages != wantMessages {
					t.Fatalf("traffic = %d bytes/%d messages, want %d/%d", gotBytes, gotMessages, wantBytes, wantMessages)
				}
			})
		}
	}
}
