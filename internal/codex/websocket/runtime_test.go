package codexws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

func TestRuntimeRequiresDependenciesAndRejectsInvalidBegin(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("constructor accepted missing mandatory dependencies")
	}
	runtime := testRuntime(t, nil)
	if _, err := runtime.Begin(context.Background(), nil, codexAPIType, "missing-request"); Classify(err) != FailureProtocol {
		t.Fatalf("missing request class = %q, err=%v", Classify(err), err)
	}
	request := testRequest("client-secret")
	if _, err := runtime.Begin(context.Background(), request, "claude", "non-codex"); Classify(err) != FailureProtocol {
		t.Fatalf("non-Codex begin class = %q, err=%v", Classify(err), err)
	}

	ambiguous := testRequest("first")
	ambiguous.Header.Set("X-Api-Key", "second")
	ambiguous.Header.Set("Thread-Id", "thread-a")
	if _, err := runtime.Begin(context.Background(), ambiguous, codexAPIType, "ambiguous"); Classify(err) != FailureIdentity {
		t.Fatalf("ambiguous credential class = %q, err=%v", Classify(err), err)
	}
}

func TestOperationUsesExistingOwnerBeforeSelection(t *testing.T) {
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	request := testRequest("client-a")
	client := testClientScope(t, "client-a")
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	claimFixtureEvidence(t, service, client, candidate, "seed", http.Header{"Thread-Id": {"thread-a"}})
	request.Header.Set("Thread-Id", "thread-a")

	op, err := runtime.Begin(context.Background(), request, codexAPIType, "request-existing")
	if err != nil {
		t.Fatal(err)
	}
	authority, route := op.RequiredAuthority()
	if authority == nil || !authority.Equal(candidate.Authority()) || route != candidate.RouteTargetID() {
		t.Fatalf("required route = (%v, %q)", authority, route)
	}
	if op.NeedsOwnerBootstrap() {
		t.Fatal("existing header owner still requested bootstrap")
	}
	otherAuthority, otherApplied := testCandidate(t, "route-other", "https://other.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), otherAuthority, otherApplied, mustURL(t, "wss://other.example.test/v1")); Classify(err) != FailureIdentity {
		t.Fatalf("existing owner allowed cross-authority route: class=%q err=%v", Classify(err), err)
	}
	sameScopeRoute, sameScopeApplied := testCandidate(t, "route-b", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), sameScopeRoute, sameScopeApplied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal("existing owner rejected same-ProtocolScope replacement:", err)
	}
	headers := make(http.Header)
	permit, err := op.PrepareDial(context.Background(), headers, candidate, applied, mustURL(t, "wss://api.example.test/v1"))
	if err != nil || permit == nil {
		t.Fatalf("PrepareDial = (%v, %v)", permit, err)
	}
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatal("idempotent permit commit failed:", err)
	}
}

func TestOperationResolvesCommittedTurnStateBeforeOwnerPolicy(t *testing.T) {
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	client := testClientScope(t, "client-a")
	candidate, _ := testCandidate(t, "route-a", "https://api.example.test/v1")
	seedHeaders := http.Header{"X-Codex-Turn-State": {"turn-from-http"}}
	seedDecision := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers: seedHeaders,
		Owners:  func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerCurrent },
	})
	if len(seedDecision.Decisions()) != 1 {
		t.Fatalf("turn-state seed decisions = %#v", seedDecision.Decisions())
	}
	lease, err := service.PrepareVisible(context.Background(), codexcontinuity.ClaimRequest{
		Evidence: evidence(seedDecision.Decisions()[0].Candidate()),
		Scope: codexcontinuity.Scope{
			CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client},
			ProtocolScope: candidate.ProtocolScope(), RouteTargetHint: candidate.RouteTargetID(),
		},
		OperationID: "http-visible",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(context.Background(), lease); err != nil {
		t.Fatal(err)
	}

	request := testRequest("client-a")
	request.Header.Set("X-Codex-Turn-State", "turn-from-http")
	op, err := runtime.Begin(context.Background(), request, codexAPIType, "ws-followup")
	if err != nil {
		t.Fatal(err)
	}
	authority, route := op.RequiredAuthority()
	if authority == nil || !authority.Equal(candidate.Authority()) || route != candidate.RouteTargetID() {
		t.Fatalf("cross-protocol route = (%v, %q)", authority, route)
	}
}

