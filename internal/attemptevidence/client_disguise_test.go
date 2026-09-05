package attemptevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClientDisguisePreservesEvidenceAndFailure(t *testing.T) {
	previous := `{"v":2,"gateway":{"terminal_error_code":"conversion_failed"},"unknown":{"keep":true}}`
	evidence := &ClientDisguise{DiagnosticID: "diag", Decision: "failed", ProviderID: "provider", Failure: &DisguiseFailure{Phase: "encode", Location: "turn.metadata", ErrorChain: []string{"invalid metadata"}, OriginalSnippet: "<original>"}, Differences: []DisguiseDifference{{Carrier: "header", Location: "Thread-Id", Original: "thread", Derived: "mapped"}}}
	result, err := EncodeClientDisguiseString(&previous, evidence)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*result), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["unknown"] == nil || envelope["gateway"] == nil || !strings.Contains(*result, "<original>") {
		t.Fatal(*result)
	}
	var decoded ClientDisguise
	if err := json.Unmarshal(envelope["client_disguise"], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Failure.Location != "turn.metadata" || decoded.Differences[0].Derived != "mapped" {
		t.Fatal(decoded)
	}
}
func TestClientDisguiseEncodingBoundaries(t *testing.T) {
	if result, err := EncodeClientDisguiseString(nil, nil); err != nil || result != nil {
		t.Fatal(result, err)
	}
	if _, err := EncodeClientDisguise(nil, &ClientDisguise{}); err == nil {
		t.Fatal("missing identity accepted")
	}
	evidence := &ClientDisguise{DiagnosticID: "diag", Decision: "excluded"}
	if _, err := EncodeClientDisguise([]byte("{"), evidence); err == nil {
		t.Fatal("malformed sibling accepted")
	}
	evidence.Failure = &DisguiseFailure{OriginalSnippet: strings.Repeat("x", MaxAttemptEvidenceBytes)}
	encoded, err := EncodeClientDisguise(nil, evidence)
	if err != nil || len(encoded) > MaxAttemptEvidenceBytes {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"truncated":true`) || !strings.Contains(string(encoded), `"diagnostic_id":"diag"`) {
		t.Fatal(string(encoded))
	}
	if len(evidence.Failure.OriginalSnippet) != MaxAttemptEvidenceBytes {
		t.Fatal("encoder mutated caller evidence")
	}
}

func TestClientDisguiseBoundsCollectionsAndReservesFailure(t *testing.T) {
	evidence := &ClientDisguise{DiagnosticID: "diag", Decision: "failed", PlatformFacts: map[string]string{}, Failure: &DisguiseFailure{Phase: "encode", Location: "metadata", ErrorChain: []string{"terminal"}}}
	for index := 0; index < 32; index++ {
		evidence.Differences = append(evidence.Differences, DisguiseDifference{Location: "field", Original: strings.Repeat("界", 2000), Derived: strings.Repeat("界", 2000)})
		evidence.Candidates = append(evidence.Candidates, DisguiseCandidate{ProviderID: "provider", Reason: strings.Repeat("x", 2000)})
		evidence.PlatformFacts[strings.Repeat("k", index+1)] = strings.Repeat("x", 2000)
	}
	for index := 0; index < 12; index++ {
		evidence.Failure.ErrorChain = append(evidence.Failure.ErrorChain, strings.Repeat("x", 2000))
	}
	raw, err := EncodeClientDisguise(nil, evidence)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Disguise ClientDisguise `json:"client_disguise"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Disguise.Truncated || envelope.Disguise.Failure.ErrorChain[0] != "terminal" || envelope.Disguise.DiagnosticID != "diag" {
		t.Fatal(string(raw))
	}
	if len(raw) > MaxAttemptEvidenceBytes {
		t.Fatal(len(raw))
	}
	terminal := &ClientDisguise{DiagnosticID: "diag", Decision: "failed"}
	previous := `{"v":2,"retained":"` + strings.Repeat("x", MaxAttemptEvidenceBytes-150) + `"}`
	if _, err := EncodeClientDisguise([]byte(previous), terminal); err == nil {
		t.Fatal("impossible sibling budget must fail explicitly")
	}
	value := ClientDisguise{PlatformFacts: map[string]string{"ua": "raw"}}
	if !trimClientDisguise(&value) || trimClientDisguise(&value) {
		t.Fatal("trim did not terminate")
	}
}
