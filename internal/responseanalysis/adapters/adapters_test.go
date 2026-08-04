package adapters

import (
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

var testLimits = Limits{TypeBytes: 256, CodeBytes: 256, MessageBytes: 4096, ReasonBytes: 256}

func TestFrozenEnvelopePredicates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		family apicontract.ErrorFamily
		kind   framing.Kind
		event  string
		data   string
		class  EventClass
		fields *SemanticFields
	}{
		{
			name: "anthropic json", family: apicontract.ErrorFamilyAnthropicMessages, kind: framing.KindJSON,
			data:  `{"type":"error","error":{"type":"overloaded_error","code":5.03e2,"message":"Overloaded","reason":"capacity"}}`,
			class: EventError, fields: &SemanticFields{Type: "overloaded_error", Code: "503", Message: "Overloaded", Reason: "capacity"},
		},
		{
			name: "anthropic sse", family: apicontract.ErrorFamilyAnthropicMessages, kind: framing.KindSSE, event: "error",
			data:  `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			class: EventError, fields: &SemanticFields{Type: "overloaded_error", Message: "Overloaded"},
		},
		{
			name: "responses json type", family: apicontract.ErrorFamilyOpenAIResponses, kind: framing.KindJSON,
			data:  `{"type":"error","error":{"type":"server_error","message":"At capacity"}}`,
			class: EventError, fields: &SemanticFields{Type: "server_error", Message: "At capacity"},
		},
		{
			name: "responses json failed", family: apicontract.ErrorFamilyOpenAIResponses, kind: framing.KindJSON,
			data:  `{"status":"failed","error":{"code":"busy","message":"At capacity"}}`,
			class: EventError, fields: &SemanticFields{Code: "busy", Message: "At capacity"},
		},
		{
			name: "responses direct", family: apicontract.ErrorFamilyOpenAIResponses, kind: framing.KindSSE,
			data:  `{"type":"error","code":"busy","message":"At capacity","param":"capacity"}`,
			class: EventError, fields: &SemanticFields{Type: "error", Code: "busy", Message: "At capacity", Reason: "capacity"},
		},
		{
			name: "responses failed event", family: apicontract.ErrorFamilyOpenAIResponses, kind: framing.KindSSE,
			data:  `{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":503,"message":"Unavailable","reason":"capacity"}}}`,
			class: EventError, fields: &SemanticFields{Type: "server_error", Code: "503", Message: "Unavailable", Reason: "capacity"},
		},
		{
			name: "chat json", family: apicontract.ErrorFamilyOpenAIChatCompletions, kind: framing.KindJSON,
			data:  `{"error":{"type":"server_error","code":503,"message":"Unavailable"}}`,
			class: EventError, fields: &SemanticFields{Type: "server_error", Code: "503", Message: "Unavailable"},
		},
		{
			name: "chat numeric type", family: apicontract.ErrorFamilyOpenAIChatCompletions, kind: framing.KindSSE,
			data:  `{"error":{"type":503,"message":"Unavailable"}}`,
			class: EventError, fields: &SemanticFields{Type: "503", Message: "Unavailable"},
		},
		{
			name: "google status", family: apicontract.ErrorFamilyGoogleGenerateContent, kind: framing.KindJSON,
			data:  `{"error":{"code":503,"message":"Unavailable","status":"UNAVAILABLE","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"CAPACITY"}]}}`,
			class: EventError, fields: &SemanticFields{Type: "UNAVAILABLE", Code: "503", Message: "Unavailable", Reason: "CAPACITY"},
		},
		{
			name: "google integer code", family: apicontract.ErrorFamilyGoogleGenerateContent, kind: framing.KindSSE,
			data:  `{"error":{"code":429e0,"message":"Quota"}}`,
			class: EventError, fields: &SemanticFields{Code: "429", Message: "Quota"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := New(test.family, test.kind, testLimits).Observe(framing.Frame{Event: test.event, Data: []byte(test.data)})
			if result.Class != test.class || !reflect.DeepEqual(result.Fields, test.fields) {
				t.Fatalf("result = %#v, want class=%q fields=%#v", result, test.class, test.fields)
			}
		})
	}
}

