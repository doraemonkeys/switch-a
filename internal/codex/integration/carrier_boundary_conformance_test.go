package codexintegration_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestTurnMetadataClaimDisclosureAndIsolationConformAcrossCarriers(t *testing.T) {
	for _, carrier := range []string{"HTTP", "WebSocket"} {
		t.Run(carrier, func(t *testing.T) {
			fixture := newRuntimeFixture(t, fixtureOptions{})
			original, originalApplied, finalURL := fixtureCandidate(t, candidateSpec{})
			replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
			crossAuthority, crossApplied, crossURL := fixtureCandidate(t, candidateSpec{
				routeTarget: "route-cross", requestURL: "https://other.example.test/v1/responses",
			})
			const (
				client   = "metadata-client"
				metadata = "opaque-turn-metadata"
			)
			headers := http.Header{"X-Codex-Turn-Metadata": {metadata}}

			switch carrier {
			case "HTTP":
				request := fixtureRequest(http.MethodPost, client, headers)
				operation, err := fixture.http.Begin(
					context.Background(), request, testAPIType, operationID("metadata-http", 1), testHTTPClientEvidence(nil, nil),
				)
				if err != nil {
					t.Fatal(err)
				}
				upstream, err := upstreamtransport.BuildRequestWithPolicy(
					context.Background(), http.MethodPost, finalURL.String(), nil, request, operation.RequestPolicy(),
				)
				if err != nil {
					t.Fatal(err)
				}
				attempt, err := operation.PrepareAttempt(context.Background(), upstream, original, originalApplied)
				if err != nil {
					t.Fatal(err)
				}
				if upstream.Header.Get("X-Codex-Turn-Metadata") != metadata {
					t.Fatal("HTTP did not disclose Turn Metadata unchanged")
				}
				assertPendingMetadataVisibleOnlyToOwner(t, fixture, client, metadata, original.Authority())
				if err := attempt.MarkDisclosed(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "WebSocket":
				request := fixtureRequest(http.MethodGet, client, headers)
				operation, err := fixture.ws.Begin(
					context.Background(), request, testAPIType, operationID("metadata-ws", 1),
				)
				if err != nil {
					t.Fatal(err)
				}
				forwarded := request.Header.Clone()
				permit, err := operation.PrepareDial(
					context.Background(), forwarded, original, originalApplied, websocketURL(t, finalURL),
				)
				if err != nil {
					t.Fatal(err)
				}
				if forwarded.Get("X-Codex-Turn-Metadata") != metadata {
					t.Fatal("WebSocket did not disclose Turn Metadata unchanged")
				}
				assertPendingMetadataVisibleOnlyToOwner(t, fixture, client, metadata, original.Authority())
				if err := permit.Commit(context.Background()); err != nil {
					t.Fatal(err)
				}
			}

			assertMetadataRetrievalAcrossCarriers(
				t, fixture, client, metadata, original.Authority(), replacement, replacementApplied, finalURL,
				crossAuthority, crossApplied, crossURL,
			)
		})
	}
}

func TestTurnStateProjectionCommitFailedWriteAndIsolationConformAcrossCarriers(t *testing.T) {
	for _, carrier := range []string{"HTTP", "WebSocket"} {
		t.Run(carrier, func(t *testing.T) {
			fixture := newRuntimeFixture(t, fixtureOptions{})
			candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
			replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
			const (
				client    = "turn-state-client"
				turnState = "opaque-visible-turn-state"
			)

			switch carrier {
			case "HTTP":
				operation, err := fixture.http.Begin(
					context.Background(), fixtureRequest(http.MethodPost, client, nil),
					testAPIType, operationID("state-http", 1), testHTTPClientEvidence(nil, nil),
				)
				if err != nil {
					t.Fatal(err)
				}
				upstream := fixtureRequest(http.MethodPost, "", nil)
				upstream.URL = finalURL
				attempt, err := operation.PrepareAttempt(context.Background(), upstream, candidate, applied)
				if err != nil {
					t.Fatal(err)
				}
				projected := http.Header{"X-Codex-Turn-State": {turnState}}
				visibility, err := attempt.PrepareVisible(context.Background(), projected)
				if err != nil {
					t.Fatal(err)
				}
				if projected.Get("X-Codex-Turn-State") != turnState {
					t.Fatal("HTTP did not project Turn State unchanged")
				}
				assertPendingTurnStateVisibleOnlyToOwner(t, fixture, client, turnState, candidate.Authority())
				if err := visibility.Commit(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "WebSocket":
				request := fixtureRequest(http.MethodGet, client, nil)
				operation, err := fixture.ws.Begin(
					context.Background(), request, testAPIType, operationID("state-ws", 1),
				)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := operation.PrepareDial(
					context.Background(), request.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
				); err != nil {
					t.Fatal(err)
				}
				permit, projected, err := operation.PrepareServerHeaders(context.Background(), http.Header{
					"X-Codex-Turn-State": {turnState},
				})
				if err != nil {
					t.Fatal(err)
				}
				if projected.Get("X-Codex-Turn-State") != turnState || !permit.PinsRouteTarget() {
					t.Fatalf("WebSocket Turn State projection = %#v, pins=%v", projected, permit.PinsRouteTarget())
				}
				assertPendingTurnStateVisibleOnlyToOwner(t, fixture, client, turnState, candidate.Authority())
				if err := permit.Commit(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := operation.CommitVisibility(context.Background()); err != nil {
					t.Fatal(err)
				}
			}

			assertTurnStateRetrievalAcrossCarriers(
				t, fixture, client, turnState, candidate.Authority(), replacement, replacementApplied, finalURL,
			)
		})
	}

	t.Run("failed downstream write remains pending, not cross-client reusable", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		const state = "failed-write-pending-state"
		request := fixtureRequest(http.MethodGet, "failed-write-owner", nil)
		operation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operationID("state-failed-write", 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := operation.PrepareDial(
			context.Background(), request.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
		); err != nil {
			t.Fatal(err)
		}
		if _, _, err := operation.PrepareServerHeaders(context.Background(), http.Header{
			"X-Codex-Turn-State": {state},
		}); err != nil {
			t.Fatal(err)
		}
		// Omitting Permit.Commit models a downstream accept/write failure. The
		// pending owner stays reserved because visibility is uncertain.
		assertPendingTurnStateVisibleOnlyToOwner(t, fixture, "failed-write-owner", state, candidate.Authority())
	})
}

func TestAttestationAuthorityLifetimeAndResponseProjectionConformAcrossCarriers(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	original, originalApplied, finalURL := fixtureCandidate(t, candidateSpec{})
	replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
	crossAuthority, crossApplied, crossURL := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-cross", requestURL: "https://other.example.test/v1/responses",
	})
	const attestation = "opaque-operation-attestation"

	t.Run("HTTP", func(t *testing.T) {
		request := fixtureRequest(http.MethodPost, "attestation-http-client", http.Header{
			"X-Oai-Attestation": {attestation},
		})
		operation, err := fixture.http.Begin(
			context.Background(), request, testAPIType, operationID("attestation-http", 1), testHTTPClientEvidence(nil, nil),
		)
		if err != nil {
			t.Fatal(err)
		}
		upstream := buildCarrierRequest(t, request, operation.RequestPolicy(), finalURL.String())
		attempt, err := operation.PrepareAttempt(context.Background(), upstream, original, originalApplied)
		if err != nil {
			t.Fatal(err)
		}
		if upstream.Header.Get("X-Oai-Attestation") != attestation {
			t.Fatal("HTTP attestation was not disclosed unchanged")
		}
		retry := buildCarrierRequest(t, request, operation.RequestPolicy(), finalURL.String())
		if _, err := operation.PrepareAttempt(context.Background(), retry, replacement, replacementApplied); err != nil {
			t.Fatal("HTTP attestation rejected same-authority replacement:", err)
		}
		undisclosedCross := buildCarrierRequest(t, request, operation.RequestPolicy(), crossURL.String())
		undisclosedCrossAttempt, err := operation.PrepareAttempt(
			context.Background(), undisclosedCross, crossAuthority, crossApplied,
		)
		if err != nil {
			t.Fatal("HTTP attestation rejected cross-authority replacement before disclosure:", err)
		}
		if err := undisclosedCrossAttempt.AbandonBeforeDisclosure(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := attempt.MarkDisclosed(context.Background()); err != nil {
			t.Fatal(err)
		}
		cross := buildCarrierRequest(t, request, operation.RequestPolicy(), crossURL.String())
		_, err = operation.PrepareAttempt(context.Background(), cross, crossAuthority, crossApplied)
		requireHTTPError(t, err, codexhttp.ErrorIdentityMismatch)

		responseHeaders := http.Header{
			"X-Oai-Attestation":     {"must-not-echo"},
			"X-Codex-Turn-Metadata": {"must-not-echo"},
		}
		if _, err := attempt.PrepareVisible(context.Background(), responseHeaders); err != nil {
			t.Fatal(err)
		}
		if responseHeaders.Get("X-Oai-Attestation") != "" || responseHeaders.Get("X-Codex-Turn-Metadata") != "" {
			t.Fatalf("HTTP response echoed request-only carriers: %#v", responseHeaders)
		}
	})

	t.Run("WebSocket", func(t *testing.T) {
		request := fixtureRequest(http.MethodGet, "attestation-ws-client", http.Header{
			"X-Oai-Attestation": {attestation},
		})
		operation, err := fixture.ws.Begin(
			context.Background(), request, testAPIType, operationID("attestation-ws", 1),
		)
		if err != nil {
			t.Fatal(err)
		}
		forwarded := request.Header.Clone()
		permit, err := operation.PrepareDial(
			context.Background(), forwarded, original, originalApplied, websocketURL(t, finalURL),
		)
		if err != nil {
			t.Fatal(err)
		}
		if forwarded.Get("X-Oai-Attestation") != attestation {
			t.Fatal("WebSocket attestation was not disclosed unchanged")
		}
		if err := permit.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := operation.PrepareDial(
			context.Background(), request.Header.Clone(), replacement, replacementApplied, websocketURL(t, finalURL),
		); err != nil {
			t.Fatal("WebSocket attestation rejected same-authority replacement:", err)
		}
		_, err = operation.PrepareDial(
			context.Background(), request.Header.Clone(), crossAuthority, crossApplied, websocketURL(t, crossURL),
		)
		requireWSFailure(t, err, codexws.FailureIdentity)

		_, projected, err := operation.PrepareServerHeaders(context.Background(), http.Header{
			"X-Oai-Attestation":     {"must-not-echo"},
			"X-Codex-Turn-Metadata": {"must-not-echo"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if projected.Get("X-Oai-Attestation") != "" || projected.Get("X-Codex-Turn-Metadata") != "" {
			t.Fatalf("WebSocket response echoed request-only carriers: %#v", projected)
		}
	})

	for _, test := range []struct {
		name   string
		client string
	}{
		{name: "same client new operation", client: "attestation-http-client"},
		{name: "different client new operation", client: "another-attestation-client"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixtureRequest(http.MethodPost, test.client, http.Header{"X-Oai-Attestation": {attestation}})
			operation, err := fixture.http.Begin(
				context.Background(), request, testAPIType, operationID("attestation-new-operation", 1), testHTTPClientEvidence(nil, nil),
			)
			if err != nil {
				t.Fatal(err)
			}
			upstream := buildCarrierRequest(t, request, operation.RequestPolicy(), crossURL.String())
			if _, err := operation.PrepareAttempt(context.Background(), upstream, crossAuthority, crossApplied); err != nil {
				t.Fatal("operation-local attestation leaked an Authority lock into a new operation:", err)
			}
		})
	}
}

func TestProviderCookieCommitRetryReplacementAndIsolationConformAcrossCarriers(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	original, originalApplied, finalURL := fixtureCandidate(t, candidateSpec{})
	crossAuthority, crossApplied, crossURL := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-cookie-cross", requestURL: "https://other.example.test/v1/responses",
	})
	handle := commitHTTPCookie(
		t, fixture, "cookie-client", "", original, originalApplied, finalURL,
		"provider_seed=one; Path=/; Secure; Max-Age=604800", operationID("cookie-seed", 1),
	)

	t.Run("HTTP and WebSocket select only the managed jar", func(t *testing.T) {
		request := requestWithHandle(http.MethodPost, "cookie-client", handle)
		request.AddCookie(&http.Cookie{Name: "raw_client_cookie", Value: "must-not-forward"})
		httpOperation, err := fixture.http.Begin(
			context.Background(), request, testAPIType, operationID("cookie-select-http", 1), testHTTPClientEvidence(nil, nil),
		)
		if err != nil {
			t.Fatal(err)
		}
		upstream := buildCarrierRequest(t, request, httpOperation.RequestPolicy(), finalURL.String())
		if _, err := httpOperation.PrepareAttempt(context.Background(), upstream, original, originalApplied); err != nil {
			t.Fatal(err)
		}
		if upstream.Header.Get("Cookie") != "provider_seed=one" {
			t.Fatalf("HTTP managed Cookie = %q", upstream.Header.Get("Cookie"))
		}

		wsRequest := requestWithHandle(http.MethodGet, "cookie-client", handle)
		wsRequest.AddCookie(&http.Cookie{Name: "raw_client_cookie", Value: "must-not-forward"})
		wsOperation, err := fixture.ws.Begin(
			context.Background(), wsRequest, testAPIType, operationID("cookie-select-ws", 1),
		)
		if err != nil {
			t.Fatal(err)
		}
		forwarded := wsRequest.Header.Clone()
		if _, err := wsOperation.PrepareDial(
			context.Background(), forwarded, original, originalApplied, websocketURL(t, finalURL),
		); err != nil {
			t.Fatal(err)
		}
		if forwarded.Get("Cookie") != "provider_seed=one" {
			t.Fatalf("WebSocket managed Cookie = %q", forwarded.Get("Cookie"))
		}
	})

	t.Run("HTTP replacement discards the abandoned overlay", func(t *testing.T) {
		request := requestWithHandle(http.MethodPost, "cookie-client", handle)
		operation, err := fixture.http.Begin(
			context.Background(), request, testAPIType, operationID("cookie-replace-http", 1), testHTTPClientEvidence(nil, nil),
		)
		if err != nil {
			t.Fatal(err)
		}
		first := fixtureRequest(http.MethodPost, "", nil)
		first.URL = finalURL
		attempt, err := operation.PrepareAttempt(context.Background(), first, original, originalApplied)
		if err != nil {
			t.Fatal(err)
		}
		head := &upstreamtransport.ResponseHead{
			SourceHeader: http.Header{"Set-Cookie": {"abandoned_http=two; Path=/; Secure"}},
			Header:       http.Header{"Set-Cookie": {"abandoned_http=two; Path=/; Secure"}},
		}
		if err := attempt.ObserveResponse(head); err != nil {
			t.Fatal(err)
		}
		if head.Header.Get("Set-Cookie") != "" {
			t.Fatal("HTTP exposed raw Provider Set-Cookie")
		}
		cross := fixtureRequest(http.MethodPost, "", nil)
		cross.URL = crossURL
		if _, err := operation.PrepareAttempt(context.Background(), cross, crossAuthority, crossApplied); err != nil {
			t.Fatal(err)
		}
		back := fixtureRequest(http.MethodPost, "", nil)
		back.URL = finalURL
		if _, err := operation.PrepareAttempt(context.Background(), back, original, originalApplied); err != nil {
			t.Fatal(err)
		}
		if got := back.Header.Get("Cookie"); got != "provider_seed=one" {
			t.Fatalf("HTTP abandoned overlay survived replacement: %q", got)
		}
	})

	t.Run("WebSocket replacement discards the abandoned handshake overlay", func(t *testing.T) {
		request := requestWithHandle(http.MethodGet, "cookie-client", handle)
		operation, err := fixture.ws.Begin(
			context.Background(), request, testAPIType, operationID("cookie-replace-ws", 1),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := operation.PrepareDial(
			context.Background(), request.Header.Clone(), original, originalApplied, websocketURL(t, finalURL),
		); err != nil {
			t.Fatal(err)
		}
		if err := operation.ApplyHandshake(websocketURL(t, finalURL), http.Header{
			"Set-Cookie": {"abandoned_ws=two; Path=/; Secure"},
		}); err != nil {
			t.Fatal(err)
		}
		if _, projected, err := operation.PrepareServerHeaders(context.Background(), http.Header{
			"Set-Cookie": {"abandoned_ws=two; Path=/; Secure"},
		}); err != nil || projected.Get("Set-Cookie") != "" {
			t.Fatalf("WebSocket raw Set-Cookie projection = %#v err=%v", projected, err)
		}
		if _, err := operation.PrepareDial(
			context.Background(), request.Header.Clone(), crossAuthority, crossApplied, websocketURL(t, crossURL),
		); err != nil {
			t.Fatal(err)
		}
		back := request.Header.Clone()
		if _, err := operation.PrepareDial(
			context.Background(), back, original, originalApplied, websocketURL(t, finalURL),
		); err != nil {
			t.Fatal(err)
		}
		if got := back.Get("Cookie"); got != "provider_seed=one" {
			t.Fatalf("WebSocket abandoned overlay survived replacement: %q", got)
		}
	})

	t.Run("WebSocket visibility commit is readable from HTTP", func(t *testing.T) {
		request := requestWithHandle(http.MethodGet, "cookie-client", handle)
		operation, err := fixture.ws.Begin(
			context.Background(), request, testAPIType, operationID("cookie-commit-ws", 1),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := operation.PrepareDial(
			context.Background(), request.Header.Clone(), original, originalApplied, websocketURL(t, finalURL),
		); err != nil {
			t.Fatal(err)
		}
		if err := operation.ApplyHandshake(websocketURL(t, finalURL), http.Header{
			"Set-Cookie": {"committed_ws=three; Path=/; Secure"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := operation.CommitVisibility(context.Background()); err != nil {
			t.Fatal(err)
		}

		readRequest := requestWithHandle(http.MethodPost, "cookie-client", handle)
		readOperation, err := fixture.http.Begin(
			context.Background(), readRequest, testAPIType, operationID("cookie-read-http", 1), testHTTPClientEvidence(nil, nil),
		)
		if err != nil {
			t.Fatal(err)
		}
		upstream := fixtureRequest(http.MethodPost, "", nil)
		upstream.URL = finalURL
		if _, err := readOperation.PrepareAttempt(context.Background(), upstream, original, originalApplied); err != nil {
			t.Fatal(err)
		}
		if got := upstream.Header.Get("Cookie"); !strings.Contains(got, "provider_seed=one") || !strings.Contains(got, "committed_ws=three") {
			t.Fatalf("HTTP did not read committed WS Cookies: %q", got)
		}
	})

	for _, isolated := range []struct {
		name      string
		client    string
		candidate codexidentity.CandidateSnapshot
		applied   codexidentity.AppliedIdentity
		finalURL  string
	}{
		{name: "different client", client: "other-cookie-client", candidate: original, applied: originalApplied, finalURL: finalURL.String()},
		{name: "different authority", client: "cookie-client", candidate: crossAuthority, applied: crossApplied, finalURL: crossURL.String()},
	} {
		t.Run(isolated.name, func(t *testing.T) {
			request := requestWithHandle(http.MethodGet, isolated.client, handle)
			operation, err := fixture.ws.Begin(
				context.Background(), request, testAPIType, operationID("cookie-isolation", 1),
			)
			if err != nil {
				t.Fatal(err)
			}
			forwarded := request.Header.Clone()
			if _, err := operation.PrepareDial(
				context.Background(), forwarded, isolated.candidate, isolated.applied,
				websocketURL(t, mustParseURL(t, isolated.finalURL)),
			); err != nil {
				t.Fatal(err)
			}
			if got := forwarded.Get("Cookie"); got != "" {
				t.Fatalf("isolated scope received %q", got)
			}
		})
	}
}

func assertPendingMetadataVisibleOnlyToOwner(
	t *testing.T,
	fixture *runtimeFixture,
	client, metadata string,
	wantAuthority codexidentity.UpstreamAuthority,
) {
	t.Helper()
	owner, err := fixture.ws.Begin(
		context.Background(), fixtureRequest(http.MethodGet, client, http.Header{"X-Codex-Turn-Metadata": {metadata}}),
		testAPIType, operationID("metadata-pending-owner", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := owner.RequiredAuthority(); required == nil || !required.Equal(wantAuthority) {
		t.Fatalf("pending metadata owner = %v", required)
	}
	_, err = fixture.ws.Begin(
		context.Background(), fixtureRequest(http.MethodGet, "wrong-metadata-client", http.Header{
			"X-Codex-Turn-Metadata": {metadata},
		}), testAPIType, operationID("metadata-pending-wrong-client", 1),
	)
	requireWSFailure(t, err, codexws.FailureIdentity)
}

func assertMetadataRetrievalAcrossCarriers(
	t *testing.T,
	fixture *runtimeFixture,
	client, metadata string,
	wantAuthority codexidentity.UpstreamAuthority,
	replacement codexidentity.CandidateSnapshot,
	replacementApplied codexidentity.AppliedIdentity,
	finalURL *url.URL,
	crossAuthority codexidentity.CandidateSnapshot,
	crossApplied codexidentity.AppliedIdentity,
	crossURL *url.URL,
) {
	t.Helper()
	headers := http.Header{"X-Codex-Turn-Metadata": {metadata}}
	httpOperation, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, client, headers),
		testAPIType, operationID("metadata-read-http", 1), testHTTPClientEvidence(nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, route := httpOperation.RequiredAuthority(); required == nil || !required.Equal(wantAuthority) || route == "" {
		t.Fatalf("HTTP metadata constraint = %v/%q", required, route)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = finalURL
	if _, err := httpOperation.PrepareAttempt(context.Background(), upstream, replacement, replacementApplied); err != nil {
		t.Fatal("HTTP rejected same-authority metadata replacement:", err)
	}
	cross := fixtureRequest(http.MethodPost, "", nil)
	cross.URL = crossURL
	_, err = httpOperation.PrepareAttempt(context.Background(), cross, crossAuthority, crossApplied)
	requireHTTPError(t, err, codexhttp.ErrorIdentityMismatch)

	wsOperation, err := fixture.ws.Begin(
		context.Background(), fixtureRequest(http.MethodGet, client, headers),
		testAPIType, operationID("metadata-read-ws", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := wsOperation.RequiredAuthority(); required == nil || !required.Equal(wantAuthority) {
		t.Fatalf("WebSocket metadata constraint = %v", required)
	}
	if _, err := wsOperation.PrepareDial(
		context.Background(), headers.Clone(), replacement, replacementApplied, websocketURL(t, finalURL),
	); err != nil {
		t.Fatal("WebSocket rejected same-authority metadata replacement:", err)
	}
}

func assertPendingTurnStateVisibleOnlyToOwner(
	t *testing.T,
	fixture *runtimeFixture,
	client, turnState string,
	wantAuthority codexidentity.UpstreamAuthority,
) {
	t.Helper()
	headers := http.Header{"X-Codex-Turn-State": {turnState}}
	owner, err := fixture.ws.Begin(
		context.Background(), fixtureRequest(http.MethodGet, client, headers),
		testAPIType, operationID("state-pending-owner", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := owner.RequiredAuthority(); required == nil || !required.Equal(wantAuthority) {
		t.Fatalf("pending Turn State owner = %v", required)
	}
	_, err = fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "wrong-state-client", headers),
		testAPIType, operationID("state-pending-wrong-client", 1), testHTTPClientEvidence(nil, nil),
	)
	requireHTTPError(t, err, codexhttp.ErrorClientInput)
}

func assertTurnStateRetrievalAcrossCarriers(
	t *testing.T,
	fixture *runtimeFixture,
	client, turnState string,
	wantAuthority codexidentity.UpstreamAuthority,
	replacement codexidentity.CandidateSnapshot,
	replacementApplied codexidentity.AppliedIdentity,
	finalURL *url.URL,
) {
	t.Helper()
	headers := http.Header{"X-Codex-Turn-State": {turnState}}
	httpOperation, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, client, headers),
		testAPIType, operationID("state-read-http", 1), testHTTPClientEvidence(nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := httpOperation.RequiredAuthority(); required == nil || !required.Equal(wantAuthority) {
		t.Fatalf("HTTP Turn State owner = %v", required)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = finalURL
	if _, err := httpOperation.PrepareAttempt(context.Background(), upstream, replacement, replacementApplied); err != nil {
		t.Fatal("HTTP rejected same-authority Turn State replacement:", err)
	}

	wsOperation, err := fixture.ws.Begin(
		context.Background(), fixtureRequest(http.MethodGet, client, headers),
		testAPIType, operationID("state-read-ws", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := wsOperation.RequiredAuthority(); required == nil || !required.Equal(wantAuthority) {
		t.Fatalf("WebSocket Turn State owner = %v", required)
	}
	if _, err := wsOperation.PrepareDial(
		context.Background(), headers.Clone(), replacement, replacementApplied, websocketURL(t, finalURL),
	); err != nil {
		t.Fatal("WebSocket rejected same-authority Turn State replacement:", err)
	}
}

func buildCarrierRequest(
	t *testing.T,
	original *http.Request,
	policy upstreamtransport.RequestPolicy,
	target string,
) *http.Request {
	t.Helper()
	request, err := upstreamtransport.BuildRequestWithPolicy(
		context.Background(), http.MethodPost, target, nil, original, policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
