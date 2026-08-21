package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
)

func TestSemanticFieldsPreserveAbsentVersusPresentEmptyOptionals(t *testing.T) {
	tests := []struct {
		name        string
		providerErr string
		wantType    bool
		wantCode    bool
		wantReason  bool
	}{
		{name: "absent", providerErr: `{"message":""}`},
		{
			name:        "present empty",
			providerErr: `{"type":"","code":"","message":"","reason":""}`,
			wantType:    true, wantCode: true, wantReason: true,
		},
		{
			name:        "present values infer presence",
			providerErr: `{"type":"ServerError","code":"Busy","message":"Failure","reason":"Capacity"}`,
			wantType:    true, wantCode: true, wantReason: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(`{"type":"response.failed","response":{"status":"failed","error":` + test.providerErr + `}}`)
			result := New(apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, testLimits).Observe(framing.Frame{Data: data})
			defer result.Release()
			if result.Class != EventError || result.Fields == nil {
				t.Fatalf("result=%#v", result)
			}
			if result.Fields.HasType() != test.wantType || result.Fields.HasCode() != test.wantCode || !result.Fields.HasMessage() || result.Fields.HasReason() != test.wantReason {
				t.Fatalf("fields=%#v presence=%t/%t/%t/%t", result.Fields, result.Fields.HasType(), result.Fields.HasCode(), result.Fields.HasMessage(), result.Fields.HasReason())
			}
		})
	}
}

func TestDirectResponsesSyntheticTypeAndExplicitEmptyNestedTypePresence(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantType string
	}{
		{
			name: "top-level type is authoritative fallback",
			data: `{"type":"error","code":"busy","message":"Failure"}`, wantType: "error",
		},
		{
			name: "explicit empty nested type remains present",
			data: `{"type":"error","message":"Failure","error":{"type":"","code":"busy"}}`, wantType: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := New(apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, testLimits).Observe(framing.Frame{Data: []byte(test.data)})
			defer result.Release()
			if result.Class != EventError || result.Fields == nil || !result.Fields.HasType() || result.Fields.Type != test.wantType {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestResponsesMessagePresenceDistinguishesAbsentAndExplicitEmpty(t *testing.T) {
	tests := []struct {
		name        string
		kind        framing.Kind
		data        string
		wantMessage bool
	}{
		{name: "JSON absent", kind: framing.KindJSON, data: `{"type":"error","error":{}}`},
		{name: "JSON present empty", kind: framing.KindJSON, data: `{"type":"error","error":{"message":""}}`, wantMessage: true},
		{name: "JSON present value", kind: framing.KindJSON, data: `{"type":"error","error":{"message":"Failure"}}`, wantMessage: true},
		{name: "SSE direct present empty", kind: framing.KindSSE, data: `{"type":"error","code":"busy","message":""}`, wantMessage: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := New(apicontract.ErrorFamilyOpenAIResponses, test.kind, testLimits).Observe(framing.Frame{Data: []byte(test.data)})
			defer result.Release()
			if result.Class != EventError || result.Fields == nil || result.Fields.HasMessage() != test.wantMessage {
				t.Fatalf("result=%#v has_message=%t", result, result.Fields != nil && result.Fields.HasMessage())
			}
		})
	}
}

func TestBoundedUsageParserPreservesCacheWritePresence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		fixture     string
		wantValue   int64
		wantPresent bool
	}{
		{name: "explicit zero", fixture: "openai-cache-write-zero.json", wantPresent: true},
		{name: "positive", fixture: "openai-cache-write-positive.json", wantValue: 5, wantPresent: true},
		{name: "missing", fixture: "", wantPresent: false},
	} {
		test := test
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
			usage := ExtractUsage(data, nil)
			if usage == nil {
				t.Fatal("expected usage")
			}
			got := tokenusage.ObservedCount{}
			if usage.CacheCreation != nil {
				got = usage.CacheCreation.InputTokens
			}
			if got.Present != test.wantPresent || got.Value != test.wantValue {
				t.Fatalf("cache write = %#v, want value=%d present=%t", got, test.wantValue, test.wantPresent)
			}
		})
	}
}

func TestBoundedUsageParserUsesPresenceBasedAliasPrecedence(t *testing.T) {
	t.Parallel()
	usage := ExtractUsage([]byte(`{"usage":{"prompt_tokens":0,"input_tokens":9,"completion_tokens":0,"output_tokens":8,"cache_read_input_tokens":0,"input_tokens_details":{"cached_tokens":7,"cache_write_tokens":0},"input_token_details":{"cache_write_tokens":6},"reasoning_tokens":0,"output_tokens_details":{"reasoning_tokens":5}}}`), nil)
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.PromptTokens != (tokenusage.ObservedCount{Value: 0, Present: true}) ||
		usage.CompletionTokens != (tokenusage.ObservedCount{Value: 0, Present: true}) ||
		usage.CacheReadInputTokens != (tokenusage.ObservedCount{Value: 0, Present: true}) ||
		usage.ReasoningTokens != (tokenusage.ObservedCount{Value: 0, Present: true}) ||
		usage.CacheCreation == nil || usage.CacheCreation.InputTokens != (tokenusage.ObservedCount{Value: 0, Present: true}) {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.TotalTokens.Present {
		t.Fatalf("missing total was derived: %#v", usage.TotalTokens)
	}
}

func TestBoundedUsageParserKeepsNegativeAndDropsInvalidCounts(t *testing.T) {
	t.Parallel()
	usage := ExtractUsage([]byte(`{"usage":{"input_tokens":-4,"output_tokens":1.5,"total_tokens":9223372036854775808,"input_tokens_details":{"cached_tokens":-2,"cache_write_tokens":"3"},"output_tokens_details":{"reasoning_tokens":-1}}}`), nil)
	if usage == nil {
		t.Fatal("expected negative integral facts to remain observable")
	}
	if usage.PromptTokens != (tokenusage.ObservedCount{Value: -4, Present: true}) ||
		usage.CacheReadInputTokens != (tokenusage.ObservedCount{Value: -2, Present: true}) ||
		usage.ReasoningTokens != (tokenusage.ObservedCount{Value: -1, Present: true}) {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.CompletionTokens.Present || usage.TotalTokens.Present || usage.CacheCreation != nil {
		t.Fatalf("invalid facts became observed: %#v", usage)
	}
}
