package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewTransport(t *testing.T) {
	cfg := TransportConfig{
		ConnectTimeout: 5 * time.Second,
		ReadTimeout:    30 * time.Second,
	}

	transport := NewTransport(cfg)
	if transport == nil {
		t.Fatal("NewTransport returned nil")
	}
	if transport.client == nil {
		t.Error("client is nil")
	}
	if transport.connectTimeout != cfg.ConnectTimeout {
		t.Errorf("connectTimeout = %v, want %v", transport.connectTimeout, cfg.ConnectTimeout)
	}
	if transport.readTimeout != cfg.ReadTimeout {
		t.Errorf("readTimeout = %v, want %v", transport.readTimeout, cfg.ReadTimeout)
	}
}

func TestTransport_Do(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "value")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	}))
	defer server.Close()

	transport := NewTransport(TransportConfig{
		ConnectTimeout: 5 * time.Second,
	})

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := transport.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if resp.Header.Get("X-Test") != "value" {
		t.Errorf("X-Test header = %q, want %q", resp.Header.Get("X-Test"), "value")
	}
}

func TestTransport_ForwardRequest(t *testing.T) {
	t.Run("normal response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}))
		defer server.Close()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
		})

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		w := httptest.NewRecorder()

		written, statusCode, err := transport.ForwardRequest(context.Background(), w, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !written {
			t.Error("expected headers to be written")
		}
		if statusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", statusCode, http.StatusOK)
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
		}
		if w.Body.String() != `{"status":"ok"}` {
			t.Errorf("body = %q, want %q", w.Body.String(), `{"status":"ok"}`)
		}
	})

	t.Run("error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
		defer server.Close()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
		})

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		w := httptest.NewRecorder()

		written, statusCode, err := transport.ForwardRequest(context.Background(), w, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !written {
			t.Error("expected headers to be written")
		}
		if statusCode != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", statusCode, http.StatusInternalServerError)
		}
	})

	t.Run("SSE response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("ResponseWriter doesn't support Flusher")
			}
			_, _ = w.Write([]byte("data: event1\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("data: event2\n\n"))
			flusher.Flush()
		}))
		defer server.Close()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
		})

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		w := httptest.NewRecorder()

		written, statusCode, err := transport.ForwardRequest(context.Background(), w, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !written {
			t.Error("expected headers to be written")
		}
		if statusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", statusCode, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "data: event1") {
			t.Error("response should contain event1")
		}
		if !strings.Contains(w.Body.String(), "data: event2") {
			t.Error("response should contain event2")
		}
	})
}

func TestIsSSEResponse(t *testing.T) {
	tests := []struct {
		contentType string
		want        bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"application/json", false},
		{"text/plain", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			resp := &http.Response{Header: make(http.Header)}
			resp.Header.Set("Content-Type", tt.contentType)
			got := isSSEResponse(resp)
			if got != tt.want {
				t.Errorf("isSSEResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCopyResponseHeaders(t *testing.T) {
	src := make(http.Header)
	src.Set("Content-Type", "application/json")
	src.Set("X-Custom", "value")
	src.Set("Connection", "keep-alive") // hop-by-hop, should be skipped

	dst := make(http.Header)
	copyResponseHeaders(dst, src)

	if dst.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", dst.Get("Content-Type"), "application/json")
	}
	if dst.Get("X-Custom") != "value" {
		t.Errorf("X-Custom = %q, want %q", dst.Get("X-Custom"), "value")
	}
	if dst.Get("Connection") != "" {
		t.Errorf("Connection should be skipped, got %q", dst.Get("Connection"))
	}
}

func TestIdleWatchdog(t *testing.T) {
	t.Run("nil when timeout is zero", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(context.Background(), closer, 0)
		if watchdog != nil {
			t.Error("expected nil watchdog when timeout is 0")
		}
	})

	t.Run("nil when timeout is negative", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(context.Background(), closer, -1*time.Second)
		if watchdog != nil {
			t.Error("expected nil watchdog when timeout is negative")
		}
	})

	t.Run("closes body on timeout", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(context.Background(), closer, 50*time.Millisecond)

		// Wait for timeout
		time.Sleep(100 * time.Millisecond)
		watchdog.Stop()

		if !closer.closed {
			t.Error("expected body to be closed on timeout")
		}
	})

	t.Run("reset prevents timeout", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(context.Background(), closer, 50*time.Millisecond)

		// Reset before timeout
		time.Sleep(30 * time.Millisecond)
		watchdog.Reset()
		time.Sleep(30 * time.Millisecond)
		watchdog.Reset()
		time.Sleep(30 * time.Millisecond)
		watchdog.Stop()

		if closer.closed {
			t.Error("body should not be closed when reset is called")
		}
	})

	t.Run("stop prevents timeout", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(context.Background(), closer, 100*time.Millisecond)

		// Stop immediately
		watchdog.Stop()

		// Wait past the original timeout
		time.Sleep(150 * time.Millisecond)

		if closer.closed {
			t.Error("body should not be closed after stop")
		}
	})

	t.Run("context cancellation stops watchdog", func(t *testing.T) {
		closer := &mockCloser{}
		ctx, cancel := context.WithCancel(context.Background())
		watchdog := newIdleWatchdog(ctx, closer, 100*time.Millisecond)

		// Cancel context immediately
		cancel()

		// Wait for watchdog to exit
		watchdog.Stop()

		if closer.closed {
			t.Error("body should not be closed on context cancellation")
		}
	})

	t.Run("reset on nil watchdog is safe", func(t *testing.T) {
		var watchdog *idleWatchdog
		// Should not panic
		watchdog.Reset()
	})

	t.Run("stop on nil watchdog is safe", func(t *testing.T) {
		var watchdog *idleWatchdog
		// Should not panic
		watchdog.Stop()
	})
}

