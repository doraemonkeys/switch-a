package codexheaders

import (
	"net/http"
	"testing"
)

func TestOpaquePassthroughHasNoOwnershipClaim(t *testing.T) {
	input := ClientInput{
		Headers: http.Header{
			"Thread-Id": {"thread"}, "Session-Id": {"session"},
			"Conversation_id": {"conversation"}, "X-Codex-Window-Id": {"window"},
			"X-Codex-Turn-State": {"state"}, "X-Codex-Turn-Metadata": {"metadata"},
		},
		Message: InspectClientFrame([]byte(`{"type":"response.create","previous_response_id":"response-a"}`)),
		Owners:  fixedLookup(OwnerOpaquePassthrough),
	}
	for _, result := range []Result{
		DecideClient(input),
		DecideClientEvidence(ClientEvidenceInput{Headers: input.Headers, Evidence: input.Message.ClientEvidence(), Owners: input.Owners}),
	} {
		if result.Rejected() || len(result.Claims()) != 0 || len(result.Decisions()) != 7 {
			t.Fatalf("opaque admission = %#v", result.Decisions())
		}
		for _, decision := range result.Decisions() {
			if decision.Action() != ActionForward || decision.Reason() != ReasonOpaquePassthrough || decision.Claim().Lifetime() != "" {
				t.Fatalf("passthrough acquired ownership: %#v", decision)
			}
		}
	}
}

func TestOpaquePassthroughPreservesCarrierAndResponseHygiene(t *testing.T) {
	owners := fixedLookup(OwnerOpaquePassthrough)
	for _, headers := range []http.Header{
		{"X-Codex-Turn-State": {"one", "two"}},
	} {
		if result := DecideClient(ClientInput{Headers: headers, Owners: owners}); !result.Rejected() {
			t.Fatal("malformed state admitted as opaque")
		}
	}
	result := DecideServerHeaders(http.Header{
		"X-Codex-Turn-State": {"state"}, "X-Codex-Turn-Metadata": {"metadata"}, "X-Oai-Attestation": {"attestation"},
	}, owners)
	if result.Rejected() || len(result.Claims()) != 0 {
		t.Fatalf("response = %#v", result.Decisions())
	}
	if decision := requireDecision(t, result, FieldTurnState); decision.Action() != ActionForward || decision.Reason() != ReasonOpaquePassthrough {
		t.Fatalf("state echo = %#v", decision)
	}
	for _, field := range []Field{FieldTurnMetadata, FieldAttestation} {
		if decision := requireDecision(t, result, field); decision.Action() != ActionDrop {
			t.Fatalf("response hygiene changed: %#v", decision)
		}
	}
}
