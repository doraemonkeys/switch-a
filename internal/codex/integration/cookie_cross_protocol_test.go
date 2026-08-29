package codexintegration_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestProviderCookiesCrossHTTPAndWebSocketEquivalentSchemes(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	httpsCandidate, httpsApplied, httpsURL := fixtureCandidate(t, candidateSpec{})
	handle := commitHTTPCookie(
		t, fixture, "client-alpha", "", httpsCandidate, httpsApplied, httpsURL,
		"secure_from_http=one; Path=/; Secure; Max-Age=604800", operationID("http-cookie", 1),
	)

	wssReplacement, wssReplacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
	wsOperation, wsHeaders := prepareWSCookieAttempt(
		t, fixture, "client-alpha", handle, wssReplacement, wssReplacementApplied,
		websocketURL(t, httpsURL), operationID("ws-cookie", 1),
	)
	if got := wsHeaders.Get("Cookie"); got != "secure_from_http=one" {
		t.Fatalf("HTTPS -> WSS Cookie = %q", got)
	}
	if err := wsOperation.ApplyHandshake(websocketURL(t, httpsURL), http.Header{
		"Set-Cookie": {"secure_from_ws=two; Path=/; Secure; Max-Age=604800"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := wsOperation.CommitCookies(context.Background()); err != nil {
		t.Fatal(err)
	}

	httpOperation, err := fixture.http.Begin(
		context.Background(), requestWithHandle(http.MethodPost, "client-alpha", handle),
		testAPIType, operationID("http-cookie", 2), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = httpsURL
	if _, err := httpOperation.PrepareAttempt(
		context.Background(), upstream, httpsCandidate, httpsApplied,
	); err != nil {
		t.Fatal(err)
	}
	if got := upstream.Header.Get("Cookie"); !strings.Contains(got, "secure_from_http=one") || !strings.Contains(got, "secure_from_ws=two") {
		t.Fatalf("WSS -> HTTPS Cookies = %q", got)
	}

	httpCandidate, httpApplied, httpURL := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-http", subject: "subject-http", requestURL: "http://plain.example.test/v1/responses",
	})
	httpHandle := commitHTTPCookie(
		t, fixture, "client-plain", "", httpCandidate, httpApplied, httpURL,
		"plain_http=three; Path=/; Max-Age=604800", operationID("http-cookie", 3),
	)
	_, wsHeaders = prepareWSCookieAttempt(
		t, fixture, "client-plain", httpHandle, httpCandidate, httpApplied,
		websocketURL(t, httpURL), operationID("ws-cookie", 2),
	)
	if got := wsHeaders.Get("Cookie"); got != "plain_http=three" {
		t.Fatalf("HTTP -> WS Cookie = %q", got)
	}
}

func TestProviderCookieScopeIgnoresAPITypeButSeparatesJarAndAuthority(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	base, baseApplied, baseURL := fixtureCandidate(t, candidateSpec{})
	handle := commitHTTPCookie(
		t, fixture, "client-alpha", "", base, baseApplied, baseURL,
		"scope_cookie=base; Path=/; Secure; Max-Age=604800", operationID("http-scope", 1),
	)

	otherAPI, otherAPIApplied, _ := fixtureCandidate(t, candidateSpec{apiType: "codex-alt"})
	if !base.Authority().CookieAuthority().Equal(otherAPI.Authority().CookieAuthority()) {
		t.Fatal("CookieAuthority unexpectedly included APIType")
	}
	_, headers := prepareWSCookieAttempt(
		t, fixture, "client-alpha", handle, otherAPI, otherAPIApplied,
		websocketURL(t, baseURL), operationID("ws-scope", 1),
	)
	if got := headers.Get("Cookie"); got != "scope_cookie=base" {
		t.Fatalf("cross-APIType Cookie = %q", got)
	}

	isolatedAuthorities := []struct {
		name string
		spec candidateSpec
	}{
		{name: "Vendor", spec: candidateSpec{vendor: "other-vendor"}},
		{name: "CredentialSubject", spec: candidateSpec{subject: "subject-b"}},
		{name: "Origin", spec: candidateSpec{requestURL: "https://other.example.test/v1/responses"}},
	}
	for index, isolated := range isolatedAuthorities {
		t.Run(isolated.name, func(t *testing.T) {
			candidate, applied, finalURL := fixtureCandidate(t, isolated.spec)
			_, isolatedHeaders := prepareWSCookieAttempt(
				t, fixture, "client-alpha", handle, candidate, applied,
				websocketURL(t, finalURL), operationID("ws-authority", index+1),
			)
			if got := isolatedHeaders.Get("Cookie"); got != "" {
				t.Fatalf("isolated Authority received %q", got)
			}
		})
	}

	for index, test := range []struct {
		name       string
		client     string
		handle     string
		wantCookie string
	}{
		{name: "different client", client: "client-beta", handle: handle},
		{name: "malformed handle falls back to ClientScope", client: "client-alpha", handle: "malformed", wantCookie: "scope_cookie=base"},
		{name: "missing handle falls back to ClientScope", client: "client-alpha", handle: "", wantCookie: "scope_cookie=base"},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation, isolatedHeaders := prepareWSCookieAttempt(
				t, fixture, test.client, test.handle, base, baseApplied,
				websocketURL(t, baseURL), operationID("ws-jar", index+1),
			)
			if got := isolatedHeaders.Get("Cookie"); got != test.wantCookie {
				t.Fatalf("upstream Cookie = %q, want %q", got, test.wantCookie)
			}
			issued := gatewayHandle(t, operation.GatewaySetCookie())
			if issued == handle {
				t.Fatal("fallback did not rotate the gateway handle")
			}
		})
	}
}

