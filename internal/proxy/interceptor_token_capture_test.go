package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

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