func TestFrameClaimsValidationAndConnectionGeneration(t *testing.T) {
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "frame-flow")
	if err != nil {
		t.Fatal(err)
	}
	if op.NeedsOwnerBootstrap() {
		t.Fatal("owner-free request demanded a state bootstrap")
	}
	warmup := []byte(`{"type":"response.create","client_metadata":{"session_id":"session-a","thread_id":"thread-a","x-codex-window-id":"window-a","x-codex-turn-metadata":"metadata-a"}}`)
	if err := op.InspectBootstrapFrame(context.Background(), true, warmup); err != nil {
		t.Fatal(err)
	}
	if err := op.InspectBootstrapFrame(context.Background(), false, warmup); err != nil {
		t.Fatalf("opaque binary bootstrap failed: %v", err)
	}

	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	headers := make(http.Header)
	if _, err := op.PrepareDial(context.Background(), headers, candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if err := op.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	permit, err := op.PrepareClientFrame(context.Background(), true, warmup)
	if err != nil || permit == nil || len(permit.leases) != 4 {
		t.Fatalf("client frame permit leases=%d err=%v", len(permit.leases), err)
	}
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	second := []byte(`{"type":"response.create","client_metadata":{"session_id":"session-a","thread_id":"thread-a","x-codex-window-id":"window-a","x-codex-turn-metadata":"metadata-a"}}`)
	if permit, err := op.PrepareClientFrame(context.Background(), true, second); err != nil || len(permit.leases) != 4 {
		t.Fatalf("owned frame permit=%#v err=%v", permit, err)
	} else if err := permit.Commit(context.Background()); err != nil {
		t.Fatal("idempotent owner finalization failed:", err)
	}
	if permit, err := op.PrepareClientFrame(context.Background(), true, []byte(`{"type":"response.inject"}`)); err != nil || permit == nil || len(permit.leases) != 0 {
		t.Fatalf("opaque inject permit=%#v err=%v", permit, err)
	}
	previousPermit, err := op.PrepareClientFrame(context.Background(), true, []byte(`{"type":"response.create","previous_response_id":"unknown"}`))
	if err != nil || previousPermit == nil || len(previousPermit.leases) != 1 {
		t.Fatalf("anchored previous permit=%#v err=%v", previousPermit, err)
	}
	if err := previousPermit.Commit(context.Background()); err != nil {
		t.Fatal("adopt previous response:", err)
	}
	if permit, err := op.PrepareClientFrame(context.Background(), false, []byte("opaque")); err != nil || permit != nil {
		t.Fatalf("binary frame permit=%#v err=%v", permit, err)
	}

	if err := op.OpenConnection(); Classify(err) != FailureIdentity {
		t.Fatalf("second generation class=%q err=%v", Classify(err), err)
	}
	op.CloseConnection()
	op.CloseConnection()
}

func TestPendingOwnerRetryFinalizesAtWebSocketWriteBoundaries(t *testing.T) {
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	finalURL := mustURL(t, "wss://api.example.test/v1")

	t.Run("client metadata and session identity", func(t *testing.T) {
		service, store := newTestContinuityFixture(t)
		runtime := testRuntime(t, service)
		payload := []byte(`{"type":"response.create","client_metadata":{"session_id":"session-pending","x-codex-turn-metadata":"metadata-pending"}}`)

		first, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "ws-request-uncertain")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := first.PrepareDial(context.Background(), make(http.Header), candidate, applied, finalURL); err != nil {
			t.Fatal(err)
		}
		uncertain, err := first.PrepareClientFrame(context.Background(), true, payload)
		if err != nil || len(uncertain.leases) != 2 {
			t.Fatalf("uncertain request permit leases=%d err=%v", len(uncertain.leases), err)
		}

		retry, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "ws-request-retry")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := retry.PrepareDial(context.Background(), make(http.Header), candidate, applied, finalURL); err != nil {
			t.Fatal(err)
		}
		finalize, err := retry.PrepareClientFrame(context.Background(), true, payload)
		if err != nil || len(finalize.leases) != 2 {
			t.Fatalf("retry request permit leases=%d err=%v", len(finalize.leases), err)
		}
		for index, lease := range finalize.leases {
			if lease.NewlyClaimed() || lease.Binding().ClaimOperationID != uncertain.leases[index].Binding().ClaimOperationID {
				t.Fatalf("retry lease[%d] = %#v", index, lease)
			}
		}
		if err := finalize.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertStoreLifecycle(t, store, 2, codexcontinuity.LifecycleCommitted)
	})

	t.Run("handshake Turn State", func(t *testing.T) {
		service, store := newTestContinuityFixture(t)
		runtime := testRuntime(t, service)
		headers := http.Header{"X-Codex-Turn-State": {"turn-pending"}}

		first, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "ws-state-uncertain")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := first.PrepareDial(context.Background(), make(http.Header), candidate, applied, finalURL); err != nil {
			t.Fatal(err)
		}
		uncertain, _, err := first.PrepareServerHeaders(context.Background(), headers)
		if err != nil || len(uncertain.leases) != 1 {
			t.Fatalf("uncertain handshake permit=%#v err=%v", uncertain, err)
		}

		retry, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "ws-state-retry")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := retry.PrepareDial(context.Background(), make(http.Header), candidate, applied, finalURL); err != nil {
			t.Fatal(err)
		}
		finalize, _, err := retry.PrepareServerHeaders(context.Background(), headers)
		if err != nil || len(finalize.leases) != 1 || finalize.leases[0].NewlyClaimed() {
			t.Fatalf("retry handshake permit=%#v err=%v", finalize, err)
		}
		if finalize.leases[0].Binding().ClaimOperationID != uncertain.leases[0].Binding().ClaimOperationID {
			t.Fatal("handshake retry replaced ClaimOperationID")
		}
		if err := finalize.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertStoreLifecycle(t, store, 1, codexcontinuity.LifecycleCommitted)
	})

	t.Run("response reference frame", func(t *testing.T) {
		service, store := newTestContinuityFixture(t)
		runtime := testRuntime(t, service)
		payload := []byte(`{"type":"response.created","response":{"id":"response-pending"}}`)

		first, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "ws-response-uncertain")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := first.PrepareDial(context.Background(), make(http.Header), candidate, applied, finalURL); err != nil {
			t.Fatal(err)
		}
		uncertain, err := first.PrepareServerFrame(context.Background(), true, payload)
		if err != nil || len(uncertain.leases) != 1 {
			t.Fatalf("uncertain response permit=%#v err=%v", uncertain, err)
		}

		retry, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "ws-response-retry")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := retry.PrepareDial(context.Background(), make(http.Header), candidate, applied, finalURL); err != nil {
			t.Fatal(err)
		}
		if err := retry.OpenConnection(); err != nil {
			t.Fatal(err)
		}
		defer retry.CloseConnection()
		finalize, err := retry.PrepareServerFrame(context.Background(), true, payload)
		if err != nil || len(finalize.leases) != 1 || finalize.leases[0].NewlyClaimed() {
			t.Fatalf("retry response permit=%#v err=%v", finalize, err)
		}
		if finalize.leases[0].Binding().ClaimOperationID != uncertain.leases[0].Binding().ClaimOperationID {
			t.Fatal("response retry replaced ClaimOperationID")
		}
		if err := finalize.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertStoreLifecycle(t, store, 1, codexcontinuity.LifecycleCommitted)
	})
}

