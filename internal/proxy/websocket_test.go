package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

// --- Test helpers ---

// newEchoWSServer creates an httptest.Server that upgrades to WebSocket and echoes messages.
func newEchoWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("echo server accept error: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
	}))
}

// newCloseAfterNWSServer creates a server that echoes N messages then closes with the given code.
func newCloseAfterNWSServer(t *testing.T, n int, code websocket.StatusCode, reason string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(wsReadLimit)
		for i := 0; i < n; i++ {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				conn.Close(websocket.StatusInternalError, "read failed")
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
		conn.Close(code, reason)
	}))
}

// newHeaderCapturingWSServer creates a server that captures the handshake request headers.
func newHeaderCapturingWSServer(t *testing.T, captured *http.Header, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*captured = r.Header.Clone()
		mu.Unlock()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}))
}

// wsURL converts an httptest.Server URL to a WebSocket URL.
func wsURL(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

// connectWSClient dials a WebSocket endpoint using a test helper.
func connectWSClient(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

// --- Unit tests ---

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

// --- Integration tests ---

func TestWebSocketForwarder_Forward_EchoRoundtrip(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	// Create a proxy server that forwards WebSocket to the echo server.
	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		if err != nil {
			t.Errorf("Forward error: %v", err)
			return
		}
		if !result.ConnectSuccess {
			t.Errorf("expected ConnectSuccess=true, got false")
		}
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect as a client to the proxy.
	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Send a message and verify the echo.
	msg := "hello websocket"
	if err := clientConn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgType, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("expected MessageText, got %v", msgType)
	}
	if string(data) != msg {
		t.Errorf("expected %q, got %q", msg, string(data))
	}
}

func TestWebSocketForwarder_Forward_BinaryMessages(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Send binary data.
	payload := []byte{0x00, 0xFF, 0xDE, 0xAD, 0xBE, 0xEF}
	if err := clientConn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	msgType, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Errorf("expected MessageBinary, got %v", msgType)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("binary payload mismatch: got %x, want %x", data, payload)
	}
}

func TestWebSocketForwarder_Forward_UpstreamDialFailure(t *testing.T) {
	t.Parallel()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	// Use an invalid upstream URL that will fail to dial.
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, "ws://127.0.0.1:1", nil)
		if result.ConnectSuccess {
			t.Error("expected ConnectSuccess=false for unreachable upstream")
		}
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Client connects to proxy — should get accepted then immediately closed
	// with StatusBadGateway because the upstream is unreachable.
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err != nil {
		// Accept + close can race with Dial; a Dial failure is also acceptable
		// as long as the proxy didn't panic.
		t.Logf("client dial failed (acceptable race): %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read should fail with a close frame from the proxy.
	_, _, readErr := conn.Read(ctx)
	if readErr == nil {
		t.Fatal("expected error from read after upstream dial failure")
	}
	// Verify the close code is StatusBadGateway, indicating the proxy
	// correctly reported the upstream failure to the client.
	var closeErr websocket.CloseError
	if errors.As(readErr, &closeErr) {
		if closeErr.Code != websocket.StatusBadGateway {
			t.Errorf("close code = %d, want StatusBadGateway (%d)", closeErr.Code, websocket.StatusBadGateway)
		}
	}
}

func TestWebSocketForwarder_Forward_HandshakeFailureCapturesUpstreamResponse(t *testing.T) {
	t.Parallel()

	const handshakeBody = `{"error":{"message":"Account quota exhausted","type":"billing_error"}}`
	dialErr := errors.New("failed to WebSocket dial: expected handshake response status code 101 but got 402")
	doneCh := make(chan *WebSocketResult, 1)

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Dialer: &mockDialer{
			dialFunc: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
				return nil, &http.Response{
					StatusCode: http.StatusPaymentRequired,
					Body:       io.NopCloser(strings.NewReader(handshakeBody)),
				}, dialErr
			},
		},
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, "ws://provider.invalid/realtime", nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err == nil {
		defer conn.Close(websocket.StatusNormalClosure, "")
		_, _, _ = conn.Read(ctx)
	} else {
		t.Logf("client dial failed (acceptable race): %v", err)
	}

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.ConnectSuccess {
			t.Fatal("expected ConnectSuccess=false when upstream rejects handshake")
		}
		if result.HandshakeStatusCode != http.StatusPaymentRequired {
			t.Fatalf("HandshakeStatusCode = %d, want %d", result.HandshakeStatusCode, http.StatusPaymentRequired)
		}
		if result.HandshakeBodySnippet != handshakeBody {
			t.Fatalf("HandshakeBodySnippet = %q, want %q", result.HandshakeBodySnippet, handshakeBody)
		}
		if !errors.Is(result.Err, dialErr) {
			t.Fatalf("Err = %v, want dialErr", result.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return a result")
	}
}

func TestWebSocketForwarder_Forward_UpstreamCloses(t *testing.T) {
	t.Parallel()

	// Upstream echoes 1 message then closes.
	upstream := newCloseAfterNWSServer(t, 1, websocket.StatusNormalClosure, "done")
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Send one message — will be echoed.
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(data) != "ping" {
		t.Errorf("expected 'ping', got %q", string(data))
	}

	// Next read should fail — upstream closed.
	_, _, err = clientConn.Read(ctx)
	if err == nil {
		t.Error("expected error after upstream close")
	}
}

func TestWebSocketForwarder_Forward_AuthHeadersPassed(t *testing.T) {
	t.Parallel()

	var captured http.Header
	var mu sync.Mutex
	upstream := newHeaderCapturingWSServer(t, &captured, &mu)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer sk-test-key")
		headers.Set("OpenAI-Beta", "realtime=v1")
		fwd.Forward(r.Context(), w, r, wsURL(upstream), headers)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	// Give the proxy time to complete the upstream handshake.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if captured.Get("Authorization") != "Bearer sk-test-key" {
		t.Errorf("expected Authorization header, got %q", captured.Get("Authorization"))
	}
	if captured.Get("OpenAI-Beta") != "realtime=v1" {
		t.Errorf("expected OpenAI-Beta header, got %q", captured.Get("OpenAI-Beta"))
	}
	if got := captured.Get(headerUserAgent); got != "" {
		t.Errorf("expected empty User-Agent, got %q", got)
	}
}

