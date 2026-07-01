package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

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
	for b.Loop() {
		interceptor := newSSETokenInterceptor(nil, "gzip")
		original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
		wrapped := interceptor.Wrap(original)
		_, _ = io.ReadAll(wrapped)
		_ = wrapped.Close()
		interceptor.Wait()
		_, _ = interceptor.Result()
	}
}

// ============================================================
// SSE Token Interceptor Brotli Passthrough tests
// ============================================================

func TestSSETokenInterceptor_BrotliPassthrough(t *testing.T) {
	// Create SSE data
	sseData := "event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"input_tokens":100,"output_tokens":50}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	// Compress with brotli
	var buf bytes.Buffer
	brWriter := brotli.NewWriter(&buf)
	_, err := brWriter.Write([]byte(sseData))
	if err != nil {
		t.Fatalf("failed to write brotli: %v", err)
	}
	if err := brWriter.Close(); err != nil {
		t.Fatalf("failed to close brotli writer: %v", err)
	}
	compressedData := buf.Bytes()

	// Test with brotli Content-Encoding
	interceptor := newSSETokenInterceptor(nil, "br")
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	// Key verification: client reads original compressed data (passthrough)
	outputData, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("failed to read wrapped body: %v", err)
	}
	_ = wrapped.Close()

	// Passthrough verification: client receives original brotli bytes
	if !bytes.Equal(outputData, compressedData) {
		t.Error("expected client to receive original compressed data (passthrough)")
		t.Errorf("output length: %d, compressed length: %d", len(outputData), len(compressedData))
	}

	// Wait for background goroutine to complete parsing
	interceptor.Wait()

	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true")
	}
	if usage == nil {
		t.Fatal("expected usage to be parsed from brotli-compressed SSE")
	}
	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
}

func TestSSETokenInterceptor_BrotliPassthrough_ClientDecompression(t *testing.T) {
	// Simulate complete flow: client receives compressed data and decompresses it
	sseData := "event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"input_tokens":300,"output_tokens":150}}` + "\n\n"

	// Compress with brotli
	var buf bytes.Buffer
	brWriter := brotli.NewWriter(&buf)
	brWriter.Write([]byte(sseData))
	brWriter.Close()
	compressedData := buf.Bytes()

	// Pass through interceptor
	interceptor := newSSETokenInterceptor(nil, "br")
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	// Client reads compressed data
	clientReceived, _ := io.ReadAll(wrapped)
	wrapped.Close()

	// Client decompresses (simulates browser behavior)
	brReader := brotli.NewReader(bytes.NewReader(clientReceived))
	decompressed, err := io.ReadAll(brReader)
	if err != nil {
		t.Fatalf("client failed to decompress brotli: %v", err)
	}

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

func TestSSETokenInterceptor_BrotliCaseInsensitive(t *testing.T) {
	// Test that Content-Encoding comparison is case-insensitive
	sseData := "data: {\"usage\":{\"input_tokens\":50,\"output_tokens\":25}}\n\n"

	var buf bytes.Buffer
	brWriter := brotli.NewWriter(&buf)
	brWriter.Write([]byte(sseData))
	brWriter.Close()
	compressedData := buf.Bytes()

	// Test with "BR" (uppercase)
	interceptor := newSSETokenInterceptor(nil, "BR")
	original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
	wrapped := interceptor.Wrap(original)

	outputData, _ := io.ReadAll(wrapped)
	wrapped.Close()

	// Should still pass through compressed data
	if !bytes.Equal(outputData, compressedData) {
		t.Error("expected passthrough for uppercase BR")
	}

	interceptor.Wait()
	usage, _ := interceptor.Result()
	if usage == nil {
		t.Fatal("expected usage to be parsed with uppercase BR")
	}
	if usage.PromptTokens != 50 {
		t.Errorf("expected PromptTokens=50, got %d", usage.PromptTokens)
	}
}

func TestSSETokenInterceptor_InvalidBrotli(t *testing.T) {
	// Invalid brotli data
	invalidBrotli := []byte{0xff, 0xfe, 0x00, 0x00, 0x00, 0x00, 0x00}

	interceptor := newSSETokenInterceptor(nil, "br")
	original := &testReadCloser{Reader: bytes.NewReader(invalidBrotli)}
	wrapped := interceptor.Wrap(original)

	// Should still be able to read the original data (passthrough)
	outputData, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	wrapped.Close()

	// Data should be passed through unchanged
	if !bytes.Equal(outputData, invalidBrotli) {
		t.Error("expected invalid brotli data to be passed through unchanged")
	}

	// Wait for goroutine (should handle error gracefully)
	interceptor.Wait()

	// Usage should be nil since brotli decompression failed
	usage, complete := interceptor.Result()
	if !complete {
		t.Error("expected complete=true even for invalid brotli")
	}
	if usage != nil {
		t.Error("expected nil usage for invalid brotli data")
	}
}

func TestSSETokenInterceptor_BrotliOpenAIFormat(t *testing.T) {
	// Test brotli with OpenAI SSE format
	sseData := `data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	var buf bytes.Buffer
	brWriter := brotli.NewWriter(&buf)
	brWriter.Write([]byte(sseData))
	brWriter.Close()
	compressedData := buf.Bytes()

	interceptor := newSSETokenInterceptor(nil, "br")
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
		t.Fatal("expected usage from brotli OpenAI format")
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

func BenchmarkSSETokenInterceptor_BrotliPassthrough(b *testing.B) {
	// Create SSE data and compress it with brotli
	sseData := `data: {"choices":[{"delta":{"content":"Hello World"}}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50}}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	var buf bytes.Buffer
	brWriter := brotli.NewWriter(&buf)
	brWriter.Write([]byte(sseData))
	brWriter.Close()
	compressedData := buf.Bytes()

	b.ResetTimer()
	for b.Loop() {
		interceptor := newSSETokenInterceptor(nil, "br")
		original := &testReadCloser{Reader: bytes.NewReader(compressedData)}
		wrapped := interceptor.Wrap(original)
		_, _ = io.ReadAll(wrapped)
		_ = wrapped.Close()
		interceptor.Wait()
		_, _ = interceptor.Result()
	}
}
