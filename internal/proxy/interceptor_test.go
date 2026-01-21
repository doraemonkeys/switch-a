package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

// testReadCloser wraps a Reader and tracks Close calls.
// Named to avoid conflict with testReadCloser in transport_test.go.
type testReadCloser struct {
	io.Reader
	closed bool
}

func (m *testReadCloser) Close() error {
	m.closed = true
	return nil
}

// ============================================================
// interceptTeeReadCloser tests
// ============================================================

func TestInterceptTeeReadCloser_Read(t *testing.T) {
	original := &testReadCloser{Reader: strings.NewReader("hello world")}
	var buf bytes.Buffer
	eofCalled := false

	trc := &interceptTeeReadCloser{
		original: original,
		tee:      io.TeeReader(original, &buf),
		onEOF:    func() { eofCalled = true },
	}

	// Read all data
	data := make([]byte, 20)
	n, err := trc.Read(data)
	if n != 11 {
		t.Errorf("expected n=11, got %d", n)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if string(data[:n]) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data[:n]))
	}
	if eofCalled {
		t.Error("onEOF should not be called yet")
	}

	// Next read should return EOF
	n, err = trc.Read(data)
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	if !eofCalled {
		t.Error("onEOF should be called on EOF")
	}

	// Verify tee captured the data
	if buf.String() != "hello world" {
		t.Errorf("expected buffer 'hello world', got %q", buf.String())
	}
}