func TestNormalOutputNeverBecomesSemanticError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		family apicontract.ErrorFamily
		kind   framing.Kind
		event  string
		data   string
	}{
		{"anthropic output keyword", apicontract.ErrorFamilyAnthropicMessages, framing.KindJSON, "", `{"type":"message","content":[{"text":"overloaded_error"}]}`},
		{"anthropic missing type", apicontract.ErrorFamilyAnthropicMessages, framing.KindJSON, "", `{"type":"error","error":{"message":"busy"}}`},
		{"anthropic explicit nonerror", apicontract.ErrorFamilyAnthropicMessages, framing.KindSSE, "message", `{"type":"error","error":{"type":"server_error","message":"busy"}}`},
		{"anthropic unknown event overrides control payload", apicontract.ErrorFamilyAnthropicMessages, framing.KindSSE, "message", `{"type":"ping"}`},
		{"responses nested", apicontract.ErrorFamilyOpenAIResponses, framing.KindJSON, "", `{"type":"response.completed","output":[{"error":{"message":"busy"}}]}`},
		{"responses missing object", apicontract.ErrorFamilyOpenAIResponses, framing.KindJSON, "", `{"type":"error","error":"busy"}`},
		{"responses direct missing code", apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, "", `{"type":"error","message":"busy"}`},
		{"responses failed missing message", apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, "", `{"type":"response.failed","response":{"status":"failed","error":{"code":503}}}`},
		{"chat missing discriminator", apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindJSON, "", `{"error":{"message":"busy"}}`},
		{"chat explicit nonerror", apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindSSE, "message", `{"error":{"type":"server_error","message":"busy"}}`},
		{"chat oversized message without discriminator", apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindJSON, "", `{"error":{"message":"server_is_overloaded"}}`},
		{"google candidate text", apicontract.ErrorFamilyGoogleGenerateContent, framing.KindJSON, "", `{"candidates":[{"text":"RESOURCE_EXHAUSTED"}]}`},
		{"google oversized message without discriminator", apicontract.ErrorFamilyGoogleGenerateContent, framing.KindJSON, "", `{"error":{"message":"RESOURCE_EXHAUSTED"}}`},
		{"google fractional code", apicontract.ErrorFamilyGoogleGenerateContent, framing.KindJSON, "", `{"error":{"code":42.9,"message":"busy"}}`},
		{"google string code", apicontract.ErrorFamilyGoogleGenerateContent, framing.KindJSON, "", `{"error":{"code":"429","message":"busy"}}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := New(test.family, test.kind, testLimits).Observe(framing.Frame{Event: test.event, Data: []byte(test.data)})
			if result.Class != EventClientVisible || result.Fields != nil {
				t.Fatalf("normal output classified as %#v", result)
			}
		})
	}
}

func TestVisibilityWinsForUnknownEventsAndNormalJSONValues(t *testing.T) {
	t.Parallel()
	for _, family := range []apicontract.ErrorFamily{
		apicontract.ErrorFamilyAnthropicMessages,
		apicontract.ErrorFamilyOpenAIResponses,
		apicontract.ErrorFamilyOpenAIChatCompletions,
		apicontract.ErrorFamilyGoogleGenerateContent,
	} {
		family := family
		for _, data := range []string{"null", `"ordinary"`, `["ordinary"]`, "42"} {
			result := New(family, framing.KindJSON, testLimits).Observe(framing.Frame{Data: []byte(data)})
			if result.Class != EventClientVisible || result.Fields != nil {
				t.Errorf("family %q normal %s = %#v", family, data, result)
			}
		}
	}

	responses := New(apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, testLimits)
	result := responses.Observe(framing.Frame{
		Event: "vendor.mystery",
		Data:  []byte(`{"type":"response.created","usage":{"input_tokens":2}}`),
	})
	if result.Class != EventClientVisible || result.Usage == nil {
		t.Fatalf("explicit unknown Responses event = %#v", result)
	}
	result = responses.Observe(framing.Frame{
		Event: "vendor.mystery",
		Data:  []byte(`{"type":"error","code":"busy","message":"retry"}`),
	})
	if result.Class != EventError || result.Fields == nil {
		t.Fatalf("Responses error predicate did not precede unknown-event visibility: %#v", result)
	}

	chat := New(apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindSSE, testLimits)
	result = chat.Observe(framing.Frame{Event: "vendor.mystery", Data: []byte(`{"usage":{"prompt_tokens":2}}`)})
	if result.Class != EventClientVisible || result.Usage == nil {
		t.Fatalf("explicit unknown Chat event = %#v", result)
	}
	result = chat.Observe(framing.Frame{Data: []byte(`{"content":{"usage":{"prompt_tokens":2}}}`)})
	if result.Class != EventClientVisible || result.Usage != nil {
		t.Fatalf("arbitrarily nested usage was scanned: %#v", result)
	}
}

func TestDoneMarkerIsFamilyAndEventSpecific(t *testing.T) {
	t.Parallel()
	for _, family := range []apicontract.ErrorFamily{
		apicontract.ErrorFamilyAnthropicMessages,
		apicontract.ErrorFamilyGoogleGenerateContent,
	} {
		result := New(family, framing.KindSSE, testLimits).Observe(framing.Frame{Done: true, Data: []byte("[DONE]")})
		if result.Class != EventClientVisible {
			t.Errorf("family %q treated an inapplicable DONE marker as %#v", family, result)
		}
	}
	for _, family := range []apicontract.ErrorFamily{
		apicontract.ErrorFamilyOpenAIResponses,
		apicontract.ErrorFamilyOpenAIChatCompletions,
	} {
		adapter := New(family, framing.KindSSE, testLimits)
		if result := adapter.Observe(framing.Frame{Done: true, Data: []byte("[DONE]")}); result.Class != EventControl {
			t.Errorf("family %q default DONE = %#v", family, result)
		}
		if result := adapter.Observe(framing.Frame{Event: "vendor.mystery", Done: true, Data: []byte("[DONE]")}); result.Class != EventClientVisible {
			t.Errorf("family %q explicit unknown DONE = %#v", family, result)
		}
	}
}

func TestExtractedFieldsTrimBeforeBoundsWithoutChangingCase(t *testing.T) {
	t.Parallel()
	limits := Limits{TypeBytes: 16, CodeBytes: 16, MessageBytes: 4, ReasonBytes: 16}
	result := New(apicontract.ErrorFamilyAnthropicMessages, framing.KindJSON, limits).Observe(framing.Frame{
		Data: []byte(`{"type":"error","error":{"type":" SERVER_ERROR ","message":"  BUSY  "}}`),
	})
	want := &SemanticFields{Type: "SERVER_ERROR", Message: "BUSY"}
	if result.Class != EventError || !reflect.DeepEqual(result.Fields, want) {
		t.Fatalf("trimmed bounded result = %#v", result)
	}
	result = New(apicontract.ErrorFamilyAnthropicMessages, framing.KindJSON, limits).Observe(framing.Frame{
		Data: []byte(`{"type":" ERROR ","error":{"type":"server_error","message":"busy"}}`),
	})
	if result.Class != EventClientVisible {
		t.Fatalf("envelope discriminator was normalized: %#v", result)
	}
}

func TestControlUsageMalformedAndUnsupportedEvents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		family apicontract.ErrorFamily
		kind   framing.Kind
		frame  framing.Frame
		class  EventClass
		usage  bool
	}{
		{"done", apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, framing.Frame{Done: true}, EventControl, false},
		{"anthropic event control", apicontract.ErrorFamilyAnthropicMessages, framing.KindSSE, framing.Frame{Event: "ping", Data: []byte(`{"type":"ping"}`)}, EventControl, false},
		{"anthropic typed control with usage", apicontract.ErrorFamilyAnthropicMessages, framing.KindSSE, framing.Frame{Data: []byte(`{"type":"message_start","usage":{"input_tokens":2}}`)}, EventControl, true},
		{"responses event control", apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, framing.Frame{Event: "response.queued", Data: []byte(`{}`)}, EventControl, false},
		{"responses typed control", apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, framing.Frame{Data: []byte(`{"type":"response.in_progress"}`)}, EventControl, false},
		{"chat usage", apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindSSE, framing.Frame{Data: []byte(`{"usage":{"prompt_tokens":2}}`)}, EventUsage, true},
		{"google usage", apicontract.ErrorFamilyGoogleGenerateContent, framing.KindSSE, framing.Frame{Data: []byte(`{"usageMetadata":{"promptTokenCount":2}}`)}, EventUsage, true},
		{"json usage remains visible", apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindJSON, framing.Frame{Data: []byte(`{"usage":{"prompt_tokens":2}}`)}, EventClientVisible, true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := New(test.family, test.kind, testLimits).Observe(test.frame)
			if result.Class != test.class || (result.Usage != nil) != test.usage {
				t.Fatalf("result = %#v", result)
			}
		})
	}

	for _, family := range []apicontract.ErrorFamily{
		apicontract.ErrorFamilyAnthropicMessages,
		apicontract.ErrorFamilyOpenAIResponses,
		apicontract.ErrorFamilyOpenAIChatCompletions,
		apicontract.ErrorFamilyGoogleGenerateContent,
	} {
		result := New(family, framing.KindSSE, testLimits).Observe(framing.Frame{Data: []byte("not json")})
		if result.Class != EventFailOpen || result.Failure != framing.FailureMalformedFrame {
			t.Fatalf("family %q malformed = %#v", family, result)
		}
	}

	result := New(apicontract.ErrorFamily("unknown"), framing.KindJSON, testLimits).Observe(framing.Frame{})
	if result.Class != EventFailOpen || result.Failure != framing.FailureInternal {
		t.Fatalf("unsupported = %#v", result)
	}
}

func TestNestedResponsesFieldsTakePrecedence(t *testing.T) {
	t.Parallel()
	data := `{"type":"error","code":"outer","message":"outer message","param":"outer reason","error":{"type":"server_error","code":503,"message":"inner message","reason":"inner reason"}}`
	result := New(apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, testLimits).Observe(framing.Frame{Data: []byte(data)})
	want := &SemanticFields{Type: "server_error", Code: "503", Message: "inner message", Reason: "inner reason"}
	if result.Class != EventError || !reflect.DeepEqual(result.Fields, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestResponsesAuthoritativeTypeExtraction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		kind framing.Kind
		data string
		want *SemanticFields
	}{
		{
			name: "json top-level error supplies absent nested type",
			kind: framing.KindJSON,
			data: `{"type":"error","error":{"message":"Original Message"}}`,
			want: &SemanticFields{Type: "error", Message: "Original Message"},
		},
		{
			name: "json nested type remains authoritative",
			kind: framing.KindJSON,
			data: `{"type":"error","error":{"type":"ProviderSpecific","message":"Original Message"}}`,
			want: &SemanticFields{Type: "ProviderSpecific", Message: "Original Message"},
		},
		{
			name: "direct event top-level error supplies absent nested type",
			kind: framing.KindSSE,
			data: `{"type":"error","code":"BusyCode","message":"Original Message"}`,
			want: &SemanticFields{Type: "error", Code: "BusyCode", Message: "Original Message"},
		},
		{
			name: "direct event nested type remains authoritative",
			kind: framing.KindSSE,
			data: `{"type":"error","message":"Outer Message","error":{"type":"ProviderSpecific","code":"BusyCode","message":"Nested Message"}}`,
			want: &SemanticFields{Type: "ProviderSpecific", Code: "BusyCode", Message: "Nested Message"},
		},
		{
			name: "status failed does not invent a type",
			kind: framing.KindJSON,
			data: `{"status":"failed","error":{"message":"Original Message"}}`,
			want: &SemanticFields{Message: "Original Message"},
		},
		{
			name: "response failed keeps fields rooted in response error",
			kind: framing.KindSSE,
			data: `{"type":"response.failed","response":{"status":"failed","error":{"message":"Original Message"}}}`,
			want: &SemanticFields{Message: "Original Message"},
		},
		{
			name: "response failed preserves explicit nested type",
			kind: framing.KindSSE,
			data: `{"type":"response.failed","response":{"status":"failed","error":{"type":"ProviderSpecific","message":"Original Message"}}}`,
			want: &SemanticFields{Type: "ProviderSpecific", Message: "Original Message"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := New(apicontract.ErrorFamilyOpenAIResponses, test.kind, testLimits).Observe(framing.Frame{Data: []byte(test.data)})
			if result.Class != EventError || !reflect.DeepEqual(result.Fields, test.want) {
				t.Fatalf("result = %#v, want %#v", result, test.want)
			}
		})
	}
}

func TestSemanticFieldBoundsFailOpen(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 65)
	limits := Limits{TypeBytes: 64, CodeBytes: 4, MessageBytes: 4, ReasonBytes: 4}
	tests := []struct {
		name   string
		family apicontract.ErrorFamily
		kind   framing.Kind
		data   string
	}{
		{"anthropic required type", apicontract.ErrorFamilyAnthropicMessages, framing.KindJSON, `{"type":"error","error":{"type":"` + long + `","message":"x"}}`},
		{"anthropic message", apicontract.ErrorFamilyAnthropicMessages, framing.KindJSON, `{"type":"error","error":{"type":"x","message":"` + long + `"}}`},
		{"responses nested code", apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, `{"type":"error","message":"x","error":{"code":"` + long + `"}}`},
		{"chat code", apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindJSON, `{"error":{"code":"` + long + `","message":"x"}}`},
		{"google status", apicontract.ErrorFamilyGoogleGenerateContent, framing.KindJSON, `{"error":{"status":"` + long + `","message":"x"}}`},
		{"google reason", apicontract.ErrorFamilyGoogleGenerateContent, framing.KindJSON, `{"error":{"status":"x","message":"x","details":[{"@type":"google.rpc.ErrorInfo","reason":"` + long + `"}]}}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			result := New(test.family, test.kind, limits).Observe(framing.Frame{Data: []byte(test.data)})
			if result.Class != EventFailOpen || result.Failure != framing.FailureSemanticFieldTooLarge {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestOversizedNonCandidateFieldsRemainVisible(t *testing.T) {
	t.Parallel()
	limits := Limits{TypeBytes: 4, CodeBytes: 4, MessageBytes: 4, ReasonBytes: 4}
	long := strings.Repeat("x", 5)
	tests := []struct {
		family apicontract.ErrorFamily
		kind   framing.Kind
		data   string
	}{
		{apicontract.ErrorFamilyAnthropicMessages, framing.KindJSON, `{"type":"` + long + `","content":[]}`},
		{apicontract.ErrorFamilyOpenAIResponses, framing.KindJSON, `{"type":"` + long + `","output":[]}`},
		{apicontract.ErrorFamilyOpenAIResponses, framing.KindSSE, `{"type":"response.failed","response":{"status":"` + long + `","error":{"message":"x"}}}`},
		{apicontract.ErrorFamilyOpenAIChatCompletions, framing.KindJSON, `{"error":{"message":"` + long + `"}}`},
		{apicontract.ErrorFamilyGoogleGenerateContent, framing.KindJSON, `{"error":{"message":"` + long + `"}}`},
	}
	for _, test := range tests {
		result := New(test.family, test.kind, limits).Observe(framing.Frame{Data: []byte(test.data)})
		if result.Class != EventClientVisible {
			t.Errorf("non-candidate %q failed open: %#v", test.data, result)
		}
	}
}