func TestWebSocketForwarder_Forward_PreservesExplicitUserAgent(t *testing.T) {
	t.Parallel()

	var captured http.Header
	var mu sync.Mutex
	upstream := newHeaderCapturingWSServer(t, &captured, &mu)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := http.Header{}
		headers.Set(headerUserAgent, "switch-a-proxy/1.0")
		fwd.Forward(r.Context(), w, r, wsURL(upstream), headers)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if got := captured.Get(headerUserAgent); got != "switch-a-proxy/1.0" {
		t.Fatalf("User-Agent = %q, want %q", got, "switch-a-proxy/1.0")
	}
}

func TestWebSocketForwarder_Forward_ContextCancel(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a proxy with a cancellable request context.
	reqCtx, reqCancel := context.WithCancel(ctx)

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Override request context with our cancellable one.
		r = r.WithContext(reqCtx)
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// Verify connection works.
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Cancel the request context — both relay goroutines should exit.
	reqCancel()

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.ConnectSuccess {
			t.Error("expected ConnectSuccess=true (connection was established before cancel)")
		}
		if !errors.Is(result.Err, context.Canceled) {
			t.Errorf("expected context cancellation to remain observable, got: %v", result.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return after context cancellation")
	}
}

func TestReduceWebSocketRelayErrors_PeerEOFClearsInternalCancellation(t *testing.T) {
	t.Parallel()

	outcome := reduceWebSocketRelayErrors(
		webSocketRelayResult{err: io.EOF, errorOrder: 1},
		webSocketRelayResult{err: context.Canceled, errorOrder: 2},
	)
	if outcome.err != nil {
		t.Fatalf("expected err=nil for peer EOF plus relay cancellation, got %v", outcome.err)
	}
	if outcome.closeCode != websocket.StatusNoStatusRcvd {
		t.Fatalf("CloseCode = %d, want %d", outcome.closeCode, websocket.StatusNoStatusRcvd)
	}
}

func TestReduceWebSocketRelayErrors_PreservesCallerCancellationWithoutPeerDisconnect(t *testing.T) {
	t.Parallel()

	outcome := reduceWebSocketRelayErrors(
		webSocketRelayResult{err: context.Canceled, errorOrder: 1},
		webSocketRelayResult{err: context.Canceled, errorOrder: 2},
	)
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("expected context cancellation to remain a failure, got %v", outcome.err)
	}
	if outcome.closeCode != websocket.StatusNormalClosure {
		t.Fatalf("CloseCode = %d, want %d", outcome.closeCode, websocket.StatusNormalClosure)
	}
}

