package codexintegration_test

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestContinuityCrossesHTTPAndWebSocketWithinOneSecurityScope(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	base, baseApplied, baseURL := fixtureCandidate(t, candidateSpec{})
	replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})

	httpRequest := fixtureRequest(http.MethodPost, "client-alpha", http.Header{
		"Thread-Id": {"thread-cross-protocol"},
	})
	httpOperation, err := fixture.http.Begin(
		context.Background(), httpRequest, testAPIType, operationID("http", 1), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	upstreamHTTP := fixtureRequest(http.MethodPost, "", nil)
	upstreamHTTP.URL = baseURL
	httpAttempt, err := httpOperation.PrepareAttempt(context.Background(), upstreamHTTP, base, baseApplied)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpAttempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	httpResponseHeaders := http.Header{"X-Codex-Turn-State": {"turn-from-http"}}
	httpVisibility, err := httpAttempt.PrepareVisible(context.Background(), httpResponseHeaders)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpVisibility.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	wsRequest := fixtureRequest(http.MethodGet, "client-alpha", http.Header{
		"Thread-Id":          {"thread-cross-protocol"},
		"X-Codex-Turn-State": {"turn-from-http"},
	})
	wsOperation, err := fixture.ws.Begin(context.Background(), wsRequest, testAPIType, operationID("ws", 1))
	if err != nil {
		t.Fatal(err)
	}
	required, preferred := wsOperation.RequiredAuthority()
	if required == nil || !required.Equal(base.Authority()) || preferred != base.RouteTargetID() {
		t.Fatalf("WS owner constraint = %v/%q, want base authority/%q", required, preferred, base.RouteTargetID())
	}
	permit, err := wsOperation.PrepareDial(
		context.Background(), wsRequest.Header.Clone(), replacement, replacementApplied, websocketURL(t, baseURL),
	)
	if err != nil {
		t.Fatal("same-authority RouteTarget replacement was rejected:", err)
	}
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	identityFrame := []byte(`{"type":"response.create","client_metadata":{"session_id":"session-from-ws"}}`)
	identityPermit, err := wsOperation.PrepareClientFrame(context.Background(), true, identityFrame)
	if err != nil {
		t.Fatal(err)
	}
	if err := identityPermit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	stateFrame := []byte(`{"type":"codex.response.metadata","headers":{"x-codex-turn-state":"turn-from-ws"}}`)
	statePermit, err := wsOperation.PrepareServerFrame(context.Background(), true, stateFrame)
	if err != nil {
		t.Fatal(err)
	}
	if err := statePermit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	returnRequest := fixtureRequest(http.MethodPost, "client-alpha", http.Header{
		"Session-Id":         {"session-from-ws"},
		"X-Codex-Turn-State": {"turn-from-ws"},
	})
	returnOperation, err := fixture.http.Begin(
		context.Background(), returnRequest, testAPIType, operationID("http", 2), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	required, preferred = returnOperation.RequiredAuthority()
	if required == nil || !required.Equal(base.Authority()) || preferred != replacement.RouteTargetID() {
		t.Fatalf("HTTP owner constraint = %v/%q, want base authority/%q", required, preferred, replacement.RouteTargetID())
	}
	retryRequest := fixtureRequest(http.MethodPost, "", nil)
	retryRequest.URL = baseURL
	if _, err := returnOperation.PrepareAttempt(
		context.Background(), retryRequest, replacement, replacementApplied,
	); err != nil {
		t.Fatal("HTTP did not continue WS-owned state on the same authority:", err)
	}
}

func TestClientRetryAfterVisibleUpstreamErrorKeepsProviderContinuity(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	original, originalApplied, originalURL := fixtureCandidate(t, candidateSpec{})
	replacement, replacementApplied, replacementURL := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
	crossAuthority, crossAuthorityApplied, crossAuthorityURL := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-cross", subject: "subject-b",
	})
	const (
		clientAPIKey    = "client-retry-key"
		threadID        = "thread-visible-error-retry"
		turnState       = "turn-visible-error-retry"
		clientRequestID = "logical-request-visible-error"
	)

	seedRequest := fixtureRequest(http.MethodPost, clientAPIKey, http.Header{"Thread-Id": {threadID}})
	seedOperation, err := fixture.http.Begin(
		context.Background(), seedRequest, testAPIType, operationID("seed-visible-error", 1), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	seedUpstream := fixtureRequest(http.MethodPost, "", nil)
	seedUpstream.URL = originalURL
	seedAttempt, err := seedOperation.PrepareAttempt(
		context.Background(), seedUpstream, original, originalApplied,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedAttempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	seedVisibility, err := seedAttempt.PrepareVisible(context.Background(), http.Header{
		"X-Codex-Turn-State": {turnState},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedVisibility.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	errorRequest := fixtureRequest(http.MethodPost, clientAPIKey, http.Header{
		"Thread-Id":           {threadID},
		"X-Codex-Turn-State":  {turnState},
		"X-Client-Request-Id": {clientRequestID},
	})
	errorOperation, err := fixture.http.Begin(
		context.Background(), errorRequest, testAPIType, operationID("visible-error", 1), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	errorUpstream, err := upstreamtransport.BuildRequestWithPolicy(
		context.Background(), http.MethodPost, originalURL.String(), nil, errorRequest, errorOperation.RequestPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	errorAttempt, err := errorOperation.PrepareAttempt(
		context.Background(), errorUpstream, original, originalApplied,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := errorAttempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Status is transport metadata, so committing an empty visible header set models
	// both a surfaced 429 and 5xx without changing continuity ownership.
	errorVisibility, err := errorAttempt.PrepareVisible(context.Background(), make(http.Header))
	if err != nil {
		t.Fatal(err)
	}
	if err := errorVisibility.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	errorOperation.Discard()

	retryRequest := fixtureRequest(http.MethodPost, clientAPIKey, http.Header{
		"Thread-Id":           {threadID},
		"X-Codex-Turn-State":  {turnState},
		"X-Client-Request-Id": {clientRequestID},
	})
	retryOperation, err := fixture.http.Begin(
		context.Background(), retryRequest, testAPIType, operationID("visible-error", 2), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	required, preferred := retryOperation.RequiredAuthority()
	if required == nil || !required.Equal(original.Authority()) || preferred != original.RouteTargetID() {
		t.Fatalf("retry owner constraint = %v/%q, want original authority/%q", required, preferred, original.RouteTargetID())
	}

	retryUpstream, err := upstreamtransport.BuildRequestWithPolicy(
		context.Background(), http.MethodPost, originalURL.String(), nil, retryRequest, retryOperation.RequestPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retryOperation.PrepareAttempt(
		context.Background(), retryUpstream, original, originalApplied,
	); err != nil {
		t.Fatal("retry to the original provider was rejected:", err)
	}
	if got := retryUpstream.Header.Get("X-Codex-Turn-State"); got != turnState {
		t.Fatalf("retry turn state = %q, want %q", got, turnState)
	}
	if got := retryUpstream.Header.Get("X-Client-Request-Id"); got != clientRequestID {
		t.Fatalf("retry client request ID = %q, want %q", got, clientRequestID)
	}
	if got := retryUpstream.Header.Get("Authorization"); got != "" {
		t.Fatalf("client API key leaked upstream on retry: %q", got)
	}

	replacementUpstream, err := upstreamtransport.BuildRequestWithPolicy(
		context.Background(), http.MethodPost, replacementURL.String(), nil, retryRequest, retryOperation.RequestPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retryOperation.PrepareAttempt(
		context.Background(), replacementUpstream, replacement, replacementApplied,
	); err != nil {
		t.Fatal("same-authority replacement was rejected:", err)
	}

	crossAuthorityUpstream, err := upstreamtransport.BuildRequestWithPolicy(
		context.Background(), http.MethodPost, crossAuthorityURL.String(), nil, retryRequest, retryOperation.RequestPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = retryOperation.PrepareAttempt(
		context.Background(), crossAuthorityUpstream, crossAuthority, crossAuthorityApplied,
	)
	requireHTTPError(t, err, codexhttp.ErrorIdentityMismatch)

	_, err = fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "different-client-key", http.Header{
			"X-Codex-Turn-State": {turnState},
		}), testAPIType, operationID("visible-error", 3), nil, nil,
	)
	requireHTTPError(t, err, codexhttp.ErrorClientInput)
}

func TestWebSocketResponseReferenceContinuesThroughHTTPPreviousResponse(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})

	wsRequest := fixtureRequest(http.MethodGet, "client-alpha", nil)
	wsOperation, err := fixture.ws.Begin(
		context.Background(), wsRequest, testAPIType, operationID("ws-response", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	permit, err := wsOperation.PrepareDial(
		context.Background(), wsRequest.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := wsOperation.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	created := []byte(`{"type":"response.created","response":{"id":"response-cross-protocol"}}`)
	createdPermit, err := wsOperation.PrepareServerFrame(context.Background(), true, created)
	if err != nil {
		t.Fatal(err)
	}
	if err := createdPermit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	wsOperation.CloseConnection()

	previous := []byte(`{"type":"response.create","previous_response_id":"response-cross-protocol"}`)
	httpOperation, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "client-alpha", nil),
		testAPIType, operationID("http-previous", 1), previous, previous,
	)
	if err != nil {
		t.Fatal(err)
	}
	required, preferred := httpOperation.RequiredAuthority()
	if required == nil || !required.Equal(candidate.Authority()) || preferred != candidate.RouteTargetID() {
		t.Fatalf("HTTP previous-response owner = %v/%q, want candidate authority/%q", required, preferred, candidate.RouteTargetID())
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = finalURL
	if _, err := httpOperation.PrepareAttempt(context.Background(), upstream, candidate, applied); err != nil {
		t.Fatal("HTTP did not continue the WS-created response reference:", err)
	}
}

func TestHTTPSSEResponseReferenceContinuesThroughHTTPAndWebSocket(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})

	httpRequest := fixtureRequest(http.MethodPost, "client-alpha", http.Header{"Thread-Id": {"http-sse-scope"}})
	httpOperation, err := fixture.http.Begin(
		context.Background(), httpRequest, testAPIType, operationID("http-sse", 1), nil, nil,
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
	if err := attempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	gate := attempt.NewSSEGate()
	if gate == nil {
		t.Fatal("continuity-enabled HTTP attempt did not create an SSE gate")
	}
	raw := []byte("event: response.created\r\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"response-from-http-sse\"}}\r\n\r\n")
	gate.Append(raw)
	event, ready, err := gate.PrepareNext(context.Background(), false)
	if err != nil || !ready {
		t.Fatalf("HTTP SSE response event = (%#v, %t, %v)", event, ready, err)
	}
	if !bytes.Equal(event.ReplayBytes(), raw) {
		t.Fatal("HTTP SSE gate changed client-visible bytes")
	}
	if err := event.Visibility().Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	gate.Consume(len(event.ReplayBytes()))

	previous := []byte(`{"type":"response.create","previous_response_id":"response-from-http-sse"}`)
	returnHTTP, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "client-alpha", nil),
		testAPIType, operationID("http-sse", 2), previous, previous,
	)
	if err != nil {
		t.Fatal(err)
	}
	required, preferred := returnHTTP.RequiredAuthority()
	if required == nil || !required.Equal(candidate.Authority()) || preferred != candidate.RouteTargetID() {
		t.Fatalf("HTTP SSE owner through HTTP = %v/%q", required, preferred)
	}

	wsRequest := fixtureRequest(http.MethodGet, "client-alpha", nil)
	returnWS, err := fixture.ws.Begin(
		context.Background(), wsRequest, testAPIType, operationID("ws-sse", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if returnWS.NeedsOwnerBootstrap() {
		t.Fatal("ordinary WS continuation unexpectedly required Probe")
	}
	replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
	if _, err := returnWS.PrepareDial(
		context.Background(), wsRequest.Header.Clone(), replacement, replacementApplied, websocketURL(t, finalURL),
	); err != nil {
		t.Fatal("ordinary WS could not select a same-scope replacement before reading continuation:", err)
	}
	frame := returnWS.ClassifyClientFrame(context.Background(), true, previous)
	if frame.Disposition() != codexws.ClientFrameForward || !frame.ReplayEligible() || frame.CurrentConnectionRequired() {
		t.Fatalf("WS continuation frame = %#v, err %v", frame.Trace(), frame.Rejection())
	}
	framePermit, err := frame.PrepareDelivery(context.Background())
	if err != nil {
		t.Fatal("WS did not continue the HTTP-SSE-created response reference:", err)
	}
	if err := framePermit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	required, preferred = returnWS.RequiredAuthority()
	if required == nil || !required.Equal(candidate.Authority()) || preferred != candidate.RouteTargetID() {
		t.Fatalf("HTTP SSE owner through WS = %v/%q", required, preferred)
	}
}

func TestContinuityIsolationMatrixAndIdentityDropSemantics(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	base, baseApplied, baseURL := fixtureCandidate(t, candidateSpec{})
	seed := fixtureRequest(http.MethodPost, "client-alpha", http.Header{
		"Thread-Id": {"identity-isolation"},
	})
	seedOperation, err := fixture.http.Begin(context.Background(), seed, testAPIType, operationID("http", 3), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	seedUpstream := fixtureRequest(http.MethodPost, "", nil)
	seedUpstream.URL = baseURL
	seedAttempt, err := seedOperation.PrepareAttempt(context.Background(), seedUpstream, base, baseApplied)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedAttempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	visible, err := seedAttempt.PrepareVisible(context.Background(), http.Header{
		"X-Codex-Turn-State": {"state-isolation"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := visible.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	wrongClient := fixtureRequest(http.MethodGet, "client-beta", http.Header{"Thread-Id": {"identity-isolation"}})
	wrongClientOperation, err := fixture.ws.Begin(
		context.Background(), wrongClient, testAPIType, operationID("ws", 2),
	)
	if err != nil {
		t.Fatal("a pure identity Header conflict should be dropped, not rejected:", err)
	}
	if required, preferred := wrongClientOperation.RequiredAuthority(); required != nil || preferred != "" {
		t.Fatalf("wrong client reused identity owner: %v/%q", required, preferred)
	}
	forwardHeaders := wrongClient.Header.Clone()
	if _, err := wrongClientOperation.PrepareDial(
		context.Background(), forwardHeaders, base, baseApplied, websocketURL(t, baseURL),
	); err != nil {
		t.Fatal(err)
	}
	if forwardHeaders.Get("Thread-Id") != "" {
		t.Fatalf("conflicting identity Header reached upstream: %#v", forwardHeaders)
	}

	wrongState := fixtureRequest(http.MethodGet, "client-beta", http.Header{
		"X-Codex-Turn-State": {"state-isolation"},
	})
	_, err = fixture.ws.Begin(context.Background(), wrongState, testAPIType, operationID("ws", 3))
	requireWSFailure(t, err, codexws.FailureIdentity)

	variations := []struct {
		name string
		spec candidateSpec
	}{
		{name: "Vendor", spec: candidateSpec{vendor: "other-vendor"}},
		{name: "CredentialSubject", spec: candidateSpec{subject: "subject-b"}},
		{name: "Origin", spec: candidateSpec{requestURL: "https://other.example.test/v1/responses"}},
		{name: "APIType", spec: candidateSpec{apiType: "codex-alt"}},
	}
	for index, variation := range variations {
		t.Run(variation.name, func(t *testing.T) {
			candidate, applied, finalURL := fixtureCandidate(t, variation.spec)
			request := fixtureRequest(http.MethodGet, "client-alpha", http.Header{"Thread-Id": {"identity-isolation"}})
			operation, beginErr := fixture.ws.Begin(
				context.Background(), request, testAPIType, operationID("ws-isolation", index+1),
			)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			_, prepareErr := operation.PrepareDial(
				context.Background(), request.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
			)
			requireWSFailure(t, prepareErr, codexws.FailureIdentity)
		})
	}

	replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
	replacementRequest := fixtureRequest(http.MethodGet, "client-alpha", http.Header{"Thread-Id": {"identity-isolation"}})
	replacementOperation, err := fixture.ws.Begin(
		context.Background(), replacementRequest, testAPIType, operationID("ws", 4),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacementOperation.PrepareDial(
		context.Background(), replacementRequest.Header.Clone(), replacement, replacementApplied, websocketURL(t, baseURL),
	); err != nil {
		t.Fatal("same Authority with a different RouteTarget was rejected:", err)
	}
}

func TestConcurrentCrossProtocolClaimsAreAtomic(t *testing.T) {
	t.Run("same owner converges", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		identity := []byte(`{"type":"response.create","client_metadata":{"session_id":"concurrent-shared"}}`)
		httpOperation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "client-alpha", nil),
			testAPIType, operationID("http-concurrent", 1), identity, identity,
		)
		if err != nil {
			t.Fatal(err)
		}
		wsRequest := fixtureRequest(http.MethodGet, "client-alpha", nil)
		wsOperation, err := fixture.ws.Begin(
			context.Background(), wsRequest, testAPIType, operationID("ws-concurrent", 1),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := wsOperation.InspectBootstrapFrame(context.Background(), true, identity); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			upstream := fixtureRequest(http.MethodPost, "", nil)
			upstream.URL = finalURL
			attempt, prepareErr := httpOperation.PrepareAttempt(context.Background(), upstream, candidate, applied)
			if prepareErr == nil {
				prepareErr = attempt.MarkDisclosed(context.Background())
			}
			results <- prepareErr
		}()
		go func() {
			<-start
			permit, prepareErr := wsOperation.PrepareDial(
				context.Background(), wsRequest.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
			)
			if prepareErr == nil {
				prepareErr = permit.Commit(context.Background())
			}
			if prepareErr == nil {
				permit, prepareErr = wsOperation.PrepareClientFrame(context.Background(), true, identity)
			}
			if prepareErr == nil {
				prepareErr = permit.Commit(context.Background())
			}
			results <- prepareErr
		}()
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		var count int64
		if err := fixture.db.Table("codex_continuity_bindings").Where("kind = ?", codexcontinuity.KindSessionID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("durable session owners = %d, want 1", count)
		}
	})

	t.Run("competing authorities have one winner", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		base, baseApplied, baseURL := fixtureCandidate(t, candidateSpec{})
		other, otherApplied, otherURL := fixtureCandidate(t, candidateSpec{
			routeTarget: "route-other", requestURL: "https://other.example.test/v1/responses",
		})
		identity := []byte(`{"type":"response.create","client_metadata":{"session_id":"concurrent-conflict"}}`)
		httpOperation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "client-alpha", nil),
			testAPIType, operationID("http-concurrent", 2), identity, identity,
		)
		if err != nil {
			t.Fatal(err)
		}
		wsRequest := fixtureRequest(http.MethodGet, "client-alpha", nil)
		wsOperation, err := fixture.ws.Begin(
			context.Background(), wsRequest, testAPIType, operationID("ws-concurrent", 2),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := wsOperation.InspectBootstrapFrame(context.Background(), true, identity); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			upstream := fixtureRequest(http.MethodPost, "", nil)
			upstream.URL = baseURL
			attempt, prepareErr := httpOperation.PrepareAttempt(context.Background(), upstream, base, baseApplied)
			if prepareErr == nil {
				prepareErr = attempt.MarkDisclosed(context.Background())
			}
			results <- prepareErr
		}()
		go func() {
			defer group.Done()
			<-start
			permit, prepareErr := wsOperation.PrepareDial(
				context.Background(), wsRequest.Header.Clone(), other, otherApplied, websocketURL(t, otherURL),
			)
			if prepareErr == nil {
				prepareErr = permit.Commit(context.Background())
			}
			if prepareErr == nil {
				permit, prepareErr = wsOperation.PrepareClientFrame(context.Background(), true, identity)
			}
			if prepareErr == nil {
				prepareErr = permit.Commit(context.Background())
			}
			results <- prepareErr
		}()
		close(start)
		group.Wait()
		close(results)
		succeeded, failed := 0, 0
		for result := range results {
			if result == nil {
				succeeded++
			} else {
				failed++
			}
		}
		if succeeded != 1 || failed != 1 {
			t.Fatalf("concurrent authority outcomes = success:%d failure:%d", succeeded, failed)
		}
	})
}

func TestPendingCapacityAndStoreFailuresRemainCrossProtocolBoundaries(t *testing.T) {
	t.Run("uncertain HTTP visibility retains owner for WebSocket", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		operation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "client-alpha", http.Header{"Thread-Id": {"uncertain-http-scope"}}),
			testAPIType, operationID("http", 5), nil, nil,
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
		visibility, err := attempt.PrepareVisible(context.Background(), http.Header{
			"X-Codex-Turn-State": {"uncertain-turn-state"},
		})
		if err != nil || visibility == nil {
			t.Fatalf("prepare uncertain visibility = %#v, %v", visibility, err)
		}

		sameOwner := fixtureRequest(http.MethodGet, "client-alpha", http.Header{
			"X-Codex-Turn-State": {"uncertain-turn-state"},
		})
		continued, err := fixture.ws.Begin(
			context.Background(), sameOwner, testAPIType, operationID("ws", 5),
		)
		if err != nil {
			t.Fatal("pending owner was not reusable by its original scope:", err)
		}
		if required, _ := continued.RequiredAuthority(); required == nil || !required.Equal(candidate.Authority()) {
			t.Fatalf("pending owner constraint = %v", required)
		}
		_, err = fixture.ws.Begin(
			context.Background(), fixtureRequest(http.MethodGet, "client-beta", http.Header{
				"X-Codex-Turn-State": {"uncertain-turn-state"},
			}), testAPIType, operationID("ws", 6),
		)
		requireWSFailure(t, err, codexws.FailureIdentity)
	})

	t.Run("continuity capacity is shared", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{
			continuityMax: 1,
		})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		seedOperation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "client-alpha", http.Header{"Thread-Id": {"capacity-one"}}),
			testAPIType, operationID("http-capacity", 1), nil, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		upstream := fixtureRequest(http.MethodPost, "", nil)
		upstream.URL = finalURL
		seedAttempt, err := seedOperation.PrepareAttempt(context.Background(), upstream, candidate, applied)
		if err != nil {
			t.Fatal(err)
		}
		if err := seedAttempt.MarkDisclosed(context.Background()); err != nil {
			t.Fatal(err)
		}

		request := fixtureRequest(http.MethodGet, "client-alpha", http.Header{"Thread-Id": {"capacity-two"}})
		operation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operationID("ws-capacity", 1))
		if err != nil {
			t.Fatal(err)
		}
		_, err = operation.PrepareDial(
			context.Background(), request.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
		)
		requireWSFailure(t, err, codexws.FailureStorage)
		if !codexcontinuity.IsError(err, codexcontinuity.ErrorCapacity) {
			t.Fatalf("capacity failure lost typed cause: %v", err)
		}
	})

	t.Run("closed shared store fails both adapters", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		seedOperation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "client-alpha", http.Header{"Thread-Id": {"store-http-scope"}}),
			testAPIType, operationID("http-store", 1), nil, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		upstream := fixtureRequest(http.MethodPost, "", nil)
		upstream.URL = finalURL
		attempt, err := seedOperation.PrepareAttempt(context.Background(), upstream, candidate, applied)
		if err != nil {
			t.Fatal(err)
		}
		visibility, err := attempt.PrepareVisible(context.Background(), http.Header{
			"X-Codex-Turn-State": {"store-failure-state"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := visibility.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		sqlDB, err := fixture.db.DB()
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatal(err)
		}

		stateHeaders := http.Header{"X-Codex-Turn-State": {"store-failure-state"}}
		_, err = fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "client-alpha", stateHeaders),
			testAPIType, operationID("http-store", 2), nil, nil,
		)
		requireHTTPError(t, err, codexhttp.ErrorDependencyUnavailable)
		_, err = fixture.ws.Begin(
			context.Background(), fixtureRequest(http.MethodGet, "client-alpha", stateHeaders),
			testAPIType, operationID("ws-store", 1),
		)
		requireWSFailure(t, err, codexws.FailureStorage)
	})
}

