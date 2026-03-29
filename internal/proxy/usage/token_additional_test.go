package usage

import "testing"

func TestTokenUsageMergeHandlesNilAndCacheCreation(t *testing.T) {
	other := &TokenUsage{
		PromptTokens:         10,
		CompletionTokens:     5,
		TotalTokens:          15,
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

	values := map[string]interface{}{
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

	usageMap := map[string]interface{}{
		"cache_creation_input_tokens": float64(11),
		"cache_creation": map[string]interface{}{
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

	tokenOnly := buildCacheCreationFromUsageMap(map[string]interface{}{
		"cache_creation_input_tokens": int64(4),
	})
	if tokenOnly == nil || tokenOnly.InputTokens != 4 {
		t.Fatalf("tokenOnly = %#v, want cache creation with input tokens", tokenOnly)
	}
}