func TestCookieRestartRotationCapacityAndProviderReachability(t *testing.T) {
	t.Run("restart and key rotation preserve legacy ownership", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		base, baseApplied, baseURL := fixtureCandidate(t, candidateSpec{})
		request := fixtureRequest(http.MethodPost, "client-alpha", http.Header{
			"Thread-Id": {"restart-identity"},
		})
		operation, err := fixture.http.Begin(
			context.Background(), request, testAPIType, operationID("http-restart", 1), nil, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		upstream := fixtureRequest(http.MethodPost, "", nil)
		upstream.URL = baseURL
		attempt, err := operation.PrepareAttempt(context.Background(), upstream, base, baseApplied)
		if err != nil {
			t.Fatal(err)
		}
		if err := attempt.MarkDisclosed(context.Background()); err != nil {
			t.Fatal(err)
		}
		head := &upstreamtransport.ResponseHead{
			SourceHeader: http.Header{"Set-Cookie": {"rotated_cookie=legacy; Path=/; Secure; Max-Age=604800"}},
			Header:       make(http.Header),
		}
		if err := attempt.ObserveResponse(head); err != nil {
			t.Fatal(err)
		}
		responseHeaders := make(http.Header)
		visibility, err := attempt.PrepareVisible(context.Background(), responseHeaders)
		if err != nil {
			t.Fatal(err)
		}
		if err := visibility.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		handle := gatewayHandle(t, responseHeaders.Get("Set-Cookie"))

		restarted := fixture.restart(t, "h2", "a2")
		replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
		wsRequest := requestWithHandle(http.MethodGet, "client-alpha", handle)
		wsRequest.Header.Set("Thread-Id", "restart-identity")
		wsOperation, err := restarted.ws.Begin(
			context.Background(), wsRequest, testAPIType, operationID("ws-restart", 1),
		)
		if err != nil {
			t.Fatal(err)
		}
		required, _ := wsOperation.RequiredAuthority()
		if required == nil || !required.Equal(base.Authority()) {
			t.Fatalf("rotated continuity owner = %v", required)
		}
		headers := wsRequest.Header.Clone()
		if _, err := wsOperation.PrepareDial(
			context.Background(), headers, replacement, replacementApplied, websocketURL(t, baseURL),
		); err != nil {
			t.Fatal(err)
		}
		if got := headers.Get("Cookie"); got != "rotated_cookie=legacy" {
			t.Fatalf("rotated Cookie = %q", got)
		}
	})

	t.Run("global handle capacity fails both protocols closed", func(t *testing.T) {
		policy := providercookie.DefaultPolicy()
		policy.MaxHandleBindingsGlobal = 1
		fixture := newRuntimeFixture(t, fixtureOptions{
			cookiePolicy: policy,
		})
		if _, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "client-alpha", nil),
			testAPIType, operationID("http-cookie-capacity", 1), nil, nil,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.ws.Begin(
			context.Background(), fixtureRequest(http.MethodGet, "client-alpha", nil),
			testAPIType, operationID("ws-cookie-capacity", 1),
		); err != nil {
			t.Fatalf("same ClientScope without a handle consumed capacity: %v", err)
		}
		_, err := fixture.ws.Begin(
			context.Background(), fixtureRequest(http.MethodGet, "client-beta", nil),
			testAPIType, operationID("ws-cookie-capacity", 2),
		)
		requireWSFailure(t, err, codexws.FailureStorage)
	})

	t.Run("cleanup retains only reachable authorities after grace", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		base, baseApplied, baseURL := fixtureCandidate(t, candidateSpec{})
		handle := commitHTTPCookie(
			t, fixture, "client-alpha", "", base, baseApplied, baseURL,
			"reachable_cookie=keep; Path=/; Secure; Max-Age=604800", operationID("http-reachable", 1),
		)
		orphan, orphanApplied, orphanURL := fixtureCandidate(t, candidateSpec{
			routeTarget: "route-orphan", requestURL: "https://orphan.example.test/v1/responses",
		})
		orphanOperation, _ := prepareWSCookieAttempt(
			t, fixture, "client-alpha", handle, orphan, orphanApplied,
			websocketURL(t, orphanURL), operationID("ws-reachable", 1),
		)
		if err := orphanOperation.ApplyHandshake(websocketURL(t, orphanURL), http.Header{
			"Set-Cookie": {"orphan_cookie=remove; Path=/; Secure; Max-Age=604800"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := orphanOperation.CommitCookies(context.Background()); err != nil {
			t.Fatal(err)
		}
		reachable := []codexidentity.CookieAuthority{base.Authority().CookieAuthority()}
		if _, err := fixture.cookies.Cleanup(
			context.Background(), mustCookieOperationID(t, "cleanup-mark-unreachable"), reachable,
		); err != nil {
			t.Fatal(err)
		}
		fixture.clock.Advance(25 * time.Hour)
		cleanup, err := fixture.cookies.Cleanup(
			context.Background(), mustCookieOperationID(t, "cleanup-delete"),
			[]codexidentity.CookieAuthority{base.Authority().CookieAuthority()},
		)
		if err != nil {
			t.Fatal(err)
		}
		if cleanup.OrphanAuthorities == 0 {
			t.Fatalf("cleanup did not remove an unreachable Authority: %#v", cleanup)
		}

		_, baseHeaders := prepareWSCookieAttempt(
			t, fixture, "client-alpha", handle, base, baseApplied,
			websocketURL(t, baseURL), operationID("ws-reachable", 2),
		)
		if got := baseHeaders.Get("Cookie"); got != "reachable_cookie=keep" {
			t.Fatalf("reachable Cookie = %q", got)
		}
		_, orphanHeaders := prepareWSCookieAttempt(
			t, fixture, "client-alpha", handle, orphan, orphanApplied,
			websocketURL(t, orphanURL), operationID("ws-reachable", 3),
		)
		if got := orphanHeaders.Get("Cookie"); got != "" {
			t.Fatalf("orphan Cookie survived provider deletion: %q", got)
		}
	})
}

func TestCookieStoreFailuresRemainTypedAcrossAdapters(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	sqlDB, err := fixture.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "client-alpha", nil),
		testAPIType, operationID("http-cookie-store", 1), nil, nil,
	)
	requireHTTPError(t, err, codexhttp.ErrorDependencyUnavailable)
	_, err = fixture.ws.Begin(
		context.Background(), fixtureRequest(http.MethodGet, "client-alpha", nil),
		testAPIType, operationID("ws-cookie-store", 1),
	)
	requireWSFailure(t, err, codexws.FailureStorage)
}