func TestServerVisibilityAndAppliedIdentityBoundaries(t *testing.T) {
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "server-flow")
	if err != nil {
		t.Fatal(err)
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}

	handshake := http.Header{
		"X-Codex-Turn-State":    {"turn-a"},
		"X-Codex-Turn-Metadata": {"must-drop"},
		"X-Oai-Attestation":     {"must-drop"},
		"Set-Cookie":            {"provider=secret"},
	}
	permit, projected, err := op.PrepareServerHeaders(context.Background(), handshake)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Get("X-Codex-Turn-State") != "turn-a" || len(projected) != 1 {
		t.Fatalf("projected headers = %#v", projected)
	}
	if authority, route := op.RequiredAuthority(); authority != nil || route != "" {
		t.Fatalf("Turn State pinned before the projected 101 committed: (%v, %q)", authority, route)
	}
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	frame := []byte(`{"type":"codex.response.metadata","headers":{"x-codex-turn-state":"turn-b"}}`)
	framePermit, err := op.PrepareServerFrame(context.Background(), true, frame)
	if err != nil || framePermit == nil {
		t.Fatalf("server frame permit=%#v err=%v", framePermit, err)
	}
	if err := framePermit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if permit, err := op.PrepareServerFrame(context.Background(), false, frame); err != nil || permit != nil {
		t.Fatalf("binary server permit=%#v err=%v", permit, err)
	}

	otherCandidate, otherApplied := testCandidate(t, "route-b", "https://other.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), otherCandidate, otherApplied, mustURL(t, "wss://other.example.test/v1")); Classify(err) != FailureIdentity {
		t.Fatalf("cross-authority replacement class=%q err=%v", Classify(err), err)
	}
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, otherApplied, mustURL(t, "wss://api.example.test/v1")); Classify(err) != FailureIdentity {
		t.Fatalf("applied mismatch class=%q err=%v", Classify(err), err)
	}
}

