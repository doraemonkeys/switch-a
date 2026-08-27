package codexhttp

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type testScopeDigester struct {
	current    codexidentity.ClientScope
	candidates []codexidentity.ClientScope
	err        error
}

func (d testScopeDigester) ClientScope([]byte) (codexidentity.ClientScope, error) {
	return d.current, d.err
}

func (d testScopeDigester) ClientScopeCandidates([]byte) ([]codexidentity.ClientScope, error) {
	return append([]codexidentity.ClientScope(nil), d.candidates...), d.err
}

type continuityRecorder struct {
	resolve        func(codexcontinuity.ResolveRequest) (codexcontinuity.Binding, error)
	acquire        func(codexcontinuity.ValidateRequest) (codexcontinuity.Lease, error)
	resolveBinding codexcontinuity.Binding
	resolveErr     error
	validateErr    error
	claimErr       error
	prepareErr     error
	commitErr      error
	resolveCalls   []codexcontinuity.ResolveRequest
	acquireCalls   []codexcontinuity.ValidateRequest
	claimCalls     []codexcontinuity.ClaimRequest
	prepareCalls   []codexcontinuity.ClaimRequest
	commitCalls    int
	abandonCalls   int
}

func (r *continuityRecorder) ResolveOwner(_ context.Context, request codexcontinuity.ResolveRequest) (codexcontinuity.Binding, error) {
	r.resolveCalls = append(r.resolveCalls, request)
	if r.resolve != nil {
		return r.resolve(request)
	}
	return r.resolveBinding, r.resolveErr
}

func (r *continuityRecorder) AcquireExisting(_ context.Context, request codexcontinuity.ValidateRequest) (codexcontinuity.Lease, error) {
	r.acquireCalls = append(r.acquireCalls, request)
	if r.acquire != nil {
		return r.acquire(request)
	}
	return codexcontinuity.Lease{}, r.validateErr
}

func (r *continuityRecorder) Claim(_ context.Context, request codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error) {
	r.claimCalls = append(r.claimCalls, request)
	return codexcontinuity.Lease{}, r.claimErr
}

func (r *continuityRecorder) PrepareVisible(_ context.Context, request codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error) {
	r.prepareCalls = append(r.prepareCalls, request)
	return codexcontinuity.Lease{}, r.prepareErr
}

func (r *continuityRecorder) Commit(context.Context, codexcontinuity.Lease) (codexcontinuity.Binding, error) {
	r.commitCalls++
	return codexcontinuity.Binding{}, r.commitErr
}

func (r *continuityRecorder) AbandonBeforeDisclosure(context.Context, codexcontinuity.Lease) error {
	r.abandonCalls++
	return nil
}

func TestRuntimeDisabledIsWireTransparent(t *testing.T) {
	runtime := New(Config{})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	wire := []byte("not-json-and-must-not-be-normalized")
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "operation-disabled", wire, wire)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Features() != (codexstartup.Snapshot{}) || operation.clientDecision.ReplayBytes() != nil {
		t.Fatalf("disabled operation captured protocol state: %#v", operation)
	}
	if required, preferred := operation.RequiredAuthority(); required != nil || preferred != "" {
		t.Fatalf("disabled required authority = %v/%q", required, preferred)
	}
	if policy := operation.RequestPolicy(); policy.Headers != upstreamtransport.PreserveClientHeaders {
		t.Fatalf("disabled request policy = %#v", policy)
	}
	operation.Discard()
}

func TestRuntimeResolvesOwnerBeforeSelectionAndValidatesAppliedIdentity(t *testing.T) {
	clientScope := testClientScope(t, "client")
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject-a")
	continuity := &continuityRecorder{resolveBinding: codexcontinuity.Binding{Owner: codexcontinuity.Owner{
		ClientScope: clientScope, ProtocolScope: candidate.ProtocolScope(), RouteTargetHint: "route-a",
	}}}
	runtime := New(Config{
		Features:     FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{Continuity: true} }),
		ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}},
		Continuity:   continuity,
	})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("Thread-Id", "thread-known")
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "operation-known", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	required, preferred := operation.RequiredAuthority()
	if required == nil || !required.Equal(candidate.Authority()) || preferred != "route-a" || len(continuity.resolveCalls) != 1 {
		t.Fatalf("required/preferred/lookups = %v/%q/%d", required, preferred, len(continuity.resolveCalls))
	}

	upstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	prepared, err := operation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if len(continuity.acquireCalls) != 1 || len(continuity.claimCalls) != 0 {
		t.Fatalf("acquire/claim calls = %d/%d", len(continuity.acquireCalls), len(continuity.claimCalls))
	}
	if err := prepared.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	operation.Discard()
}

