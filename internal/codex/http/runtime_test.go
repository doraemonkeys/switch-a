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

func TestRuntimeResolvesOwnerBeforeSelectionAndValidatesAppliedIdentity(t *testing.T) {
	clientScope := testClientScope(t, "client")
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject-a")
	continuity := &continuityRecorder{resolveBinding: codexcontinuity.Binding{Owner: codexcontinuity.Owner{
		ClientScope: clientScope, ProtocolScope: candidate.ProtocolScope(), RouteTargetHint: "route-a",
	}}}
	runtime := newAlwaysOnTestRuntime(t, Config{
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
	runtime := newAlwaysOnTestRuntime(t, Config{
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
	runtime := newAlwaysOnTestRuntime(t, Config{
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
	runtime := newAlwaysOnTestRuntime(t, Config{
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
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	if _, err := New(Config{}); err == nil {
		t.Fatal("constructor accepted missing mandatory dependencies")
	}
	clientScope := testClientScope(t, "client")
	runtime := newAlwaysOnTestRuntime(t, Config{ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}}, Continuity: &continuityRecorder{}})
	request.Header.Set("Authorization", "Bearer one")
	request.Header.Set("X-Api-Key", "two")
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "operation", nil, nil); !IsKind(err, ErrorClientInput) {
		t.Fatalf("always-on ambiguous credential error = %v", err)
	}
	request.Header.Del("X-Api-Key")
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "opaque-json", []byte("{"), []byte("{")); err != nil {
		t.Fatalf("non-JSON request body was not opaque: %v", err)
	}
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "decode-failure", []byte{0x1f, 0x8b}, nil); err != nil {
		t.Fatalf("semantic decode failure was not opaque: %v", err)
	}
	invalidKnown := []byte(`{"type":"response.create","previous_response_id":null}`)
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "recognized-invalid", invalidKnown, invalidKnown); !IsKind(err, ErrorClientInput) {
		t.Fatalf("malformed recognized projection error = %v", err)
	}
	if !IsKind(identityError("test", errors.New("mismatch")), ErrorIdentityMismatch) || IsKind(nil, ErrorIdentityMismatch) {
		t.Fatal("typed error classification failed")
	}
}

func TestOperationCookiePolicyIsRequestScoped(t *testing.T) {
	operation := &Operation{apiType: codexAPIType}
	if policy := operation.RequestPolicy(); policy.Cookies != upstreamtransport.ServerManagedCookies {
		t.Fatalf("Cookie request policy = %#v", policy)
	}
	if err := (&Attempt{}).ObserveResponse(&upstreamtransport.ResponseHead{}); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderHygieneIsAlwaysOnOnlyForCodex(t *testing.T) {
	operation := &Operation{apiType: codexAPIType}
	if policy := operation.RequestPolicy(); policy.Headers != upstreamtransport.SanitizeProviderHeaders || policy.Cookies != upstreamtransport.ServerManagedCookies {
		t.Fatalf("Codex policy = %#v", policy)
	}
	nonCodex, err := (*Runtime)(nil).Begin(context.Background(), nil, "claude", "non-codex", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy := nonCodex.RequestPolicy(); policy.Headers != upstreamtransport.PreserveClientHeaders {
		t.Fatalf("non-Codex policy = %#v", policy)
	}
}

func newAlwaysOnTestRuntime(t *testing.T, config Config) *Runtime {
	t.Helper()
	if config.ClientScopes == nil {
		scope := testClientScope(t, "default-client")
		config.ClientScopes = testScopeDigester{current: scope, candidates: []codexidentity.ClientScope{scope}}
	}
	if config.Continuity == nil {
		config.Continuity = &continuityRecorder{}
	}
	if config.ProviderCookies == nil {
		config.ProviderCookies = newCookieTestService(t, newCookieTestRepository())
	}
	if config.ExternalScheme == nil {
		config.ExternalScheme = NewTrustedProxySchemeResolver(nil)
	}
	runtime, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
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
