package codexheaders

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureViewsPreserveExactWireBytes(t *testing.T) {
	tests := []struct {
		file      string
		direction messageDirection
		event     string
		want      map[Field]string
	}{
		{
			file:      "ws-client-response-create-warmup.json",
			direction: directionClient,
			event:     eventResponseCreate,
			want: map[Field]string{
				FieldThreadID:     "01a03e26-ee92-7370-9513-72897d095749",
				FieldSessionID:    "01a03e26-ee92-7370-9513-72897d095749",
				FieldWindowID:     "01a03e26-ee92-7370-9513-72897d095749:0",
				FieldTurnMetadata: `{"installation_id":"412a4794-6234-4e9a-88d9-e08ee3952557","session_id":"01a03e26-ee92-7370-9513-72897d095749","thread_id":"01a03e26-ee92-7370-9513-72897d095749","agent_name":"/root","turn_id":"","window_id":"01a03e26-ee92-7370-9513-72897d095749:0","request_kind":"prewarm","thread_source":"user","sandbox":"none","sandbox_mode":"danger-full-access","auto_review_enabled":false,"node_repl_auto_review_required":false,"node_repl_disabled":false}`,
			},
		},
		{
			file:      "ws-client-response-create-second.json",
			direction: directionClient,
			event:     eventResponseCreate,
			want: map[Field]string{
				FieldThreadID:          "01a03e26-ee92-7370-9513-72897d095749",
				FieldSessionID:         "01a03e26-ee92-7370-9513-72897d095749",
				FieldWindowID:          "01a03e26-ee92-7370-9513-72897d095749:0",
				FieldResponseReference: "resp_0f3fd2b96e949a05016a8ee314f00087d0b428d9d5010d2a40",
			},
		},
		{
			file:      "ws-server-codex-response-metadata.json",
			direction: directionServer,
			event:     eventCodexResponseMetadata,
			want: map[Field]string{
				FieldTurnState: "gAAAAABqjuMU7mwjPvnSSZM-AjdwvqXm_WU8bNy_3_Q4Et7C0smPDaTSpHHIdVYQ549czqaulQvIEuyyNeQVm8vPjoHBuYV84-Ir0wn967c5TKQeF61utIX1iZxhnVNXZbIm4iBqkg00vQk7ThPvhQ3j0PT44sPf23aTZ3TrO5-0pZxBlJOm6goeq3uUi1MyassNCdaqvq6hotFWNBwIJyJ9MXz7GkHTkSD1y1hzuNxnIaHvCOC5WWBxYBoDRn89_h055jnGjT7DYapQYG7G7g9p0shAhBBVrw==",
			},
		},
		{
			file:      "ws-server-response-created.json",
			direction: directionServer,
			event:     eventResponseCreated,
			want: map[Field]string{
				FieldResponseReference: "resp_0f3fd2b96e949a05016a8ee314f00087d0b428d9d5010d2a40",
			},
		},
		{
			file:      "ws-server-response-in-progress.json",
			direction: directionServer,
			event:     eventResponseInProgress,
			want: map[Field]string{
				FieldResponseReference: "resp_0f3fd2b96e949a05016a8ee314f00087d0b428d9d5010d2a40",
			},
		},
		{
			file:      "ws-server-response-completed.json",
			direction: directionServer,
			event:     eventResponseCompleted,
			want: map[Field]string{
				FieldResponseReference: "resp_0f3fd2b96e949a05016a8ee314f00087d0b428d9d5010d2a40",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			raw := readVersionFixture(t, test.file)
			before := append([]byte(nil), raw...)
			var result Result
			if test.direction == directionClient {
				view := InspectClientFrame(raw)
				if !view.Recognized() || view.EventType() != test.event || !bytes.Equal(view.ReplayBytes(), raw) {
					t.Fatalf("view event=%q replay_equal=%t", view.EventType(), bytes.Equal(view.ReplayBytes(), raw))
				}
				result = DecideClient(ClientInput{Message: view, Owners: fixedLookup(OwnerCurrent)})
			} else {
				view := InspectServerFrame(raw)
				if !view.Recognized() || view.EventType() != test.event || !bytes.Equal(view.ReplayBytes(), raw) {
					t.Fatalf("view event=%q replay_equal=%t", view.EventType(), bytes.Equal(view.ReplayBytes(), raw))
				}
				result = DecideServerMessage(view, fixedLookup(OwnerCurrent))
			}
			if result.Rejected() {
				t.Fatalf("fixture rejected: %#v", result.Decisions())
			}
			if !bytes.Equal(raw, before) || !bytes.Equal(result.ReplayBytes(), raw) {
				t.Fatal("fixture bytes changed")
			}
			if len(raw) > 0 && &result.ReplayBytes()[0] != &raw[0] {
				t.Fatal("replay did not retain the original buffer")
			}
			for field, want := range test.want {
				decision := requireDecision(t, result, field)
				if got := string(decision.Candidate().Value().Bytes()); got != want {
					t.Fatalf("%s = %q, want %q", field, got, want)
				}
			}
			if test.file == "ws-client-response-create-second.json" {
				for _, decision := range result.Decisions() {
					if decision.Field() == FieldTurnState {
						t.Fatal("confirmed-absent client_metadata turn state was synthesized")
					}
				}
			}
		})
	}
}