func TestRuntimeClaimsUnknownRequestOnlyAfterAppliedIdentity(t *testing.T) {
	clientScope := testClientScope(t, "client")
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject-a")
	_, wrongApplied := testCandidate(t, "route-b", "provider.test", "subject-b")
	continuity := &continuityRecorder{resolveErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}}
	runtime := New(Config{
		Features:     FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{Continuity: true} }),
		ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}},
		Continuity:   continuity,
	})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	request.Header.Set("X-Api-Key", "client-secret")
	request.Header.Set("Thread-Id", "thread-new")
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "operation-claim", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	if _, err := operation.PrepareAttempt(context.Background(), upstream.Clone(context.Background()), candidate, wrongApplied); !IsKind(err, ErrorIdentityMismatch) {
		t.Fatalf("mismatched AppliedIdentity error = %v", err)
	}
	if len(continuity.claimCalls) != 0 {
		t.Fatal("request evidence was claimed before AppliedIdentity validation")
	}
	attempt, err := operation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if len(continuity.claimCalls) != 1 || continuity.commitCalls != 0 {
		t.Fatalf("claims/commits before disclosure = %d/%d", len(continuity.claimCalls), continuity.commitCalls)
	}
	if err := attempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if continuity.commitCalls != 1 {
		t.Fatalf("commits after disclosure = %d", continuity.commitCalls)
	}
}

func TestRuntimeBindsResponseStateOnlyAtVisibleBoundary(t *testing.T) {
	clientScope := testClientScope(t, "client")
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject-a")
	continuity := &continuityRecorder{
		resolveErr:  &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown},
		validateErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown},
	}
	runtime := New(Config{
		Features:     FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{Continuity: true} }),
		ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}},
		Continuity:   continuity,
	})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("Thread-Id", "request-anchor")
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "operation-response", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	attempt, err := operation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if len(continuity.prepareCalls) != 0 {
		t.Fatal("response state prepared before a final visibility boundary")
	}
	headers := make(http.Header)
	headers.Set("X-Codex-Turn-State", "turn-final")
	visibility, err := attempt.PrepareVisible(context.Background(), headers)
	if err != nil {
		t.Fatal(err)
	}
	if len(continuity.prepareCalls) != 1 || continuity.commitCalls != 0 {
		t.Fatalf("prepare/commit calls = %d/%d", len(continuity.prepareCalls), continuity.commitCalls)
	}
	if err := visibility.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if continuity.commitCalls != 1 {
		t.Fatalf("visible commit calls = %d", continuity.commitCalls)
	}
}