func TestInterceptTeeReadCloser_Close(t *testing.T) {
	original := &testReadCloser{Reader: strings.NewReader("test")}
	trc := &interceptTeeReadCloser{
		original: original,
		tee:      io.TeeReader(original, &bytes.Buffer{}),
	}

	if err := trc.Close(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if !original.closed {
		t.Error("expected original to be closed")
	}
}

func TestInterceptTeeReadCloser_NilOnEOF(t *testing.T) {
	original := &testReadCloser{Reader: strings.NewReader("")}
	trc := &interceptTeeReadCloser{
		original: original,
		tee:      io.TeeReader(original, &bytes.Buffer{}),
		onEOF:    nil, // nil callback should not panic
	}

	data := make([]byte, 10)
	_, err := trc.Read(data)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	// Should not panic with nil onEOF
}

// ============================================================
// tokenCaptureInterceptor tests - Wrap behavior
// ============================================================

func TestTokenCaptureInterceptor_WrapNilBody(t *testing.T) {
	interceptor := newTokenCaptureInterceptor(100, nil)
	result := interceptor.Wrap(nil)

	if result != nil {
		t.Error("expected nil result for nil body")
	}

	usage, complete := interceptor.Result()
	if usage != nil {
		t.Error("expected nil usage")
	}
	if !complete {
		t.Error("expected complete=true for nil body")
	}
}

func TestTokenCaptureInterceptor_WrapZeroContentLength(t *testing.T) {
	interceptor := newTokenCaptureInterceptor(0, nil)
	original := &testReadCloser{Reader: strings.NewReader("")}
	result := interceptor.Wrap(original)

	// With content-length 0, buf is nil, so original body should be returned as-is
	if result != original {
		t.Error("expected original body to be returned for zero content-length")
	}

	usage, complete := interceptor.Result()
	if usage != nil {
		t.Error("expected nil usage for empty response")
	}
	if !complete {
		t.Error("expected complete=true for zero content-length")
	}
}

func TestTokenCaptureInterceptor_WrapSmallResponse(t *testing.T) {
	// Small response should use fullCaptureBuffer
	jsonData := `{"usage":{"prompt_tokens":100,"completion_tokens":50}}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	if wrapped == nil {
		t.Fatal("expected non-nil wrapped body")
	}
	if wrapped == original {
		t.Error("expected wrapped body to be different from original")
	}

	// Read all data
	data, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != jsonData {
		t.Errorf("expected %q, got %q", jsonData, string(data))
	}

	// Close the wrapped body
	if err := wrapped.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Check result
	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true after full read")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
}

func TestTokenCaptureInterceptor_WrapLargeResponse(t *testing.T) {
	// Large response should use tailBuffer
	// Create a large response where usage is at the end
	padding := strings.Repeat("x", 50*1024) // 50KB padding
	jsonData := `{"content":"` + padding + `","usage":{"prompt_tokens":200,"completion_tokens":100}}`

	// Use -1 (chunked) to ensure tailBuffer is used
	interceptor := newTokenCaptureInterceptor(-1, nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	// Read all data
	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	// Check result - tailBuffer should have captured the usage at the end
	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true after full read")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage from tail capture")
	}
	if usage.PromptTokens != 200 {
		t.Errorf("expected PromptTokens=200, got %d", usage.PromptTokens)
	}
}

func TestTokenCaptureInterceptor_WrapChunkedResponse(t *testing.T) {
	// Chunked (content-length -1) should use tailBuffer
	jsonData := `{"usage":{"input_tokens":150,"output_tokens":75}}`
	interceptor := newTokenCaptureInterceptor(-1, nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 150 {
		t.Errorf("expected PromptTokens=150, got %d", usage.PromptTokens)
	}
}

// ============================================================
// tokenCaptureInterceptor tests - Result behavior
// ============================================================

func TestTokenCaptureInterceptor_ResultCaching(t *testing.T) {
	jsonData := `{"usage":{"prompt_tokens":50,"completion_tokens":25}}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	// First call
	usage1, _ := interceptor.Result()
	// Second call should return cached result
	usage2, _ := interceptor.Result()

	if usage1 != usage2 {
		t.Error("expected Result() to return cached result")
	}
}

func TestTokenCaptureInterceptor_ResultWithOpenAI(t *testing.T) {
	jsonData := `{"id":"chatcmpl-123","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 200 {
		t.Errorf("expected CompletionTokens=200, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 300 {
		t.Errorf("expected TotalTokens=300, got %d", usage.TotalTokens)
	}
}

func TestTokenCaptureInterceptor_ResultWithClaude(t *testing.T) {
	jsonData := `{"content":[],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":30,"cache_creation_input_tokens":20}}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CacheReadInputTokens != 30 {
		t.Errorf("expected CacheReadInputTokens=30, got %d", usage.CacheReadInputTokens)
	}
	if usage.CacheCreation == nil {
		t.Fatal("expected non-nil CacheCreation")
	}
	if usage.CacheCreation.InputTokens != 20 {
		t.Errorf("expected CacheCreation.InputTokens=20, got %d", usage.CacheCreation.InputTokens)
	}
}

func TestTokenCaptureInterceptor_ResultWithGemini(t *testing.T) {
	jsonData := `{"candidates":[],"usageMetadata":{"promptTokenCount":80,"candidatesTokenCount":120,"totalTokenCount":200}}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 80 {
		t.Errorf("expected PromptTokens=80, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 120 {
		t.Errorf("expected CompletionTokens=120, got %d", usage.CompletionTokens)
	}
}

func TestTokenCaptureInterceptor_ResultNoUsage(t *testing.T) {
	jsonData := `{"id":"123","content":"hello"}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage != nil {
		t.Error("expected nil usage when no usage field")
	}
}

func TestTokenCaptureInterceptor_ResultInvalidJSON(t *testing.T) {
	jsonData := `{invalid json}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage != nil {
		t.Error("expected nil usage for invalid JSON")
	}
}

func TestTokenCaptureInterceptor_GzipResponse(t *testing.T) {
	// Test that gzip compressed response is properly decompressed and parsed
	jsonData := `{"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`

	// Compress the data with gzip
	var gzipBuf bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipBuf)
	_, err := gzipWriter.Write([]byte(jsonData))
	if err != nil {
		t.Fatalf("gzip write failed: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close failed: %v", err)
	}

	compressedData := gzipBuf.Bytes()

	// Verify it starts with gzip magic number
	if len(compressedData) < 2 || compressedData[0] != 0x1f || compressedData[1] != 0x8b {
		t.Fatal("test data is not valid gzip")
	}

	interceptor := newTokenCaptureInterceptor(int64(len(compressedData)), nil)
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	_, err = io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage from gzip compressed response")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 150 {
		t.Errorf("expected TotalTokens=150, got %d", usage.TotalTokens)
	}
}