func TestReduceWebSocketRelayErrors_PrefersActualFirstFailureOverSiblingCancellation(t *testing.T) {
	t.Parallel()

	upstreamClose := websocket.CloseError{
		Code:   websocket.StatusPolicyViolation,
		Reason: "blocked",
	}
	outcome := reduceWebSocketRelayErrors(
		webSocketRelayResult{err: context.Canceled, errorOrder: 2},
		webSocketRelayResult{err: upstreamClose, errorOrder: 1},
	)
	if websocket.CloseStatus(outcome.err) != websocket.StatusPolicyViolation {
		t.Fatalf("CloseStatus(outcome.err) = %d, want %d", websocket.CloseStatus(outcome.err), websocket.StatusPolicyViolation)
	}
	if outcome.closeCode != websocket.StatusPolicyViolation {
		t.Fatalf("CloseCode = %d, want %d", outcome.closeCode, websocket.StatusPolicyViolation)
	}
}

func TestWebSocketForwarder_Forward_ClientCloseNowNotAnError(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if err := clientConn.CloseNow(); err != nil {
		t.Fatalf("CloseNow: %v", err)
	}

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.ConnectSuccess {
			t.Fatal("expected ConnectSuccess=true")
		}
		if result.Err != nil {
			t.Fatalf("expected Err=nil for CloseNow teardown after successful traffic, got %v", result.Err)
		}
		if result.CloseCode != websocket.StatusNoStatusRcvd {
			t.Fatalf("CloseCode = %d, want %d", result.CloseCode, websocket.StatusNoStatusRcvd)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return after CloseNow")
	}
}

func TestWebSocketForwarder_Forward_ByteCountsAccurate(t *testing.T) {
	t.Parallel()

	upstream := newEchoWSServer(t)
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))

	// Send two messages.
	msg1 := "hello"
	msg2 := "world!"
	clientConn.Write(ctx, websocket.MessageText, []byte(msg1))
	clientConn.Read(ctx) // echo 1
	clientConn.Write(ctx, websocket.MessageText, []byte(msg2))
	clientConn.Read(ctx) // echo 2

	// Close client — triggers relay shutdown.
	clientConn.Close(websocket.StatusNormalClosure, "")

	select {
	case result := <-doneCh:
		expectedBytes := int64(len(msg1) + len(msg2))
		if result.BytesClientToUpstream != expectedBytes {
			t.Errorf("BytesClientToUpstream = %d, want %d", result.BytesClientToUpstream, expectedBytes)
		}
		if result.BytesUpstreamToClient != expectedBytes {
			t.Errorf("BytesUpstreamToClient = %d, want %d", result.BytesUpstreamToClient, expectedBytes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return")
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

func TestWebSocketForwarder_Forward_NormalCloseNoError(t *testing.T) {
	t.Parallel()

	// Upstream echoes 1 message then closes normally.
	upstream := newCloseAfterNWSServer(t, 1, websocket.StatusNormalClosure, "done")
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	clientConn.Write(ctx, websocket.MessageText, []byte("ping"))
	clientConn.Read(ctx)

	// Trigger close by reading again (upstream already closed).
	clientConn.Read(ctx) //nolint:errcheck

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		// Normal close should produce Err=nil (contract: clean close is not an error).
		if result.Err != nil {
			t.Errorf("expected Err=nil for normal close, got: %v", result.Err)
		}
		if result.CloseCode != websocket.StatusNormalClosure {
			t.Errorf("CloseCode = %d, want %d", result.CloseCode, websocket.StatusNormalClosure)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return")
	}
}

func TestWebSocketForwarder_Forward_NonCleanClosePreservesFirstError(t *testing.T) {
	t.Parallel()

	upstream := newCloseAfterNWSServer(t, 1, websocket.StatusPolicyViolation, "blocked")
	defer upstream.Close()

	fwd := NewWebSocketForwarder(WebSocketForwarderConfig{
		Logger: zaptest.NewLogger(t),
	})

	doneCh := make(chan *WebSocketResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := fwd.Forward(r.Context(), w, r, wsURL(upstream), nil)
		doneCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	if err := clientConn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := clientConn.Read(ctx); err != nil {
		t.Fatalf("read echo: %v", err)
	}

	clientConn.Read(ctx) //nolint:errcheck

	select {
	case result := <-doneCh:
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if websocket.CloseStatus(result.Err) != websocket.StatusPolicyViolation {
			t.Fatalf("CloseStatus(result.Err) = %d, want %d (err=%v)", websocket.CloseStatus(result.Err), websocket.StatusPolicyViolation, result.Err)
		}
		if result.CloseCode != websocket.StatusPolicyViolation {
			t.Fatalf("CloseCode = %d, want %d", result.CloseCode, websocket.StatusPolicyViolation)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forward did not return")
	}
}

// mockDialer implements WebSocketDialer for testing.
type mockDialer struct {
	dialFunc func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}

func (m *mockDialer) Dial(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	if m.dialFunc != nil {
		return m.dialFunc(ctx, url, opts)
	}
	return nil, nil, nil
}
