package tokenusage

import (
	"bytes"
	"testing"
)

func observed(value int64) ObservedCount {
	return ObservedCount{Value: value, Present: true}
}

// ============================================================
// tailBuffer tests
// ============================================================

func TestTailBuffer_SmallWrite(t *testing.T) {
	tb := newTailBuffer(10)
	_, _ = tb.Write([]byte("hello"))
	got := string(tb.Bytes())
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestTailBuffer_ExactSize(t *testing.T) {
	tb := newTailBuffer(10)
	_, _ = tb.Write([]byte("0123456789"))
	got := string(tb.Bytes())
	if got != "0123456789" {
		t.Errorf("expected '0123456789', got %q", got)
	}
}

func TestTailBuffer_Overflow(t *testing.T) {
	tb := newTailBuffer(10)
	_, _ = tb.Write([]byte("0123456789ABCDEF"))
	got := string(tb.Bytes())
	// Should keep last 10 bytes
	if got != "6789ABCDEF" {
		t.Errorf("expected '6789ABCDEF', got %q", got)
	}
}

func TestTailBuffer_MultipleWrites(t *testing.T) {
	tb := newTailBuffer(10)
	_, _ = tb.Write([]byte("hello"))
	_, _ = tb.Write([]byte("world"))
	got := string(tb.Bytes())
	if got != "helloworld" {
		t.Errorf("expected 'helloworld', got %q", got)
	}
}

func TestTailBuffer_WrapAround(t *testing.T) {
	tb := newTailBuffer(10)
	_, _ = tb.Write([]byte("12345678")) // 8 bytes
	_, _ = tb.Write([]byte("ABCDE"))    // 5 bytes, total 13, should wrap
	got := string(tb.Bytes())
	// Should keep last 10 bytes: "678ABCDE" wait that's only 8
	// Let me recalculate: after first write pos=8
	// After second write: need 5 bytes
	// firstPart = 10-8 = 2, so write "AB" at [8:10], then "CDE" at [0:3]
	// pos becomes (8+5)%10 = 3
	// Bytes(): buf[3:] + buf[:3] = "45678AB" + "CDE" = "45678ABCDE" wait that's 10 bytes
	// Actually: buf = [C,D,E,4,5,6,7,8,A,B], pos=3, full=true
	// Bytes(): buf[3:] = "45678AB" (7 bytes), buf[:3] = "CDE" (3 bytes) = "45678ABCDE" - 10 bytes, wrong
	// Let me trace more carefully:
	// Initial: buf=[0,0,0,0,0,0,0,0,0,0], pos=0, full=false
	// Write "12345678": firstPart=10, n=8, copy to buf[0:8]
	// buf=[1,2,3,4,5,6,7,8,0,0], pos=8, full=false
	// Write "ABCDE": firstPart=10-8=2, n=5
	// Since pos+n=13 >= size=10, full=true
	// Since firstPart=2 < n=5, copy "AB" to buf[8:10], copy "CDE" to buf[0:3]
	// buf=[C,D,E,4,5,6,7,8,A,B], pos=(8+5)%10=3
	// Bytes(): full=true, pos=3
	// result = buf[3:] + buf[:3] = [4,5,6,7,8,A,B] + [C,D,E] = "45678ABCDE"
	// But we expect "678ABCDE" which is 8 bytes - that's wrong expectation
	// Actually the last 10 bytes of "12345678ABCDE" (13 chars) is "345678ABCDE" (11 chars) - wrong again
	// "12345678ABCDE" has 13 chars, last 10 = "45678ABCDE" - that's correct!
	if got != "45678ABCDE" {
		t.Errorf("expected '45678ABCDE', got %q", got)
	}
}

func TestTailBuffer_LargeOverflow(t *testing.T) {
	tb := newTailBuffer(5)
	_, _ = tb.Write([]byte("0123456789ABCDEF")) // 16 bytes into 5-byte buffer
	got := string(tb.Bytes())
	// Should keep last 5 bytes
	if got != "BCDEF" {
		t.Errorf("expected 'BCDEF', got %q", got)
	}
}

func TestTailBuffer_BytesAtPosZeroFull(t *testing.T) {
	// Test the optimization path when pos=0 and full=true
	// This happens when the buffer is exactly filled to capacity
	tb := newTailBuffer(10)
	_, _ = tb.Write([]byte("1234567890")) // Exactly 10 bytes, pos becomes 0, full=true
	got := string(tb.Bytes())
	if got != "1234567890" {
		t.Errorf("expected '1234567890', got %q", got)
	}

	// Verify multiple calls return the same result
	got2 := string(tb.Bytes())
	if got != got2 {
		t.Errorf("expected consistent results, got %q and %q", got, got2)
	}
}

func TestTailBuffer_BytesAtPosZeroFullAfterWrap(t *testing.T) {
	// Test pos=0 and full=true after wrap-around
	// Write 20 bytes into 10-byte buffer: pos = 20 % 10 = 0, full=true
	tb := newTailBuffer(10)
	_, _ = tb.Write([]byte("12345678901234567890")) // 20 bytes
	got := string(tb.Bytes())
	// Should keep last 10 bytes
	if got != "1234567890" {
		t.Errorf("expected '1234567890', got %q", got)
	}
}

// ============================================================
// captureBuffer tests
// ============================================================

func TestNewCaptureBuffer_Zero(t *testing.T) {
	buf := newCaptureBuffer(0)
	if buf != nil {
		t.Error("expected nil for zero content length")
	}
}

func TestNewCaptureBuffer_Small(t *testing.T) {
	buf := newCaptureBuffer(100)
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	_, ok := buf.(*fullCaptureBuffer)
	if !ok {
		t.Error("expected fullCaptureBuffer for small content length")
	}
}

func TestNewCaptureBuffer_Large(t *testing.T) {
	buf := newCaptureBuffer(100 * 1024) // 100KB
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	_, ok := buf.(*tailBuffer)
	if !ok {
		t.Error("expected tailBuffer for large content length")
	}
}

func TestNewCaptureBuffer_Unknown(t *testing.T) {
	buf := newCaptureBuffer(-1) // chunked encoding
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	_, ok := buf.(*tailBuffer)
	if !ok {
		t.Error("expected tailBuffer for unknown content length")
	}
}

// ============================================================
// TokenUsage helper method tests
// ============================================================

func TestTokenUsage_BillableInputTokens_Basic(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:     observed(1000),
		CompletionTokens: observed(500),
	}
	got := usage.BillableInputTokens()
	// uncached = 1000, no cache = 1000.0
	if got != 1000.0 {
		t.Errorf("expected 1000.0, got %f", got)
	}
}