func TestMaybeDecompressGzip(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "plain text",
			input:    []byte(`{"usage":{"prompt_tokens":100}}`),
			expected: `{"usage":{"prompt_tokens":100}}`,
		},
		{
			name:     "empty",
			input:    []byte{},
			expected: "",
		},
		{
			name:     "single byte",
			input:    []byte{0x1f},
			expected: string([]byte{0x1f}),
		},
		{
			name:     "invalid gzip magic",
			input:    []byte{0x1f, 0x00, 0x08, 0x00},
			expected: string([]byte{0x1f, 0x00, 0x08, 0x00}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maybeDecompressGzip(tt.input)
			if string(result) != tt.expected {
				t.Errorf("maybeDecompressGzip() = %q, want %q", string(result), tt.expected)
			}
		})
	}

	// Test valid gzip
	t.Run("valid gzip", func(t *testing.T) {
		original := `{"usage":{"prompt_tokens":200}}`
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write([]byte(original))
		_ = w.Close()

		result := maybeDecompressGzip(buf.Bytes())
		if string(result) != original {
			t.Errorf("maybeDecompressGzip() = %q, want %q", string(result), original)
		}
	})
}

// ============================================================
// tokenCaptureInterceptor tests - Incomplete read
// ============================================================

func TestTokenCaptureInterceptor_IncompleteRead(t *testing.T) {
	jsonData := `{"usage":{"prompt_tokens":100,"completion_tokens":50}}`
	interceptor := newTokenCaptureInterceptor(int64(len(jsonData)), nil)
	original := &testReadCloser{Reader: strings.NewReader(jsonData)}
	wrapped := interceptor.Wrap(original)

	// Only read partial data, don't reach EOF
	buf := make([]byte, 10)
	_, _ = wrapped.Read(buf)
	_ = wrapped.Close()

	// Result should show incomplete
	usage, complete := interceptor.Result()
	if complete {
		t.Error("expected complete=false for incomplete read")
	}
	// Usage might still be parseable from partial data, or nil
	_ = usage // Don't assert on usage value for incomplete read
}

// ============================================================
// tokenCaptureInterceptor tests - Interface compliance
// ============================================================

func TestTokenCaptureInterceptor_ImplementsInterface(t *testing.T) {
	var _ ResponseInterceptor = (*tokenCaptureInterceptor)(nil)
}

// ============================================================
// Benchmark tests
// ============================================================

func BenchmarkTokenCaptureInterceptor_SmallResponse(b *testing.B) {
	jsonData := `{"id":"123","usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`
	dataLen := int64(len(jsonData))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interceptor := newTokenCaptureInterceptor(dataLen, nil)
		original := &testReadCloser{Reader: strings.NewReader(jsonData)}
		wrapped := interceptor.Wrap(original)
		_, _ = io.ReadAll(wrapped)
		_ = wrapped.Close()
		_, _ = interceptor.Result()
	}
}

func BenchmarkTokenCaptureInterceptor_LargeResponse(b *testing.B) {
	padding := strings.Repeat("x", 100*1024) // 100KB
	jsonData := `{"content":"` + padding + `","usage":{"prompt_tokens":100,"completion_tokens":200}}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interceptor := newTokenCaptureInterceptor(-1, nil) // chunked
		original := &testReadCloser{Reader: strings.NewReader(jsonData)}
		wrapped := interceptor.Wrap(original)
		_, _ = io.ReadAll(wrapped)
		_ = wrapped.Close()
		_, _ = interceptor.Result()
	}
}

// ============================================================
// SSE Token Interceptor tests (Phase 4b)
// ============================================================

func TestSSETokenInterceptor_ImplementsInterface(t *testing.T) {
	var _ ResponseInterceptor = (*sseTokenInterceptor)(nil)
}

func TestSSETokenInterceptor_WrapNilBody(t *testing.T) {
	interceptor := newSSETokenInterceptor(nil, "")
	result := interceptor.Wrap(nil)

	if result != nil {
		t.Error("expected nil result for nil body")
	}

	usage, complete := interceptor.Result()
	if usage != nil {
		t.Error("expected nil usage")
	}
	if !complete {
		t.Error("expected complete=true for nil body")
	}
}

func TestSSETokenInterceptor_OpenAIFormat(t *testing.T) {
	// OpenAI SSE format: data: {...}\n\n
	sseData := `data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n` +
		`data: {"choices":[{"delta":{"content":" World"}}]}\n\n` +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}\n\n` +
		`data: [DONE]\n\n`

	// Convert escaped newlines to actual newlines
	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 150 {
		t.Errorf("expected TotalTokens=150, got %d", usage.TotalTokens)
	}
}

