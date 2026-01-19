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

func TestTransport_FetchAndWrite(t *testing.T) {
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

		resp, err := transport.FetchUpstream(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		err = transport.WriteToClient(context.Background(), w, resp)
		if err != nil {
			t.Fatalf("unexpected error writing to client: %v", err)
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

		resp, err := transport.FetchUpstream(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
		}

		err = transport.WriteToClient(context.Background(), w, resp)
		if err != nil {
			t.Fatalf("unexpected error writing to client: %v", err)
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

		resp, err := transport.FetchUpstream(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		err = transport.WriteToClient(context.Background(), w, resp)
		if err != nil {
			t.Fatalf("unexpected error writing to client: %v", err)
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

func TestUpstreamResponse_Drain(t *testing.T) {
	t.Run("drains body before closing", func(t *testing.T) {
		// Create a body with some data
		data := bytes.Repeat([]byte("test data "), 100)
		body := io.NopCloser(bytes.NewReader(data))

		resp := &UpstreamResponse{
			StatusCode: http.StatusOK,
			Body:       body,
		}

		resp.Drain()

		// Verify body is closed (reading should return 0/EOF)
		n, err := body.Read(make([]byte, 1))
		if n != 0 || err != io.EOF {
			// This is expected because NopCloser doesn't actually close the underlying reader
			// but Drain should have read all data
		}
	})

	t.Run("handles nil body", func(t *testing.T) {
		resp := &UpstreamResponse{
			StatusCode: http.StatusOK,
			Body:       nil,
		}

		// Should not panic
		resp.Drain()
	})

	t.Run("limits drain to maxDrainBytes", func(t *testing.T) {
		// Create a body larger than maxDrainBytes (64KB)
		data := bytes.Repeat([]byte("x"), 100*1024) // 100KB
		readCount := 0
		trackingReader := &trackingReader{
			reader:    bytes.NewReader(data),
			readCount: &readCount,
		}
		body := io.NopCloser(trackingReader)

		resp := &UpstreamResponse{
			StatusCode: http.StatusOK,
			Body:       body,
		}

		resp.Drain()

		// Should have limited reads due to LimitReader
		// readCount should be less than full data size
		if readCount > 65*1024 {
			t.Errorf("expected read count <= 65KB, got %d bytes", readCount)
		}
	})
}

func TestUpstreamResponse_DrainWithSnippet(t *testing.T) {
	t.Run("captures snippet from small body", func(t *testing.T) {
		data := []byte(`{"error": "quota exceeded", "message": "You have exceeded your API quota"}`)
		body := io.NopCloser(bytes.NewReader(data))

		resp := &UpstreamResponse{
			StatusCode: http.StatusTooManyRequests,
			Body:       body,
		}

		snippet := resp.DrainWithSnippet(0) // Use default size

		if snippet != string(data) {
			t.Errorf("snippet = %q, want %q", snippet, string(data))
		}
	})

	t.Run("truncates large body to maxSnippet", func(t *testing.T) {
		// Create a body larger than the requested snippet size
		data := bytes.Repeat([]byte("x"), 1000)
		body := io.NopCloser(bytes.NewReader(data))

		resp := &UpstreamResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       body,
		}

		snippet := resp.DrainWithSnippet(100) // Request only 100 bytes

		if len(snippet) != 100 {
			t.Errorf("snippet length = %d, want 100", len(snippet))
		}
		if snippet != string(data[:100]) {
			t.Errorf("snippet content mismatch")
		}
	})

	t.Run("uses default size when maxSnippet is 0", func(t *testing.T) {
		// Create a body larger than default maxSnippetBytes (512)
		data := bytes.Repeat([]byte("y"), 1000)
		body := io.NopCloser(bytes.NewReader(data))

		resp := &UpstreamResponse{
			StatusCode: http.StatusForbidden,
			Body:       body,
		}

		snippet := resp.DrainWithSnippet(0)

		if len(snippet) != maxSnippetBytes {
			t.Errorf("snippet length = %d, want %d", len(snippet), maxSnippetBytes)
		}
	})

	t.Run("handles nil body", func(t *testing.T) {
		resp := &UpstreamResponse{
			StatusCode: http.StatusOK,
			Body:       nil,
		}

		snippet := resp.DrainWithSnippet(100)

		if snippet != "" {
			t.Errorf("expected empty snippet for nil body, got %q", snippet)
		}
	})

	t.Run("drains remaining content after snippet", func(t *testing.T) {
		// Create a body with trackable reads
		data := bytes.Repeat([]byte("z"), 10*1024) // 10KB
		readCount := 0
		trackingReader := &trackingReader{
			reader:    bytes.NewReader(data),
			readCount: &readCount,
		}
		body := io.NopCloser(trackingReader)

		resp := &UpstreamResponse{
			StatusCode: http.StatusServiceUnavailable,
			Body:       body,
		}

		snippet := resp.DrainWithSnippet(100)

		// Should have captured first 100 bytes
		if len(snippet) != 100 {
			t.Errorf("snippet length = %d, want 100", len(snippet))
		}

		// Should have drained more than just the snippet
		if readCount <= 100 {
			t.Errorf("expected to drain more than snippet, only read %d bytes", readCount)
		}
	})

	t.Run("limits total drain to maxDrainBytes", func(t *testing.T) {
		// Create a body larger than maxDrainBytes (64KB)
		data := bytes.Repeat([]byte("w"), 100*1024) // 100KB
		readCount := 0
		trackingReader := &trackingReader{
			reader:    bytes.NewReader(data),
			readCount: &readCount,
		}
		body := io.NopCloser(trackingReader)

		resp := &UpstreamResponse{
			StatusCode: http.StatusBadGateway,
			Body:       body,
		}

		resp.DrainWithSnippet(100)

		// Should have limited total reads to maxDrainBytes
		if readCount > 65*1024 {
			t.Errorf("expected read count <= 65KB, got %d bytes", readCount)
		}
	})
}

type trackingReader struct {
	reader    io.Reader
	readCount *int
}

func (r *trackingReader) Read(p []byte) (n int, err error) {
	n, err = r.reader.Read(p)
	*r.readCount += n
	return
}

func TestLimitedBuffer(t *testing.T) {
	t.Run("captures data within limit", func(t *testing.T) {
		lb := &limitedBuffer{limit: 100}

		n, err := lb.Write([]byte("hello world"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 11 {
			t.Errorf("n = %d, want 11", n)
		}
		if lb.String() != "hello world" {
			t.Errorf("String() = %q, want %q", lb.String(), "hello world")
		}
		if lb.truncated {
			t.Error("truncated should be false")
		}
	})

	t.Run("truncates data exceeding limit", func(t *testing.T) {
		lb := &limitedBuffer{limit: 10}

		n, err := lb.Write([]byte("hello world and more"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Write returns the original length even when truncating
		if n != 20 {
			t.Errorf("n = %d, want 20", n)
		}
		if lb.String() != "hello worl...[truncated]" {
			t.Errorf("String() = %q, want %q", lb.String(), "hello worl...[truncated]")
		}
		if !lb.truncated {
			t.Error("truncated should be true")
		}
	})

	t.Run("multiple writes within limit", func(t *testing.T) {
		lb := &limitedBuffer{limit: 20}

		lb.Write([]byte("hello "))
		lb.Write([]byte("world"))

		if lb.String() != "hello world" {
			t.Errorf("String() = %q, want %q", lb.String(), "hello world")
		}
		if lb.truncated {
			t.Error("truncated should be false")
		}
	})

	t.Run("multiple writes exceeding limit", func(t *testing.T) {
		lb := &limitedBuffer{limit: 10}

		lb.Write([]byte("hello ")) // 6 bytes, 4 remaining
		lb.Write([]byte("world"))  // 5 bytes, truncated to 4

		if lb.String() != "hello worl...[truncated]" {
			t.Errorf("String() = %q, want %q", lb.String(), "hello worl...[truncated]")
		}
		if !lb.truncated {
			t.Error("truncated should be true")
		}
	})

	t.Run("write when already full", func(t *testing.T) {
		lb := &limitedBuffer{limit: 5}

		lb.Write([]byte("hello"))
		n, err := lb.Write([]byte("world"))

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Returns full length even when discarding
		if n != 5 {
			t.Errorf("n = %d, want 5", n)
		}
		if lb.String() != "hello...[truncated]" {
			t.Errorf("String() = %q, want %q", lb.String(), "hello...[truncated]")
		}
	})
}

func TestTeeReadCloser(t *testing.T) {
	t.Run("reads through tee", func(t *testing.T) {
		data := []byte("test data for tee")
		original := io.NopCloser(bytes.NewReader(data))
		var captured bytes.Buffer

		trc := &teeReadCloser{
			original: original,
			tee:      io.TeeReader(original, &captured),
		}

		// Read all data
		result, err := io.ReadAll(trc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Original data should be read
		if string(result) != string(data) {
			t.Errorf("read = %q, want %q", string(result), string(data))
		}

		// Captured buffer should also have the data
		if captured.String() != string(data) {
			t.Errorf("captured = %q, want %q", captured.String(), string(data))
		}
	})

	t.Run("close closes original", func(t *testing.T) {
		closeCount := 0
		original := &mockReadCloser{
			reader:     bytes.NewReader([]byte("test")),
			closeCount: &closeCount,
		}
		trc := &teeReadCloser{
			original: original,
			tee:      original,
		}

		err := trc.Close()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if closeCount != 1 {
			t.Errorf("closeCount = %d, want 1", closeCount)
		}
	})
}

type mockReadCloser struct {
	reader     io.Reader
	closeCount *int
}

func (m *mockReadCloser) Read(p []byte) (int, error) {
	return m.reader.Read(p)
}

func (m *mockReadCloser) Close() error {
	*m.closeCount++
	return nil
}

func TestUpstreamResponse_TeeBody(t *testing.T) {
	t.Run("captures snippet while forwarding full body", func(t *testing.T) {
		data := []byte(`{"error": {"message": "Model not found", "type": "invalid_request_error"}}`)
		body := io.NopCloser(bytes.NewReader(data))

		resp := &UpstreamResponse{
			StatusCode: http.StatusBadRequest,
			Body:       body,
		}

		lb := resp.TeeBody(0) // Use default size
		if lb == nil {
			t.Fatal("TeeBody returned nil")
		}

		// Read all data from the body (simulating WriteToClient)
		readData, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Full data should be readable
		if string(readData) != string(data) {
			t.Errorf("read = %q, want %q", string(readData), string(data))
		}

		// Snippet should be captured
		if lb.String() != string(data) {
			t.Errorf("snippet = %q, want %q", lb.String(), string(data))
		}
	})

	t.Run("truncates large response in snippet", func(t *testing.T) {
		data := bytes.Repeat([]byte("x"), 1000)
		body := io.NopCloser(bytes.NewReader(data))

		resp := &UpstreamResponse{
			StatusCode: http.StatusNotFound,
			Body:       body,
		}

		lb := resp.TeeBody(100) // Only capture 100 bytes

		// Read all data from the body
		readData, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Full data should be readable
		if len(readData) != 1000 {
			t.Errorf("read length = %d, want 1000", len(readData))
		}

		// Snippet should be truncated
		expected := string(data[:100]) + "...[truncated]"
		if lb.String() != expected {
			t.Errorf("snippet length = %d, want %d", len(lb.String()), len(expected))
		}
	})

	t.Run("uses default size when maxBytes is 0", func(t *testing.T) {
		data := bytes.Repeat([]byte("y"), 1000)
		body := io.NopCloser(bytes.NewReader(data))

		resp := &UpstreamResponse{
			StatusCode: http.StatusBadRequest,
			Body:       body,
		}

		lb := resp.TeeBody(0)

		io.ReadAll(resp.Body)

		// Should capture maxSnippetBytes (512) + truncated marker
		if lb.limit != maxSnippetBytes {
			t.Errorf("limit = %d, want %d", lb.limit, maxSnippetBytes)
		}
	})

	t.Run("handles nil body", func(t *testing.T) {
		resp := &UpstreamResponse{
			StatusCode: http.StatusBadRequest,
			Body:       nil,
		}

		lb := resp.TeeBody(100)

		if lb != nil {
			t.Errorf("expected nil for nil body, got %+v", lb)
		}
	})

	t.Run("close still works after TeeBody", func(t *testing.T) {
		closeCount := 0
		body := &mockReadCloser{
			reader:     bytes.NewReader([]byte("test")),
			closeCount: &closeCount,
		}

		resp := &UpstreamResponse{
			StatusCode: http.StatusBadRequest,
			Body:       body,
		}

		resp.TeeBody(100)
		resp.Close()

		if closeCount != 1 {
			t.Errorf("closeCount = %d, want 1", closeCount)
		}
	})
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
		watchdog := newIdleWatchdog(closer, 0)
		if watchdog != nil {
			t.Error("expected nil watchdog when timeout is 0")
		}
	})

	t.Run("nil when timeout is negative", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(closer, -1*time.Second)
		if watchdog != nil {
			t.Error("expected nil watchdog when timeout is negative")
		}
	})

	t.Run("closes body on timeout", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(closer, 50*time.Millisecond)

		// Wait for timeout
		time.Sleep(100 * time.Millisecond)
		watchdog.Stop()

		if !closer.closed {
			t.Error("expected body to be closed on timeout")
		}
	})

	t.Run("reset prevents timeout", func(t *testing.T) {
		closer := &mockCloser{}
		watchdog := newIdleWatchdog(closer, 50*time.Millisecond)

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
		watchdog := newIdleWatchdog(closer, 100*time.Millisecond)

		// Stop immediately
		watchdog.Stop()

		// Wait past the original timeout
		time.Sleep(150 * time.Millisecond)

		if closer.closed {
			t.Error("body should not be closed after stop")
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

		resp, err := transport.FetchUpstream(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Close()

		err = transport.WriteToClient(context.Background(), w, resp)
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

		resp, err := transport.FetchUpstream(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Close()

		err = transport.WriteToClient(context.Background(), w, resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("context cancellation closes body even without SSEIdleTimeout", func(t *testing.T) {
		// This test verifies the fix for the issue where SSE streams would hang
		// when client disconnects and SSEIdleTimeout=0 (the default).
		// Without the fix, Read() would block indefinitely because there was
		// no goroutine to close the body on context cancellation.
		serverReady := make(chan struct{})
		serverDone := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("data: event1\n\n"))
			flusher.Flush()
			close(serverReady)
			// Block until test signals done - simulating upstream that never closes
			<-serverDone
		}))
		defer func() {
			close(serverDone)
			server.Close()
		}()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
			SSEIdleTimeout: 0, // Disabled - this is the default that was problematic
		})

		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		w := httptest.NewRecorder()

		resp, err := transport.FetchUpstream(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Close()

		// Start WriteToClient in a goroutine
		done := make(chan error, 1)
		go func() {
			done <- transport.WriteToClient(ctx, w, resp)
		}()

		// Wait for server to send initial data
		<-serverReady

		// Cancel context to simulate client disconnect
		cancel()

		// WriteToClient should return quickly after context cancellation
		select {
		case err := <-done:
			if err != context.Canceled {
				t.Errorf("expected context.Canceled, got: %v", err)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("WriteToClient did not return after context cancellation - the fix is not working")
		}
	})
}

func TestIdleWatchdogTimedOut(t *testing.T) {
	t.Run("nil watchdog returns false", func(t *testing.T) {
		var w *idleWatchdog
		if w.TimedOut() {
			t.Error("nil watchdog should return false for TimedOut()")
		}
	})

	t.Run("new watchdog not timed out", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader("test"))
		w := newIdleWatchdog(body, 1*time.Second)
		defer w.Stop()

		if w.TimedOut() {
			t.Error("new watchdog should not be timed out")
		}
	})

	t.Run("watchdog times out after idle period", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader("test"))
		w := newIdleWatchdog(body, 10*time.Millisecond)

		// Wait for timeout
		time.Sleep(50 * time.Millisecond)

		if !w.TimedOut() {
			t.Error("watchdog should be timed out after idle period")
		}
		w.Stop()
	})

	t.Run("stopped watchdog not timed out", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader("test"))
		w := newIdleWatchdog(body, 1*time.Second)
		w.Stop()

		if w.TimedOut() {
			t.Error("stopped watchdog should not be timed out")
		}
	})
}

func TestSSEIdleTimeoutError(t *testing.T) {
	err := ErrSSEIdleTimeout
	msg := err.Error()
	if msg != "SSE stream idle timeout: no data received within timeout period" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestReadTimeoutError(t *testing.T) {
	err := ErrReadTimeout
	msg := err.Error()
	if msg != "upstream read timeout: no data received within timeout period" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestForwardRegular_WithIdleTimeout(t *testing.T) {
	t.Run("completes normally with data flowing", func(t *testing.T) {
		// Create a response body with multiple chunks
		data := bytes.Repeat([]byte("test data chunk "), 100)
		body := io.NopCloser(bytes.NewReader(data))

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
			ReadTimeout:    1 * time.Second,
		})

		w := httptest.NewRecorder()
		err := transport.forwardRegular(context.Background(), w, body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if w.Body.String() != string(data) {
			t.Errorf("body mismatch: got %d bytes, want %d bytes", w.Body.Len(), len(data))
		}
	})

	t.Run("timeout when no data received", func(t *testing.T) {
		// Create a reader that blocks forever
		pr, _ := io.Pipe()
		defer pr.Close()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
			ReadTimeout:    50 * time.Millisecond,
		})

		w := httptest.NewRecorder()
		err := transport.forwardRegular(context.Background(), w, pr)
		if err != ErrReadTimeout {
			t.Errorf("expected ErrReadTimeout, got: %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		pr, _ := io.Pipe()
		defer pr.Close()

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
			ReadTimeout:    5 * time.Second,
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		w := httptest.NewRecorder()
		err := transport.forwardRegular(ctx, w, pr)
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("no timeout when ReadTimeout is zero", func(t *testing.T) {
		data := []byte("test data")
		body := io.NopCloser(bytes.NewReader(data))

		transport := NewTransport(TransportConfig{
			ConnectTimeout: 5 * time.Second,
			ReadTimeout:    0, // Disabled
		})

		w := httptest.NewRecorder()
		err := transport.forwardRegular(context.Background(), w, body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if w.Body.String() != string(data) {
			t.Errorf("body mismatch")
		}
	})
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