func TestTokenUsage_BillableInputTokens_WithCache(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:         observed(1000),
		CompletionTokens:     observed(500),
		CacheReadInputTokens: observed(600), // 600 from cache
		CacheCreation: &CacheCreation{
			InputTokens: observed(200), // 200 written to cache
		},
	}
	got := usage.BillableInputTokens()
	// uncached = 1000 - 600 = 400
	// billable = 400 + 600*0.1 + 200*1.25 = 400 + 60 + 250 = 710
	if got != 710.0 {
		t.Errorf("expected 710.0, got %f", got)
	}
}

func TestTokenUsage_BillableInputTokens_Nil(t *testing.T) {
	var usage *TokenUsage
	got := usage.BillableInputTokens()
	if got != 0 {
		t.Errorf("expected 0 for nil usage, got %f", got)
	}
}

func TestTokenUsage_CacheHitRatio(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:         observed(1000),
		CacheReadInputTokens: observed(600),
	}
	got := usage.CacheHitRatio()
	if got != 0.6 {
		t.Errorf("expected 0.6, got %f", got)
	}
}

func TestTokenUsage_CacheHitRatio_Zero(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens: observed(0),
	}
	got := usage.CacheHitRatio()
	if got != 0 {
		t.Errorf("expected 0 for zero prompt tokens, got %f", got)
	}
}