func TestCookieOverlayIsSelectedOnRedialAndCommittedAtVisibility(t *testing.T) {
	cookieRepository := &testCookieRepository{}
	cookieService := newTestCookieService(t, cookieRepository)
	runtime := newTestRuntime(t, Config{
		ClientScopes: testClientDigester{}, Continuity: newTestContinuity(t),
		ProviderCookies: cookieService,
		ExternalScheme:  testSchemeResolver("https"),
	})
	request := testRequest("client-a")
	op, err := runtime.Begin(context.Background(), request, codexAPIType, "cookie-flow")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(op.GatewaySetCookie(), providercookie.GatewayHandleName+"=") || !strings.Contains(op.GatewaySetCookie(), "Secure") {
		t.Fatalf("gateway Set-Cookie = %q", op.GatewaySetCookie())
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	first := http.Header{"Cookie": {"client=must-not-pass"}}
	if _, err := op.PrepareDial(context.Background(), first, candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if first.Get("Cookie") != "" {
		t.Fatalf("raw Cookie crossed boundary: %q", first.Get("Cookie"))
	}
	if err := op.ApplyHandshake(mustURL(t, "wss://api.example.test/v1"), http.Header{"Set-Cookie": {"provider=overlay; Path=/; Secure"}}); err != nil {
		t.Fatal(err)
	}
	second := make(http.Header)
	if _, err := op.PrepareDial(context.Background(), second, candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if second.Get("Cookie") != "" {
		t.Fatalf("redial Cookie = %q", second.Get("Cookie"))
	}
	if err := op.ApplyHandshake(mustURL(t, "wss://api.example.test/v1"), http.Header{"Set-Cookie": {"provider=selected; Path=/; Secure"}}); err != nil {
		t.Fatal(err)
	}
	if err := op.CommitVisibility(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := op.CommitVisibility(context.Background()); err != nil {
		t.Fatal("idempotent cookie commit failed:", err)
	}
	if cookieRepository.merges != 1 {
		t.Fatalf("cookie merges = %d", cookieRepository.merges)
	}
	op.DiscardCookies()
}

func TestFailureClassificationAndCredentialParsing(t *testing.T) {
	for _, test := range []struct {
		headers http.Header
		state   clientcredential.State
	}{
		{http.Header{}, clientcredential.StateAbsent},
		{http.Header{"Authorization": {"Basic value"}}, clientcredential.StateInvalid},
		{http.Header{"Authorization": {"Bearer one", "Bearer two"}}, clientcredential.StateInvalid},
		{http.Header{"X-Api-Key": {"one", "two"}}, clientcredential.StateInvalid},
		{http.Header{"Authorization": {"Bearer same"}, "X-Api-Key": {"same"}}, clientcredential.StateSingle},
	} {
		observed := clientcredential.Extract(map[string][]string(test.headers))
		if observed.State != test.state {
			t.Fatalf("credential state = %q, want %q", observed.State, test.state)
		}
		observed.Clear()
	}
	base := errors.New("base")
	failure := &Failure{Class: FailureStorage, Stage: "test", Cause: base}
	if Classify(failure) != FailureStorage || !errors.Is(failure, base) || failure.Error() == "" {
		t.Fatalf("failure contract = (%q, %v)", Classify(failure), failure)
	}
	if Classify(base) != "" || Classify((*Failure)(nil)) != "" {
		t.Fatal("untyped failure was classified")
	}
}

func TestBoundaryFailuresRemainTypedAndLocal(t *testing.T) {
	service, continuityStore := newTestContinuityFixture(t)
	runtime := testRuntime(t, service)
	request := testRequest("client-a")
	request.Header.Set("Thread-Id", "thread-a")
	continuityStore.lookupErr = errors.New("lookup unavailable")
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "lookup-failure"); Classify(err) != FailureStorage {
		t.Fatalf("lookup failure class=%q err=%v", Classify(err), err)
	}
	continuityStore.lookupErr = nil

	op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "claim-failure")
	if err != nil {
		t.Fatal(err)
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	continuityStore.failClaimAt = 2
	frame := []byte(`{"type":"response.create","client_metadata":{"session_id":"session-a","thread_id":"thread-a"}}`)
	if _, err := op.PrepareClientFrame(context.Background(), true, frame); Classify(err) != FailureStorage {
		t.Fatalf("claim failure class=%q err=%v", Classify(err), err)
	}
	if len(continuityStore.bindings) != 0 {
		t.Fatal("locally prepared claim was not abandoned")
	}

	continuityStore.failClaimAt = 0
	continuityStore.claimCount = 0
	permit, err := op.PrepareClientFrame(context.Background(), true, frame)
	if err != nil {
		t.Fatal(err)
	}
	continuityStore.commitErr = errors.New("commit unavailable")
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatalf("commit provenance fallback = %v", err)
	}

	var nilOperation *Operation
	if _, err := nilOperation.PrepareDial(context.Background(), nil, candidate, applied, nil); Classify(err) != FailureStorage {
		t.Fatalf("nil operation class=%q err=%v", Classify(err), err)
	}
	if nilOperation.GatewaySetCookie() != "" {
		t.Fatal("nil operation accessors were not zero-valued")
	}
	nilOperation.DiscardCookies()
	nilOperation.CloseConnection()
}

func TestResponseActivationRequiresCurrentGeneration(t *testing.T) {
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "activation")
	if err != nil {
		t.Fatal(err)
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	newResponseLease := func(input string) codexcontinuity.Lease {
		lease, err := service.PrepareVisible(context.Background(), codexcontinuity.ClaimRequest{
			Evidence: codexcontinuity.Evidence{Kind: codexcontinuity.KindResponseReference, DigestInput: []byte(input)},
			Scope: codexcontinuity.Scope{
				CurrentClientScope:    testClientScope(t, "client-a"),
				ClientScopeCandidates: []codexidentity.ClientScope{testClientScope(t, "client-a")},
				ProtocolScope:         candidate.ProtocolScope(), RouteTargetHint: candidate.RouteTargetID(),
			},
			OperationID: "activation",
		})
		if err != nil {
			t.Fatal(err)
		}
		return lease
	}
	lease := newResponseLease("response-reference")
	withoutGeneration := &Permit{operation: op, leases: []codexcontinuity.Lease{lease}, activate: []codexcontinuity.Lease{lease}}
	if err := withoutGeneration.Commit(context.Background()); Classify(err) != FailureIdentity {
		t.Fatalf("activation without generation class=%q err=%v", Classify(err), err)
	}
	responseLease := newResponseLease("response-reference-2")
	if err := op.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	withGeneration := &Permit{operation: op, leases: []codexcontinuity.Lease{responseLease}, activate: []codexcontinuity.Lease{responseLease}}
	if err := withGeneration.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsLease(withGeneration.activate, responseLease) || containsLease(nil, responseLease) {
		t.Fatal("response activation lease lookup is inconsistent")
	}
	op.CloseConnection()
}

func TestCookieFailureAndDiscardPaths(t *testing.T) {
	repository := &testCookieRepository{}
	service := newTestCookieService(t, repository)
	base := Config{ClientScopes: testClientDigester{}, Continuity: newTestContinuity(t), ExternalScheme: testSchemeResolver("https")}
	if _, err := New(base); err == nil {
		t.Fatal("missing cookie dependency was accepted")
	}
	base.ProviderCookies = service
	base.ExternalScheme = testSchemeResolver("invalid")
	if _, err := newTestRuntime(t, base).Begin(context.Background(), testRequest("client-a"), codexAPIType, "bad-scheme"); Classify(err) != FailureStorage {
		t.Fatalf("external scheme class=%q err=%v", Classify(err), err)
	}
	base.ExternalScheme = testSchemeResolver("http")
	op, err := newTestRuntime(t, base).Begin(context.Background(), testRequest("client-a"), codexAPIType, "cookie-discard")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(op.GatewaySetCookie(), "Secure") {
		t.Fatalf("HTTP gateway cookie was Secure: %q", op.GatewaySetCookie())
	}
	if err := op.ApplyHandshake(mustURL(t, "ws://api.example.test/v1"), nil); Classify(err) != FailureStorage {
		t.Fatalf("handshake without attempt class=%q err=%v", Classify(err), err)
	}
	op.DiscardCookies()
	op.DiscardCookies()
	if err := op.CommitCookies(context.Background()); err != nil {
		t.Fatal("closed cookie commit should be idempotent:", err)
	}

	failingRepository := &testCookieRepository{loadErr: errors.New("load failed")}
	base.ProviderCookies = newTestCookieService(t, failingRepository)
	op, err = newTestRuntime(t, base).Begin(context.Background(), testRequest("client-a"), codexAPIType, "cookie-load")
	if err != nil {
		t.Fatal(err)
	}
	candidate, applied := testCandidate(t, "route-a", "http://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "ws://api.example.test/v1")); Classify(err) != FailureStorage {
		t.Fatalf("cookie select class=%q err=%v", Classify(err), err)
	}

	mergeRepository := &testCookieRepository{mergeErr: errors.New("merge failed")}
	base.ProviderCookies = newTestCookieService(t, mergeRepository)
	op, err = newTestRuntime(t, base).Begin(context.Background(), testRequest("client-a"), codexAPIType, "cookie-merge")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "ws://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if err := op.CommitCookies(context.Background()); Classify(err) != FailureStorage {
		t.Fatalf("cookie merge class=%q err=%v", Classify(err), err)
	}
}

func TestMultipleOwnersAndGatewayHandleStayFailClosed(t *testing.T) {
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	client := testClientScope(t, "client-a")
	first, _ := testCandidate(t, "route-a", "https://api.example.test/v1")
	second, _ := testCandidate(t, "route-b", "https://other.example.test/v1")
	claimFixtureEvidence(t, service, client, first, "seed-one", http.Header{"Thread-Id": {"thread-a"}})
	claimFixtureEvidence(t, service, client, second, "seed-two", http.Header{"Session-Id": {"session-a"}})
	request := testRequest("client-a")
	request.Header.Set("Thread-Id", "thread-a")
	request.Header.Set("Session-Id", "session-a")
	if _, err := runtime.Begin(context.Background(), request, codexAPIType, "multi-owner"); Classify(err) != FailureIdentity {
		t.Fatalf("multiple owner class=%q err=%v", Classify(err), err)
	}

	handleRequest := testRequest("client-a")
	handleRequest.Header.Add("Cookie", providercookie.GatewayHandleName+"=one")
	handleRequest.Header.Add("Cookie", providercookie.GatewayHandleName+"=two")
	if got := gatewayHandle(handleRequest); got != "invalid-multiple-handle" {
		t.Fatalf("multiple gateway handle = %q", got)
	}
	if cloneURL(nil) != nil {
		t.Fatal("nil URL clone was non-nil")
	}
}

func TestProtocolAndZeroValueEdges(t *testing.T) {
	var nilFailure *Failure
	if nilFailure.Error() == "" || nilFailure.Unwrap() != nil {
		t.Fatal("nil Failure contract is unstable")
	}
	withoutCause := &Failure{Class: FailureProtocol, Stage: "edge"}
	if withoutCause.Error() == "" {
		t.Fatal("cause-free Failure has no message")
	}
	for _, value := range []string{"Bearer", "Bearer " + strings.Repeat("x", clientcredential.MaxClientCredentialBytes+1)} {
		observed := clientcredential.Extract(map[string][]string{"Authorization": {value}})
		if observed.State != clientcredential.StateInvalid {
			t.Fatalf("invalid bearer accepted: %q", value[:min(len(value), 32)])
		}
	}
	internalWhitespace := clientcredential.Extract(map[string][]string{"Authorization": {"Bearer value trailing"}})
	defer internalWhitespace.Clear()
	if internalWhitespace.State != clientcredential.StateSingle || string(internalWhitespace.Token) != "value trailing" {
		t.Fatalf("internal whitespace result = (%q, %q)", internalWhitespace.State, internalWhitespace.Token)
	}
	if observed := clientcredential.Extract(map[string][]string{"X-Api-Key": {" "}}); observed.State != clientcredential.StateInvalid {
		t.Fatalf("blank API key state = %q", observed.State)
	}

	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	malformed := testRequest("client-a")
	malformed.Header["Thread-Id"] = []string{"one", "two"}
	if _, err := runtime.Begin(context.Background(), malformed, codexAPIType, "malformed-header"); err != nil {
		t.Fatalf("header-only malformed state should be dropped: %v", err)
	}
	stateWithoutClient := testRequest("")
	stateWithoutClient.Header.Del("Authorization")
	stateWithoutClient.Header.Set("Thread-Id", "thread-a")
	if _, err := runtime.Begin(context.Background(), stateWithoutClient, codexAPIType, "missing-client"); Classify(err) != FailureIdentity {
		t.Fatalf("state without client class=%q err=%v", Classify(err), err)
	}

	op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "protocol-edge")
	if err != nil {
		t.Fatal(err)
	}
	if err := op.OpenConnection(); Classify(err) != FailureIdentity {
		t.Fatalf("unbound connection class=%q err=%v", Classify(err), err)
	}
	if permit, err := op.PrepareClientFrame(context.Background(), true, []byte(`{`)); err != nil || permit == nil {
		t.Fatalf("opaque malformed client frame permit=%#v err=%v", permit, err)
	}
	if permit, err := op.PrepareServerFrame(context.Background(), true, []byte(`{`)); err != nil || permit == nil {
		t.Fatalf("opaque malformed server frame permit=%#v err=%v", permit, err)
	}
	if _, _, err := op.PrepareServerHeaders(context.Background(), http.Header{"X-Codex-Turn-State": {"one", "two"}}); Classify(err) != FailureProtocol {
		t.Fatalf("malformed server header class=%q err=%v", Classify(err), err)
	}
	if err := op.bindPhysicalCandidate(codexidentity.CandidateSnapshot{}); Classify(err) != FailureIdentity {
		t.Fatalf("zero candidate class=%q err=%v", Classify(err), err)
	}
	if ownerLookup(nil)(codexheaders.BindingCandidate{}) != codexheaders.OwnerUnavailable {
		t.Fatal("missing owner lookup was permissive")
	}

	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	secondRoute, secondApplied := testCandidate(t, "route-b", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), secondRoute, secondApplied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal("same-authority replacement was rejected:", err)
	}
	if err := op.ApplyHandshake(nil, nil); err != nil {
		t.Fatal("empty handshake cookie response failed:", err)
	}
	if err := op.CommitCookies(context.Background()); err != nil {
		t.Fatal("empty cookie commit failed:", err)
	}

	var nilOperation *Operation
	if permit, projected, err := nilOperation.PrepareServerHeaders(context.Background(), nil); err != nil || permit != nil || len(projected) != 0 {
		t.Fatalf("nil server header boundary = (%#v, %#v, %v)", permit, projected, err)
	}
	if permit, err := nilOperation.PrepareServerFrame(context.Background(), true, nil); err != nil || permit != nil {
		t.Fatalf("nil server frame boundary = (%#v, %v)", permit, err)
	}
	if permit, err := nilOperation.PrepareClientFrame(context.Background(), true, nil); err != nil || permit != nil {
		t.Fatalf("nil client frame boundary = (%#v, %v)", permit, err)
	}
	if err := nilOperation.OpenConnection(); err != nil {
		t.Fatal("nil operation OpenConnection failed:", err)
	}
}

