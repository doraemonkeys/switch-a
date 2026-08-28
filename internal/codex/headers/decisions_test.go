package codexheaders

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"
)

func TestDecideClientIdentityHeaders(t *testing.T) {
	t.Run("aliases casing and repeated equal values normalize once", func(t *testing.T) {
		headers := http.Header{
			"thREAD-ID":           {"thread", "thread"},
			"SESSION-id":          {"session"},
			"session_id":          {"session"},
			"conversation_ID":     {"conversation"},
			"x-CoDeX-WiNdOw-Id":   {"window"},
			"X-Client-Request-Id": {"request"},
		}
		var seen []BindingCandidate
		result := DecideClient(ClientInput{
			Headers: headers,
			Owners:  recordingLookup(OwnerCurrent, &seen),
		})
		if result.Outcome() != ActionForward || len(result.Decisions()) != 4 {
			t.Fatalf("result = %#v", result.Decisions())
		}
		if len(seen) != 4 {
			t.Fatalf("owner lookups = %d, want 4", len(seen))
		}
		session := requireDecision(t, result, FieldSessionID)
		if got, want := session.HeaderNames(), []string{"SESSION-id", "session_id"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("session header names = %#v, want %#v", got, want)
		}
		if session.Carriers() != CarrierHeader || session.Reason() != ReasonOwnerMatch {
			t.Fatalf("session decision = %#v", session)
		}
		for _, candidate := range seen {
			if candidate.Field() == FieldEnvelope {
				t.Fatal("unrelated request ID entered the identity catalog")
			}
		}
	})

	t.Run("unknown identities request durable claims", func(t *testing.T) {
		result := DecideClient(ClientInput{
			Headers: http.Header{"Thread-Id": {"thread"}, "Session-Id": {"session"}},
			Owners:  fixedLookup(OwnerUnknown),
		})
		if result.Outcome() != ActionClaim || len(result.Claims()) != 2 {
			t.Fatalf("result = %#v", result.Decisions())
		}
		for _, decision := range result.Claims() {
			if decision.Claim().Lifetime() != ClaimLifetimeDurable || decision.Claim().Boundary() != ClaimBoundaryProtocolScope {
				t.Fatalf("claim = %#v", decision.Claim())
			}
		}
	})

	t.Run("owner conflict drops the complete alias category without reclaim", func(t *testing.T) {
		lookups := 0
		result := DecideClient(ClientInput{
			Headers: http.Header{"Session-Id": {"session"}, "session_id": {"session"}},
			Owners: func(BindingCandidate) OwnerStatus {
				lookups++
				return OwnerConflict
			},
		})
		decision := requireDecision(t, result, FieldSessionID)
		if result.Outcome() != ActionDrop || decision.Action() != ActionDrop || decision.Reason() != ReasonOwnerConflict {
			t.Fatalf("decision = %#v", decision)
		}
		if lookups != 1 || len(result.Claims()) != 0 {
			t.Fatalf("lookups=%d claims=%d", lookups, len(result.Claims()))
		}
		if got := result.HeaderNamesToDrop(); !reflect.DeepEqual(got, []string{"Session-Id", "session_id"}) {
			t.Fatalf("drop names = %#v", got)
		}
	})

	for _, test := range []struct {
		name    string
		headers http.Header
	}{
		{name: "different aliases", headers: http.Header{"Session-Id": {"one"}, "session_id": {"two"}}},
		{name: "empty value", headers: http.Header{"Thread-Id": {""}}},
		{name: "no values", headers: http.Header{"Conversation_id": nil}},
	} {
		t.Run(test.name+" drops header-only ambiguity", func(t *testing.T) {
			lookups := 0
			result := DecideClient(ClientInput{
				Headers: test.headers,
				Owners: func(BindingCandidate) OwnerStatus {
					lookups++
					return OwnerUnknown
				},
			})
			if result.Outcome() != ActionDrop || lookups != 0 {
				t.Fatalf("outcome=%s lookups=%d decisions=%#v", result.Outcome(), lookups, result.Decisions())
			}
			if requireOnlyDecision(t, result).Reason() != ReasonMalformedHeader {
				t.Fatalf("decision = %#v", result.Decisions())
			}
		})
	}
}

