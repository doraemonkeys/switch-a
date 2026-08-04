package adapters

import "testing"

func TestExtractUsageUsesOnlyReviewedEnvelopeLocations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		data       string
		prompt     int64
		completion int64
		total      int64
		cacheRead  int64
		reasoning  int64
		want       bool
	}{
		{
			name:       "root standard aliases and details",
			data:       "{\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"input_tokens_details\":{\"cached_tokens\":2},\"output_tokens_details\":{\"reasoning_tokens\":1},\"service_tier\":\" FLEX \",\"cache_creation_input_tokens\":3,\"cache_creation\":{\"ephemeral_1h_input_tokens\":2,\"ephemeral_5m_input_tokens\":1}}}",
			prompt:     10,
			completion: 4,
			total:      14,
			cacheRead:  2,
			reasoning:  1,
			want:       true,
		},
		{
			name:       "google root metadata",
			data:       "{\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":3,\"totalTokenCount\":12,\"cachedContentTokenCount\":2}}",
			prompt:     8,
			completion: 3,
			total:      12,
			cacheRead:  2,
			want:       true,
		},
		{
			name:       "anthropic message usage",
			data:       "{\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}",
			prompt:     5,
			completion: 2,
			total:      7,
			want:       true,
		},
		{
			name:       "responses nested usage",
			data:       "{\"response\":{\"usage\":{\"input_tokens\":6,\"output_tokens\":4}}}",
			prompt:     6,
			completion: 4,
			total:      10,
			want:       true,
		},
		{name: "arbitrary nested usage", data: "{\"content\":{\"usage\":{\"prompt_tokens\":99}}}"},
		{name: "nonobject usage", data: "{\"usage\":[1]}"},
		{name: "zero usage", data: "{\"usage\":{\"prompt_tokens\":0}}"},
		{name: "valid nonobject root", data: "[]"},
		{name: "malformed", data: "{\"usage\":"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			usage := ExtractUsage([]byte(test.data), nil)
			if (usage != nil) != test.want {
				t.Fatalf("usage = %#v", usage)
			}
			if usage == nil {
				return
			}
			if usage.PromptTokens != test.prompt ||
				usage.CompletionTokens != test.completion ||
				usage.TotalTokens != test.total ||
				usage.CacheReadInputTokens != test.cacheRead ||
				usage.ReasoningTokens != test.reasoning {
				t.Fatalf("usage = %#v", usage)
			}
			if test.name == "root standard aliases and details" {
				if usage.ServiceTier != "flex" || usage.CacheCreation == nil ||
					usage.CacheCreation.InputTokens != 3 ||
					usage.CacheCreation.Ephemeral1hInputTokens != 2 ||
					usage.CacheCreation.Ephemeral5mInputTokens != 1 {
					t.Fatalf("extended usage = %#v", usage)
				}
			}
		})
	}
}

func TestUsageIgnoresInvalidNumericFormsWithoutLosingValidFields(t *testing.T) {
	t.Parallel()
	usage := ExtractUsage([]byte(
		"{\"usage\":{\"prompt_tokens\":1.5,\"input_tokens\":7,\"completion_tokens\":\"4\",\"output_tokens\":3,\"total_tokens\":9223372036854775808,\"cache_read_input_tokens\":null,\"prompt_tokens_details\":{\"cached_tokens\":2.2},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}",
	), nil)
	if usage == nil || usage.PromptTokens != 7 || usage.CompletionTokens != 3 ||
		usage.TotalTokens != 10 || usage.CacheReadInputTokens != 0 ||
		usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %#v", usage)
	}
}