func TestSSETokenInterceptor_ClaudeFormat(t *testing.T) {
	// Claude SSE format: event: xxx\ndata: {...}\n\n
	sseData := `event: message_start\ndata: {"type":"message_start","message":{"id":"msg_123"}}\n\n` +
		`event: content_block_delta\ndata: {"type":"content_block_delta","delta":{"text":"Hello"}}\n\n` +
		`event: message_delta\ndata: {"type":"message_delta","usage":{"input_tokens":200,"output_tokens":100}}\n\n` +
		`event: message_stop\ndata: {"type":"message_stop"}\n\n`

	// Convert escaped newlines to actual newlines
	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 200 {
		t.Errorf("expected PromptTokens=200, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 100 {
		t.Errorf("expected CompletionTokens=100, got %d", usage.CompletionTokens)
	}
}

func TestSSETokenInterceptor_ClaudeWithCache(t *testing.T) {
	// Claude SSE with cache fields
	sseData := `event: message_start\ndata: {"type":"message_start"}\n\n` +
		`event: message_delta\ndata: {"type":"message_delta","usage":{"input_tokens":1000,"output_tokens":125,"cache_creation_input_tokens":358,"cache_read_input_tokens":500,"service_tier":"standard"}}\n\n` +
		`event: message_stop\ndata: {"type":"message_stop"}\n\n`

	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 1000 {
		t.Errorf("expected PromptTokens=1000, got %d", usage.PromptTokens)
	}
	if usage.CacheReadInputTokens != 500 {
		t.Errorf("expected CacheReadInputTokens=500, got %d", usage.CacheReadInputTokens)
	}
	if usage.CacheCreation == nil {
		t.Fatal("expected non-nil CacheCreation")
	}
	if usage.CacheCreation.InputTokens != 358 {
		t.Errorf("expected CacheCreation.InputTokens=358, got %d", usage.CacheCreation.InputTokens)
	}
}

func TestSSETokenInterceptor_GeminiFormat(t *testing.T) {
	// Gemini SSE format
	sseData := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}\n\n` +
		`data: {"candidates":[{"content":{"parts":[{"text":" World"}]}}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":75,"totalTokenCount":125}}\n\n`

	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 50 {
		t.Errorf("expected PromptTokens=50, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 75 {
		t.Errorf("expected CompletionTokens=75, got %d", usage.CompletionTokens)
	}
}

func TestSSETokenInterceptor_NoUsage(t *testing.T) {
	// SSE stream without usage field
	sseData := `data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n` +
		`data: {"choices":[{"delta":{"content":" World"}}]}\n\n` +
		`data: [DONE]\n\n`

	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage != nil {
		t.Error("expected nil usage when no usage field in stream")
	}
}

func TestSSETokenInterceptor_SkipsDoneMarker(t *testing.T) {
	// Ensure [DONE] marker is not parsed as JSON
	sseData := `data: {"usage":{"prompt_tokens":100,"completion_tokens":50}}\n\n` +
		`data: [DONE]\n\n`

	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_UsageTextInContent(t *testing.T) {
	// "usage" in content should not be confused with actual usage field
	sseData := `data: {"choices":[{"delta":{"content":"Let me explain the usage of this API"}}]}\n\n` +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50}}\n\n` +
		`data: [DONE]\n\n`

	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	// Should get the actual usage from the last chunk with "usage":
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_IncrementalReads(t *testing.T) {
	// Simulate slow/chunked reads
	sseData := `data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n` +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50}}\n\n` +
		`data: [DONE]\n\n`

	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	// Read in small chunks
	buf := make([]byte, 10)
	for {
		n, err := wrapped.Read(buf)
		if n == 0 && err == io.EOF {
			break
		}
		if err != nil && err != io.EOF {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_ResultCaching(t *testing.T) {
	sseData := `data: {"usage":{"prompt_tokens":50,"completion_tokens":25}}\n\n`
	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	// First call
	usage1, _ := interceptor.Result()
	// Second call should return same result
	usage2, _ := interceptor.Result()

	if usage1 != usage2 {
		t.Error("expected Result() to return cached result")
	}
}

func TestSSETokenInterceptor_BufferOverflow(t *testing.T) {
	// Test that buffer doesn't grow indefinitely
	// Create a stream larger than maxSSEBuffer without \n\n separators, then add valid data
	largeChunk := strings.Repeat("x", maxSSEBuffer+1000)
	sseData := largeChunk + "\n\ndata: {\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50}}\n\n"

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_EmptyStream(t *testing.T) {
	sseData := ""

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage != nil {
		t.Error("expected nil usage for empty stream")
	}
}

func TestSSETokenInterceptor_MultipleUsageChunks(t *testing.T) {
	// Multiple chunks with usage - should keep the last one
	sseData := `data: {"usage":{"prompt_tokens":50,"completion_tokens":25}}\n\n` +
		`data: {"usage":{"prompt_tokens":100,"completion_tokens":50}}\n\n` +
		`data: {"usage":{"prompt_tokens":200,"completion_tokens":100}}\n\n`

	sseData = strings.ReplaceAll(sseData, `\n`, "\n")

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	_ = wrapped.Close()

	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	// Should have the last usage values
	if usage.PromptTokens != 200 {
		t.Errorf("expected PromptTokens=200 (last chunk), got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 100 {
		t.Errorf("expected CompletionTokens=100 (last chunk), got %d", usage.CompletionTokens)
	}
}

// ============================================================
// SSE Token Interceptor Benchmark tests
// ============================================================

func BenchmarkSSETokenInterceptor_SmallStream(b *testing.B) {
	sseData := `data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50}}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interceptor := newSSETokenInterceptor(nil, "")
		original := &testReadCloser{Reader: strings.NewReader(sseData)}
		wrapped := interceptor.Wrap(original)
		_, _ = io.ReadAll(wrapped)
		_ = wrapped.Close()
		_, _ = interceptor.Result()
	}
}

func BenchmarkSSETokenInterceptor_LargeStream(b *testing.B) {
	// Simulate a large SSE stream with many chunks
	var builder strings.Builder
	for i := 0; i < 100; i++ {
		builder.WriteString(`data: {"choices":[{"delta":{"content":"Hello World "}}]}`)
		builder.WriteString("\n\n")
	}
	builder.WriteString(`data: {"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":500}}`)
	builder.WriteString("\n\n")
	builder.WriteString(`data: [DONE]`)
	builder.WriteString("\n\n")
	sseData := builder.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interceptor := newSSETokenInterceptor(nil, "")
		original := &testReadCloser{Reader: strings.NewReader(sseData)}
		wrapped := interceptor.Wrap(original)
		_, _ = io.ReadAll(wrapped)
		_ = wrapped.Close()
		_, _ = interceptor.Result()
	}
}

func TestSSETokenInterceptor_CRLFSeparator(t *testing.T) {
	// Test SSE stream with \r\n\r\n separators (Windows/HTTP style)
	sseData := "event: message_start\r\ndata: {\"type\":\"message_start\"}\r\n\r\n" +
		"event: message_delta\r\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":300,\"output_tokens\":150}}\r\n\r\n" +
		"event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n"

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage for CRLF separated stream")
	}
	if usage.PromptTokens != 300 {
		t.Errorf("expected PromptTokens=300, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 150 {
		t.Errorf("expected CompletionTokens=150, got %d", usage.CompletionTokens)
	}
}

func TestFindSSEChunkSeparator(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantIdx    int
		wantSepLen int
	}{
		{"LF only", "data: test\n\nmore", 10, 2},
		{"CRLF only", "data: test\r\n\r\nmore", 10, 4},
		{"LF before CRLF", "a\n\nb\r\n\r\n", 1, 2},
		{"CRLF before LF", "a\r\n\r\nb\n\n", 1, 4},
		{"no separator", "data: test", -1, 0},
		{"empty", "", -1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, sepLen := findSSEChunkSeparator([]byte(tt.data))
			if idx != tt.wantIdx {
				t.Errorf("idx = %d, want %d", idx, tt.wantIdx)
			}
			if sepLen != tt.wantSepLen {
				t.Errorf("sepLen = %d, want %d", sepLen, tt.wantSepLen)
			}
		})
	}
}

// TestSSETokenInterceptor_NoTrailingSeparator tests that usage data is captured
// even when the last SSE chunk doesn't have a trailing \n\n separator.
// This can happen when the upstream server closes the connection without
// sending a final separator.
func TestSSETokenInterceptor_NoTrailingSeparator(t *testing.T) {
	// SSE stream where the last chunk with usage has no trailing \n\n
	sseData := `event: message_start` + "\n" +
		`data: {"type":"message_start"}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","delta":{"text":"Hello"}}` + "\n\n" +
		`event: message_delta` + "\n" +
		`data: {"type":"message_delta","usage":{"input_tokens":250,"output_tokens":125}}` // No trailing \n\n

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage even without trailing separator")
	}
	if usage.PromptTokens != 250 {
		t.Errorf("expected PromptTokens=250, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 125 {
		t.Errorf("expected CompletionTokens=125, got %d", usage.CompletionTokens)
	}
}

// TestSSETokenInterceptor_RealClaudeStreamFormat tests with realistic Claude SSE format
// from the actual API response, including the message_stop event without trailing separator.
func TestSSETokenInterceptor_RealClaudeStreamFormat(t *testing.T) {
	// Simulates real Claude API response format from 响应体.txt
	sseData := `event: message_start` + "\n" +
		`data: {"type":"message_start","message":{"model":"claude-haiku-4-5-20251001","id":"msg_123"}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
		`event: message_delta` + "\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":117,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":25}}` + "\n\n" +
		`event: message_stop` + "\n" +
		`data: {"type":"message_stop"}` // No trailing \n\n at very end

	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 117 {
		t.Errorf("expected PromptTokens=117, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 25 {
		t.Errorf("expected CompletionTokens=25, got %d", usage.CompletionTokens)
	}
}

// ============================================================
// SSE Token Interceptor Gzip Passthrough tests (Phase 4b - TeeReader)
// ============================================================

func TestSSETokenInterceptor_GzipPassthrough(t *testing.T) {
	// Create SSE data
	sseData := "event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"input_tokens":100,"output_tokens":50}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	// Compress the SSE data
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	_, err := gzWriter.Write([]byte(sseData))
	if err != nil {
		t.Fatalf("failed to write gzip: %v", err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	compressedData := buf.Bytes()

	// Verify it's valid gzip
	if len(compressedData) < 2 || compressedData[0] != 0x1f || compressedData[1] != 0x8b {
		t.Fatal("test data is not valid gzip")
	}

	// Test with gzip Content-Encoding
	interceptor := newSSETokenInterceptor(nil, "gzip")
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	// Key verification: client reads original compressed data (passthrough)
	outputData, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("failed to read wrapped body: %v", err)
	}
	_ = wrapped.Close()

	// Passthrough verification: client receives original gzip bytes
	if !bytes.Equal(outputData, compressedData) {
		t.Error("expected client to receive original compressed data (passthrough)")
		t.Errorf("output length: %d, compressed length: %d", len(outputData), len(compressedData))
	}

	// Verify gzip magic number is preserved
	if len(outputData) < 2 || outputData[0] != 0x1f || outputData[1] != 0x8b {
		t.Error("output data should start with gzip magic number")
	}

	// Wait for background goroutine to complete parsing
	interceptor.Wait()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected usage to be parsed from gzip-compressed SSE")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
}

func TestSSETokenInterceptor_NoGzipWithoutEncoding(t *testing.T) {
	// Plain text SSE data (no compression)
	sseData := "event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"input_tokens":200,"output_tokens":100}}` + "\n\n"

	// Test without Content-Encoding (empty string)
	interceptor := newSSETokenInterceptor(nil, "")
	original := &testReadCloser{Reader: strings.NewReader(sseData)}
	wrapped := interceptor.Wrap(original)

	_, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	_ = wrapped.Close()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected usage to be parsed")
	}
	if usage.PromptTokens != 200 {
		t.Errorf("expected PromptTokens=200, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_GzipPassthrough_ClientDecompression(t *testing.T) {
	// Simulate complete flow: client receives compressed data and decompresses it
	sseData := "event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"input_tokens":300,"output_tokens":150}}` + "\n\n"

	// Compress
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	gzWriter.Write([]byte(sseData))
	gzWriter.Close()
	compressedData := buf.Bytes()

	// Pass through interceptor
	interceptor := newSSETokenInterceptor(nil, "gzip")
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	// Client reads compressed data
	clientReceived, _ := io.ReadAll(wrapped)
	wrapped.Close()

	// Client decompresses (simulates browser behavior)
	gzReader, err := gzip.NewReader(bytes.NewReader(clientReceived))
	if err != nil {
		t.Fatalf("client failed to create gzip reader: %v", err)
	}
	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("client failed to decompress: %v", err)
	}
	gzReader.Close()

	// Verify client gets original SSE data after decompression
	if string(decompressed) != sseData {
		t.Errorf("client decompressed data mismatch\nexpected: %s\ngot: %s", sseData, string(decompressed))
	}

	// Wait and verify internal parsing worked too
	interceptor.Wait()
	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected usage to be parsed internally")
	}
	if usage.PromptTokens != 300 {
		t.Errorf("expected PromptTokens=300, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_InvalidGzip(t *testing.T) {
	// Invalid gzip data (has magic number but is corrupted)
	invalidGzip := []byte{0x1f, 0x8b, 0x08, 0x00, 0xff, 0xff, 0xff}

	interceptor := newSSETokenInterceptor(nil, "gzip")
	original := &testReadCloser{Reader: bytes.NewReader(invalidGzip)}
	wrapped := interceptor.Wrap(original)

	// Should still be able to read the original data (passthrough)
	outputData, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	wrapped.Close()

	// Data should be passed through unchanged
	if !bytes.Equal(outputData, invalidGzip) {
		t.Error("expected invalid gzip data to be passed through unchanged")
	}

	// Wait for goroutine (should handle error gracefully)
	interceptor.Wait()

	// Usage should be nil since gzip decompression failed
	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true even for invalid gzip")
	}
	if usage != nil {
		t.Error("expected nil usage for invalid gzip data")
	}
}

func TestSSETokenInterceptor_GzipCaseInsensitive(t *testing.T) {
	// Test that Content-Encoding comparison is case-insensitive
	sseData := "data: {\"usage\":{\"input_tokens\":50,\"output_tokens\":25}}\n\n"

	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	gzWriter.Write([]byte(sseData))
	gzWriter.Close()
	compressedData := buf.Bytes()

	// Test with "GZIP" (uppercase)
	interceptor := newSSETokenInterceptor(nil, "GZIP")
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	outputData, _ := io.ReadAll(wrapped)
	wrapped.Close()

	// Should still pass through compressed data
	if !bytes.Equal(outputData, compressedData) {
		t.Error("expected passthrough for uppercase GZIP")
	}

	interceptor.Wait()
	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected usage to be parsed with uppercase GZIP")
	}
	if usage.PromptTokens != 50 {
		t.Errorf("expected PromptTokens=50, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_GzipOpenAIFormat(t *testing.T) {
	// Test gzip with OpenAI SSE format
	sseData := `data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	gzWriter.Write([]byte(sseData))
	gzWriter.Close()
	compressedData := buf.Bytes()

	interceptor := newSSETokenInterceptor(nil, "gzip")
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	_, _ = io.ReadAll(wrapped)
	wrapped.Close()
	interceptor.Wait()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected usage from gzip OpenAI format")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 150 {
		t.Errorf("expected TotalTokens=150, got %d", usage.TotalTokens)
	}
}

func BenchmarkSSETokenInterceptor_GzipPassthrough(b *testing.B) {
	// Create SSE data and compress it
	sseData := `data: {"choices":[{"delta":{"content":"Hello World"}}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50}}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	gzWriter.Write([]byte(sseData))
	gzWriter.Close()
	compressedData := buf.Bytes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interceptor := newSSETokenInterceptor(nil, "gzip")
		original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
		wrapped := interceptor.Wrap(original)
		_, _ = io.ReadAll(wrapped)
		_ = wrapped.Close()
		interceptor.Wait()
		_, _ = interceptor.Result()
	}
}
