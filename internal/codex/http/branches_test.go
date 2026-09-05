package codexhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestAttemptGuardAndAuthorityFailures(t *testing.T) {
	candidateA, appliedA := testCandidate(t, "route-a", "provider-a.test", "subject-a")
	candidateB, _ := testCandidate(t, "route-b", "provider-b.test", "subject-b")
	request := httptest.NewRequest(http.MethodPost, "https://provider-a.test/v1/responses", nil)
	var missing *Operation
	if _, err := missing.PrepareAttempt(context.Background(), request, candidateA, appliedA); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("nil operation error = %v", err)
	}
	operation := &Operation{apiType: codexAPIType}
	if _, err := operation.PrepareAttempt(context.Background(), nil, candidateA, appliedA); !IsKind(err, ErrorClientInput) {
		t.Fatalf("nil request error = %v", err)
	}
	required := candidateB.Authority()
	operation.requiredAuthority = &required
	if _, err := operation.PrepareAttempt(context.Background(), request, candidateA, appliedA); !IsKind(err, ErrorIdentityMismatch) {
		t.Fatalf("required authority error = %v", err)
	}
	operation = &Operation{apiType: codexAPIType}
	if _, err := operation.PrepareAttempt(context.Background(), request, candidateA, appliedA); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("missing Cookie request error = %v", err)
	}
	nonCodex := &Operation{apiType: "claude"}
	attempt, err := nonCodex.PrepareAttempt(context.Background(), request, codexidentity.CandidateSnapshot{}, codexidentity.AppliedIdentity{})
	if err != nil || attempt == nil {
		t.Fatalf("non-Codex attempt = %v, %v", attempt, err)
	}
}