func TestDecideClientIdentityProjection(t *testing.T) {
	t.Run("matching carriers share one lookup", func(t *testing.T) {
		message := InspectClientFrame([]byte(
			`{"type":"response.create","client_metadata":{"session_id":"same"}}`,
		))
		lookups := 0
		result := DecideClient(ClientInput{
			Headers: http.Header{"SESSION_ID": {"same"}},
			Message: message,
			Owners: func(candidate BindingCandidate) OwnerStatus {
				lookups++
				if candidate.Field() != FieldSessionID || !bytes.Equal(candidate.Value().Bytes(), []byte("same")) {
					t.Fatalf("candidate = %#v", candidate)
				}
				return OwnerCurrent
			},
		})
		decision := requireOnlyDecision(t, result)
		if decision.Action() != ActionForward || decision.Carriers() != CarrierHeader|CarrierProjection || lookups != 1 {
			t.Fatalf("decision=%#v lookups=%d", decision, lookups)
		}
	})

	for _, test := range []struct {
		name    string
		headers http.Header
		body    string
		reason  Reason
		value   string
	}{
		{
			name:    "different values",
			headers: http.Header{"Thread-Id": {"header"}},
			body:    `{"type":"response.create","client_metadata":{"thread_id":"body"}}`,
			reason:  ReasonCarrierConflict,
			value:   "body",
		},
		{
			name:    "ambiguous aliases with projection",
			headers: http.Header{"Session-Id": {"one"}, "session_id": {"two"}},
			body:    `{"type":"response.create","client_metadata":{"session_id":"one"}}`,
			reason:  ReasonMalformedHeader,
			value:   "one",
		},
	} {
		t.Run(test.name+" prefers the immutable projection", func(t *testing.T) {
			lookups := 0
			result := DecideClient(ClientInput{
				Headers: test.headers,
				Message: InspectClientFrame([]byte(test.body)),
				Owners: func(candidate BindingCandidate) OwnerStatus {
					lookups++
					if !bytes.Equal(candidate.Value().Bytes(), []byte(test.value)) {
						t.Fatalf("projection candidate = %#v", candidate)
					}
					return OwnerCurrent
				},
			})
			decisions := result.Decisions()
			if result.Rejected() || len(decisions) != 2 || decisions[0].Action() != ActionDrop ||
				decisions[0].Reason() != test.reason || decisions[1].Action() != ActionForward || lookups != 1 {
				t.Fatalf("decisions=%#v lookups=%d", decisions, lookups)
			}
			if len(result.HeaderNamesToDrop()) == 0 {
				t.Fatal("conflicting Header category was not removed")
			}
		})
	}

	t.Run("body-only unknown identity claims", func(t *testing.T) {
		result := DecideClient(ClientInput{
			Message: InspectClientFrame([]byte(
				`{"type":"response.create","client_metadata":{"x-codex-window-id":"window"}}`,
			)),
			Owners: fixedLookup(OwnerUnknown),
		})
		decision := requireOnlyDecision(t, result)
		if decision.Action() != ActionClaim || decision.Carriers() != CarrierProjection {
			t.Fatalf("decision = %#v", decision)
		}
	})

	t.Run("body projection cannot be dropped on owner conflict", func(t *testing.T) {
		message := InspectClientFrame([]byte(
			`{"type":"response.create","client_metadata":{"thread_id":"thread"}}`,
		))
		for _, headers := range []http.Header{nil, {"Thread-Id": {"thread"}}} {
			result := DecideClient(ClientInput{Headers: headers, Message: message, Owners: fixedLookup(OwnerConflict)})
			decision := requireOnlyDecision(t, result)
			if decision.Action() != ActionReject || decision.Reason() != ReasonOwnerConflict {
				t.Fatalf("decision = %#v", decision)
			}
			if result.HeaderNamesToDrop() != nil {
				t.Fatal("a rejected projection must not expose an actionable Header drop")
			}
		}
	})
}

