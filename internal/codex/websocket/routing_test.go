package codexws

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestOwnerFreePhysicalReplacementMayCrossAuthorityBeforeDisclosure(t *testing.T) {
	first, firstApplied := testCandidate(t, "route-a", "https://first.example.test/v1")
	second, secondApplied := testCandidate(t, "route-b", "https://second.example.test/v1")
	firstURL := mustURL(t, "wss://first.example.test/v1")
	secondURL := mustURL(t, "wss://second.example.test/v1")

	t.Run("continuity owner-free", func(t *testing.T) {
		runtime := testRuntime(t, nil)
		op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "owner-free")
		if err != nil {
			t.Fatal(err)
		}
		assertOwnerFreeCrossAuthorityReplacement(t, op, first, firstApplied, firstURL, second, secondApplied, secondURL)
	})

	t.Run("cookie-only", func(t *testing.T) {
		repository := &testCookieRepository{}
		runtime := newTestRuntime(t, Config{
			ClientScopes: testClientDigester{}, ProviderCookies: newTestCookieService(t, repository),
			ExternalScheme: testSchemeResolver("https"),
		})
		op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "cookie-owner-free")
		if err != nil {
			t.Fatal(err)
		}
		firstHeaders := make(http.Header)
		if _, err := op.PrepareDial(context.Background(), firstHeaders, first, firstApplied, firstURL); err != nil {
			t.Fatal(err)
		}
		if err := op.ApplyHandshake(firstURL, http.Header{"Set-Cookie": {"provider=first; Path=/; Secure"}}); err != nil {
			t.Fatal(err)
		}
		secondHeaders := make(http.Header)
		if _, err := op.PrepareDial(context.Background(), secondHeaders, second, secondApplied, secondURL); err != nil {
			t.Fatal("cookie-only replacement rejected:", err)
		}
		if got := secondHeaders.Get("Cookie"); got != "" {
			t.Fatalf("discarded authority overlay leaked into replacement: %q", got)
		}
		backToFirst := make(http.Header)
		if _, err := op.PrepareDial(context.Background(), backToFirst, first, firstApplied, firstURL); err != nil {
			t.Fatal("return to discarded cookie scope failed:", err)
		}
		if got := backToFirst.Get("Cookie"); got != "" {
			t.Fatalf("discarded overlay reappeared after returning to its Authority: %q", got)
		}
		if authority, route := op.RequiredAuthority(); authority != nil || route != "" {
			t.Fatalf("cookie-only dial created security pin: (%v, %q)", authority, route)
		}
	})

	t.Run("stateless first frame", func(t *testing.T) {
		runtime := testRuntime(t, nil)
		op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "stateless-first-frame")
		if err != nil {
			t.Fatal(err)
		}
		if err := op.InspectBootstrapFrame(context.Background(), true, []byte(`{"type":"response.create","model":"gpt-5"}`)); err != nil {
			t.Fatal(err)
		}
		assertOwnerFreeCrossAuthorityReplacement(t, op, first, firstApplied, firstURL, second, secondApplied, secondURL)
	})
}

func assertOwnerFreeCrossAuthorityReplacement(
	t *testing.T,
	op *Operation,
	first codexidentity.CandidateSnapshot,
	firstApplied codexidentity.AppliedIdentity,
	firstURL *url.URL,
	second codexidentity.CandidateSnapshot,
	secondApplied codexidentity.AppliedIdentity,
	secondURL *url.URL,
) {
	t.Helper()
	if _, err := op.PrepareDial(context.Background(), make(http.Header), first, firstApplied, firstURL); err != nil {
		t.Fatal(err)
	}
	if authority, route := op.RequiredAuthority(); authority != nil || route != "" {
		t.Fatalf("physical dial created security pin: (%v, %q)", authority, route)
	}
	if _, err := op.PrepareDial(context.Background(), make(http.Header), second, secondApplied, secondURL); err != nil {
		t.Fatal("owner-free cross-authority replacement rejected:", err)
	}
}