func TestAttestationPinsLogicalOperationAuthorityOnlyAfterDisclosure(t *testing.T) {
	clientScope := testClientScope(t, "attestation-client")
	continuity := &continuityRecorder{}
	runtime := newContinuityTestRuntime(t, clientScope, continuity)
	clientRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	clientRequest.Header.Set("Authorization", "Bearer client")
	clientRequest.Header.Set("X-Oai-Attestation", "attestation")
	operation, err := runtime.Begin(context.Background(), clientRequest, codexAPIType, "attestation-operation", "preserve_conversation", testClientEvidence(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	candidateA, appliedA := testCandidate(t, "route-a", "provider-a.test", "subject-a")
	requestA := httptest.NewRequest(http.MethodPost, "https://provider-a.test/v1/responses", nil)
	attemptA, err := operation.PrepareAttempt(context.Background(), requestA, candidateA, appliedA)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := operation.RequiredAuthority(); required != nil {
		t.Fatal("attestation pinned authority before disclosure")
	}
	if err := attemptA.AbandonBeforeDisclosure(context.Background()); err != nil {
		t.Fatal(err)
	}
	candidateB, appliedB := testCandidate(t, "route-b", "provider-b.test", "subject-b")
	requestB := httptest.NewRequest(http.MethodPost, "https://provider-b.test/v1/responses", nil)
	attemptB, err := operation.PrepareAttempt(context.Background(), requestB, candidateB, appliedB)
	if err != nil {
		t.Fatal("undisclosed attestation prevented replacement:", err)
	}
	if err := attemptB.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.PrepareAttempt(context.Background(), requestA.Clone(context.Background()), candidateA, appliedA); !IsKind(err, ErrorIdentityMismatch) {
		t.Fatalf("disclosed attestation cross-authority error = %v", err)
	}
}

func TestContinuityFailuresStayTypedAtRequestAndResponseBoundaries(t *testing.T) {
	clientScope := testClientScope(t, "failure-client")
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject")
	unknown := &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}
	unavailable := &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable}
	conflict := &codexcontinuity.Error{Kind: codexcontinuity.ErrorConflict}

	claimFailure := &continuityRecorder{resolveErr: unknown, claimErr: unavailable}
	claimOperation := beginContinuityOperation(t, clientScope, claimFailure, http.Header{"Thread-Id": []string{"thread"}})
	upstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	claimAttempt, err := claimOperation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatalf("claim outage should preserve the pinned attempt: %v", err)
	}
	if required, _ := claimOperation.RequiredAuthority(); required != nil {
		t.Fatalf("claim outage pinned authority before disclosure = %v", required)
	}
	if err := claimAttempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if required, _ := claimOperation.RequiredAuthority(); required == nil || !required.Equal(candidate.Authority()) {
		t.Fatalf("disclosed claim outage authority = %v", required)
	}

	known := codexcontinuity.Binding{Owner: codexcontinuity.Owner{ClientScope: clientScope, ProtocolScope: candidate.ProtocolScope()}}
	validationFailure := &continuityRecorder{resolveBinding: known, validateErr: conflict}
	validationOperation := beginContinuityOperation(t, clientScope, validationFailure, http.Header{"Thread-Id": []string{"thread"}})
	if _, err := validationOperation.PrepareAttempt(context.Background(), upstream.Clone(context.Background()), candidate, applied); !IsKind(err, ErrorClientInput) {
		t.Fatalf("validation conflict error = %v", err)
	}

	responseUnavailable := &continuityRecorder{validateErr: unavailable}
	responseOperation := beginContinuityOperation(t, clientScope, responseUnavailable, nil)
	bindOperationScopeForResponse(responseOperation, clientScope)
	responseAttempt, err := responseOperation.PrepareAttempt(context.Background(), upstream.Clone(context.Background()), candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	responseHeaders := http.Header{"X-Codex-Turn-State": []string{"turn"}}
	visibility, err := responseAttempt.PrepareVisible(context.Background(), responseHeaders)
	if err != nil || visibility == nil || len(visibility.leases) != 1 {
		t.Fatalf("response lookup degradation visibility=%#v error=%v", visibility, err)
	}

	responseConflict := &continuityRecorder{validateErr: conflict}
	conflictOperation := beginContinuityOperation(t, clientScope, responseConflict, nil)
	bindOperationScopeForResponse(conflictOperation, clientScope)
	conflictAttempt, err := conflictOperation.PrepareAttempt(context.Background(), upstream.Clone(context.Background()), candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conflictAttempt.PrepareVisible(context.Background(), http.Header{"X-Codex-Turn-State": []string{"turn"}}); !IsKind(err, ErrorClientInput) {
		t.Fatalf("response conflict error = %v", err)
	}

	prepareFailure := &continuityRecorder{validateErr: unknown, prepareErr: unavailable}
	prepareOperation := beginContinuityOperation(t, clientScope, prepareFailure, nil)
	bindOperationScopeForResponse(prepareOperation, clientScope)
	prepareAttempt, err := prepareOperation.PrepareAttempt(context.Background(), upstream.Clone(context.Background()), candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareAttempt.PrepareVisible(context.Background(), http.Header{"X-Codex-Turn-State": []string{"turn"}}); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("response prepare without provenance error=%v", err)
	}
}

func TestContinuityCommitFailuresRemainPending(t *testing.T) {
	clientScope := testClientScope(t, "commit-client")
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject")
	commitFailure := errors.New("commit failed")
	requestRecorder := &continuityRecorder{
		resolveErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}, commitErr: commitFailure,
	}
	requestOperation := beginContinuityOperation(t, clientScope, requestRecorder, http.Header{"Thread-Id": []string{"thread"}})
	upstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	requestAttempt, err := requestOperation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if err := requestAttempt.MarkDisclosed(context.Background()); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("request commit error = %v", err)
	}
	requestOutage := &continuityRecorder{
		resolveErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown},
		commitErr:  &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable},
	}
	outageOperation := beginContinuityOperation(t, clientScope, requestOutage, http.Header{"Thread-Id": []string{"thread-outage"}})
	outageAttempt, err := outageOperation.PrepareAttempt(context.Background(), upstream.Clone(context.Background()), candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if err := outageAttempt.MarkDisclosed(context.Background()); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("request commit without provenance error = %v", err)
	}

	responseRecorder := &continuityRecorder{
		validateErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}, commitErr: commitFailure,
	}
	responseOperation := beginContinuityOperation(t, clientScope, responseRecorder, nil)
	bindOperationScopeForResponse(responseOperation, clientScope)
	responseAttempt, err := responseOperation.PrepareAttempt(context.Background(), upstream.Clone(context.Background()), candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	visibility, err := responseAttempt.PrepareVisible(context.Background(), http.Header{"X-Codex-Turn-State": []string{"turn"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := visibility.Commit(context.Background()); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("response commit error = %v", err)
	}
}

func TestRuntimeUtilityBranches(t *testing.T) {
	fields := map[codexheaders.Field]codexcontinuity.Kind{
		codexheaders.FieldThreadID: codexcontinuity.KindThreadID, codexheaders.FieldSessionID: codexcontinuity.KindSessionID,
		codexheaders.FieldConversationID: codexcontinuity.KindConversationID, codexheaders.FieldWindowID: codexcontinuity.KindWindowID,
		codexheaders.FieldTurnState: codexcontinuity.KindTurnState, codexheaders.FieldTurnMetadata: codexcontinuity.KindTurnMetadata,
		codexheaders.FieldResponseReference: codexcontinuity.KindResponseReference,
	}
	for field, want := range fields {
		if got := continuityKind(field); got != want {
			t.Fatalf("continuityKind(%s) = %s", field, got)
		}
	}
	if got := continuityKind(codexheaders.FieldAttestation); got != "" {
		t.Fatalf("attestation continuity kind = %q", got)
	}
	if cloneURL(nil) != nil {
		t.Fatal("nil utility contract failed")
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/", nil)
	request.AddCookie(&http.Cookie{Name: providercookie.GatewayHandleName, Value: "one"})
	request.AddCookie(&http.Cookie{Name: providercookie.GatewayHandleName, Value: "two"})
	if got := gatewayHandle(request); got != "invalid-multiple-handle" {
		t.Fatalf("multiple gateway handle = %q", got)
	}
	var nilError *Error
	if nilError.Error() == "" || nilError.Unwrap() != nil {
		t.Fatal("nil typed error contract failed")
	}
	cause := errors.New("cause")
	typed := &Error{Kind: ErrorClientInput, Stage: "stage", Cause: cause}
	if !strings.Contains(typed.Error(), "stage") || !errors.Is(typed, cause) {
		t.Fatalf("typed error formatting/unwrapping = %q/%v", typed.Error(), typed.Unwrap())
	}
	withoutCause := (&Error{Kind: ErrorClientInput, Stage: "stage"}).Error()
	if !strings.Contains(withoutCause, "client_input") {
		t.Fatalf("typed error without cause = %q", withoutCause)
	}
	if err := (*Visibility)(nil).Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	(*Operation)(nil).Discard()
	visibility, err := (*Attempt)(nil).PrepareVisible(context.Background(), make(http.Header))
	if err != nil || visibility == nil {
		t.Fatalf("nil attempt visibility = %v, %v", visibility, err)
	}
	cookieOperation := &Operation{apiType: codexAPIType}
	if err := (&Attempt{operation: cookieOperation}).ObserveResponse(&upstreamtransport.ResponseHead{}); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("missing Cookie response state error = %v", err)
	}
}

func TestBeginAdditionalFailureBranches(t *testing.T) {
	scope := testClientScope(t, "begin")
	runtime := newAlwaysOnTestRuntime(t, Config{ClientIdentities: testScopeDigester{current: scope, candidates: []codexidentity.ClientScope{scope}}})
	if _, err := runtime.Begin(context.Background(), nil, codexAPIType, "operation", "preserve_conversation", testClientEvidence(nil, nil)); !IsKind(err, ErrorClientInput) {
		t.Fatalf("nil request error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/", nil)
	request.Header.Set("Authorization", "Bearer client")
	digestFailure := newAlwaysOnTestRuntime(t, Config{
		ClientIdentities: testScopeDigester{err: errors.New("HMAC unavailable")}, Continuity: &continuityRecorder{},
	})
	request.Header.Set("Thread-Id", "state-requires-scope")
	if _, err := digestFailure.Begin(context.Background(), request, codexAPIType, "operation", "preserve_conversation", testClientEvidence(nil, nil)); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("scope digest error = %v", err)
	}
	conflictingOwner := newAlwaysOnTestRuntime(t, Config{
		ClientIdentities: testScopeDigester{current: scope, candidates: []codexidentity.ClientScope{scope}},
		Continuity:       &continuityRecorder{resolveErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorConflict}},
	})
	request.Header.Set("X-Codex-Turn-State", "turn")
	if _, err := conflictingOwner.Begin(context.Background(), request, codexAPIType, "operation", "preserve_conversation", testClientEvidence(nil, nil)); !IsKind(err, ErrorClientInput) {
		t.Fatalf("owner conflict error = %v", err)
	}
	unavailableOwner := newAlwaysOnTestRuntime(t, Config{
		ClientIdentities: testScopeDigester{current: scope, candidates: []codexidentity.ClientScope{scope}},
		Continuity:       &continuityRecorder{resolveErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable}},
	})
	if _, err := unavailableOwner.Begin(context.Background(), request, codexAPIType, "operation", "preserve_conversation", testClientEvidence(nil, nil)); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("owner unavailable error = %v", err)
	}
}

func bindOperationScopeForResponse(operation *Operation, scope codexidentity.ClientScope) {
	operation.currentClientScope = scope
	operation.clientScopes = []codexidentity.ClientScope{scope}
	operation.hasClientScope = true
}

func beginContinuityOperation(t *testing.T, scope codexidentity.ClientScope, continuity *continuityRecorder, headers http.Header) *Operation {
	t.Helper()
	runtime := newContinuityTestRuntime(t, scope, continuity)
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer client")
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "branch-operation", "preserve_conversation", testClientEvidence(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func newContinuityTestRuntime(t *testing.T, scope codexidentity.ClientScope, continuity *continuityRecorder) *Runtime {
	return newAlwaysOnTestRuntime(t, Config{
		ClientIdentities: testScopeDigester{current: scope, candidates: []codexidentity.ClientScope{scope}},
		Continuity:       continuity,
	})
}
