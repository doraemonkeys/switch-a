package adapters

import (
	"encoding/json"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

func FuzzEnvelopeAdaptersNeverScanOrPanic(f *testing.F) {
	f.Add([]byte(`{"type":"error","error":{"type":"busy","message":"retry"}}`), "error", byte(0))
	f.Add([]byte(`{"output":[{"message":"server_is_overloaded"}]}`), "", byte(1))
	f.Add([]byte("not json"), "message", byte(2))

	families := []apicontract.ErrorFamily{
		apicontract.ErrorFamilyAnthropicMessages,
		apicontract.ErrorFamilyOpenAIResponses,
		apicontract.ErrorFamilyOpenAIChatCompletions,
		apicontract.ErrorFamilyGoogleGenerateContent,
	}
	f.Fuzz(func(t *testing.T, data []byte, event string, selector byte) {
		kind := framing.KindJSON
		if selector&1 != 0 {
			kind = framing.KindSSE
		}
		family := families[int(selector>>1)%len(families)]
		result := New(family, kind, testLimits).Observe(framing.Frame{Event: event, Data: data})
		if result.Class == EventError && result.Fields == nil {
			t.Fatal("error classification omitted semantic fields")
		}
		if result.Class == EventFailOpen && result.Failure == "" {
			t.Fatal("fail-open classification omitted stable reason")
		}

		quoted, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		nestedValue := string(quoted)
		var ordinary []byte
		switch family {
		case apicontract.ErrorFamilyAnthropicMessages:
			ordinary = []byte(`{"type":"message","content":[{"error":{"type":"server_error","message":` + nestedValue + `},"usage":{"input_tokens":2}}]}`)
		case apicontract.ErrorFamilyOpenAIResponses:
			ordinary = []byte(`{"type":"response.completed","output":[{"error":{"message":` + nestedValue + `},"usage":{"input_tokens":2}}]}`)
		case apicontract.ErrorFamilyOpenAIChatCompletions:
			ordinary = []byte(`{"choices":[{"message":{"error":{"type":"server_error","message":` + nestedValue + `},"usage":{"input_tokens":2}}}]}`)
		case apicontract.ErrorFamilyGoogleGenerateContent:
			ordinary = []byte(`{"candidates":[{"content":{"error":{"code":503,"message":` + nestedValue + `},"usageMetadata":{"promptTokenCount":2}}}]}`)
		}
		ordinaryResult := New(family, kind, testLimits).Observe(framing.Frame{Event: "vendor.unknown", Data: ordinary})
		if ordinaryResult.Class != EventClientVisible || ordinaryResult.Fields != nil || ordinaryResult.Usage != nil {
			t.Fatalf("nested provider-looking values were scanned: %#v", ordinaryResult)
		}
	})
}
