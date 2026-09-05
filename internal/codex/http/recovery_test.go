package codexhttp

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestRecoveryMixedProvenanceSurvivesThreeDisclosedAttempts(t *testing.T) {
	ctx := context.Background()
	client := testClientScope(t, "recovery-client")
	sourceA, _ := testCandidate(t, "source-a", "a.example", "a")
	sourceB, _ := testCandidate(t, "source-b", "b.example", "b")
	recorder := &continuityRecorder{resolve: func(request codexcontinuity.ResolveRequest) (codexcontinuity.Binding, error) {
		owner := codexcontinuity.Owner{ClientScope: client, ProtocolScope: sourceA.ProtocolScope(), RouteTargetHint: "source-a"}
		if request.Evidence.Kind == codexcontinuity.KindTurnState {
			owner.ProtocolScope = sourceB.ProtocolScope()
			owner.RouteTargetHint = "source-b"
		}
		return codexcontinuity.Binding{Owner: owner}, nil
	}}
	runtime := newContinuityTestRuntime(t, client, recorder)
	body := []byte("{ \"type\":\"response.create\", \"model\":\"gpt\", \"previous_response_id\":\"old-response\", \"input\":[] }")
	request := httptest.NewRequest(http.MethodPost, "http://gateway/codex/v1/responses", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer client")
	request.Header.Set("Thread-Id", "old-thread")
	request.Header.Set("X-Codex-Turn-State", "old-turn")
	request.Header.Set("X-Oai-Attestation", "opaque-attestation")
	op, err := runtime.Begin(ctx, request, codexAPIType, "mixed", model.ConversationRecoverySwitchAccountPreserveConversation, testClientEvidence(body, body))
	if err != nil {
		t.Fatal(err)
	}
	defer op.Discard()
	if !op.ClientScope().Equal(client) {
		t.Fatal("scope lost")
	}
	if authority, preferred := op.RequiredAuthority(); authority != nil || preferred != "" {
		t.Fatal("historical owner constrained routing")
	}

	var last *Attempt
	for _, id := range []string{"a", "b", "c"} {
		candidate, applied := testCandidate(t, "target-"+id, id+".example", id)
		upstream := httptest.NewRequest(http.MethodPost, "https://"+id+".example/v1/responses", bytes.NewReader(body))
		upstream.Header = request.Header.Clone()
		last, err = op.PrepareAttempt(ctx, upstream, candidate, applied)
		if err != nil {
			t.Fatal(err)
		}
		if upstream.Header.Get("X-Codex-Turn-State") != "old-turn" || upstream.Header.Get("X-Oai-Attestation") != "opaque-attestation" {
			t.Fatal("opaque state changed")
		}
		if err := last.MarkDisclosed(ctx); err != nil {
			t.Fatal(err)
		}
		if authority, preferred := op.RequiredAuthority(); authority != nil || preferred != "" {
			t.Fatal("disclosure escaped physical attempt")
		}
	}
	if len(recorder.acquireCalls) != 9 {
		t.Fatalf("acquisitions = %d", len(recorder.acquireCalls))
	}
	for _, acquisition := range recorder.acquireCalls {
		expected := sourceA.ProtocolScope()
		if acquisition.Evidence.Kind == codexcontinuity.KindTurnState {
			expected = sourceB.ProtocolScope()
		}
		if !acquisition.ProtocolScope.Equal(expected) {
			t.Fatal("acquisition used target account instead of source")
		}
	}
	recorder.acquire = func(codexcontinuity.ValidateRequest) (codexcontinuity.Lease, error) {
		return codexcontinuity.Lease{}, errors.New("echo must not resolve again")
	}
	before := len(recorder.acquireCalls)
	visibility, err := last.PrepareVisible(ctx, http.Header{"X-Codex-Turn-State": {"old-turn"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := visibility.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(recorder.acquireCalls) != before || len(recorder.prepareCalls) != 0 {
		t.Fatal("echo provenance changed")
	}
	other, applied := testCandidate(t, "target-d", "d.example", "d")
	if _, err := op.PrepareAttempt(ctx, httptest.NewRequest(http.MethodPost, "https://d.example", nil), other, applied); !IsKind(err, ErrorIdentityMismatch) {
		t.Fatalf("visible replacement = %v", err)
	}
	_, err = runtime.Begin(ctx, request, codexAPIType, "strict-mixed", model.ConversationRecoveryPreserveConversation, testClientEvidence(body, body))
	if !IsKind(err, ErrorClientInput) {
		t.Fatalf("strict mixed = %v", err)
	}
}

func TestRecoveryOpaqueEntranceAndNewResponsePersistence(t *testing.T) {
	for _, entrance := range []codexcontinuity.ErrorKind{codexcontinuity.ErrorUnknown, codexcontinuity.ErrorExpired, codexcontinuity.ErrorUnavailable} {
		t.Run(string(entrance), func(t *testing.T) {
			ctx := context.Background()
			client := testClientScope(t, "opaque-client")
			recorder := &continuityRecorder{resolveErr: &codexcontinuity.Error{Kind: entrance}, validateErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}}
			runtime := newContinuityTestRuntime(t, client, recorder)
			request := httptest.NewRequest(http.MethodPost, "http://gateway/responses", nil)
			request.Header.Set("Authorization", "Bearer client")
			request.Header.Set("X-Codex-Turn-State", "old-turn")
			op, err := runtime.Begin(ctx, request, codexAPIType, "opaque", model.ConversationRecoverySwitchAccountPreserveConversation, testClientEvidence(nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer op.Discard()
			candidate, applied := testCandidate(t, "target", "target.example", "target")
			attempt, err := op.PrepareAttempt(ctx, httptest.NewRequest(http.MethodPost, "https://target.example/responses", nil), candidate, applied)
			if err != nil {
				t.Fatal(err)
			}
			if err := attempt.MarkDisclosed(ctx); err != nil {
				t.Fatal(err)
			}
			if len(recorder.acquireCalls) != 0 || len(recorder.claimCalls) != 0 || len(recorder.adoptCalls) != 0 {
				t.Fatal("opaque entrance acquired ownership")
			}
			op.mu.Lock()
			_, leases, err := op.prepareServerDecisionLocked(ctx, candidate.ProtocolScope(), candidate.RouteTargetID(),
				codexheaders.DecideServerHeaders(http.Header{"X-Codex-Turn-State": {"old-turn"}}, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerUnknown }),
				func(owners codexheaders.OwnerLookup) codexheaders.Result {
					return codexheaders.DecideServerHeaders(http.Header{"X-Codex-Turn-State": {"old-turn"}}, owners)
				})
			op.mu.Unlock()
			if err != nil || len(leases) != 0 || len(recorder.acquireCalls) != 0 {
				t.Fatalf("echo = %v, leases=%d", err, len(leases))
			}
			recorder.resolveErr = &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}
			visibility, err := attempt.PrepareVisible(ctx, http.Header{"X-Codex-Turn-State": {"new-turn"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(recorder.prepareCalls) != 1 || !recorder.prepareCalls[0].Scope.ProtocolScope.Equal(candidate.ProtocolScope()) {
				t.Fatal("new response state not attributed to target")
			}
			recorder.commitErr = &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable}
			if err := visibility.Commit(ctx); err != nil {
				t.Fatalf("continuity blocked delivery: %v", err)
			}
		})
	}
}

func TestRecoverySourceExpiryAfterEntranceAndResponseStoreOutage(t *testing.T) {
	for _, acquisitionFailure := range []codexcontinuity.ErrorKind{codexcontinuity.ErrorUnknown, codexcontinuity.ErrorExpired, codexcontinuity.ErrorUnavailable} {
		t.Run(string(acquisitionFailure), func(t *testing.T) {
			client := testClientScope(t, "expiry-client")
			source, _ := testCandidate(t, "source", "source.example", "source")
			target, applied := testCandidate(t, "target", "target.example", "target")
			recorder := &continuityRecorder{resolveBinding: codexcontinuity.Binding{Owner: codexcontinuity.Owner{ClientScope: client, ProtocolScope: source.ProtocolScope()}}, validateErr: &codexcontinuity.Error{Kind: acquisitionFailure}}
			runtime := newContinuityTestRuntime(t, client, recorder)
			request := httptest.NewRequest(http.MethodPost, "http://gateway/responses", nil)
			request.Header.Set("Authorization", "Bearer client")
			request.Header.Set("X-Codex-Turn-State", "old-turn")
			op, err := runtime.Begin(context.Background(), request, codexAPIType, "expiry", model.ConversationRecoverySwitchAccountPreserveConversation, testClientEvidence(nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer op.Discard()
			attempt, err := op.PrepareAttempt(context.Background(), httptest.NewRequest(http.MethodPost, "https://target.example/responses", nil), target, applied)
			if err != nil {
				t.Fatal(err)
			}
			if err := attempt.MarkDisclosed(context.Background()); err != nil {
				t.Fatal(err)
			}
			if recorder.commitCalls != 0 {
				t.Fatal("degraded source obtained a lease")
			}
			recorder.validateErr = &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}
			recorder.prepareErr = &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable}
			recorder.resolveErr = &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}
			visibility, err := attempt.PrepareVisible(context.Background(), http.Header{"X-Codex-Turn-State": {"new-turn"}})
			if err != nil {
				t.Fatalf("new response blocked by store outage: %v", err)
			}
			if err := visibility.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(recorder.prepareCalls) != 1 {
				t.Fatal("new state did not attempt persistence")
			}
		})
	}
}

func TestRecoveryRejectsKnownForeignClientIncludingExpired(t *testing.T) {
	for _, expired := range []bool{false, true} {
		client := testClientScope(t, "client")
		foreign := testClientScope(t, "foreign")
		candidate, _ := testCandidate(t, "source", "source.example", "source")
		recorder := &continuityRecorder{resolveBinding: codexcontinuity.Binding{Owner: codexcontinuity.Owner{ClientScope: foreign, ProtocolScope: candidate.ProtocolScope()}}}
		if expired {
			recorder.resolveErr = &codexcontinuity.Error{Kind: codexcontinuity.ErrorExpired}
		}
		runtime := newContinuityTestRuntime(t, client, recorder)
		request := httptest.NewRequest(http.MethodPost, "http://gateway/responses", nil)
		request.Header.Set("Authorization", "Bearer client")
		request.Header.Set("X-Codex-Turn-State", "foreign-turn")
		if _, err := runtime.Begin(context.Background(), request, codexAPIType, "foreign", model.ConversationRecoverySwitchAccountPreserveConversation, testClientEvidence(nil, nil)); !IsKind(err, ErrorClientInput) {
			t.Fatalf("expired=%v error=%v", expired, err)
		}
	}
}

func TestRecoveryResponseExpiryValidatesRetainedClient(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		client := testClientScope(t, "response-client")
		ownerClient := client
		if foreign {
			ownerClient = testClientScope(t, "foreign-response-client")
		}
		source, _ := testCandidate(t, "source", "source.example", "source")
		target, applied := testCandidate(t, "target", "target.example", "target")
		recorder := &continuityRecorder{resolveBinding: codexcontinuity.Binding{Owner: codexcontinuity.Owner{ClientScope: ownerClient, ProtocolScope: source.ProtocolScope()}}, resolveErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorExpired}}
		runtime := newContinuityTestRuntime(t, client, recorder)
		request := httptest.NewRequest(http.MethodPost, "http://gateway/responses", nil)
		request.Header.Set("Authorization", "Bearer client")
		op, err := runtime.Begin(context.Background(), request, codexAPIType, "response-expiry", model.ConversationRecoverySwitchAccountPreserveConversation, testClientEvidence(nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer op.Discard()
		attempt, err := op.PrepareAttempt(context.Background(), httptest.NewRequest(http.MethodPost, "https://target.example/responses", nil), target, applied)
		if err != nil {
			t.Fatal(err)
		}
		visibility, err := attempt.PrepareVisible(context.Background(), http.Header{"X-Codex-Turn-State": {"expired-response"}})
		if foreign {
			if !IsKind(err, ErrorClientInput) {
				t.Fatalf("foreign expired response=%v", err)
			}
		} else {
			if err != nil {
				t.Fatalf("same-client expired response=%v", err)
			}
			if err := visibility.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(recorder.prepareCalls) != 0 || len(recorder.acquireCalls) != 0 {
				t.Fatal("expired response gained ownership")
			}
		}
	}
}

func TestRecoveryDoesNotDowngradeUnclassifiedContinuityFailures(t *testing.T) {
	client := testClientScope(t, "client")
	candidate, applied := testCandidate(t, "source", "source.example", "source")
	for _, kind := range []codexcontinuity.ErrorKind{codexcontinuity.ErrorConflict, codexcontinuity.ErrorCapacity, codexcontinuity.ErrorInvalidTransition} {
		recorder := &continuityRecorder{resolveBinding: codexcontinuity.Binding{Owner: codexcontinuity.Owner{ClientScope: client, ProtocolScope: candidate.ProtocolScope()}}, validateErr: &codexcontinuity.Error{Kind: kind}}
		runtime := newContinuityTestRuntime(t, client, recorder)
		request := httptest.NewRequest(http.MethodPost, "http://gateway/responses", nil)
		request.Header.Set("Authorization", "Bearer client")
		request.Header.Set("X-Codex-Turn-State", "turn")
		op, err := runtime.Begin(context.Background(), request, codexAPIType, "failure", model.ConversationRecoverySwitchAccountPreserveConversation, testClientEvidence(nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		_, err = op.PrepareAttempt(context.Background(), httptest.NewRequest(http.MethodPost, "https://source.example/responses", nil), candidate, applied)
		op.Discard()
		if err == nil {
			t.Fatalf("downgraded %s", kind)
		}
	}
}
