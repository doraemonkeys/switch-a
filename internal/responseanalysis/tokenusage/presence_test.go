package tokenusage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFullAndRecoveryParsersPreserveCacheWritePresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		fixture     string
		wantValue   int64
		wantPresent bool
	}{
		{name: "explicit zero", fixture: "openai-cache-write-zero.json", wantPresent: true},
		{name: "positive", fixture: "openai-cache-write-positive.json", wantValue: 5, wantPresent: true},
		{name: "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := []byte(`{"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}`)
			if test.fixture != "" {
				var err error
				data, err = os.ReadFile(filepath.Join("..", "testdata", "token-usage", test.fixture))
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, input := range [][]byte{data, append(append([]byte("prefix "), data...), []byte(" trailing")...)} {
				usage := Parse(input)
				if usage == nil {
					t.Fatal("expected usage")
				}
				got := ObservedCount{}
				if usage.CacheCreation != nil {
					got = usage.CacheCreation.InputTokens
				}
				if got.Present != test.wantPresent || got.Value != test.wantValue {
					t.Fatalf("cache write = %#v, want value=%d present=%t", got, test.wantValue, test.wantPresent)
				}
			}
		})
	}
}

func TestFullAndRecoveryParsersPreserveMissingAndInvalidCounts(t *testing.T) {
	t.Parallel()
	data := []byte(`{"usage":{"prompt_tokens":0,"input_tokens":9,"completion_tokens":-2,"output_tokens":7,"total_tokens":9223372036854775808,"input_tokens_details":{"cached_tokens":0},"input_token_details":{"cache_write_tokens":"3"},"output_tokens_details":{"reasoning_tokens":1.5}}}`)
	for _, input := range [][]byte{data, append(append([]byte("prefix "), data...), []byte(" trailing")...)} {
		usage := Parse(input)
		if usage == nil {
			t.Fatal("expected usage")
		}
		if usage.PromptTokens != observed(0) || usage.CompletionTokens != observed(-2) || usage.CacheReadInputTokens != observed(0) {
			t.Fatalf("usage = %#v", usage)
		}
		if usage.TotalTokens.Present || usage.ReasoningTokens.Present || usage.CacheCreation != nil {
			t.Fatalf("missing or invalid fact became observed: %#v", usage)
		}
	}
}

func TestFullAndRecoveryParsersHonorExplicitZeroAliasPrecedence(t *testing.T) {
	t.Parallel()
	data := []byte(`{"usage":{"prompt_tokens":1,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"input_tokens_details":{"cached_tokens":6,"cache_write_tokens":7},"completion_tokens":0,"completion_tokens_details":{"reasoning_tokens":0},"output_tokens_details":{"reasoning_tokens":8}}}`)
	for _, input := range [][]byte{data, append(append([]byte("prefix "), data...), []byte(" trailing")...)} {
		usage := Parse(input)
		if usage == nil || usage.CacheCreation == nil || usage.CacheCreation.InputTokens != observed(0) ||
			usage.CacheReadInputTokens != observed(0) || usage.ReasoningTokens != observed(0) {
			t.Fatalf("usage = %#v", usage)
		}
	}
}

func TestFullAndRecoveryParsersBoundExponentForms(t *testing.T) {
	t.Parallel()
	data := []byte(`{"usage":{"prompt_tokens":1e3,"completion_tokens":1.5,"total_tokens":1e1000000}}`)
	for _, input := range [][]byte{data, append(append([]byte("prefix "), data...), []byte(" trailing")...)} {
		usage := Parse(input)
		if usage == nil || usage.PromptTokens != observed(1000) || usage.CompletionTokens.Present || usage.TotalTokens.Present {
			t.Fatalf("usage = %#v", usage)
		}
	}
}

func TestToModelFieldsPreservesAllZeroObservedEnvelope(t *testing.T) {
	t.Parallel()
	usage := Parse([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	if usage == nil {
		t.Fatal("expected usage")
	}
	prompt, completion, total, _, _, _, _ := usage.ToModelFields()
	if prompt == nil || *prompt != 0 || completion == nil || *completion != 0 || total == nil || *total != 0 {
		t.Fatalf("all-zero model fields = prompt:%v completion:%v total:%v", prompt, completion, total)
	}
}

func TestOverlayObservedAndAccumulateHaveDistinctSemantics(t *testing.T) {
	t.Parallel()
	earlier := &TokenUsage{
		PromptTokens:         observed(10),
		CacheReadInputTokens: observed(3),
		CacheCreation:        &CacheCreation{InputTokens: observed(2)},
	}
	later := &TokenUsage{CompletionTokens: observed(4), PromptTokens: observed(0)}
	overlaid := earlier.Clone().OverlayObserved(later)
	if overlaid.PromptTokens != observed(0) || overlaid.CompletionTokens != observed(4) ||
		overlaid.CacheReadInputTokens != observed(3) || overlaid.CacheCreation == nil || overlaid.CacheCreation.InputTokens != observed(2) {
		t.Fatalf("overlaid = %#v", overlaid)
	}

	accumulated := earlier.Clone().Accumulate(later)
	if accumulated.PromptTokens != observed(10) || accumulated.CompletionTokens != observed(4) ||
		accumulated.CacheReadInputTokens != observed(3) || !accumulated.PromptTokens.Present {
		t.Fatalf("accumulated = %#v", accumulated)
	}
	zeroOnly := (&TokenUsage{}).Accumulate(&TokenUsage{ReasoningTokens: observed(0)})
	if zeroOnly.ReasoningTokens != observed(0) {
		t.Fatalf("zero presence was not unioned: %#v", zeroOnly)
	}
	conflict := (&TokenUsage{ServiceTier: "standard"}).Accumulate(&TokenUsage{ServiceTier: "priority"})
	if conflict.ServiceTier != "" {
		t.Fatalf("conflicting service tiers = %q, want empty", conflict.ServiceTier)
	}
}

func TestToModelFieldsPreservesPartialZeroAndNegativeFacts(t *testing.T) {
	t.Parallel()
	usage := &TokenUsage{
		PromptTokens:         observed(0),
		ReasoningTokens:      observed(-1),
		CacheReadInputTokens: observed(0),
		CacheCreation: &CacheCreation{
			InputTokens:            observed(-2),
			Ephemeral1hInputTokens: observed(0),
		},
	}
	prompt, completion, total, reasoning, cacheRead, cacheWrite, details := usage.ToModelFields()
	if prompt == nil || *prompt != 0 || completion != nil || total != nil || reasoning == nil || *reasoning != -1 ||
		cacheRead == nil || *cacheRead != 0 || cacheWrite == nil || *cacheWrite != -2 {
		t.Fatalf("model fields = prompt:%v completion:%v total:%v reasoning:%v cacheRead:%v cacheWrite:%v", prompt, completion, total, reasoning, cacheRead, cacheWrite)
	}
	if details == nil || *details != `{"ephemeral_1h_input_tokens":0}` {
		t.Fatalf("details = %v", details)
	}
}
