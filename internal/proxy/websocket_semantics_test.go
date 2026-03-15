package proxy

import (
	"testing"

	"github.com/coder/websocket"
)

func TestCodexWebSocketMessageObserver_CapturesUpstreamErrorWithNestedErrorType(t *testing.T) {
	t.Parallel()

	observer := newCodexWebSocketMessageObserver("gpt-5.4", nil, nil)
	payload := []byte(`{"error":{"message":"Model 'gpt-5.4' is not allowed","type":"model_not_allowed"},"status":403,"type":"error"}`)

	observer.ObserveUpstreamMessage(websocket.MessageText, payload)

	observation := observer.Snapshot()
	if observation.UpstreamError == nil {
		t.Fatal("expected UpstreamError to be captured")
	}
	if observation.UpstreamError.EventType != "model_not_allowed" {
		t.Fatalf("EventType = %q, want %q", observation.UpstreamError.EventType, "model_not_allowed")
	}
	if observation.UpstreamError.StatusCode != 403 {
		t.Fatalf("StatusCode = %d, want 403", observation.UpstreamError.StatusCode)
	}
	if observation.UpstreamError.Message != "Model 'gpt-5.4' is not allowed" {
		t.Fatalf("Message = %q, want %q", observation.UpstreamError.Message, "Model 'gpt-5.4' is not allowed")
	}
	if observation.UpstreamError.Raw != string(payload) {
		t.Fatalf("Raw = %q, want payload", observation.UpstreamError.Raw)
	}
}