type mockCloser struct {
	closed bool
}

func (m *mockCloser) Close() error {
	m.closed = true
	return nil
}

func TestSSEIdleTimeout(t *testing.T) {
	t.Run("stream completes normally without timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("data: event1\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("data: event2\n\n"))
			flusher.Flush()
		}))
		defer server.Close()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
			SSEIdleTimeout: 5 * time.Second, // Long timeout, should not trigger
		})

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		w := httptest.NewRecorder()

		_, _, err := transport.ForwardRequest(context.Background(), w, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(w.Body.String(), "event1") {
			t.Error("response should contain event1")
		}
	})

	t.Run("no timeout when SSEIdleTimeout is zero", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("data: event1\n\n"))
			flusher.Flush()
		}))
		defer server.Close()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
			SSEIdleTimeout: 0, // Disabled
		})

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		w := httptest.NewRecorder()

		_, _, err := transport.ForwardRequest(context.Background(), w, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestIsClosedError(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		want    bool
	}{
		{"closed error", "use of closed network connection", true},
		{"EOF error", "unexpected EOF", true},
		{"reset error", "connection reset by peer", true},
		{"timeout error", "i/o timeout", false},
		{"random error", "some random error", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &testError{msg: tt.errMsg}
			got := isClosedError(err)
			if got != tt.want {
				t.Errorf("isClosedError(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

func TestBuildUpstreamRequest(t *testing.T) {
	t.Run("with body", func(t *testing.T) {
		origReq := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		origReq.Header.Set("Content-Type", "application/json")
		origReq.Header.Set("X-Custom", "value")
		origReq.Header.Set("Authorization", "Bearer original") // should be filtered

		body := []byte(`{"message":"hello"}`)
		upstreamReq, err := BuildUpstreamRequest(context.Background(), http.MethodPost, "https://api.example.com/v1/messages", body, origReq)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if upstreamReq.Method != http.MethodPost {
			t.Errorf("method = %q, want %q", upstreamReq.Method, http.MethodPost)
		}
		if upstreamReq.URL.String() != "https://api.example.com/v1/messages" {
			t.Errorf("url = %q, want %q", upstreamReq.URL.String(), "https://api.example.com/v1/messages")
		}
		if upstreamReq.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want %q", upstreamReq.Header.Get("Content-Type"), "application/json")
		}
		if upstreamReq.Header.Get("X-Custom") != "value" {
			t.Errorf("X-Custom = %q, want %q", upstreamReq.Header.Get("X-Custom"), "value")
		}
		if upstreamReq.Header.Get("Authorization") != "" {
			t.Errorf("Authorization should be filtered, got %q", upstreamReq.Header.Get("Authorization"))
		}

		// Check body
		bodyBytes, _ := io.ReadAll(upstreamReq.Body)
		if !bytes.Equal(bodyBytes, body) {
			t.Errorf("body = %q, want %q", string(bodyBytes), string(body))
		}
	})

	t.Run("without body", func(t *testing.T) {
		origReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

		upstreamReq, err := BuildUpstreamRequest(context.Background(), http.MethodGet, "https://api.example.com/v1/models", nil, origReq)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if upstreamReq.Method != http.MethodGet {
			t.Errorf("method = %q, want %q", upstreamReq.Method, http.MethodGet)
		}
		if upstreamReq.Body != nil {
			body, _ := io.ReadAll(upstreamReq.Body)
			if len(body) > 0 {
				t.Errorf("expected nil or empty body, got %q", string(body))
			}
		}
	})
}
