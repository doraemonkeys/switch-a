package websocketproxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
)

type observerTestLiveTraffic struct {
	BytesSent, BytesReceived, MsgsSent, MsgsReceived atomic.Int64
	LastActivityAt                                   atomic.Int64
}

func TestIsWebSocketUpgrade(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		headers  http.Header
		expected bool
	}{
		{name: "valid upgrade", headers: http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}}, expected: true},
		{name: "case insensitive", headers: http.Header{"Upgrade": {"WebSocket"}, "Connection": {"upgrade"}}, expected: true},
		{name: "connection with multiple values", headers: http.Header{"Upgrade": {"websocket"}, "Connection": {"keep-alive, Upgrade"}}, expected: true},
		{name: "missing upgrade header", headers: http.Header{"Connection": {"Upgrade"}}},
		{name: "missing connection header", headers: http.Header{"Upgrade": {"websocket"}}},
		{name: "wrong upgrade value", headers: http.Header{"Upgrade": {"h2c"}, "Connection": {"Upgrade"}}},
		{name: "empty headers", headers: http.Header{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := &http.Request{Header: tt.headers}
			if got := IsUpgrade(request); got != tt.expected {
				t.Errorf("IsUpgrade() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractWebSocketModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{name: "model in query", url: "/responses?model=gpt-4o-realtime", expected: "gpt-4o-realtime"},
		{name: "no model param", url: "/responses", expected: ModelUnknown},
		{name: "empty model param", url: "/responses?model=", expected: ModelUnknown},
		{name: "model with other params", url: "/responses?foo=bar&model=claude-4", expected: "claude-4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, tt.url, nil)
			if got := extractWebSocketModel(request); got != tt.expected {
				t.Errorf("extractWebSocketModel() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildWebSocketDialHeaders(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/responses", nil)
	request.Header.Set("OpenAI-Beta", "realtime=v1")
	request.Header.Set("X-Custom", "value")
	request.Header.Set("Authorization", "Bearer client-key")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")

	provider := &model.Provider{
		ID:                 "p1",
		AuthMode:           "bearer",
		CredentialSessions: testCredentialSessions("p1", "codex", credentialsession.KindAPIKey, "sk-provider-key"),
	}
	headers := buildWebSocketDialHeaders(request, provider, "codex", "auto")

	if got := headers.Get("Authorization"); got != "Bearer sk-provider-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-provider-key")
	}
	if got := headers.Get("OpenAI-Beta"); got != "realtime=v1" {
		t.Errorf("OpenAI-Beta = %q, want %q", got, "realtime=v1")
	}
	if got := headers.Get("X-Custom"); got != "value" {
		t.Errorf("X-Custom = %q, want %q", got, "value")
	}
	if got := headers.Get("Connection"); got != "" {
		t.Errorf("Connection should be empty, got %q", got)
	}
	if got := headers.Get("Upgrade"); got != "" {
		t.Errorf("Upgrade should be empty, got %q", got)
	}
}

func TestBuildWebSocketDialHeaders_UsesAPITypeKeyOverride(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/responses", nil)
	provider := &model.Provider{
		ID:       "p1",
		AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "codex",
			BaseURL:    "https://example.com",
		}},
		CredentialSessions: testCredentialSessions("p1", "codex", credentialsession.KindAPIKey, "codex-key"),
	}

	headers := buildWebSocketDialHeaders(request, provider, "codex", "auto")
	if got := headers.Get("Authorization"); got != "Bearer codex-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer codex-key")
	}
}

func (tracker *observerTestLiveTraffic) ObserveClientToUpstream(bytes int64) {
	tracker.BytesSent.Add(bytes)
	tracker.MsgsSent.Add(1)
	tracker.LastActivityAt.Store(time.Now().UnixMilli())
}

func (tracker *observerTestLiveTraffic) ObserveUpstreamToClient(bytes int64) {
	tracker.BytesReceived.Add(bytes)
	tracker.MsgsReceived.Add(1)
	tracker.LastActivityAt.Store(time.Now().UnixMilli())
}

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
	r.Header.Set("Authorization", "Bearer client-secret")
	r.Header.Set("X-Api-Key", "client-secret")
	r.Header.Set("ChatGPT-Account-Id", "client-account")

	provider := &model.Provider{
		ID:                 "p1",
		AuthMode:           "bearer",
		CredentialSessions: testCredentialSessions("p1", "codex", credentialsession.KindAPIKey, "sk-key"),
	}
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
	if got := headers.Get("Authorization"); got != "Bearer sk-key" {
		t.Errorf("Authorization = %q, want provider credential", got)
	}
	for _, h := range []string{"X-Api-Key", "ChatGPT-Account-Id"} {
		if got := headers.Get(h); got != "" {
			t.Errorf("%s should be removed before provider injection, got %q", h, got)
		}
	}
}

func TestBytesTrackingObserver_CountsAndTimestamp(t *testing.T) {
	t.Parallel()

	tracker := &observerTestLiveTraffic{}
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
	tracker := &observerTestLiveTraffic{}
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
	tracker := &observerTestLiveTraffic{}
	obs := newBytesTrackingObserver(inner, tracker)

	snap := obs.Snapshot()
	if snap.Model != "gpt-5" {
		t.Errorf("Snapshot().Model = %q, want %q", snap.Model, "gpt-5")
	}
}

func TestBytesTrackingObserver_NilInner(t *testing.T) {
	t.Parallel()

	tracker := &observerTestLiveTraffic{}
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
