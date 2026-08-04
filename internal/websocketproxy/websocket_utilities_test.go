package websocketproxy

import (
	"context"
	"fmt"
	"testing"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func TestHttpToWSURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "https to wss", input: "https://api.openai.com/v1/realtime", expected: "wss://api.openai.com/v1/realtime"},
		{name: "http to ws", input: "http://localhost:8080/ws", expected: "ws://localhost:8080/ws"},
		{name: "HTTPS uppercase", input: "HTTPS://api.example.com/path", expected: "wss://api.example.com/path"},
		{name: "HTTP uppercase", input: "HTTP://localhost/ws", expected: "ws://localhost/ws"},
		{name: "mixed case scheme", input: "Https://Mixed.Case/path", expected: "wss://Mixed.Case/path"},
		{name: "wss passthrough", input: "wss://already.ws/path", expected: "wss://already.ws/path"},
		{name: "ws passthrough", input: "ws://already.ws/path", expected: "ws://already.ws/path"},
		{name: "empty string", input: "", expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := httpToWSURL(tt.input)
			if got != tt.expected {
				t.Errorf("httpToWSURL(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractCloseCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		expected websocket.StatusCode
	}{
		{
			name:     "close error with code",
			err:      websocket.CloseError{Code: websocket.StatusGoingAway, Reason: "bye"},
			expected: websocket.StatusGoingAway,
		},
		{
			name:     "non-close error",
			err:      context.Canceled,
			expected: websocket.StatusNormalClosure,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractCloseCode(tt.err)
			if got != tt.expected {
				t.Errorf("extractCloseCode() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestNewWebSocketForwarder_DefaultDialer(t *testing.T) {
	t.Parallel()
	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})
	if fwd.dialer == nil {
		t.Error("expected non-nil default dialer")
	}
}

func TestNewWebSocketForwarder_CustomDialer(t *testing.T) {
	t.Parallel()
	custom := &mockDialer{}
	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Dialer: custom,
		Logger: zaptest.NewLogger(t),
	})
	if fwd.dialer != custom {
		t.Error("expected custom dialer to be used")
	}
}

func TestIsNormalClose(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "normal closure", err: websocket.CloseError{Code: websocket.StatusNormalClosure}, expected: true},
		{name: "going away", err: websocket.CloseError{Code: websocket.StatusGoingAway}, expected: true},
		{name: "internal error code", err: websocket.CloseError{Code: websocket.StatusInternalError}, expected: false},
		{name: "policy violation", err: websocket.CloseError{Code: websocket.StatusPolicyViolation}, expected: false},
		{name: "non-close error", err: context.Canceled, expected: false},
		{name: "generic error", err: fmt.Errorf("something broke"), expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isNormalClose(tt.err)
			if got != tt.expected {
				t.Errorf("isNormalClose() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTruncateUTF8(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		max      int
		expected string
	}{
		{name: "short string no truncation", input: "hello", max: 10, expected: "hello"},
		{name: "exact length", input: "hello", max: 5, expected: "hello"},
		{name: "truncate ascii", input: "hello world", max: 5, expected: "hello"},
		{name: "truncate at rune boundary", input: "日本語テスト", max: 9, expected: "日本語"},
		{name: "truncate mid-rune falls back", input: "日本語テスト", max: 7, expected: "日本"},
		{name: "empty string", input: "", max: 10, expected: ""},
		{name: "max zero", input: "hello", max: 0, expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncateUTF8(tt.input, tt.max)
			if got != tt.expected {
				t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
			}
		})
	}
}