func TestPendingOwnerRetryFinalizesAtHTTPBoundaries(t *testing.T) {
	clientScope := testClientScope(t, "client")
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject-a")
	continuity := &continuityRecorder{resolveBinding: codexcontinuity.Binding{
		Lifecycle: codexcontinuity.LifecyclePending,
		Owner: codexcontinuity.Owner{
			ClientScope: clientScope, ProtocolScope: candidate.ProtocolScope(), RouteTargetHint: "route-a",
		},
	}}
	runtime := New(Config{
		Features:     FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{Continuity: true} }),
		ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}},
		Continuity:   continuity,
	})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("Session-Id", "session-pending")
	request.Header.Set("X-Codex-Turn-Metadata", "metadata-pending")
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "operation-pending-request", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	attempt, err := operation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if len(continuity.acquireCalls) != 2 || continuity.commitCalls != 0 {
		t.Fatalf("request acquire/commit calls = %d/%d", len(continuity.acquireCalls), continuity.commitCalls)
	}
	if err := attempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if continuity.commitCalls != 2 {
		t.Fatalf("request commits after physical attempt = %d", continuity.commitCalls)
	}

	continuity.acquireCalls = nil
	continuity.commitCalls = 0
	visible, err := attempt.PrepareVisible(context.Background(), http.Header{
		"X-Codex-Turn-State": {"turn-pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	const finalizers = 8
	var group sync.WaitGroup
	errors := make(chan error, finalizers)
	group.Add(finalizers)
	for range finalizers {
		go func() {
			defer group.Done()
			errors <- visible.Commit(context.Background())
		}()
	}
	group.Wait()
	close(errors)
	for commitErr := range errors {
		if commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	if len(continuity.acquireCalls) != 1 || continuity.commitCalls != 1 {
		t.Fatalf("Turn State acquire/commit calls = %d/%d", len(continuity.acquireCalls), continuity.commitCalls)
	}

	continuity.acquireCalls = nil
	continuity.commitCalls = 0
	gate := attempt.NewSSEGate()
	gate.Append([]byte("data: {\"type\":\"response.metadata\",\"response_id\":\"response-pending\"}\n\n"))
	event, ready, err := gate.PrepareNext(context.Background(), false)
	if err != nil || !ready {
		t.Fatalf("prepare response reference = ready:%v err:%v", ready, err)
	}
	if err := event.Visibility().Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(continuity.acquireCalls) != 1 || continuity.commitCalls != 1 {
		t.Fatalf("response ref acquire/commit calls = %d/%d", len(continuity.acquireCalls), continuity.commitCalls)
	}
}

func TestRuntimeFailClosedInputAndDependencyErrors(t *testing.T) {
	feature := FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{Continuity: true} })
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	if _, err := New(Config{Features: feature}).Begin(context.Background(), request, codexAPIType, "operation", nil, nil); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("missing digester error = %v", err)
	}
	clientScope := testClientScope(t, "client")
	runtime := New(Config{Features: feature, ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}}, Continuity: &continuityRecorder{}})
	request.Header.Set("Authorization", "Bearer one")
	request.Header.Set("X-Api-Key", "two")
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "operation-stateless", nil, nil); err != nil {
		t.Fatalf("stateless ambiguous credential was consumed: %v", err)
	}
	request.Header.Set("Thread-Id", "state-requires-scope")
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "operation", nil, nil); !IsKind(err, ErrorClientInput) {
		t.Fatalf("stateful ambiguous credential error = %v", err)
	}
	request.Header.Del("Thread-Id")
	request.Header.Del("X-Api-Key")
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "operation", []byte("{"), []byte("{")); !IsKind(err, ErrorClientInput) {
		t.Fatalf("malformed fixed projection error = %v", err)
	}
	if !IsKind(identityError("test", errors.New("mismatch")), ErrorIdentityMismatch) || IsKind(nil, ErrorIdentityMismatch) {
		t.Fatal("typed error classification failed")
	}
}

func TestRuntimeConsumesClientCredentialOnlyForStatefulOperations(t *testing.T) {
	continuity := &continuityRecorder{}
	statelessRuntime := New(Config{
		Features:   FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{Continuity: true} }),
		Continuity: continuity,
	})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	operation, err := statelessRuntime.Begin(context.Background(), request, codexAPIType, "stateless-no-key", nil, nil)
	if err != nil || operation.hasClientScope {
		t.Fatalf("stateless no-key operation = scope:%t err:%v", operation.hasClientScope, err)
	}
	request.Header.Set("Authorization", "Bearer one")
	request.Header.Set("X-Api-Key", "two")
	operation, err = statelessRuntime.Begin(context.Background(), request, codexAPIType, "stateless-unconsumed-key", nil, nil)
	if err != nil || operation.hasClientScope {
		t.Fatalf("stateless credential was consumed = scope:%t err:%v", operation.hasClientScope, err)
	}

	scope := testClientScope(t, "stateful-client")
	statefulRuntime := New(Config{
		Features:     FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{Continuity: true} }),
		ClientScopes: testScopeDigester{current: scope, candidates: []codexidentity.ClientScope{scope}},
		Continuity:   continuity,
	})
	stateful := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	stateful.Header.Set("Thread-Id", "thread-without-client-key")
	if _, err := statefulRuntime.Begin(context.Background(), stateful, codexAPIType, "stateful-no-key", nil, nil); !IsKind(err, ErrorClientInput) {
		t.Fatalf("state evidence without client key error = %v", err)
	}
	cookieRuntime := New(Config{
		Features:     FeatureSourceFunc(func() codexstartup.Snapshot { return codexstartup.Snapshot{ProviderCookieJar: true} }),
		ClientScopes: testScopeDigester{current: scope, candidates: []codexidentity.ClientScope{scope}},
	})
	if _, err := cookieRuntime.Begin(context.Background(), httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil), codexAPIType, "cookie-no-key", nil, nil); !IsKind(err, ErrorClientInput) {
		t.Fatalf("Cookie operation without client key error = %v", err)
	}

	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject-a")
	attempt, err := operation.PrepareAttempt(context.Background(), httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil), candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attempt.PrepareVisible(context.Background(), http.Header{"X-Codex-Turn-State": {"late-state"}}); !IsKind(err, ErrorClientInput) {
		t.Fatalf("late response evidence without ClientScope error = %v", err)
	}
}

