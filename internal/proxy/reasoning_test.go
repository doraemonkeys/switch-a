package proxy

import (
	"strings"
	"testing"

	"switch-a/internal/model"
)

func TestExtractRequestedReasoningSupportedShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		apiType    string
		path       string
		body       string
		wantState  model.ReasoningObservationState
		wantEffort *string
		wantMode   *string
		wantBudget *int64
	}{
		{
			name:       "Claude effort and thinking",
			apiType:    APITypeClaude,
			path:       RouteClaudeMessages,
			body:       `{"model":"claude-sonnet","output_config":{"effort":"high"},"thinking":{"type":"enabled","budget_tokens":4096}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
			wantMode:   reasoningStringPointer("enabled"),
			wantBudget: reasoningInt64Pointer(4096),
		},
		{
			name:       "OpenAI responses path",
			apiType:    APITypeCodex,
			path:       RouteCodexResponses,
			body:       `{"reasoning":{"effort":"medium"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("medium"),
		},
		{
			name:       "OpenAI v1 responses path",
			apiType:    APITypeCodex,
			path:       RouteCodexResponsesV1,
			body:       `{"reasoning":{"effort":"low"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("low"),
		},
		{
			name:       "Codex web search path",
			apiType:    APITypeCodex,
			path:       RouteCodexWebSearch,
			body:       `{"input":"search","reasoning":{"effort":"high"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:       "Codex v1 web search path",
			apiType:    APITypeCodex,
			path:       RouteCodexWebSearchV1,
			body:       `{"input":"search","reasoning":{"effort":"medium"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("medium"),
		},
		{
			name:       "Codex namespaced web search path",
			apiType:    APITypeCodex,
			path:       "/codex/alpha/search",
			body:       `{"input":"search","reasoning":{"effort":"low"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("low"),
		},
		{
			name:       "Grok chat completions path",
			apiType:    APITypeGrok,
			path:       RouteGrokChatCompletions,
			body:       `{"model":"grok-4","reasoning_effort":"high"}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:       "Grok v1 chat completions path",
			apiType:    APITypeGrok,
			path:       RouteGrokChatCompletionsV1,
			body:       `{"model":"grok-3-mini","reasoning_effort":"low"}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("low"),
		},
		{
			name:      "Grok ignores Codex-shaped reasoning object",
			apiType:   APITypeGrok,
			path:      RouteGrokChatCompletions,
			body:      `{"model":"grok-4","reasoning":{"effort":"high"}}`,
			wantState: model.ReasoningObservationAbsent,
		},
		{
			name:       "Grok namespaced path",
			apiType:    APITypeGrok,
			path:       "/grok/v1/chat/completions",
			body:       `{"model":"grok-4","reasoning_effort":"high"}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:       "Claude namespaced path",
			apiType:    APITypeClaude,
			path:       "/claude/v1/messages",
			body:       `{"output_config":{"effort":"low"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("low"),
		},
		{
			name:      "Claude namespaced count tokens stays unsupported",
			apiType:   APITypeClaude,
			path:      "/claude" + RouteClaudeCountTokens,
			body:      `{"thinking":{"type":"enabled"}}`,
			wantState: model.ReasoningObservationUnsupported,
		},
		{
			name:      "supported request without controls",
			apiType:   APITypeClaude,
			path:      RouteClaudeMessages,
			body:      `{"model":"claude-sonnet","messages":[{"role":"user","content":"hello"}]}`,
			wantState: model.ReasoningObservationAbsent,
		},
		{
			name:      "Gemini API",
			apiType:   APITypeGemini,
			path:      "/gemini/v1beta/models/gemini-pro:generateContent",
			body:      `{"reasoning":{"effort":"high"}}`,
			wantState: model.ReasoningObservationUnsupported,
		},
		{
			name:      "Claude count tokens endpoint",
			apiType:   APITypeClaude,
			path:      RouteClaudeCountTokens,
			body:      `{"thinking":{"type":"enabled"}}`,
			wantState: model.ReasoningObservationUnsupported,
		},
		{
			name:      "custom API using Claude-shaped path",
			apiType:   CustomAPITypePrefix + "tool",
			path:      RouteClaudeMessages,
			body:      `{"thinking":{"type":"enabled"}}`,
			wantState: model.ReasoningObservationUnsupported,
		},
		{
			name:      "Grok on Claude-shaped path",
			apiType:   APITypeGrok,
			path:      RouteClaudeMessages,
			body:      `{"reasoning_effort":"high"}`,
			wantState: model.ReasoningObservationUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractRequestedReasoning(test.apiType, test.path, []byte(test.body))
			assertReasoningObservation(t, got, test.wantState, test.wantEffort, test.wantMode, test.wantBudget)
		})
	}
}

func TestExtractRequestedReasoningInvalidDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantEffort *string
		wantMode   *string
	}{
		{name: "empty", body: ""},
		{name: "non-object", body: `[]`},
		{name: "malformed", body: `{"reasoning":`},
		{name: "wrong relevant object type", body: `{"reasoning":[]}`},
		{name: "wrong effort type", body: `{"reasoning":{"effort":7}}`},
		{name: "null effort", body: `{"reasoning":{"effort":null}}`},
		{
			name:       "syntax failure after captured object",
			body:       `{"reasoning":{"effort":"high"},"input":[}`,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:       "trailing malformed data",
			body:       `{"reasoning":{"effort":"high"}} {`,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:       "valid trailing document",
			body:       `{"reasoning":{"effort":"high"}} {}`,
			wantEffort: reasoningStringPointer("high"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractRequestedReasoning(APITypeCodex, RouteCodexResponses, []byte(test.body))
			assertReasoningObservation(t, got, model.ReasoningObservationInvalid, test.wantEffort, test.wantMode, nil)
		})
	}
}

func TestExtractRequestedReasoningGrokScalarMember(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantState  model.ReasoningObservationState
		wantEffort *string
	}{
		{
			name:      "null effort",
			body:      `{"reasoning_effort":null}`,
			wantState: model.ReasoningObservationInvalid,
		},
		{
			name:      "wrong effort type",
			body:      `{"reasoning_effort":7}`,
			wantState: model.ReasoningObservationInvalid,
		},
		{
			name:      "object where scalar expected",
			body:      `{"reasoning_effort":{"effort":"high"}}`,
			wantState: model.ReasoningObservationInvalid,
		},
		{
			name:      "over-limit value",
			body:      `{"reasoning_effort":"` + strings.Repeat("界", model.MaxReasoningValueRunes+1) + `"}`,
			wantState: model.ReasoningObservationInvalid,
		},
		{
			name:       "duplicate members use last decoded value",
			body:       `{"reasoning_effort":"low","reasoning_effort":"high"}`,
			wantState:  model.ReasoningObservationAmbiguous,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:      "invalid duplicate clears earlier value and takes precedence",
			body:      `{"reasoning_effort":"low","reasoning_effort":false}`,
			wantState: model.ReasoningObservationInvalid,
		},
		{
			name:       "invalid flag stays sticky across a later valid duplicate",
			body:       `{"reasoning_effort":false,"reasoning_effort":"high"}`,
			wantState:  model.ReasoningObservationInvalid,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:       "exact whitespace retention",
			body:       `{"reasoning_effort":"  HIGH  "}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("  HIGH  "),
		},
		{
			name:       "target after large leading field",
			body:       `{"messages":[{"role":"user","content":"` + strings.Repeat("x", 2*1024*1024) + `"}],"reasoning_effort":"high"}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractRequestedReasoning(APITypeGrok, RouteGrokChatCompletions, []byte(test.body))
			assertReasoningObservation(t, got, test.wantState, test.wantEffort, nil, nil)
		})
	}
}

func TestExtractRequestedReasoningPreservesStringsAndRejectsOverLimitValues(t *testing.T) {
	t.Parallel()

	whitespace := "  HIGH  "
	got := ExtractRequestedReasoning(
		APITypeClaude,
		RouteClaudeMessages,
		[]byte(`{"output_config":{"effort":"  HIGH  "},"thinking":{"type":""}}`),
	)
	assertReasoningObservation(
		t,
		got,
		model.ReasoningObservationCaptured,
		&whitespace,
		reasoningStringPointer(""),
		nil,
	)

	overLimit := strings.Repeat("界", model.MaxReasoningValueRunes+1)
	got = ExtractRequestedReasoning(
		APITypeClaude,
		RouteClaudeMessages,
		[]byte(`{"output_config":{"effort":"`+overLimit+`"},"thinking":{"type":"enabled"}}`),
	)
	assertReasoningObservation(
		t,
		got,
		model.ReasoningObservationInvalid,
		nil,
		reasoningStringPointer("enabled"),
		nil,
	)
}

func TestExtractRequestedReasoningDuplicateMembersUseLastDecodedValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "top-level object",
			body: `{"reasoning":{"effort":"low"},"reasoning":{"effort":"high"}}`,
			want: "high",
		},
		{
			name: "nested member",
			body: `{"reasoning":{"effort":"low","effort":"medium"}}`,
			want: "medium",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractRequestedReasoning(APITypeCodex, RouteCodexResponses, []byte(test.body))
			assertReasoningObservation(
				t,
				got,
				model.ReasoningObservationAmbiguous,
				reasoningStringPointer(test.want),
				nil,
				nil,
			)
		})
	}
}

