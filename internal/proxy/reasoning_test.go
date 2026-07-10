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