type testClientDigester struct{}

func (testClientDigester) ClientScope(raw []byte) (codexidentity.ClientScope, error) {
	sum := sha256.Sum256(raw)
	return codexidentity.ClientScopeFromDigest("h1", sum)
}

func (d testClientDigester) ClientScopeCandidates(raw []byte) ([]codexidentity.ClientScope, error) {
	scope, err := d.ClientScope(raw)
	return []codexidentity.ClientScope{scope}, err
}

func testClientScope(t *testing.T, raw string) codexidentity.ClientScope {
	t.Helper()
	scope, err := (testClientDigester{}).ClientScope([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testRequest(secret string) *http.Request {
	request, _ := http.NewRequest(http.MethodGet, "https://gateway.example.test/responses", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	return request
}

func testCandidate(t *testing.T, routeTarget, target string) (codexidentity.CandidateSnapshot, codexidentity.AppliedIdentity) {
	t.Helper()
	subject, err := credentialsession.KeyedDigestSubject("h1", bytes.Repeat([]byte{7}, sha256.Size))
	if err != nil {
		t.Fatal(err)
	}
	credential := credentialsession.Snapshot{
		SessionID: "credential-" + routeTarget, Vendor: "openai", Kind: credentialsession.KindAPIKey,
		SecretData: "secret", Version: 1, Subject: subject,
	}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(credentialsession.RouteSnapshot{
		RouteTargetID: routeTarget, APIType: codexAPIType, Credential: credential,
	}, codexAPIType, mustURL(t, target))
	if err != nil {
		t.Fatal(err)
	}
	authority := candidate.Authority()
	applied, err := codexidentity.NewAppliedIdentity(authority.Vendor(), authority.Origin(), authority.Subject())
	if err != nil {
		t.Fatal(err)
	}
	return candidate, applied
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func testRuntime(t *testing.T, continuity Continuity) *Runtime {
	t.Helper()
	return newTestRuntime(t, Config{Continuity: continuity})
}

func newTestRuntime(t *testing.T, config Config) *Runtime {
	t.Helper()
	if config.ClientScopes == nil {
		config.ClientScopes = testClientDigester{}
	}
	if config.Continuity == nil {
		config.Continuity = newTestContinuity(t)
	}
	if config.ProviderCookies == nil {
		config.ProviderCookies = newTestCookieService(t, &testCookieRepository{})
	}
	if config.ExternalScheme == nil {
		config.ExternalScheme = testSchemeResolver("https")
	}
	runtime, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

type testOpaqueDigester struct{}

func (testOpaqueDigester) OpaqueDigest(namespace codexidentity.OpaqueNamespace, input []byte) (codexidentity.OpaqueDigest, error) {
	sum := sha256.Sum256(append([]byte(namespace+":"), input...))
	return codexidentity.OpaqueDigestFromParts(namespace, "h1", sum)
}

func (d testOpaqueDigester) OpaqueDigestCandidates(namespace codexidentity.OpaqueNamespace, input []byte) ([]codexidentity.OpaqueDigest, error) {
	digest, err := d.OpaqueDigest(namespace, input)
	return []codexidentity.OpaqueDigest{digest}, err
}

type testContinuityStore struct {
	mu          sync.Mutex
	bindings    map[codexidentity.OpaqueDigest]codexcontinuity.Binding
	claimCount  int
	failClaimAt int
	lookupErr   error
	commitErr   error
}

func assertStoreLifecycle(t *testing.T, store *testContinuityStore, count int, lifecycle codexcontinuity.Lifecycle) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.bindings) != count {
		t.Fatalf("binding count = %d, want %d", len(store.bindings), count)
	}
	for _, binding := range store.bindings {
		if binding.Lifecycle != lifecycle {
			t.Fatalf("binding lifecycle = %q, want %q", binding.Lifecycle, lifecycle)
		}
	}
}

func (s *testContinuityStore) Claim(_ context.Context, claim codexcontinuity.StoreClaim) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCount++
	if s.failClaimAt > 0 && s.claimCount == s.failClaimAt {
		return codexcontinuity.StoreResult{}, errors.New("claim unavailable")
	}
	if binding, exists := s.bindings[claim.CurrentDigest]; exists {
		if binding.Owner.Equal(claim.Owner) {
			return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreOwned, Binding: binding}, nil
		}
		return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreConflict, Binding: binding}, nil
	}
	binding := codexcontinuity.Binding{
		Kind: claim.Kind, Digest: claim.CurrentDigest, Owner: claim.Owner,
		Lifecycle: codexcontinuity.LifecyclePending, ClaimOperationID: claim.OperationID,
		CreatedAt: claim.Now, UpdatedAt: claim.Now, ExpiresAt: claim.Now.Add(claim.Limits.PendingTTL),
	}
	s.bindings[claim.CurrentDigest] = binding
	return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreClaimed, Binding: binding}, nil
}