func TestExtractRequestedReasoningInvalidTakesPrecedenceOverAmbiguous(t *testing.T) {
	t.Parallel()

	got := ExtractRequestedReasoning(
		APITypeCodex,
		RouteCodexResponses,
		[]byte(`{"reasoning":{"effort":"low","effort":false}}`),
	)
	assertReasoningObservation(
		t,
		got,
		model.ReasoningObservationInvalid,
		nil,
		nil,
		nil,
	)
}

func TestExtractRequestedReasoningLaterObjectCanClearEarlierValue(t *testing.T) {
	t.Parallel()

	got := ExtractRequestedReasoning(
		APITypeCodex,
		RouteCodexResponses,
		[]byte(`{"reasoning":{"effort":"low"},"reasoning":{}}`),
	)
	assertReasoningObservation(
		t,
		got,
		model.ReasoningObservationAmbiguous,
		nil,
		nil,
		nil,
	)
}

func TestExtractRequestedReasoningFindsTargetAfterLargeLeadingField(t *testing.T) {
	t.Parallel()

	body := `{"input":{"items":["` + strings.Repeat("x", 2*1024*1024) + `"]},"reasoning":{"effort":"high"}}`
	got := ExtractRequestedReasoning(APITypeCodex, RouteCodexResponses, []byte(body))
	assertReasoningObservation(
		t,
		got,
		model.ReasoningObservationCaptured,
		reasoningStringPointer("high"),
		nil,
		nil,
	)
}

func assertReasoningObservation(
	t *testing.T,
	got model.RequestedReasoningObservation,
	wantState model.ReasoningObservationState,
	wantEffort, wantMode *string,
	wantBudget *int64,
) {
	t.Helper()
	if got.State == nil || *got.State != wantState {
		t.Fatalf("State = %v, want %q", got.State, wantState)
	}
	assertReasoningStringPointer(t, "Effort", got.Effort, wantEffort)
	assertReasoningStringPointer(t, "Mode", got.Mode, wantMode)
	if got.BudgetTokens == nil || wantBudget == nil {
		if got.BudgetTokens != nil || wantBudget != nil {
			t.Fatalf("BudgetTokens = %v, want %v", got.BudgetTokens, wantBudget)
		}
		return
	}
	if *got.BudgetTokens != *wantBudget {
		t.Fatalf("BudgetTokens = %d, want %d", *got.BudgetTokens, *wantBudget)
	}
}

func assertReasoningStringPointer(t *testing.T, name string, got, want *string) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if *got != *want {
		t.Fatalf("%s = %q, want %q", name, *got, *want)
	}
}

func reasoningStringPointer(value string) *string {
	return &value
}

func reasoningInt64Pointer(value int64) *int64 {
	return &value
}
