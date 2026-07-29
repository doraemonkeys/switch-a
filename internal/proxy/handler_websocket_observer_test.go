package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
)

func TestBuildWebSocketDialHeaders_FiltersSecWebSocketHeaders(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/responses", nil)
	r.Header.Set("OpenAI-Beta", "realtime=v1")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Extensions", "permessage-deflate")
	r.Header.Set("Sec-WebSocket-Protocol", "graphql-ws")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")

	provider := &model.Provider{APIKey: "sk-key", AuthMode: "bearer"}
	headers := buildWebSocketDialHeaders(r, provider, "codex", "auto")

	// Business headers should pass through.
	if got := headers.Get("OpenAI-Beta"); got != "realtime=v1" {
		t.Errorf("OpenAI-Beta = %q, want 'realtime=v1'", got)
	}

	// WebSocket handshake headers must NOT be forwarded.
	for _, h := range []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol"} {
		if got := headers.Get(h); got != "" {
			t.Errorf("%s should be filtered, got %q", h, got)
		}
	}
}

// TestIsWebSocketHandshakeHeader tests the handshake header classification.
func TestIsWebSocketHandshakeHeader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key      string
		expected bool
	}{
		{"Sec-WebSocket-Key", true},
		{"Sec-Websocket-Version", true},
		{"Sec-WebSocket-Extensions", true},
		{"Sec-WebSocket-Protocol", true},
		{"sec-websocket-key", true},
		{"Authorization", false},
		{"OpenAI-Beta", false},
		{"Sec-Fetch-Mode", false}, // 14 chars prefix doesn't match
		{"Sec-", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			got := isWebSocketHandshakeHeader(tt.key)
			if got != tt.expected {
				t.Errorf("isWebSocketHandshakeHeader(%q) = %v, want %v", tt.key, got, tt.expected)
			}
		})
	}
}

func TestBytesTrackingObserver_CountsAndTimestamp(t *testing.T) {
	t.Parallel()

	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(nil, tracker)

	// Simulate client → upstream messages.
	obs.ObserveClientMessage(websocket.MessageText, []byte("hello")) // 5 bytes
	obs.ObserveClientMessage(websocket.MessageText, []byte("world")) // 5 bytes

	// Simulate upstream → client messages.
	obs.ObserveUpstreamMessage(websocket.MessageText, []byte("response data 1234567890")) // 24 bytes

	if got := tracker.BytesSent.Load(); got != 10 {
		t.Errorf("BytesSent = %d, want 10", got)
	}
	if got := tracker.MsgsSent.Load(); got != 2 {
		t.Errorf("MsgsSent = %d, want 2", got)
	}
	if got := tracker.BytesReceived.Load(); got != 24 {
		t.Errorf("BytesReceived = %d, want 24", got)
	}
	if got := tracker.MsgsReceived.Load(); got != 1 {
		t.Errorf("MsgsReceived = %d, want 1", got)
	}
	if got := tracker.LastActivityAt.Load(); got == 0 {
		t.Error("LastActivityAt should be non-zero after messages")
	}
}

func TestBytesTrackingObserver_DelegatesToInner(t *testing.T) {
	t.Parallel()

	var clientCalls, upstreamCalls int
	inner := &stubObserver{
		onClient:   func() { clientCalls++ },
		onUpstream: func() { upstreamCalls++ },
	}
	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(inner, tracker)

	obs.ObserveClientMessage(websocket.MessageText, []byte("a"))
	obs.ObserveUpstreamMessage(websocket.MessageText, []byte("b"))

	if clientCalls != 1 {
		t.Errorf("inner.ObserveClientMessage called %d times, want 1", clientCalls)
	}
	if upstreamCalls != 1 {
		t.Errorf("inner.ObserveUpstreamMessage called %d times, want 1", upstreamCalls)
	}
}

func TestBytesTrackingObserver_SnapshotDelegatesToInner(t *testing.T) {
	t.Parallel()

	inner := &stubObserver{
		snapshot: WebSocketObservation{Model: "gpt-5"},
	}
	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(inner, tracker)

	snap := obs.Snapshot()
	if snap.Model != "gpt-5" {
		t.Errorf("Snapshot().Model = %q, want %q", snap.Model, "gpt-5")
	}
}

func TestBytesTrackingObserver_NilInner(t *testing.T) {
	t.Parallel()

	tracker := &LiveBytesTracker{}
	obs := newBytesTrackingObserver(nil, tracker)

	// Should not panic with nil inner observer.
	obs.ObserveClientMessage(websocket.MessageText, []byte("data"))
	obs.ObserveUpstreamMessage(websocket.MessageBinary, []byte("data"))
	snap := obs.Snapshot()

	if snap.Model != "" {
		t.Errorf("expected empty Model from nil inner Snapshot, got %q", snap.Model)
	}
	if obs.HasSemanticObservation() {
		t.Fatal("nil inner observer must not report semantic observation support")
	}
	if tracker.MsgsSent.Load() != 1 || tracker.MsgsReceived.Load() != 1 {
		t.Error("counters should still increment with nil inner")
	}
}

// stubObserver is a minimal test double for WebSocketMessageObserver.
type stubObserver struct {
	onClient   func()
	onUpstream func()
	snapshot   WebSocketObservation
}

func (s *stubObserver) ObserveClientMessage(_ websocket.MessageType, _ []byte) {
	if s.onClient != nil {
		s.onClient()
	}
}

func (s *stubObserver) ObserveUpstreamMessage(_ websocket.MessageType, _ []byte) {
	if s.onUpstream != nil {
		s.onUpstream()
	}
}

func (s *stubObserver) Snapshot() WebSocketObservation {
	return s.snapshot
}

func (s *stubObserver) ParseDegraded() bool {
	return s.snapshot.ParseDegraded
}

func (s *stubObserver) HasSemanticObservation() bool {
	return true
}
