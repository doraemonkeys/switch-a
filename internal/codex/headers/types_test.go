package codexheaders

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestBindingCandidateDigestContract(t *testing.T) {
	candidates := []BindingCandidate{
		{field: FieldThreadID, value: newOpaqueValue("same")},
		{field: FieldSessionID, value: newOpaqueValue("same")},
		{field: FieldConversationID, value: newOpaqueValue("same")},
		{field: FieldWindowID, value: newOpaqueValue("same")},
	}
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		namespace, persistent := candidate.PersistentNamespace()
		if !persistent || namespace != codexidentity.OpaqueSessionIdentity {
			t.Fatalf("candidate %s namespace=%q persistent=%t", candidate.Field(), namespace, persistent)
		}
		input := candidate.DigestInput()
		if len(input) == 0 {
			t.Fatalf("candidate %s has empty digest input", candidate.Field())
		}
		if _, duplicate := seen[string(input)]; duplicate {
			t.Fatalf("candidate %s collided with another identity category", candidate.Field())
		}
		seen[string(input)] = struct{}{}
	}

	for field, want := range map[Field]codexidentity.OpaqueNamespace{
		FieldTurnState:         codexidentity.OpaqueTurnState,
		FieldTurnMetadata:      codexidentity.OpaqueTurnMetadata,
		FieldResponseReference: codexidentity.OpaqueResponseReference,
	} {
		candidate := BindingCandidate{field: field, value: newOpaqueValue("opaque")}
		got, persistent := candidate.PersistentNamespace()
		if !persistent || got != want || len(candidate.DigestInput()) == 0 {
			t.Fatalf("field=%s namespace=%q persistent=%t", field, got, persistent)
		}
	}

	for _, candidate := range []BindingCandidate{
		{field: FieldAttestation, value: newOpaqueValue("attestation")},
		{field: FieldEnvelope, value: newOpaqueValue("event")},
	} {
		if namespace, persistent := candidate.PersistentNamespace(); persistent || namespace != "" || candidate.DigestInput() != nil {
			t.Fatalf("candidate %#v unexpectedly persistent", candidate)
		}
	}
	emptyIdentity := BindingCandidate{field: FieldThreadID}
	if namespace, persistent := emptyIdentity.PersistentNamespace(); !persistent || namespace != codexidentity.OpaqueSessionIdentity || emptyIdentity.DigestInput() != nil {
		t.Fatalf("empty identity namespace=%q persistent=%t digest=%x", namespace, persistent, emptyIdentity.DigestInput())
	}
}

func TestOpaqueTypesAreRedactedAndImmutableByCopy(t *testing.T) {
	value := newOpaqueValue("secret-value")
	copyBytes := value.Bytes()
	copyBytes[0] = 'X'
	if string(value.Bytes()) != "secret-value" || value.Empty() || !value.Equal(newOpaqueValue("secret-value")) {
		t.Fatalf("opaque value changed: %s", value.Bytes())
	}
	if value.Equal(newOpaqueValue("other")) || !newOpaqueValue("").Empty() {
		t.Fatal("opaque equality/empty contract failed")
	}
	if fmt.Sprint(value) != "opaque-value(redacted)" || fmt.Sprintf("%#v", value) != "opaque-value(redacted)" {
		t.Fatalf("opaque formatting leaked: %s %#v", value, value)
	}

	candidate := BindingCandidate{field: FieldThreadID, value: value}
	if candidate.Field() != FieldThreadID || !candidate.Value().Equal(value) {
		t.Fatalf("candidate accessors = %#v", candidate)
	}
	if fmt.Sprint(candidate) != "binding-candidate(field=thread_id,value=redacted)" || fmt.Sprintf("%#v", candidate) != candidate.String() {
		t.Fatalf("candidate formatting = %s %#v", candidate, candidate)
	}
}