func (s *testContinuityStore) Lookup(_ context.Context, lookup codexcontinuity.StoreLookup) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookupErr != nil {
		return codexcontinuity.StoreResult{}, s.lookupErr
	}
	for _, digest := range lookup.DigestCandidates {
		binding, exists := s.bindings[digest]
		if !exists {
			continue
		}
		ownedClient := false
		for _, client := range lookup.ClientScopeCandidates {
			ownedClient = ownedClient || binding.Owner.ClientScope.Equal(client)
		}
		if !ownedClient || (lookup.ProtocolScope != nil && !binding.Owner.ProtocolScope.Equal(*lookup.ProtocolScope)) {
			return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreConflict, Binding: binding}, nil
		}
		return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreOwned, Binding: binding}, nil
	}
	return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreUnknown}, nil
}

func (s *testContinuityStore) Commit(_ context.Context, commit codexcontinuity.StoreCommit) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitErr != nil {
		return codexcontinuity.StoreResult{}, s.commitErr
	}
	binding := s.bindings[commit.Binding.Digest]
	binding.Lifecycle = codexcontinuity.LifecycleCommitted
	binding.UpdatedAt = commit.Now
	binding.CommittedAt = &commit.Now
	binding.ExpiresAt = commit.Now.Add(commit.Limits.CommittedTTL)
	s.bindings[commit.Binding.Digest] = binding
	return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreCommitted, Binding: binding}, nil
}

