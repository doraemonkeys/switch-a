package codexintegration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestAlwaysOnPolicyAndClientRequestIDStayOperationScoped(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	original := fixtureRequest(http.MethodPost, "client-feature-snapshot", http.Header{
		"Cookie":              {"client_cookie=must-not-reach-provider"},
		"X-Client-Request-Id": {"stable-logical-request"},
	})

	httpOperation, err := fixture.http.Begin(
		context.Background(), original, testAPIType, operationID("http-always-on", 1), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.ws.Begin(
		context.Background(), original, testAPIType, operationID("ws-always-on", 1),
	); err != nil {
		t.Fatal(err)
	}

	policy := httpOperation.RequestPolicy()
	first, err := upstreamtransport.BuildRequestWithPolicy(
		context.Background(), http.MethodPost, "https://api.example.test/v1/responses", nil, original, policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	first.Header.Set("X-Client-Request-Id", "attempt-local-mutation")
	second, err := upstreamtransport.BuildRequestWithPolicy(
		context.Background(), http.MethodPost, "https://api.example.test/v1/responses", nil, original, policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Header.Get("X-Client-Request-Id"); got != "stable-logical-request" {
		t.Fatalf("retry X-Client-Request-Id = %q", got)
	}
	if got := second.Header.Get("Authorization"); got != "" {
		t.Fatalf("client Authorization reached retry: %q", got)
	}
	if got := second.Header.Get("Cookie"); got != "" {
		t.Fatalf("client Cookie reached server-managed retry: %q", got)
	}
}

func TestStructuredTraceAndSQLiteStaySecretFreeAcrossProtocols(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
	const (
		clientSecret = "client-trace-secret"
		threadID     = "trace-thread-secret"
		turnState    = "trace-turn-secret"
		cookieValue  = "trace-cookie-secret"
		httpID       = "wave4-trace-http"
		wsID         = "wave4-trace-ws"
	)

	httpRequest := fixtureRequest(http.MethodPost, clientSecret, http.Header{
		"Thread-Id":           {threadID},
		"X-Client-Request-Id": {"trace-logical-request"},
	})
	httpOperation, err := fixture.http.Begin(
		context.Background(), httpRequest, testAPIType, httpID, nil, nil,
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
	if err := attempt.ObserveResponse(&upstreamtransport.ResponseHead{
		SourceHeader: http.Header{"Set-Cookie": {"trace_cookie=" + cookieValue + "; Path=/; Secure"}},
		Header:       make(http.Header),
	}); err != nil {
		t.Fatal(err)
	}
	responseHeaders := http.Header{"X-Codex-Turn-State": {turnState}}
	visibility, err := attempt.PrepareVisible(context.Background(), responseHeaders)
	if err != nil {
		t.Fatal(err)
	}
	if err := visibility.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	handle := gatewayHandle(t, responseHeaders.Get("Set-Cookie"))

	wsRequest := requestWithHandle(http.MethodGet, clientSecret, handle)
	wsRequest.Header.Set("Thread-Id", threadID)
	wsRequest.Header.Set("X-Codex-Turn-State", turnState)
	wsOperation, err := fixture.ws.Begin(context.Background(), wsRequest, testAPIType, wsID)
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
	wsOperation.CloseConnection()

	continuityEvents, cookieEvents := fixture.trace.Snapshot()
	if len(continuityEvents) == 0 || len(cookieEvents) == 0 {
		t.Fatalf("trace event counts = continuity:%d cookie:%d", len(continuityEvents), len(cookieEvents))
	}
	var generationOpened, generationClosed bool
	for _, event := range continuityEvents {
		switch event.Action {
		case "generation_open":
			generationOpened = event.SessionID == wsID && event.Generation == "generation-1"
		case "generation_close":
			generationClosed = event.SessionID == wsID && event.Generation == "generation-1"
		default:
			if event.OperationID != httpID && event.OperationID != wsID {
				t.Fatalf("continuity event lacks stable operation identity: %#v", event)
			}
		}
	}
	if !generationOpened || !generationClosed {
		t.Fatalf("generation trace incomplete: opened=%v closed=%v", generationOpened, generationClosed)
	}
	for _, event := range cookieEvents {
		if event.OperationID != httpID && event.OperationID != wsID {
			t.Fatalf("cookie event lacks stable operation identity: %#v", event)
		}
	}

	traceJSON, err := json.Marshal(struct {
		Continuity any `json:"continuity"`
		Cookies    any `json:"cookies"`
	}{Continuity: continuityEvents, Cookies: cookieEvents})
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{clientSecret, threadID, turnState, cookieValue, handle, "fixture-provider-secret"}
	assertBytesExclude(t, "structured trace", traceJSON, forbidden)

	paths, err := filepath.Glob(fixture.databasePath + "*")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		assertBytesExclude(t, path, contents, forbidden)
	}
}

func TestAlwaysOnRuntimesTreatFutureContentAsOpaqueWithoutReleaseConfiguration(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	opaque := []byte("future protocol bytes that are intentionally not JSON")
	wire := append([]byte(nil), opaque...)
	if _, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "always-on-opaque-client", nil),
		testAPIType, operationID("release-opaque-http", 1), opaque, opaque,
	); err != nil {
		t.Fatal(err)
	}
	request := fixtureRequest(http.MethodGet, "always-on-opaque-client", nil)
	operation, err := fixture.ws.Begin(
		context.Background(), request, testAPIType, operationID("release-opaque-ws", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	frame := operation.ClassifyClientFrame(context.Background(), true, opaque)
	if frame.Disposition() != codexws.ClientFrameForward || frame.Trace().Kind != codexws.ClientFrameOpaque ||
		!frame.ReplayEligible() || !frame.ReplacementEligible() {
		t.Fatalf("always-on opaque permit = %#v, err %v", frame.Trace(), frame.Rejection())
	}
	if !bytes.Equal(opaque, wire) {
		t.Fatal("always-on runtime changed opaque release bytes")
	}
}

func assertBytesExclude(t *testing.T, source string, contents []byte, forbidden []string) {
	t.Helper()
	for _, secret := range forbidden {
		if bytes.Contains(contents, []byte(secret)) {
			t.Fatalf("%s contains raw secret %q", source, secret)
		}
	}
}
