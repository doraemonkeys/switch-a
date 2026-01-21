package proxy

import (
	"io"
	"strings"
	"testing"
)

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