func (s *testContinuityStore) Abandon(_ context.Context, abandon codexcontinuity.StoreAbandon) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, abandon.Binding.Digest)
	return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreAbandoned}, nil
}

func (*testContinuityStore) Cleanup(context.Context, codexcontinuity.StoreCleanup) (codexcontinuity.CleanupResult, error) {
	return codexcontinuity.CleanupResult{}, nil
}

func (*testContinuityStore) RequiredHMACVersions(context.Context) ([]string, error) {
	return []string{"h1"}, nil
}

func newTestContinuity(t *testing.T) *codexcontinuity.Service {
	service, _ := newTestContinuityFixture(t)
	return service
}

func newTestContinuityFixture(t *testing.T) (*codexcontinuity.Service, *testContinuityStore) {
	t.Helper()
	limits := codexcontinuity.Limits{
		PendingTTL: time.Minute, CommittedTTL: time.Hour, TombstoneTTL: time.Minute, MaxBindings: 100,
	}
	policy, err := codexcontinuity.NewPolicy(map[codexcontinuity.Kind]codexcontinuity.Limits{
		codexcontinuity.KindThreadID: limits, codexcontinuity.KindSessionID: limits,
		codexcontinuity.KindConversationID: limits, codexcontinuity.KindWindowID: limits,
		codexcontinuity.KindTurnState: limits, codexcontinuity.KindTurnMetadata: limits,
		codexcontinuity.KindResponseReference: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &testContinuityStore{bindings: make(map[codexidentity.OpaqueDigest]codexcontinuity.Binding)}
	service, err := codexcontinuity.NewService(codexcontinuity.Config{
		Store:    store,
		Digester: testOpaqueDigester{}, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func claimFixtureEvidence(
	t *testing.T,
	service *codexcontinuity.Service,
	client codexidentity.ClientScope,
	candidate codexidentity.CandidateSnapshot,
	operation string,
	headers http.Header,
) {
	t.Helper()
	decision := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers:         headers,
		Owners:          func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerUnknown },
		AttestationLock: codexheaders.OperationUnlocked,
	})
	for _, claim := range decision.Claims() {
		lease, err := service.Claim(context.Background(), codexcontinuity.ClaimRequest{
			Evidence: evidence(claim.Candidate()),
			Scope: codexcontinuity.Scope{
				CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client},
				ProtocolScope: candidate.ProtocolScope(), RouteTargetHint: candidate.RouteTargetID(),
			},
			OperationID: operation,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Commit(context.Background(), lease); err != nil {
			t.Fatal(err)
		}
	}
}

type testHandleDigester struct{}

func (testHandleDigester) Sign(_ codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	return codexkeyring.Digest{Version: "h1", Sum: sha256.Sum256(input)}, nil
}

func (d testHandleDigester) LookupDigests(purpose codexkeyring.HMACPurpose, input []byte) ([]codexkeyring.Digest, error) {
	digest, err := d.Sign(purpose, input)
	return []codexkeyring.Digest{digest}, err
}

type testCookieRepository struct {
	mu       sync.Mutex
	binding  providercookie.BindingRecord
	merges   int
	loadErr  error
	mergeErr error
}

func (*testCookieRepository) UseBinding(context.Context, providercookie.BindingLookup) (providercookie.BindingUse, error) {
	return providercookie.BindingUse{Disposition: providercookie.BindingUnknown}, nil
}

func (r *testCookieRepository) CreateBinding(_ context.Context, binding providercookie.BindingRecord, _ providercookie.Policy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.binding = binding
	return nil
}

func (r *testCookieRepository) Load(_ context.Context, scope providercookie.CookieScope, _ time.Time) (providercookie.Snapshot, error) {
	if r.loadErr != nil {
		return providercookie.Snapshot{}, r.loadErr
	}
	return providercookie.NewSnapshot(scope, nil)
}

func (*testCookieRepository) Touch(context.Context, providercookie.CookieScope, []providercookie.CookieKey, time.Time) error {
	return nil
}

func (r *testCookieRepository) Merge(_ context.Context, _ providercookie.CookieScope, changes []providercookie.Mutation, _ time.Time, _ providercookie.Policy) (providercookie.MergeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mergeErr != nil {
		return providercookie.MergeResult{}, r.mergeErr
	}
	r.merges++
	return providercookie.MergeResult{Upserted: len(changes)}, nil
}

func (*testCookieRepository) Cleanup(context.Context, providercookie.CleanupRequest) (providercookie.CleanupResult, error) {
	return providercookie.CleanupResult{}, nil
}

func newTestCookieService(t *testing.T, repository providercookie.Repository) *providercookie.Service {
	t.Helper()
	service, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository: repository, HandleDigester: testHandleDigester{},
		Random: bytes.NewReader(bytes.Repeat([]byte{9}, 256)),
		HostCanonicalizer: providercookie.HostCanonicalizerFunc(func(host string) (string, error) {
			return strings.ToLower(host), nil
		}),
		PublicSuffixList: providercookie.PublicSuffixFunc(func(domain string) string {
			parts := strings.Split(domain, ".")
			return parts[len(parts)-1]
		}),
		Policy: providercookie.DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type testSchemeResolver string

func (s testSchemeResolver) ResolveExternalScheme(*http.Request) (providercookie.ResolvedExternalScheme, error) {
	return providercookie.NewResolvedExternalScheme(string(s))
}

func TestContinuityKindCatalogAndGatewayCookieStripping(t *testing.T) {
	if discovery, err := initialClientEvidence(nil); err != nil || len(discovery.Decisions()) != 0 {
		t.Fatalf("empty discovery = %#v, %v", discovery, err)
	}
	if err := (*Runtime)(nil).bindClientScope(&Operation{}, nil, false); Classify(err) != FailureStorage {
		t.Fatalf("missing digester error = %v", err)
	}

	fields := []codexheaders.Field{
		codexheaders.FieldThreadID,
		codexheaders.FieldSessionID,
		codexheaders.FieldConversationID,
		codexheaders.FieldWindowID,
		codexheaders.FieldTurnState,
		codexheaders.FieldTurnMetadata,
		codexheaders.FieldResponseReference,
		codexheaders.FieldEnvelope,
	}
	for _, field := range fields {
		_ = continuityKind(field)
	}

	headers := http.Header{
		"cookie": {
			"first=one; " + providercookie.GatewayHandleName + "=secret; malformed",
			providercookie.GatewayHandleName + "=second; last=two",
		},
	}
	stripGatewayHandleCookie(headers)
	if got := headers.Values("Cookie"); len(got) != 2 || got[0] != "first=one; malformed" || got[1] != "last=two" {
		t.Fatalf("filtered cookies = %#v", got)
	}
}