func TestProviderCookieSurvivesWebSocketReconnectWithoutCrossingClientScope(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
	handle := commitHTTPCookie(
		t, fixture, "reconnect-cookie-client", "", candidate, applied, finalURL,
		"reconnect_cookie=stable; Path=/; Secure; Max-Age=604800", operationID("cookie-reconnect-seed", 1),
	)

	for sequence := 1; sequence <= 2; sequence++ {
		operation, headers := prepareWSCookieAttempt(
			t, fixture, "reconnect-cookie-client", handle, candidate, applied,
			websocketURL(t, finalURL), operationID("cookie-reconnect", sequence),
		)
		if got := headers.Get("Cookie"); got != "reconnect_cookie=stable" {
			t.Fatalf("reconnect %d Cookie = %q", sequence, got)
		}
		if err := operation.OpenConnection(); err != nil {
			t.Fatal(err)
		}
		operation.CloseConnection()
	}

	_, isolated := prepareWSCookieAttempt(
		t, fixture, "other-reconnect-cookie-client", handle, candidate, applied,
		websocketURL(t, finalURL), operationID("cookie-reconnect-isolated", 1),
	)
	if got := isolated.Get("Cookie"); got != "" {
		t.Fatalf("different ClientScope received reconnected Cookie %q", got)
	}
}

