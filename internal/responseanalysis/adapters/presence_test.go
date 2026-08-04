package adapters

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

func TestSemanticFieldsPreserveAbsentVersusPresentEmptyOptionals(t *testing.T) {
	tests := []struct {
		name        string
		providerErr string
		wantType    bool
		wantCode    bool
		wantReason  bool
	}{
		{
			name:        "absent",
			providerErr: `{"message":""}`,
		},
		{
			name:        "present empty",
			providerErr: `{"type":"","code":"","message":"","reason":""}`,
			wantType:    true,
			wantCode:    true,
			wantReason:  true,
		},
		{
			name:        "present values infer presence",
			providerErr: `{"type":"ServerError","code":"Busy","message":"Failure","reason":"Capacity"}`,
			wantType:    true,
			wantCode:    true,
			wantReason:  true,
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
			name:     "top-level type is authoritative fallback",
			data:     `{"type":"error","code":"busy","message":"Failure"}`,
			wantType: "error",
		},
		{
			name:     "explicit empty nested type remains present",
			data:     `{"type":"error","message":"Failure","error":{"type":"","code":"busy"}}`,
			wantType: "",
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