func TestOperationCookiePolicyIsRequestScoped(t *testing.T) {
	operation := &Operation{features: codexstartup.Snapshot{ProviderCookieJar: true}}
	if policy := operation.RequestPolicy(); policy.Cookies != upstreamtransport.ServerManagedCookies {
		t.Fatalf("Cookie request policy = %#v", policy)
	}
	if err := (&Attempt{}).ObserveResponse(&upstreamtransport.ResponseHead{}); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderHygieneUsesExplicitFeatureSnapshot(t *testing.T) {
	features := codexstartup.Snapshot{}
	runtime := New(Config{Features: FeatureSourceFunc(func() codexstartup.Snapshot { return features })})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "hygiene-off", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy := operation.RequestPolicy(); policy.Headers != upstreamtransport.PreserveClientHeaders {
		t.Fatalf("hygiene-off policy = %#v", policy)
	}
	features.UpstreamHeaderHygiene = true
	if policy := operation.RequestPolicy(); policy.Headers != upstreamtransport.PreserveClientHeaders {
		t.Fatalf("captured hygiene-off policy changed = %#v", policy)
	}
	operation, err = runtime.Begin(context.Background(), request, codexAPIType, "hygiene-on", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy := operation.RequestPolicy(); policy.Headers != upstreamtransport.SanitizeProviderHeaders {
		t.Fatalf("hygiene-on policy = %#v", policy)
	}
	features.UpstreamHeaderHygiene = false
	if policy := operation.RequestPolicy(); policy.Headers != upstreamtransport.SanitizeProviderHeaders {
		t.Fatalf("captured hygiene-on policy changed = %#v", policy)
	}
	features.UpstreamHeaderHygiene = true
	nonCodex, err := runtime.Begin(context.Background(), request, "claude", "non-codex", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy := nonCodex.RequestPolicy(); policy.Headers != upstreamtransport.PreserveClientHeaders {
		t.Fatalf("non-Codex policy = %#v", policy)
	}
}

func testClientScope(t *testing.T, label string) codexidentity.ClientScope {
	t.Helper()
	sum := sha256.Sum256([]byte(label))
	scope, err := codexidentity.ClientScopeFromDigest("test-v1", sum)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testCandidate(t *testing.T, routeTarget, host, subjectLabel string) (codexidentity.CandidateSnapshot, codexidentity.AppliedIdentity) {
	t.Helper()
	digest := sha256.Sum256([]byte(subjectLabel))
	subject, err := credentialsession.KeyedDigestSubject("test-v1", digest[:])
	if err != nil {
		t.Fatal(err)
	}
	finalURL := &url.URL{Scheme: "https", Host: host, Path: "/v1/responses"}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(credentialsession.RouteSnapshot{
		RouteTargetID: routeTarget,
		APIType:       codexAPIType,
		Credential: credentialsession.Snapshot{
			SessionID: routeTarget + "-session", Vendor: "openai", Kind: credentialsession.KindAPIKey,
			SecretData: "provider-secret", Version: 1, Subject: subject,
			AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
		},
	}, codexAPIType, finalURL)
	if err != nil {
		t.Fatal(err)
	}
	identitySubject, err := codexidentity.CredentialSubjectFromSession(subject)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := codexidentity.AppliedIdentityFromRequest("openai", finalURL, identitySubject)
	if err != nil {
		t.Fatal(err)
	}
	return candidate, applied
}