func commitHTTPCookie(
	t *testing.T,
	fixture *runtimeFixture,
	client, handle string,
	candidate codexidentity.CandidateSnapshot,
	applied codexidentity.AppliedIdentity,
	finalURL *url.URL,
	setCookie, operation string,
) string {
	t.Helper()
	request := requestWithHandle(http.MethodPost, client, handle)
	httpOperation, err := fixture.http.Begin(
		context.Background(), request, testAPIType, operation, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = finalURL
	attempt, err := httpOperation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	head := &upstreamtransport.ResponseHead{
		SourceHeader: http.Header{"Set-Cookie": {setCookie}}, Header: make(http.Header),
	}
	if err := attempt.ObserveResponse(head); err != nil {
		t.Fatal(err)
	}
	responseHeaders := make(http.Header)
	visibility, err := attempt.PrepareVisible(context.Background(), responseHeaders)
	if err != nil {
		t.Fatal(err)
	}
	if err := visibility.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if handle != "" {
		return handle
	}
	return gatewayHandle(t, responseHeaders.Get("Set-Cookie"))
}

func prepareWSCookieAttempt(
	t *testing.T,
	fixture *runtimeFixture,
	client, handle string,
	candidate codexidentity.CandidateSnapshot,
	applied codexidentity.AppliedIdentity,
	finalURL *url.URL,
	operation string,
) (*codexws.Operation, http.Header) {
	t.Helper()
	request := requestWithHandle(http.MethodGet, client, handle)
	wsOperation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operation)
	if err != nil {
		t.Fatal(err)
	}
	headers := request.Header.Clone()
	if _, err := wsOperation.PrepareDial(context.Background(), headers, candidate, applied, finalURL); err != nil {
		t.Fatal(err)
	}
	return wsOperation, headers
}

func requestWithHandle(method, client, handle string) *http.Request {
	request := fixtureRequest(method, client, nil)
	if handle != "" {
		request.AddCookie(&http.Cookie{Name: providercookie.GatewayHandleName, Value: handle})
	}
	return request
}

func mustCookieOperationID(t *testing.T, value string) providercookie.OperationID {
	t.Helper()
	operation, err := providercookie.NewOperationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}