func TestDecideClientContinuityFields(t *testing.T) {
	tests := []struct {
		name       string
		headers    http.Header
		body       string
		owner      OwnerStatus
		wantAction Action
		wantReason Reason
		wantField  Field
	}{
		{
			name: "known turn state forwards", headers: http.Header{"x-codex-turn-state": {"state"}}, owner: OwnerCurrent,
			wantAction: ActionForward, wantReason: ReasonOwnerMatch, wantField: FieldTurnState,
		},
		{
			name: "unknown turn state cannot claim", headers: http.Header{"X-Codex-Turn-State": {"state"}}, owner: OwnerUnknown,
			wantAction: ActionReject, wantReason: ReasonOwnerUnknown, wantField: FieldTurnState,
		},
		{
			name: "conflicting turn state rejects", headers: http.Header{"X-Codex-Turn-State": {"state"}}, owner: OwnerConflict,
			wantAction: ActionReject, wantReason: ReasonOwnerConflict, wantField: FieldTurnState,
		},
		{
			name: "unknown metadata claims", headers: http.Header{"X-Codex-Turn-Metadata": {"metadata"}}, owner: OwnerUnknown,
			wantAction: ActionClaim, wantReason: ReasonOwnerUnknown, wantField: FieldTurnMetadata,
		},
		{
			name: "known metadata forwards", headers: http.Header{"X-Codex-Turn-Metadata": {"metadata"}}, owner: OwnerCurrent,
			wantAction: ActionForward, wantReason: ReasonOwnerMatch, wantField: FieldTurnMetadata,
		},
		{
			name: "conflicting metadata rejects", headers: http.Header{"X-Codex-Turn-Metadata": {"metadata"}}, owner: OwnerConflict,
			wantAction: ActionReject, wantReason: ReasonOwnerConflict, wantField: FieldTurnMetadata,
		},
		{
			name: "known previous response forwards", body: `{"type":"response.create","previous_response_id":"response"}`, owner: OwnerCurrent,
			wantAction: ActionForward, wantReason: ReasonOwnerMatch, wantField: FieldResponseReference,
		},
		{
			name: "unknown previous response rejects", body: `{"type":"response.create","previous_response_id":"response"}`, owner: OwnerUnknown,
			wantAction: ActionReject, wantReason: ReasonOwnerUnknown, wantField: FieldResponseReference,
		},
		{
			name: "conflicting previous response rejects", body: `{"type":"response.create","previous_response_id":"response"}`, owner: OwnerConflict,
			wantAction: ActionReject, wantReason: ReasonOwnerConflict, wantField: FieldResponseReference,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var message MessageView
			if test.body != "" {
				message = InspectClientFrame([]byte(test.body))
			}
			result := DecideClient(ClientInput{Headers: test.headers, Message: message, Owners: fixedLookup(test.owner)})
			decision := requireOnlyDecision(t, result)
			if decision.Action() != test.wantAction || decision.Reason() != test.wantReason || decision.Field() != test.wantField {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}

	t.Run("matching metadata Header and projection claim once", func(t *testing.T) {
		lookups := 0
		result := DecideClient(ClientInput{
			Headers: http.Header{"X-Codex-Turn-Metadata": {"opaque"}},
			Message: InspectClientFrame([]byte(
				`{"type":"response.create","client_metadata":{"x-codex-turn-metadata":"opaque"}}`,
			)),
			Owners: func(BindingCandidate) OwnerStatus { lookups++; return OwnerUnknown },
		})
		decision := requireOnlyDecision(t, result)
		if decision.Action() != ActionClaim || decision.Carriers() != CarrierHeader|CarrierProjection || lookups != 1 {
			t.Fatalf("decision=%#v lookups=%d", decision, lookups)
		}
	})

	t.Run("metadata carrier mismatch prefers projection", func(t *testing.T) {
		result := DecideClient(ClientInput{
			Headers: http.Header{"X-Codex-Turn-Metadata": {"header"}},
			Message: InspectClientFrame([]byte(
				`{"type":"response.create","client_metadata":{"x-codex-turn-metadata":"projection"}}`,
			)),
			Owners: fixedLookup(OwnerCurrent),
		})
		decisions := result.Decisions()
		if result.Rejected() || len(decisions) != 2 || decisions[0].Action() != ActionDrop ||
			decisions[0].Reason() != ReasonCarrierConflict || decisions[1].Action() != ActionForward ||
			!reflect.DeepEqual(result.HeaderNamesToDrop(), []string{"X-Codex-Turn-Metadata"}) {
			t.Fatalf("decisions = %#v", decisions)
		}
	})

	for _, header := range []http.Header{
		{"X-Codex-Turn-State": {""}},
		{"X-Codex-Turn-State": {"one", "two"}},
	} {
		result := DecideClient(ClientInput{Headers: header, Owners: fixedLookup(OwnerCurrent)})
		decision := requireOnlyDecision(t, result)
		if decision.Action() != ActionReject || decision.Reason() != ReasonMalformedHeader {
			t.Fatalf("decision = %#v", decision)
		}
	}

	t.Run("malformed metadata Header is auxiliary", func(t *testing.T) {
		result := DecideClient(ClientInput{Headers: http.Header{"X-Codex-Turn-Metadata": nil}, Owners: fixedLookup(OwnerCurrent)})
		decision := requireOnlyDecision(t, result)
		if decision.Action() != ActionDrop || decision.Reason() != ReasonMalformedHeader {
			t.Fatalf("decision = %#v", decision)
		}
	})

	for _, test := range []struct {
		name    string
		headers http.Header
		body    string
	}{
		{name: "turn state", headers: http.Header{"X-Codex-Turn-State": {"state"}}},
		{name: "previous response", body: `{"type":"response.create","previous_response_id":"response"}`},
	} {
		t.Run("anchored unknown "+test.name+" adopts", func(t *testing.T) {
			var message MessageView
			if test.body != "" {
				message = InspectClientFrame([]byte(test.body))
			}
			result := DecideClient(ClientInput{
				Headers: test.headers, Message: message, Owners: fixedLookup(OwnerUnknown),
				StateAdmission: StateAdmissionAnchored,
			})
			decision := requireOnlyDecision(t, result)
			if decision.Action() != ActionAdopt || len(result.Adoptions()) != 1 ||
				decision.Claim().Boundary() != ClaimBoundaryProtocolScope {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}

	t.Run("anchored store outage forwards only auxiliary fields", func(t *testing.T) {
		result := DecideClient(ClientInput{
			Headers: http.Header{
				"Thread-Id":             {"thread"},
				"X-Codex-Turn-Metadata": {"metadata"},
			},
			Owners: fixedLookup(OwnerStoreUnavailable), StateAdmission: StateAdmissionAnchored,
		})
		if result.Outcome() != ActionForwardDegraded || len(result.Decisions()) != 2 {
			t.Fatalf("decisions = %#v", result.Decisions())
		}
		for _, decision := range result.Decisions() {
			if decision.Action() != ActionForwardDegraded {
				t.Fatalf("decision = %#v", decision)
			}
		}
		state := DecideClient(ClientInput{
			Headers: http.Header{"X-Codex-Turn-State": {"state"}},
			Owners:  fixedLookup(OwnerStoreUnavailable), StateAdmission: StateAdmissionAnchored,
		})
		if decision := requireOnlyDecision(t, state); decision.Action() != ActionReject {
			t.Fatalf("state decision = %#v", decision)
		}
		strict := DecideClient(ClientInput{
			Headers: http.Header{"X-Codex-Turn-State": {"state"}}, Owners: fixedLookup(OwnerStoreUnavailable),
		})
		if decision := requireOnlyDecision(t, strict); decision.Action() != ActionReject {
			t.Fatalf("strict decision = %#v", decision)
		}
	})

	t.Run("missing owner capability fails closed", func(t *testing.T) {
		result := DecideClient(ClientInput{Headers: http.Header{"Thread-Id": {"thread"}}})
		decision := requireOnlyDecision(t, result)
		if decision.Action() != ActionReject || decision.Reason() != ReasonOwnerUnavailable {
			t.Fatalf("decision = %#v", decision)
		}
	})
}

func TestDecideClientAttestationUsesOperationLockOnly(t *testing.T) {
	for _, test := range []struct {
		name       string
		lock       OperationLockStatus
		wantAction Action
		wantReason Reason
	}{
		{name: "unlocked", lock: OperationUnlocked, wantAction: ActionClaim, wantReason: ReasonOperationUnlocked},
		{name: "same authority", lock: OperationAuthorityCurrent, wantAction: ActionForward, wantReason: ReasonOperationMatch},
		{name: "conflicting authority", lock: OperationAuthorityConflict, wantAction: ActionReject, wantReason: ReasonOperationConflict},
		{name: "unavailable", lock: OperationLockUnavailable, wantAction: ActionReject, wantReason: ReasonOperationUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			ownerCalls := 0
			result := DecideClient(ClientInput{
				Headers:         http.Header{"x-oai-attestation": {"opaque-attestation"}},
				Owners:          func(BindingCandidate) OwnerStatus { ownerCalls++; return OwnerUnknown },
				AttestationLock: test.lock,
			})
			decision := requireOnlyDecision(t, result)
			if decision.Action() != test.wantAction || decision.Reason() != test.wantReason || ownerCalls != 0 {
				t.Fatalf("decision=%#v ownerCalls=%d", decision, ownerCalls)
			}
			if test.lock == OperationUnlocked {
				if decision.Claim().Lifetime() != ClaimLifetimeOperation || decision.Claim().Boundary() != ClaimBoundaryAuthority {
					t.Fatalf("operation claim = %#v", decision.Claim())
				}
				if namespace, persistent := decision.Candidate().PersistentNamespace(); persistent || namespace != "" || decision.Candidate().DigestInput() != nil {
					t.Fatalf("attestation unexpectedly persistent: namespace=%q persistent=%t", namespace, persistent)
				}
			}
		})
	}

	result := DecideClient(ClientInput{
		Headers:         http.Header{"X-Oai-Attestation": {"one", "two"}},
		AttestationLock: OperationUnlocked,
	})
	if decision := requireOnlyDecision(t, result); decision.Reason() != ReasonMalformedHeader {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestDecideServerHeaders(t *testing.T) {
	t.Run("metadata and attestation are never echoed", func(t *testing.T) {
		result := DecideServerHeaders(http.Header{
			"x-codex-turn-metadata": {"metadata"},
			"X-Oai-Attestation":     nil,
		}, nil)
		if result.Outcome() != ActionDrop || len(result.Decisions()) != 2 {
			t.Fatalf("result = %#v", result.Decisions())
		}
		if got := result.HeaderNamesToDrop(); !reflect.DeepEqual(got, []string{"X-Oai-Attestation", "x-codex-turn-metadata"}) {
			t.Fatalf("drop names = %#v", got)
		}
		for _, decision := range result.Decisions() {
			if decision.Reason() != ReasonResponseEchoForbidden {
				t.Fatalf("decision = %#v", decision)
			}
		}
	})

	for _, test := range []struct {
		name       string
		value      []string
		owner      OwnerStatus
		wantAction Action
		wantReason Reason
	}{
		{name: "new state", value: []string{"state"}, owner: OwnerUnknown, wantAction: ActionClaim, wantReason: ReasonOwnerUnknown},
		{name: "known state", value: []string{"state", "state"}, owner: OwnerCurrent, wantAction: ActionForward, wantReason: ReasonOwnerMatch},
		{name: "store unavailable", value: []string{"state"}, owner: OwnerStoreUnavailable, wantAction: ActionClaim, wantReason: ReasonOwnerUnavailable},
		{name: "conflicting state", value: []string{"state"}, owner: OwnerConflict, wantAction: ActionReject, wantReason: ReasonOwnerConflict},
		{name: "malformed state", value: []string{"one", "two"}, owner: OwnerCurrent, wantAction: ActionReject, wantReason: ReasonMalformedHeader},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := DecideServerHeaders(http.Header{"x-CoDeX-tUrN-sTaTe": test.value}, fixedLookup(test.owner))
			decision := requireOnlyDecision(t, result)
			if decision.Action() != test.wantAction || decision.Reason() != test.wantReason {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}

	if result := DecideServerHeaders(nil, nil); result.Outcome() != ActionForward || len(result.Decisions()) != 0 {
		t.Fatalf("empty response result = %#v", result)
	}
}

func fixedLookup(status OwnerStatus) OwnerLookup {
	return func(BindingCandidate) OwnerStatus { return status }
}

func recordingLookup(status OwnerStatus, seen *[]BindingCandidate) OwnerLookup {
	return func(candidate BindingCandidate) OwnerStatus {
		*seen = append(*seen, candidate)
		return status
	}
}

func requireOnlyDecision(t *testing.T, result Result) Decision {
	t.Helper()
	decisions := result.Decisions()
	if len(decisions) != 1 {
		t.Fatalf("decisions = %#v, want exactly one", decisions)
	}
	return decisions[0]
}

func requireDecision(t *testing.T, result Result, field Field) Decision {
	t.Helper()
	for _, decision := range result.Decisions() {
		if decision.Field() == field {
			return decision
		}
	}
	t.Fatalf("no decision for %s in %#v", field, result.Decisions())
	return Decision{}
}
