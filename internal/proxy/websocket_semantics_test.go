package proxy

import (
	"testing"

	"github.com/coder/websocket"
)

func TestCodexWebSocketMessageObserver_CapturesUpstreamErrorWithNestedErrorType(t *testing.T) {
	t.Parallel()

	observer := newCodexWebSocketMessageObserver("gpt-5.4", nil, nil, nil)
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
	if observation.SessionCommitted {
		t.Fatal("semantic error must not mark the session committed")
	}
}

func TestCodexWebSocketMessageObserver_CommitsOnResponseCreated(t *testing.T) {
	t.Parallel()

	var updates []WebSocketObservation
	var commits []WebSocketObservation
	observer := newCodexWebSocketMessageObserver(
		ModelUnknown,
		nil,
		func(observation WebSocketObservation) {
			updates = append(updates, observation)
		},
		func(observation WebSocketObservation) {
			commits = append(commits, observation)
		},
	)

	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"session.created","session":{"model":"gpt-5.4"}}`))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4"}}`))

	observation := observer.Snapshot()
	if !observation.SessionCommitted {
		t.Fatal("expected SessionCommitted=true after response.created")
	}
	if observation.CommitEventType != webSocketEventResponseCreated {
		t.Fatalf("CommitEventType = %q, want %q", observation.CommitEventType, webSocketEventResponseCreated)
	}
	if observation.Model != "gpt-5.4" {
		t.Fatalf("Model = %q, want %q", observation.Model, "gpt-5.4")
	}
	if observation.ParseDegraded {
		t.Fatal("ParseDegraded must stay false for valid semantic frames")
	}
	if len(commits) != 1 {
		t.Fatalf("commit callback count = %d, want 1", len(commits))
	}
	if !commits[0].SessionCommitted {
		t.Fatal("commit callback must observe committed state")
	}
	if len(updates) == 0 {
		t.Fatal("expected onUpdate to publish semantic changes")
	}
}

func TestCodexWebSocketMessageObserver_ParseDegradedOnInvalidUpstreamJSON(t *testing.T) {
	t.Parallel()

	observer := newCodexWebSocketMessageObserver("gpt-5.4", nil, nil, nil)

	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.created"`))

	observation := observer.Snapshot()
	if !observation.ParseDegraded {
		t.Fatal("expected ParseDegraded=true after invalid upstream JSON")
	}
	if !observer.ParseDegraded() {
		t.Fatal("ParseDegraded accessor must mirror snapshot state")
	}
	if observation.SessionCommitted {
		t.Fatal("parse degradation alone must not mark the session committed")
	}
}
