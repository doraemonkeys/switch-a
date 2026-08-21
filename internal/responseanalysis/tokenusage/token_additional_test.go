package tokenusage

import (
	"bytes"
	"testing"
)

func TestTokenUsageAccumulateHandlesNilAndCacheCreation(t *testing.T) {
	other := &TokenUsage{
		PromptTokens:         observed(10),
		CompletionTokens:     observed(5),
		TotalTokens:          observed(15),
		ReasoningTokens:      observed(6),
		CacheReadInputTokens: observed(3),
		CacheCreation: &CacheCreation{
			InputTokens:            observed(7),
			Ephemeral1hInputTokens: observed(2),
			Ephemeral5mInputTokens: observed(1),
		},
	}

	if cloned := (*TokenUsage)(nil).Accumulate(other); cloned == nil || cloned.TotalTokens.Value != 15 {
		t.Fatalf("nil Accumulate() = %#v, want cloned usage totals", cloned)
	}

	current := &TokenUsage{
		PromptTokens:         observed(1),
		CompletionTokens:     observed(2),
		TotalTokens:          observed(3),
		ReasoningTokens:      observed(4),
		CacheReadInputTokens: observed(4),
	}
	merged := current.Accumulate(other)
	if merged != current {
		t.Fatal("expected Accumulate to update the receiver in place")
	}
	if merged.PromptTokens.Value != 11 || merged.CompletionTokens.Value != 7 || merged.TotalTokens.Value != 18 {
		t.Fatalf("merged totals = %#v, want receiver plus other", merged)
	}
	if merged.CacheReadInputTokens.Value != 7 {
		t.Fatalf("CacheReadInputTokens = %d, want 7", merged.CacheReadInputTokens.Value)
	}
	if merged.ReasoningTokens.Value != 10 {
		t.Fatalf("ReasoningTokens = %d, want 10", merged.ReasoningTokens.Value)
	}
	if merged.CacheCreation == nil {
		t.Fatal("expected cache creation totals to be initialized")
	}
	if merged.CacheCreation.InputTokens.Value != 7 ||
		merged.CacheCreation.Ephemeral1hInputTokens.Value != 2 ||
		merged.CacheCreation.Ephemeral5mInputTokens.Value != 1 {
		t.Fatalf("cache creation = %#v, want merged cache creation totals", merged.CacheCreation)
	}
}

func TestUsageParsingHelpersHandleTypedAndMissingValues(t *testing.T) {
	t.Parallel()

	if got := usageInt64(float64(42)); !got.Present || got.Value != 42 {
		t.Fatalf("usageInt64(float64) = %#v, want observed 42", got)
	}
	if got := usageInt64(int64(7)); !got.Present || got.Value != 7 {
		t.Fatalf("usageInt64(int64) = %#v, want observed 7", got)
	}
	if got := usageInt64(int(3)); !got.Present || got.Value != 3 {
		t.Fatalf("usageInt64(int) = %#v, want observed 3", got)
	}
	if got := usageInt64("nope"); got.Present || got.Value != 0 {
		t.Fatalf("usageInt64(string) = %#v, want absent", got)
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
	if cacheCreation.InputTokens.Value != 11 {
		t.Fatalf("InputTokens = %d, want 11", cacheCreation.InputTokens.Value)
	}
	if cacheCreation.Ephemeral1hInputTokens.Value != 5 || cacheCreation.Ephemeral5mInputTokens.Value != 2 {
		t.Fatalf("cache creation = %#v, want nested ephemeral values", cacheCreation)
	}

	tokenOnly := buildCacheCreationFromUsageMap(map[string]any{
		"cache_creation_input_tokens": int64(4),
	})
	if tokenOnly == nil || tokenOnly.InputTokens.Value != 4 {
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
