package tokenusage

import (
	"bytes"
	"testing"
)

func TestTokenUsageMergeHandlesNilAndCacheCreation(t *testing.T) {
	other := &TokenUsage{
		PromptTokens:         10,
		CompletionTokens:     5,
		TotalTokens:          15,
		ReasoningTokens:      6,
		CacheReadInputTokens: 3,
		CacheCreation: &CacheCreation{
			InputTokens:            7,
			Ephemeral1hInputTokens: 2,
			Ephemeral5mInputTokens: 1,
		},
	}

	if cloned := (*TokenUsage)(nil).Merge(other); cloned == nil || cloned.TotalTokens != 15 {
		t.Fatalf("nil Merge() = %#v, want cloned usage totals", cloned)
	}

	current := &TokenUsage{
		PromptTokens:         1,
		CompletionTokens:     2,
		TotalTokens:          3,
		ReasoningTokens:      4,
		CacheReadInputTokens: 4,
	}
	merged := current.Merge(other)
	if merged != current {
		t.Fatal("expected Merge to update the receiver in place")
	}
	if merged.PromptTokens != 11 || merged.CompletionTokens != 7 || merged.TotalTokens != 18 {
		t.Fatalf("merged totals = %#v, want receiver plus other", merged)
	}
	if merged.CacheReadInputTokens != 7 {
		t.Fatalf("CacheReadInputTokens = %d, want 7", merged.CacheReadInputTokens)
	}
	if merged.ReasoningTokens != 10 {
		t.Fatalf("ReasoningTokens = %d, want 10", merged.ReasoningTokens)
	}
	if merged.CacheCreation == nil {
		t.Fatal("expected cache creation totals to be initialized")
	}
	if merged.CacheCreation.InputTokens != 7 ||
		merged.CacheCreation.Ephemeral1hInputTokens != 2 ||
		merged.CacheCreation.Ephemeral5mInputTokens != 1 {
		t.Fatalf("cache creation = %#v, want merged cache creation totals", merged.CacheCreation)
	}
}

func TestUsageParsingHelpersHandleTypedAndMissingValues(t *testing.T) {
	t.Parallel()

	if got, ok := usageInt64(float64(42)); !ok || got != 42 {
		t.Fatalf("usageInt64(float64) = (%d, %t), want (42, true)", got, ok)
	}
	if got, ok := usageInt64(int64(7)); !ok || got != 7 {
		t.Fatalf("usageInt64(int64) = (%d, %t), want (7, true)", got, ok)
	}
	if got, ok := usageInt64(int(3)); !ok || got != 3 {
		t.Fatalf("usageInt64(int) = (%d, %t), want (3, true)", got, ok)
	}
	if got, ok := usageInt64("nope"); ok || got != 0 {
		t.Fatalf("usageInt64(string) = (%d, %t), want (0, false)", got, ok)
	}

	values := map[string]any{
		"text": "cached",
		"bad":  99,
	}
	if got := lookupUsageString(values, "text"); got != "cached" {
		t.Fatalf("lookupUsageString(text) = %q, want %q", got, "cached")
	}
	if got := lookupUsageString(values, "bad"); got != "" {
		t.Fatalf("lookupUsageString(non-string) = %q, want empty", got)
	}
	if got := lookupUsageString(values, "missing"); got != "" {
		t.Fatalf("lookupUsageString(missing) = %q, want empty", got)
	}
}

func TestBuildCacheCreationFromUsageMapParsesNestedValues(t *testing.T) {
	t.Parallel()

	if got := buildCacheCreationFromUsageMap(nil); got != nil {
		t.Fatalf("buildCacheCreationFromUsageMap(nil) = %#v, want nil", got)
	}

	usageMap := map[string]any{
		"cache_creation_input_tokens": float64(11),
		"cache_creation": map[string]any{
			"ephemeral_1h_input_tokens": float64(5),
			"ephemeral_5m_input_tokens": int64(2),
		},
	}
	cacheCreation := buildCacheCreationFromUsageMap(usageMap)
	if cacheCreation == nil {
		t.Fatal("expected cache creation values to be parsed")
	}
	if cacheCreation.InputTokens != 11 {
		t.Fatalf("InputTokens = %d, want 11", cacheCreation.InputTokens)
	}
	if cacheCreation.Ephemeral1hInputTokens != 5 || cacheCreation.Ephemeral5mInputTokens != 2 {
		t.Fatalf("cache creation = %#v, want nested ephemeral values", cacheCreation)
	}

	tokenOnly := buildCacheCreationFromUsageMap(map[string]any{
		"cache_creation_input_tokens": int64(4),
	})
	if tokenOnly == nil || tokenOnly.InputTokens != 4 {
		t.Fatalf("tokenOnly = %#v, want cache creation with input tokens", tokenOnly)
	}
}

type recordingZapLogger struct {
	msg           string
	keysAndValues []any
}

func (l *recordingZapLogger) Debugw(msg string, keysAndValues ...any) {
	l.msg = msg
	l.keysAndValues = append([]any(nil), keysAndValues...)
}

func TestFullCaptureBufferWriteAndBytesMirrorBackingBuffer(t *testing.T) {
	t.Parallel()

	buffer := &fullCaptureBuffer{buf: bytes.NewBuffer(nil)}
	written, err := buffer.Write([]byte("usage"))
	if err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
	if written != len("usage") {
		t.Fatalf("Write() wrote %d bytes, want %d", written, len("usage"))
	}
	if got := string(buffer.Bytes()); got != "usage" {
		t.Fatalf("Bytes() = %q, want %q", got, "usage")
	}
}

func TestZapLoggerAdapterHandlesNilAndForwardsStructuredDebug(t *testing.T) {
	t.Parallel()

	if adapter := NewZapLoggerAdapter(nil); adapter != nil {
		t.Fatalf("NewZapLoggerAdapter(nil) = %#v, want nil", adapter)
	}

	var nilAdapter *ZapLoggerAdapter
	nilAdapter.Debug("ignored", "k", "v")

	adapter := &ZapLoggerAdapter{}
	adapter.Debug("ignored", "k", "v")

	recording := &recordingZapLogger{}
	adapter = NewZapLoggerAdapter(recording)
	if adapter == nil {
		t.Fatal("NewZapLoggerAdapter(recording) = nil, want adapter")
	}

	adapter.Debug("usage parsed", "prompt_tokens", int64(42))
	if recording.msg != "usage parsed" {
		t.Fatalf("forwarded message = %q, want %q", recording.msg, "usage parsed")
	}
	if len(recording.keysAndValues) != 2 {
		t.Fatalf("forwarded key/value count = %d, want 2", len(recording.keysAndValues))
	}
	if recording.keysAndValues[0] != "prompt_tokens" || recording.keysAndValues[1] != int64(42) {
		t.Fatalf("forwarded key/values = %#v, want prompt_tokens=42", recording.keysAndValues)
	}
}