func TestDisclosureAndVisibilityEstablishSecurityPins(t *testing.T) {
	first, firstApplied := testCandidate(t, "route-a", "https://first.example.test/v1")
	second, secondApplied := testCandidate(t, "route-b", "https://second.example.test/v1")
	sameScope, sameScopeApplied := testCandidate(t, "route-same-scope", "https://first.example.test/v1")
	firstURL := mustURL(t, "wss://first.example.test/v1")
	secondURL := mustURL(t, "wss://second.example.test/v1")

	t.Run("durable claim", func(t *testing.T) {
		request := testRequest("client-a")
		request.Header.Set("Thread-Id", "new-thread")
		op, err := testRuntime(t, nil).Begin(context.Background(), request, codexAPIType, "claim-pin")
		if err != nil {
			t.Fatal(err)
		}
		permit, err := op.PrepareDial(context.Background(), make(http.Header), first, firstApplied, firstURL)
		if err != nil {
			t.Fatal(err)
		}
		if authority, _ := op.RequiredAuthority(); authority != nil {
			t.Fatal("durable claim pinned authority before physical disclosure")
		}
		if err := permit.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := op.PrepareDial(context.Background(), make(http.Header), sameScope, sameScopeApplied, firstURL); err != nil {
			t.Fatal("ProtocolScope claim rejected same-scope RouteTarget replacement:", err)
		}
		assertCrossAuthorityRejected(t, op, second, secondApplied, secondURL)
	})

	t.Run("attestation", func(t *testing.T) {
		request := testRequest("client-a")
		request.Header.Set("X-Oai-Attestation", "opaque-attestation")
		op, err := testRuntime(t, nil).Begin(context.Background(), request, codexAPIType, "attestation-pin")
		if err != nil {
			t.Fatal(err)
		}
		permit, err := op.PrepareDial(context.Background(), make(http.Header), first, firstApplied, firstURL)
		if err != nil {
			t.Fatal(err)
		}
		if authority, _ := op.RequiredAuthority(); authority != nil {
			t.Fatal("attestation pinned authority before physical disclosure")
		}
		if err := permit.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := op.PrepareDial(context.Background(), make(http.Header), sameScope, sameScopeApplied, firstURL); err != nil {
			t.Fatal("attestation authority pin rejected same-authority route:", err)
		}
		assertCrossAuthorityRejected(t, op, second, secondApplied, secondURL)
	})

	t.Run("active response", func(t *testing.T) {
		op, err := testRuntime(t, nil).Begin(context.Background(), testRequest("client-a"), codexAPIType, "response-pin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := op.PrepareDial(context.Background(), make(http.Header), first, firstApplied, firstURL); err != nil {
			t.Fatal(err)
		}
		if err := op.OpenConnection(); err != nil {
			t.Fatal(err)
		}
		defer op.CloseConnection()
		permit, err := op.PrepareServerFrame(context.Background(), true, []byte(`{"type":"response.created","response":{"id":"response-pins-route"}}`))
		if err != nil {
			t.Fatal(err)
		}
		if authority, _ := op.RequiredAuthority(); authority != nil {
			t.Fatal("active response pinned authority before downstream write")
		}
		if err := permit.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := op.CommitVisibility(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertCrossAuthorityRejected(t, op, second, secondApplied, secondURL)
	})

	t.Run("client visible route", func(t *testing.T) {
		op, err := testRuntime(t, nil).Begin(context.Background(), testRequest("client-a"), codexAPIType, "visible-pin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := op.PrepareDial(context.Background(), make(http.Header), first, firstApplied, firstURL); err != nil {
			t.Fatal(err)
		}
		if err := op.CommitVisibility(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := op.PrepareDial(context.Background(), make(http.Header), sameScope, sameScopeApplied, firstURL); Classify(err) != FailureIdentity {
			t.Fatalf("visible route target changed: class=%q err=%v", Classify(err), err)
		}
		assertCrossAuthorityRejected(t, op, second, secondApplied, secondURL)
	})
}

func assertCrossAuthorityRejected(
	t *testing.T,
	op *Operation,
	candidate codexidentity.CandidateSnapshot,
	applied codexidentity.AppliedIdentity,
	finalURL *url.URL,
) {
	t.Helper()
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, finalURL); Classify(err) != FailureIdentity {
		t.Fatalf("cross-authority replacement class=%q err=%v", Classify(err), err)
	}
}