func TestResponseReferenceAndConnectionControlsHonorTheirNativeBoundaries(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	previous := []byte(`{"type":"response.create","previous_response_id":"unknown-response"}`)
	_, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "client-alpha", nil),
		testAPIType, operationID("http-evidence", 1), previous, previous,
	)
	requireHTTPError(t, err, codexhttp.ErrorClientInput)

	candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
	request := fixtureRequest(http.MethodGet, "client-alpha", nil)
	operation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operationID("ws-evidence", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.PrepareDial(
		context.Background(), request.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
	); err != nil {
		t.Fatal(err)
	}
	_, err = operation.PrepareClientFrame(context.Background(), true, previous)
	requireWSFailure(t, err, codexws.FailureIdentity)

	for _, event := range []string{"response.append", "response.inject"} {
		payload := []byte(`{"type":"` + event + `","response_id":{"opaque":"upstream-owned"}}`)
		frame := operation.ClassifyClientFrame(context.Background(), true, payload)
		if frame.Disposition() != codexws.ClientFrameReject || !frame.CurrentConnectionRequired() ||
			frame.ReplayEligible() || frame.ReplacementEligible() {
			t.Fatalf("connection-free %s = %#v, err %v", event, frame.Trace(), frame.Rejection())
		}
		requireWSFailure(t, frame.Rejection(), codexws.FailureIdentity)
	}

	if err := operation.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	defer operation.CloseConnection()
	for _, event := range []string{"response.append", "response.inject"} {
		payload := []byte(`{"type":"` + event + `","response_id":{"opaque":"upstream-owned"}}`)
		wire := append([]byte(nil), payload...)
		frame := operation.ClassifyClientFrame(context.Background(), true, payload)
		if frame.Disposition() != codexws.ClientFrameForward || frame.ReplayEligible() ||
			frame.ReplacementEligible() || !frame.CurrentConnectionRequired() {
			t.Fatalf("connected %s = %#v, err %v", event, frame.Trace(), frame.Rejection())
		}
		delivery, prepareErr := frame.PrepareDelivery(context.Background())
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		if err := delivery.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(payload, wire) {
			t.Fatalf("%s wire bytes changed", event)
		}
	}
	if operation.ReplacementAllowed() {
		t.Fatal("a delivered connection-bound control left provider replacement open")
	}

	var responseReferenceRows int64
	if err := fixture.db.Table("codex_continuity_bindings").
		Where("kind = ?", codexcontinuity.KindResponseReference).
		Count(&responseReferenceRows).Error; err != nil {
		t.Fatal(err)
	}
	if responseReferenceRows != 0 {
		t.Fatalf("evidence-unavailable paths fabricated %d response-reference owners", responseReferenceRows)
	}
}