func TestResultAccessorsDoNotExposeMutableDecisionState(t *testing.T) {
	decision := newDecision(
		ActionDrop,
		FieldSessionID,
		CarrierHeader,
		ReasonOwnerConflict,
		BindingCandidate{field: FieldSessionID, value: newOpaqueValue("session")},
		[]string{"Session-Id"},
		ClaimSpec{},
	)
	result := Result{decisions: []Decision{decision}}
	copyDecisions := result.Decisions()
	copyDecisions[0].action = ActionReject
	copyNames := decision.HeaderNames()
	copyNames[0] = "changed"
	if result.Outcome() != ActionDrop || requireOnlyDecision(t, result).Action() != ActionDrop {
		t.Fatalf("result mutated: %#v", result.Decisions())
	}
	if got := requireOnlyDecision(t, result).HeaderNames(); !reflect.DeepEqual(got, []string{"Session-Id"}) {
		t.Fatalf("header names mutated: %#v", got)
	}
	if len(result.Claims()) != 0 || !reflect.DeepEqual(result.HeaderNamesToDrop(), []string{"Session-Id"}) {
		t.Fatalf("action projections failed: claims=%#v drops=%#v", result.Claims(), result.HeaderNamesToDrop())
	}

	rejected := Result{decisions: []Decision{
		newDecision(ActionClaim, FieldThreadID, CarrierHeader, ReasonOwnerUnknown, BindingCandidate{}, nil, ClaimSpec{}),
		newDecision(ActionDrop, FieldSessionID, CarrierHeader, ReasonOwnerConflict, BindingCandidate{}, []string{"Session-Id"}, ClaimSpec{}),
		newDecision(ActionReject, FieldTurnState, CarrierHeader, ReasonOwnerConflict, BindingCandidate{}, nil, ClaimSpec{}),
	}}
	if rejected.Outcome() != ActionReject || !rejected.Rejected() || rejected.Claims() != nil || rejected.HeaderNamesToDrop() != nil {
		t.Fatalf("rejected projections = %#v", rejected)
	}

	claimAndDrop := Result{decisions: []Decision{
		newDecision(ActionDrop, FieldSessionID, CarrierHeader, ReasonOwnerConflict, BindingCandidate{}, nil, ClaimSpec{}),
		newDecision(ActionClaim, FieldThreadID, CarrierHeader, ReasonOwnerUnknown, BindingCandidate{}, nil, ClaimSpec{}),
	}}
	if claimAndDrop.Outcome() != ActionClaim {
		t.Fatalf("mixed outcome = %s", claimAndDrop.Outcome())
	}
}

func TestHeaderObservationDoesNotSplitOpaqueCommas(t *testing.T) {
	observation := observeHeader(http.Header{"X-Codex-Turn-Metadata": {"opaque,comma"}}, turnMetadataHeader)
	if !observation.present || !observation.valid || !bytes.Equal(observation.value.Bytes(), []byte("opaque,comma")) {
		t.Fatalf("observation = %#v", observation)
	}
	if matchesAlias("X-Other", turnMetadataHeader.aliases) || CarrierHeader.Has(CarrierProjection) || !(CarrierHeader | CarrierProjection).Has(CarrierProjection) {
		t.Fatal("alias or carrier predicate failed")
	}
}

func TestManagedHeaderCatalogRecognizesEveryAlias(t *testing.T) {
	for _, spec := range headerCatalog {
		if len(spec.aliases) == 0 {
			t.Fatalf("field %q has no wire aliases", spec.field)
		}
		for _, alias := range spec.aliases {
			if !IsManagedHeader(alias) || !IsManagedHeader(strings.ToLower(alias)) {
				t.Fatalf("field %q alias %q is not managed case-insensitively", spec.field, alias)
			}
		}
	}
	if IsManagedHeader("X-Unrelated") {
		t.Fatal("unrelated header was classified as Codex-managed")
	}
}

func TestInvalidOwnerStatusFailsClosed(t *testing.T) {
	result := DecideClient(ClientInput{
		Headers: http.Header{"Thread-Id": {"thread"}},
		Owners:  fixedLookup(OwnerStatus(255)),
	})
	decision := requireOnlyDecision(t, result)
	if decision.Action() != ActionReject || decision.Reason() != ReasonOwnerUnavailable {
		t.Fatalf("decision = %#v", decision)
	}
}