func TestTokenUsage_CacheHitRatio_Nil(t *testing.T) {
	var usage *TokenUsage
	got := usage.CacheHitRatio()
	if got != 0 {
		t.Errorf("expected 0 for nil usage, got %f", got)
	}
}

// ============================================================
// parseTokenUsage tests - OpenAI format
// ============================================================

func TestParseTokenUsage_OpenAI(t *testing.T) {
	data := []byte(`{"id":"chatcmpl-123","choices":[{"message":{"content":"Hello"}}],"usage":{"prompt_tokens":25,"completion_tokens":150,"total_tokens":175}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 25 {
		t.Errorf("expected PromptTokens=25, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 150 {
		t.Errorf("expected CompletionTokens=150, got %d", usage.CompletionTokens.Value)
	}
	if !usage.TotalTokens.Present || usage.TotalTokens.Value != 175 {
		t.Errorf("expected observed TotalTokens=175, got %#v", usage.TotalTokens)
	}
}

func TestParseTokenUsage_OpenAI_WithPromptTokenDetails(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":45}}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.CacheReadInputTokens.Value != 45 {
		t.Errorf("expected CacheReadInputTokens=45, got %d", usage.CacheReadInputTokens.Value)
	}
}

func TestParseTokenUsage_OpenAI_WithCompletionReasoningTokens(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"completion_tokens_details":{"reasoning_tokens":18}}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.ReasoningTokens.Value != 18 {
		t.Errorf("expected ReasoningTokens=18, got %d", usage.ReasoningTokens.Value)
	}
}

func TestParseTokenUsage_OpenAIRealtime_WithInputTokenDetails(t *testing.T) {
	data := []byte(`{"type":"response.completed","response":{"id":"resp_123","usage":{"input_tokens":64,"output_tokens":16,"total_tokens":80,"input_token_details":{"cached_tokens":9}}}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 64 {
		t.Errorf("expected PromptTokens=64, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 16 {
		t.Errorf("expected CompletionTokens=16, got %d", usage.CompletionTokens.Value)
	}
	if usage.TotalTokens.Value != 80 {
		t.Errorf("expected TotalTokens=80, got %d", usage.TotalTokens.Value)
	}
	if usage.CacheReadInputTokens.Value != 9 {
		t.Errorf("expected CacheReadInputTokens=9, got %d", usage.CacheReadInputTokens.Value)
	}
}

func TestParseTokenUsage_OpenAIResponses_WithInputTokensDetails(t *testing.T) {
	data := []byte(`{"id":"resp_123","object":"response","usage":{"input_tokens":64,"output_tokens":16,"total_tokens":80,"input_tokens_details":{"cached_tokens":9}}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 64 {
		t.Errorf("expected PromptTokens=64, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 16 {
		t.Errorf("expected CompletionTokens=16, got %d", usage.CompletionTokens.Value)
	}
	if usage.TotalTokens.Value != 80 {
		t.Errorf("expected TotalTokens=80, got %d", usage.TotalTokens.Value)
	}
	if usage.CacheReadInputTokens.Value != 9 {
		t.Errorf("expected CacheReadInputTokens=9, got %d", usage.CacheReadInputTokens.Value)
	}
}

func TestParseTokenUsage_OpenAIResponses_WithOutputReasoningTokens(t *testing.T) {
	data := []byte(`{"id":"resp_123","object":"response","usage":{"input_tokens":64,"output_tokens":16,"total_tokens":80,"output_tokens_details":{"reasoning_tokens":12}}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 64 {
		t.Errorf("expected PromptTokens=64, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 16 {
		t.Errorf("expected CompletionTokens=16, got %d", usage.CompletionTokens.Value)
	}
	if usage.ReasoningTokens.Value != 12 {
		t.Errorf("expected ReasoningTokens=12, got %d", usage.ReasoningTokens.Value)
	}
}

// ============================================================
// parseTokenUsage tests - Claude format
// ============================================================

func TestParseTokenUsage_Claude_Basic(t *testing.T) {
	data := []byte(`{"content":[{"text":"Hello"}],"usage":{"input_tokens":25,"output_tokens":150}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 25 {
		t.Errorf("expected PromptTokens=25, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 150 {
		t.Errorf("expected CompletionTokens=150, got %d", usage.CompletionTokens.Value)
	}
	if usage.TotalTokens.Present {
		t.Errorf("expected missing total to remain absent, got %#v", usage.TotalTokens)
	}
}

func TestParseTokenUsage_Claude_WithCache_Flat(t *testing.T) {
	// message_delta format (flat fields)
	data := []byte(`{"usage":{"input_tokens":2009,"output_tokens":125,"cache_creation_input_tokens":358,"cache_read_input_tokens":19040,"service_tier":"standard"}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 2009 {
		t.Errorf("expected PromptTokens=2009, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 125 {
		t.Errorf("expected CompletionTokens=125, got %d", usage.CompletionTokens.Value)
	}
	if usage.CacheReadInputTokens.Value != 19040 {
		t.Errorf("expected CacheReadInputTokens=19040, got %d", usage.CacheReadInputTokens.Value)
	}
	if usage.CacheCreation == nil {
		t.Fatal("expected non-nil CacheCreation")
	}
	if usage.CacheCreation.InputTokens.Value != 358 {
		t.Errorf("expected CacheCreation.InputTokens=358, got %d", usage.CacheCreation.InputTokens.Value)
	}
	if usage.ServiceTier != "standard" {
		t.Errorf("expected ServiceTier='standard', got %q", usage.ServiceTier)
	}
}

func TestParseTokenUsage_Claude_WithCache_Nested(t *testing.T) {
	// message_start format (nested cache_creation object)
	data := []byte(`{"usage":{"input_tokens":100,"output_tokens":50,"cache_creation":{"ephemeral_1h_input_tokens":200,"ephemeral_5m_input_tokens":0},"cache_creation_input_tokens":200,"cache_read_input_tokens":500}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens.Value)
	}
	if usage.CacheReadInputTokens.Value != 500 {
		t.Errorf("expected CacheReadInputTokens=500, got %d", usage.CacheReadInputTokens.Value)
	}
	if usage.CacheCreation == nil {
		t.Fatal("expected non-nil CacheCreation")
	}
	if usage.CacheCreation.InputTokens.Value != 200 {
		t.Errorf("expected CacheCreation.InputTokens=200, got %d", usage.CacheCreation.InputTokens.Value)
	}
	if usage.CacheCreation.Ephemeral1hInputTokens.Value != 200 {
		t.Errorf("expected Ephemeral1hInputTokens=200, got %d", usage.CacheCreation.Ephemeral1hInputTokens.Value)
	}
	if usage.CacheCreation.Ephemeral5mInputTokens.Value != 0 {
		t.Errorf("expected Ephemeral5mInputTokens=0, got %d", usage.CacheCreation.Ephemeral5mInputTokens.Value)
	}
}

// ============================================================
// parseTokenUsage tests - Gemini format
// ============================================================

func TestParseTokenUsage_Gemini(t *testing.T) {
	data := []byte(`{"candidates":[{"content":{"parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":25,"candidatesTokenCount":150,"totalTokenCount":175}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 25 {
		t.Errorf("expected PromptTokens=25, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 150 {
		t.Errorf("expected CompletionTokens=150, got %d", usage.CompletionTokens.Value)
	}
	if usage.TotalTokens.Value != 175 {
		t.Errorf("expected TotalTokens=175, got %d", usage.TotalTokens.Value)
	}
}

func TestParseTokenUsage_Gemini_WithCache(t *testing.T) {
	data := []byte(`{"candidates":[{}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"totalTokenCount":150,"cachedContentTokenCount":30}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.CacheReadInputTokens.Value != 30 {
		t.Errorf("expected CacheReadInputTokens=30, got %d", usage.CacheReadInputTokens.Value)
	}
}

// ============================================================
// parseTokenUsage tests - Edge cases
// ============================================================

func TestParseTokenUsage_Empty(t *testing.T) {
	usage := Parse([]byte{})
	if usage != nil {
		t.Error("expected nil for empty data")
	}
}

func TestParseTokenUsage_NoUsage(t *testing.T) {
	data := []byte(`{"id":"123","content":"hello"}`)
	usage := Parse(data)
	if usage != nil {
		t.Error("expected nil when no usage field")
	}
}

func TestParseTokenUsage_NullUsage(t *testing.T) {
	data := []byte(`{"id":"123","usage":null}`)
	usage := Parse(data)
	if usage != nil {
		t.Error("expected nil for null usage")
	}
}

func TestParseTokenUsage_ZeroTokens(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
	usage := Parse(data)
	if usage == nil || !usage.PromptTokens.Present || !usage.CompletionTokens.Present || !usage.TotalTokens.Present {
		t.Fatalf("expected explicit zero counts to remain observed, got %#v", usage)
	}
}

func TestParseTokenUsage_InvalidJSON(t *testing.T) {
	data := []byte(`{invalid json}`)
	usage := Parse(data)
	if usage != nil {
		t.Error("expected nil for invalid JSON")
	}
}

// ============================================================
// parseTokenUsage tests - Bracket matching (truncated JSON)
// ============================================================

func TestParseTokenUsage_TruncatedJSON_OpenAI(t *testing.T) {
	// Simulates tail buffer capturing only the end of a response
	data := []byte(`...truncated content..."}],"usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage from bracket matching")
	}
	if usage.PromptTokens.Value != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 200 {
		t.Errorf("expected CompletionTokens=200, got %d", usage.CompletionTokens.Value)
	}
}

func TestParseTokenUsage_TruncatedJSON_Claude(t *testing.T) {
	data := []byte(`...truncated...","usage":{"input_tokens":50,"output_tokens":100,"cache_read_input_tokens":20}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage from bracket matching")
	}
	if usage.PromptTokens.Value != 50 {
		t.Errorf("expected PromptTokens=50, got %d", usage.PromptTokens.Value)
	}
	if usage.CacheReadInputTokens.Value != 20 {
		t.Errorf("expected CacheReadInputTokens=20, got %d", usage.CacheReadInputTokens.Value)
	}
}

func TestParseTokenUsage_TruncatedJSON_Gemini(t *testing.T) {
	data := []byte(`...truncated...,"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":70,"totalTokenCount":100}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage from bracket matching")
	}
	if usage.PromptTokens.Value != 30 {
		t.Errorf("expected PromptTokens=30, got %d", usage.PromptTokens.Value)
	}
}

func TestParseTokenUsage_UsageInContent(t *testing.T) {
	// Test that "usage" in string content doesn't get mistakenly parsed
	// This tests the `"usage":` pattern matching
	data := []byte(`{"content":"Let me explain the usage of this function","usage":{"prompt_tokens":10,"completion_tokens":20}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 10 {
		t.Errorf("expected PromptTokens=10, got %d", usage.PromptTokens.Value)
	}
}

// ============================================================
// parseTokenUsage tests - With prefix data
// ============================================================

func TestParseTokenUsage_WithPrefix(t *testing.T) {
	// JSON with leading non-JSON content (like in SSE)
	data := []byte(`data: {"id":"123","usage":{"prompt_tokens":50,"completion_tokens":100}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 50 {
		t.Errorf("expected PromptTokens=50, got %d", usage.PromptTokens.Value)
	}
}

func TestParseTokenUsage_UsageCrossesTailBufferBoundary(t *testing.T) {
	// Simulates when usage JSON is split across tail buffer boundary
	// The bracket matching fallback should handle this
	data := []byte(`truncated_content","usage":{"prompt_tokens":500,"completion_tokens":250,"total_tokens":750}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage from bracket matching")
	}
	if usage.PromptTokens.Value != 500 {
		t.Errorf("expected PromptTokens=500, got %d", usage.PromptTokens.Value)
	}
	if usage.CompletionTokens.Value != 250 {
		t.Errorf("expected CompletionTokens=250, got %d", usage.CompletionTokens.Value)
	}
	if usage.TotalTokens.Value != 750 {
		t.Errorf("expected TotalTokens=750, got %d", usage.TotalTokens.Value)
	}
}

func TestParseTokenUsage_UsageKeyTruncated(t *testing.T) {
	// When "usage": key itself is truncated, should return nil gracefully
	data := []byte(`sage":{"prompt_tokens":100}}`)
	usage := Parse(data)
	// Should not find usage since the key is incomplete
	if usage != nil {
		t.Error("expected nil usage when 'usage' key is truncated")
	}
}

func TestParseTokenUsage_NestedUsageField(t *testing.T) {
	// Test that we handle nested objects that might contain "usage" text
	data := []byte(`{"metadata":{"description":"Check usage stats"},"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	usage := Parse(data)
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens.Value != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens.Value)
	}
}

// ============================================================
// Benchmark tests
// ============================================================

func BenchmarkParseTokenUsage_OpenAI(b *testing.B) {
	data := []byte(`{"id":"chatcmpl-123","choices":[{"message":{"content":"Hello world, this is a test response"}}],"usage":{"prompt_tokens":25,"completion_tokens":150,"total_tokens":175}}`)
	b.ResetTimer()
	for b.Loop() {
		Parse(data)
	}
}

func BenchmarkParseTokenUsage_Claude_WithCache(b *testing.B) {
	data := []byte(`{"content":[{"text":"Hello"}],"usage":{"input_tokens":2009,"output_tokens":125,"cache_creation_input_tokens":358,"cache_read_input_tokens":19040,"service_tier":"standard"}}`)
	b.ResetTimer()
	for b.Loop() {
		Parse(data)
	}
}

func BenchmarkParseTokenUsage_Truncated(b *testing.B) {
	data := []byte(`...truncated content that is quite long to simulate real response..."}],"usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`)
	b.ResetTimer()
	for b.Loop() {
		Parse(data)
	}
}

func BenchmarkTailBuffer_Write(b *testing.B) {
	tb := newTailBuffer(4096)
	data := bytes.Repeat([]byte("x"), 1024)
	b.ResetTimer()
	for b.Loop() {
		_, _ = tb.Write(data)
	}
}

// ============================================================
// ToModelFields tests (Phase 4a-4)
// ============================================================

func TestTokenUsage_ToModelFields_Nil(t *testing.T) {
	var usage *TokenUsage
	prompt, completion, total, reasoning, cacheRead, cacheCreate, details := usage.ToModelFields()
	if prompt != nil || completion != nil || total != nil || reasoning != nil || cacheRead != nil || cacheCreate != nil || details != nil {
		t.Error("expected all nil for nil usage")
	}
}

func TestTokenUsage_ToModelFields_Basic(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:     observed(100),
		CompletionTokens: observed(200),
		TotalTokens:      observed(300),
	}
	prompt, completion, total, reasoning, cacheRead, cacheCreate, details := usage.ToModelFields()

	if prompt == nil || *prompt != 100 {
		t.Errorf("expected PromptTokens=100, got %v", prompt)
	}
	if completion == nil || *completion != 200 {
		t.Errorf("expected CompletionTokens=200, got %v", completion)
	}
	if total == nil || *total != 300 {
		t.Errorf("expected TotalTokens=300, got %v", total)
	}
	if reasoning != nil {
		t.Errorf("expected nil reasoning, got %v", reasoning)
	}
	if cacheRead != nil {
		t.Errorf("expected nil cacheRead, got %v", cacheRead)
	}
	if cacheCreate != nil {
		t.Errorf("expected nil cacheCreate, got %v", cacheCreate)
	}
	if details != nil {
		t.Errorf("expected nil details, got %v", details)
	}
}

func TestTokenUsage_ToModelFields_WithCache(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:         observed(1000),
		CompletionTokens:     observed(500),
		TotalTokens:          observed(1500),
		CacheReadInputTokens: observed(600),
		CacheCreation: &CacheCreation{
			InputTokens:            observed(200),
			Ephemeral1hInputTokens: observed(150),
			Ephemeral5mInputTokens: observed(50),
		},
	}
	prompt, completion, total, _, cacheRead, cacheCreate, details := usage.ToModelFields()

	if prompt == nil || *prompt != 1000 {
		t.Errorf("expected PromptTokens=1000, got %v", prompt)
	}
	if completion == nil || *completion != 500 {
		t.Errorf("expected CompletionTokens=500, got %v", completion)
	}
	if total == nil || *total != 1500 {
		t.Errorf("expected TotalTokens=1500, got %v", total)
	}
	if cacheRead == nil || *cacheRead != 600 {
		t.Errorf("expected CacheReadInputTokens=600, got %v", cacheRead)
	}
	if cacheCreate == nil || *cacheCreate != 200 {
		t.Errorf("expected CacheCreationInputTokens=200, got %v", cacheCreate)
	}
	if details == nil {
		t.Fatal("expected non-nil details")
	}
	// Verify JSON contains expected fields
	if !bytes.Contains([]byte(*details), []byte(`"ephemeral_1h_input_tokens":150`)) {
		t.Errorf("details should contain ephemeral_1h_input_tokens, got %s", *details)
	}
	if !bytes.Contains([]byte(*details), []byte(`"ephemeral_5m_input_tokens":50`)) {
		t.Errorf("details should contain ephemeral_5m_input_tokens, got %s", *details)
	}
}

func TestTokenUsage_ToModelFields_WithReasoningTokens(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:     observed(100),
		CompletionTokens: observed(200),
		TotalTokens:      observed(300),
		ReasoningTokens:  observed(75),
	}
	_, _, _, reasoning, _, _, _ := usage.ToModelFields()

	if reasoning == nil || *reasoning != 75 {
		t.Errorf("expected ReasoningTokens=75, got %v", reasoning)
	}
}

func TestTokenUsage_ToModelFields_WithServiceTier(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:     observed(100),
		CompletionTokens: observed(200),
		TotalTokens:      observed(300),
		ServiceTier:      "standard",
	}
	_, _, _, _, _, _, details := usage.ToModelFields()

	if details == nil {
		t.Fatal("expected non-nil details for service_tier")
	}
	if !bytes.Contains([]byte(*details), []byte(`"service_tier":"standard"`)) {
		t.Errorf("details should contain service_tier, got %s", *details)
	}
}

func TestTokenUsage_ToModelFields_ObservedZeroCacheStored(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:         observed(100),
		CompletionTokens:     observed(200),
		TotalTokens:          observed(300),
		CacheReadInputTokens: observed(0),
	}
	_, _, _, _, cacheRead, _, _ := usage.ToModelFields()

	if cacheRead == nil || *cacheRead != 0 {
		t.Errorf("expected pointer for observed zero CacheReadInputTokens, got %v", cacheRead)
	}
}

func TestTokenUsage_ToModelFields_CacheCreationWithZeroTokens(t *testing.T) {
	usage := &TokenUsage{
		PromptTokens:     observed(100),
		CompletionTokens: observed(200),
		TotalTokens:      observed(300),
		CacheCreation: &CacheCreation{
			InputTokens: observed(0),
		},
	}
	_, _, _, _, _, cacheCreate, _ := usage.ToModelFields()

	if cacheCreate == nil || *cacheCreate != 0 {
		t.Errorf("expected pointer for observed zero CacheCreation.InputTokens, got %v", cacheCreate)
	}
}

// ============================================================
// Integration benchmark for full flow
// ============================================================

func BenchmarkTokenUsage_ToModelFields(b *testing.B) {
	usage := &TokenUsage{
		PromptTokens:         observed(2009),
		CompletionTokens:     observed(125),
		TotalTokens:          observed(2134),
		CacheReadInputTokens: observed(19040),
		CacheCreation: &CacheCreation{
			InputTokens:            observed(358),
			Ephemeral1hInputTokens: observed(200),
			Ephemeral5mInputTokens: observed(158),
		},
		ServiceTier: "standard",
	}
	b.ResetTimer()
	for b.Loop() {
		usage.ToModelFields()
	}
}