func TestInspectClientPayloadKeepsCompressedWire(t *testing.T) {
	semantic := []byte(`{"type":"response.create","client_metadata":{"thread_id":"thread"}}`)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(semantic); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	wire := compressed.Bytes()
	beforeWire := append([]byte(nil), wire...)
	beforeSemantic := append([]byte(nil), semantic...)
	view := InspectClientPayload(wire, semantic)
	result := DecideClient(ClientInput{Message: view, Owners: fixedLookup(OwnerCurrent)})
	if result.Rejected() || !bytes.Equal(result.ReplayBytes(), beforeWire) || !bytes.Equal(semantic, beforeSemantic) {
		t.Fatalf("compressed observation changed buffers: %#v", result.Decisions())
	}
	if &result.ReplayBytes()[0] != &wire[0] {
		t.Fatal("compressed replay replaced the original wire buffer")
	}

	reader, err := gzip.NewReader(bytes.NewReader(result.ReplayBytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, semantic) {
		t.Fatal("replayed gzip no longer decodes to the semantic payload")
	}
}

func TestRecognizedClientJSONSecurityShapeValidation(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		wantReason Reason
		wantField  Field
	}{
		{name: "duplicate metadata container", body: `{"type":"response.create","client_metadata":{},"client_metadata":{}}`, wantReason: ReasonDuplicateSecurityKey, wantField: FieldEnvelope},
		{name: "null metadata", body: `{"type":"response.create","client_metadata":null}`, wantReason: ReasonInvalidProjection, wantField: FieldEnvelope},
		{name: "array metadata", body: `{"type":"response.create","client_metadata":[]}`, wantReason: ReasonInvalidProjection, wantField: FieldEnvelope},
		{name: "duplicate fixed projection", body: `{"type":"response.create","client_metadata":{"thread_id":"one","thread_id":"two"}}`, wantReason: ReasonDuplicateSecurityKey, wantField: FieldThreadID},
		{name: "escaped duplicate fixed projection", body: `{"type":"response.create","client_metadata":{"session_id":"one","session\u005fid":"two"}}`, wantReason: ReasonDuplicateSecurityKey, wantField: FieldSessionID},
		{name: "empty projection", body: `{"type":"response.create","client_metadata":{"x-codex-window-id":""}}`, wantReason: ReasonInvalidProjection, wantField: FieldWindowID},
		{name: "null projection", body: `{"type":"response.create","client_metadata":{"thread_id":null}}`, wantReason: ReasonInvalidProjection, wantField: FieldThreadID},
		{name: "non-string projection", body: `{"type":"response.create","client_metadata":{"session_id":7}}`, wantReason: ReasonInvalidProjection, wantField: FieldSessionID},
		{name: "duplicate previous response", body: `{"type":"response.create","previous_response_id":"one","previous_response_id":"two"}`, wantReason: ReasonDuplicateSecurityKey, wantField: FieldResponseReference},
		{name: "empty previous response", body: `{"type":"response.create","previous_response_id":""}`, wantReason: ReasonInvalidProjection, wantField: FieldResponseReference},
		{name: "null previous response", body: `{"type":"response.create","previous_response_id":null}`, wantReason: ReasonInvalidProjection, wantField: FieldResponseReference},
		{name: "non-string previous response", body: `{"type":"response.create","previous_response_id":{}}`, wantReason: ReasonInvalidProjection, wantField: FieldResponseReference},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := DecideClient(ClientInput{
				Message: InspectClientFrame([]byte(test.body)),
				Owners:  fixedLookup(OwnerCurrent),
			})
			decision := requireOnlyDecision(t, result)
			if decision.Action() != ActionReject || decision.Reason() != test.wantReason || decision.Field() != test.wantField {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestClientJSONUnknownPathsStayOpaque(t *testing.T) {
	tests := []string{
		`{"unknown":{"client_metadata":{"thread_id":"nested"}}}`,
		`{"type":"unknown","client_metadata":{"thread_id":null},"previous_response_id":7}`,
		`{"type":"response.create","unknown":1,"unknown":2,"client_metadata":{"unknown":{"session_id":"nested"},"unknown":null}}`,
		`{"type":"response.create","client_metadata":{"Thread_Id":"wrong-case","x-codex-turn-state":"unconfirmed"}}`,
		`{"client_metadata":{"thread_id":"no-envelope"}}`,
	}
	for index, body := range tests {
		result := DecideClient(ClientInput{
			Message: InspectClientFrame([]byte(body)),
			Owners:  fixedLookup(OwnerConflict),
		})
		if result.Outcome() != ActionForward || len(result.Decisions()) != 0 {
			t.Fatalf("case %d interpreted unknown path: %#v", index, result.Decisions())
		}
	}

	missingMetadata := DecideClient(ClientInput{
		Message: InspectClientFrame([]byte(`{"type":"response.create"}`)),
		Owners:  fixedLookup(OwnerConflict),
	})
	if missingMetadata.Outcome() != ActionForward {
		t.Fatalf("missing metadata = %#v", missingMetadata.Decisions())
	}
}

func TestConnectionBoundClientEventsAreRecognizedWithoutInventingAFieldShape(t *testing.T) {
	for _, event := range []string{eventResponseInject, eventResponseAppend} {
		body := []byte(`{"type":"` + event + `","response":{"id":"must-not-be-read"},"response_id":"also-unknown"}`)
		view := InspectClientFrame(body)
		result := DecideClient(ClientInput{
			Message: view,
			Owners: func(BindingCandidate) OwnerStatus {
				t.Fatal("connection-bound event must not perform an owner lookup")
				return OwnerCurrent
			},
		})
		if !view.Recognized() || view.EventType() != event || result.Outcome() != ActionForward || len(result.Decisions()) != 0 {
			t.Fatalf("event=%q recognized=%t decisions=%#v", view.EventType(), view.Recognized(), result.Decisions())
		}
		if !bytes.Equal(result.ReplayBytes(), body) {
			t.Fatal("connection-bound event buffer was rewritten")
		}
	}
}

func TestUnrecognizedPayloadsStayOpaque(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "empty", body: nil},
		{name: "non JSON text", body: []byte("future wire format")},
		{name: "truncated JSON", body: []byte(`{"type":`)},
		{name: "trailing JSON", body: []byte(`{"type":"other"}{}`)},
		{name: "JSON array", body: []byte(`[]`)},
		{name: "JSON scalar", body: []byte(`7`)},
		{name: "missing type", body: []byte(`{"future":true}`)},
		{name: "unknown type", body: []byte(`{"type":"future.event","response":{"id":null}}`)},
		{name: "duplicate type", body: []byte(`{"type":"other","type":"response.create"}`)},
		{name: "escaped duplicate type", body: []byte(`{"type":"other","\u0074ype":"response.create"}`)},
		{name: "null type", body: []byte(`{"type":null}`)},
		{name: "non-string type", body: []byte(`{"type":1}`)},
		{name: "empty type", body: []byte(`{"type":""}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, inspect := range []struct {
				name   string
				view   MessageView
				decide func(MessageView, OwnerLookup) Result
			}{
				{name: "client", view: InspectClientFrame(test.body), decide: func(view MessageView, owners OwnerLookup) Result {
					return DecideClient(ClientInput{Message: view, Owners: owners})
				}},
				{name: "server", view: InspectServerFrame(test.body), decide: DecideServerMessage},
			} {
				t.Run(inspect.name, func(t *testing.T) {
					result := inspect.decide(inspect.view, func(BindingCandidate) OwnerStatus {
						t.Fatal("opaque payload performed an owner lookup")
						return OwnerConflict
					})
					if inspect.view.Recognized() || inspect.view.EventType() != "" || inspect.view.ResponseLifecycle() != ResponseLifecycleNone {
						t.Fatalf("opaque view recognized event %q", inspect.view.EventType())
					}
					if result.Outcome() != ActionForward || len(result.Decisions()) != 0 || !bytes.Equal(result.ReplayBytes(), test.body) {
						t.Fatalf("opaque result = %#v", result)
					}
				})
			}
		})
	}

	wire := []byte{0x1f, 0x8b, 0x08, 0x00}
	view := InspectClientPayload(wire, nil)
	result := DecideClient(ClientInput{Message: view})
	if view.Recognized() || result.Outcome() != ActionForward || !bytes.Equal(result.ReplayBytes(), wire) {
		t.Fatalf("semantic decode failure was not opaque: %#v", result)
	}
}

func TestDirectionMisuseIsRejected(t *testing.T) {
	serverView := InspectServerFrame([]byte(`{"type":"other"}`))
	if decision := requireOnlyDecision(t, DecideClient(ClientInput{Message: serverView})); decision.Reason() != ReasonInvalidEnvelope {
		t.Fatalf("decision = %#v", decision)
	}
	clientView := InspectClientFrame([]byte(`{"type":"other"}`))
	if decision := requireOnlyDecision(t, DecideServerMessage(clientView, nil)); decision.Reason() != ReasonInvalidEnvelope {
		t.Fatalf("decision = %#v", decision)
	}
	if decision := requireOnlyDecision(t, DecideServerMessage(MessageView{}, nil)); decision.Reason() != ReasonInvalidEnvelope {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestServerFrameEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		owner      OwnerStatus
		wantAction Action
		wantReason Reason
		wantField  Field
	}{
		{
			name: "new state claims", body: `{"type":"codex.response.metadata","headers":{"X-CoDeX-TuRn-StAtE":"state"}}`, owner: OwnerUnknown,
			wantAction: ActionClaim, wantReason: ReasonOwnerUnknown, wantField: FieldTurnState,
		},
		{
			name: "known state forwards", body: `{"type":"codex.response.metadata","headers":{"x-codex-turn-state":"state"}}`, owner: OwnerCurrent,
			wantAction: ActionForward, wantReason: ReasonOwnerMatch, wantField: FieldTurnState,
		},
		{
			name: "conflicting state rejects", body: `{"type":"codex.response.metadata","headers":{"x-codex-turn-state":"state"}}`, owner: OwnerConflict,
			wantAction: ActionReject, wantReason: ReasonOwnerConflict, wantField: FieldTurnState,
		},
		{
			name: "duplicate casing rejects", body: `{"type":"codex.response.metadata","headers":{"x-codex-turn-state":"one","X-Codex-Turn-State":"two"}}`, owner: OwnerCurrent,
			wantAction: ActionReject, wantReason: ReasonDuplicateSecurityKey, wantField: FieldTurnState,
		},
		{
			name: "null state rejects", body: `{"type":"codex.response.metadata","headers":{"x-codex-turn-state":null}}`, owner: OwnerCurrent,
			wantAction: ActionReject, wantReason: ReasonInvalidProjection, wantField: FieldTurnState,
		},
		{
			name: "duplicate headers rejects", body: `{"type":"codex.response.metadata","headers":{},"headers":{}}`, owner: OwnerCurrent,
			wantAction: ActionReject, wantReason: ReasonDuplicateSecurityKey, wantField: FieldEnvelope,
		},
		{
			name: "invalid headers rejects", body: `{"type":"codex.response.metadata","headers":null}`, owner: OwnerCurrent,
			wantAction: ActionReject, wantReason: ReasonInvalidProjection, wantField: FieldEnvelope,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := InspectServerFrame([]byte(test.body))
			result := DecideServerMessage(view, fixedLookup(test.owner))
			decision := requireOnlyDecision(t, result)
			if decision.Action() != test.wantAction || decision.Reason() != test.wantReason || decision.Field() != test.wantField {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}

	for _, body := range []string{
		`{"type":"codex.response.metadata"}`,
		`{"type":"codex.response.metadata","headers":{"unknown":{"x-codex-turn-state":"nested"}}}`,
		`{"type":"unknown","headers":{"x-codex-turn-state":null}}`,
	} {
		result := DecideServerMessage(
			InspectServerFrame([]byte(body)),
			fixedLookup(OwnerConflict),
		)
		if result.Outcome() != ActionForward || len(result.Decisions()) != 0 {
			t.Fatalf("unknown or absent server evidence was interpreted: %#v", result.Decisions())
		}
	}
}

func TestServerResponseReferenceEvidence(t *testing.T) {
	for _, eventType := range []string{
		eventResponseCreated,
		eventResponseInProgress,
		eventResponseCompleted,
		eventResponseIncomplete,
		eventResponseFailed,
	} {
		for _, test := range []struct {
			name       string
			body       string
			owner      OwnerStatus
			wantAction Action
			wantReason Reason
		}{
			{name: "new reference claims", body: `{"type":"%s","response":{"id":"response"}}`, owner: OwnerUnknown, wantAction: ActionClaim, wantReason: ReasonOwnerUnknown},
			{name: "known reference forwards", body: `{"type":"%s","response":{"id":"response"}}`, owner: OwnerCurrent, wantAction: ActionForward, wantReason: ReasonOwnerMatch},
			{name: "conflicting reference rejects", body: `{"type":"%s","response":{"id":"response"}}`, owner: OwnerConflict, wantAction: ActionReject, wantReason: ReasonOwnerConflict},
		} {
			t.Run(eventType+"/"+test.name, func(t *testing.T) {
				body := fmt.Appendf(nil, test.body, eventType)
				view := InspectServerFrame(body)
				wantLifecycle := ResponseLifecycleActive
				if eventType == eventResponseCompleted || eventType == eventResponseIncomplete || eventType == eventResponseFailed {
					wantLifecycle = ResponseLifecycleTerminal
				}
				if !view.Recognized() || view.ResponseLifecycle() != wantLifecycle {
					t.Fatalf("recognized=%t lifecycle=%d, want %d", view.Recognized(), view.ResponseLifecycle(), wantLifecycle)
				}
				result := DecideServerMessage(view, fixedLookup(test.owner))
				decision := requireOnlyDecision(t, result)
				if decision.Action() != test.wantAction || decision.Reason() != test.wantReason || decision.Field() != FieldResponseReference {
					t.Fatalf("decision = %#v", decision)
				}
				if !bytes.Equal(result.ReplayBytes(), body) {
					t.Fatal("response reference observation rewrote frame bytes")
				}
			})
		}
	}

	for _, test := range []struct {
		name   string
		body   string
		reason Reason
	}{
		{name: "duplicate response", body: `{"type":"response.created","response":{"id":"one"},"response":{"id":"two"}}`, reason: ReasonDuplicateSecurityKey},
		{name: "invalid response", body: `{"type":"response.created","response":null}`, reason: ReasonInvalidProjection},
		{name: "duplicate id", body: `{"type":"response.created","response":{"id":"one","id":"two"}}`, reason: ReasonDuplicateSecurityKey},
		{name: "escaped duplicate id", body: `{"type":"response.created","response":{"id":"one","\u0069d":"two"}}`, reason: ReasonDuplicateSecurityKey},
		{name: "empty id", body: `{"type":"response.created","response":{"id":""}}`, reason: ReasonInvalidProjection},
		{name: "null id", body: `{"type":"response.created","response":{"id":null}}`, reason: ReasonInvalidProjection},
		{name: "non-string id", body: `{"type":"response.created","response":{"id":1}}`, reason: ReasonInvalidProjection},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := DecideServerMessage(InspectServerFrame([]byte(test.body)), fixedLookup(OwnerCurrent))
			decision := requireOnlyDecision(t, result)
			if decision.Action() != ActionReject || decision.Reason() != test.reason || decision.Field() != FieldResponseReference {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}

	for _, body := range []string{
		`{"type":"response.created"}`,
		`{"type":"response.created","response":{}}`,
		`{"type":"unknown","response":{"id":"opaque"}}`,
	} {
		result := DecideServerMessage(InspectServerFrame([]byte(body)), fixedLookup(OwnerConflict))
		if result.Outcome() != ActionForward || len(result.Decisions()) != 0 {
			t.Fatalf("absent or unknown reference was interpreted: %#v", result.Decisions())
		}
	}
}

func readVersionFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", fixtureVersionDirectory, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
