package websocketproxy

import (
	"context"
	"errors"
	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestRelayInitialReadFailurePrecedesReadyClientFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client, clientPeer := newCodexRelayPair(t)
	upstream, upstreamPeer := newCodexRelayPair(t)
	original := errors.New("read failed during replay")
	messages := make(chan webSocketInitialReadResult, 1)
	messages <- webSocketInitialReadResult{messageType: websocket.MessageText, data: []byte("next request")}
	result := (&WebSocketForwarder{}).relay(ctx, client, upstream, webSocketRelayOptions{
		InitialUpstreamRead:               retainedWebSocketRead(webSocketInitialReadResult{err: original}),
		ClientReadHandoff:                 newWebSocketClientReadHandoff(messages),
		SkipPreVisibleWindow:              true,
		PreserveClientOnPreVisibleFailure: true,
	})
	if result == nil || !errors.Is(result.Err, original) || result.FailureOperation != webSocketRelayFailureOperationRead || result.BytesClientToUpstream != 0 {
		t.Fatalf("original failure replaced by next client delivery: %#v", result)
	}
	select {
	case message := <-messages:
		if string(message.data) != "next request" {
			t.Fatal("pending message changed")
		}
	default:
		t.Fatal("next request was consumed by an already failed attempt")
	}
	if err := clientPeer.Write(ctx, websocket.MessageText, []byte("still connected")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := client.Read(ctx); err != nil || string(data) != "still connected" {
		t.Fatalf("downstream was not preserved: %s %v", data, err)
	}
	if _, _, err := upstreamPeer.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("failed upstream was not closed promptly: %v", err)
	}
}
