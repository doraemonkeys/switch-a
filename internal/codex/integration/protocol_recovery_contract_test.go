package codexintegration_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
)

const protocolFixtureDirectory = "codex-desktop-0.150.0-alpha.8"

func TestCreateAcceptanceAcrossHTTPJSONSSEAndWebSocket(t *testing.T) {
	create := readProtocolGolden(t, "ws-client-response-create-warmup.json")
	createdSSE := readProtocolGolden(t, "http-sse-response-created.txt")

	t.Run("HTTP JSON", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		wire := append([]byte(nil), create...)
		operation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "create-http-client", nil),
			testAPIType, operationID("create-http-json", 1), testHTTPClientEvidence(create, create),
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
		if err := attempt.MarkDisclosed(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(create, wire) {
			t.Fatal("HTTP create inspection changed captured wire bytes")
		}
		if required, _ := operation.RequiredAuthority(); required == nil || !required.Equal(candidate.Authority()) {
			t.Fatalf("HTTP create owner = %v", required)
		}
	})

	t.Run("HTTP SSE", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		operation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "create-sse-client", nil),
			testAPIType, operationID("create-http-sse", 1), testHTTPClientEvidence(create, create),
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
		if err := attempt.MarkDisclosed(context.Background()); err != nil {
			t.Fatal(err)
		}
		gate := attempt.NewSSEGate()
		gate.Append(createdSSE)
		event, ready, err := gate.PrepareNext(context.Background(), false)
		if err != nil || !ready {
			t.Fatalf("captured SSE response.created = ready:%v err:%v", ready, err)
		}
		if !bytes.Equal(event.ReplayBytes(), createdSSE) {
			t.Fatal("HTTP/SSE create changed captured event bytes")
		}
		if err := event.Visibility().Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("WebSocket", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
		request := fixtureRequest(http.MethodGet, "create-ws-client", nil)
		operation, err := fixture.ws.Begin(
			context.Background(), request, testAPIType, operationID("create-ws", 1),
		)
		if err != nil {
			t.Fatal(err)
		}
		dial, err := operation.PrepareDial(
			context.Background(), request.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := dial.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := operation.OpenConnection(); err != nil {
			t.Fatal(err)
		}
		defer operation.CloseConnection()

		wire := append([]byte(nil), create...)
		frame := operation.ClassifyClientFrame(context.Background(), true, create)
		if frame.Disposition() != codexws.ClientFrameForward || !frame.IsResponseCreate() ||
			!frame.ReplayEligible() || !frame.ReplacementEligible() {
			t.Fatalf("WS create permit = %#v, err %v", frame.Trace(), frame.Rejection())
		}
		delivery, err := frame.PrepareDelivery(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := delivery.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(create, wire) {
			t.Fatal("WS create inspection changed captured wire bytes")
		}
	})
}

func TestWebSocketAppendAndInjectAreOpaqueCurrentConnectionControls(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
	request := fixtureRequest(http.MethodGet, "controls-client", nil)
	operation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operationID("controls", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.PrepareDial(
		context.Background(), request.Header.Clone(), candidate, applied, websocketURL(t, finalURL),
	); err != nil {
		t.Fatal(err)
	}
	if err := operation.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	defer operation.CloseConnection()

	beforeRows := countContinuityBindings(t, fixture)
	for _, event := range []string{"response.append", "response.inject"} {
		t.Run(event, func(t *testing.T) {
			payload := []byte(" \n{\"type\":\"" + event + "\",\"response_id\":{\"unknown_shape\":[1,2,3]}}\t")
			wire := append([]byte(nil), payload...)
			frame := operation.ClassifyClientFrame(context.Background(), true, payload)
			if frame.Disposition() != codexws.ClientFrameForward || frame.ReplayEligible() ||
				frame.ReplacementEligible() || !frame.CurrentConnectionRequired() {
				t.Fatalf("%s permit = %#v, err %v", event, frame.Trace(), frame.Rejection())
			}
			delivery, prepareErr := frame.PrepareDelivery(context.Background())
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			if err := delivery.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, wire) {
				t.Fatalf("%s payload was normalized or inspected beyond its type", event)
			}
		})
	}
	if operation.ReplacementAllowed() {
		t.Fatal("delivered connection-bound controls did not close provider replacement")
	}
	if rows := countContinuityBindings(t, fixture); rows != beforeRows {
		t.Fatalf("append/inject fabricated continuity owners: before=%d after=%d", beforeRows, rows)
	}
	operation.CloseConnection()
	frame := operation.ClassifyClientFrame(context.Background(), true, []byte(`{"type":"response.append"}`))
	if frame.Disposition() != codexws.ClientFrameReject || !frame.CurrentConnectionRequired() {
		t.Fatalf("disconnected append = %#v, err %v", frame.Trace(), frame.Rejection())
	}
}

func TestPreviousResponseSurvivesDisconnectAndMixedCarrierReconnect(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	original, originalApplied, finalURL := fixtureCandidate(t, candidateSpec{})
	replacement, replacementApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
	const responseID = "response-reconnect-acceptance"

	seedRequest := fixtureRequest(http.MethodGet, "reconnect-client", nil)
	seed, err := fixture.ws.Begin(context.Background(), seedRequest, testAPIType, operationID("reconnect-seed", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.PrepareDial(
		context.Background(), seedRequest.Header.Clone(), original, originalApplied, websocketURL(t, finalURL),
	); err != nil {
		t.Fatal(err)
	}
	if err := seed.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	created, err := seed.PrepareServerFrame(
		context.Background(), true, []byte(`{"type":"response.created","response":{"id":"`+responseID+`"}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	seed.CloseConnection()

	previous := []byte(`{"type":"response.create","previous_response_id":"` + responseID + `"}`)
	reconnectRequest := fixtureRequest(http.MethodGet, "reconnect-client", nil)
	reconnect, err := fixture.ws.Begin(
		context.Background(), reconnectRequest, testAPIType, operationID("reconnect-ws", 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconnect.PrepareDial(
		context.Background(), reconnectRequest.Header.Clone(), replacement, replacementApplied, websocketURL(t, finalURL),
	); err != nil {
		t.Fatal("same-scope RouteTarget changed across reconnect:", err)
	}
	frame := reconnect.ClassifyClientFrame(context.Background(), true, previous)
	if frame.Disposition() != codexws.ClientFrameForward || frame.CurrentConnectionRequired() {
		t.Fatalf("reconnected previous response = %#v, err %v", frame.Trace(), frame.Rejection())
	}
	if _, err := frame.PrepareDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}

	httpOperation, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "reconnect-client", nil),
		testAPIType, operationID("reconnect-http", 1), testHTTPClientEvidence(previous, previous),
	)
	if err != nil {
		t.Fatal(err)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = finalURL
	if _, err := httpOperation.PrepareAttempt(
		context.Background(), upstream, replacement, replacementApplied,
	); err != nil {
		t.Fatal("HTTP did not retrieve the WS response after reconnect:", err)
	}
	_, err = fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "other-reconnect-client", nil),
		testAPIType, operationID("reconnect-wrong-client", 1), testHTTPClientEvidence(previous, previous),
	)
	requireHTTPError(t, err, codexhttp.ErrorClientInput)
}

func TestCompactionAndFutureContentRemainOpaqueAcrossCarriers(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	beforeRows := countContinuityBindings(t, fixture)
	payload := []byte(" \n{\"type\":\"response.compact\",\"future\":{\"thread_id\":null}}\t")
	wire := append([]byte(nil), payload...)
	httpOperation, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, "opaque-client", nil),
		testAPIType, operationID("opaque-http", 1), testHTTPClientEvidence(payload, payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, route := httpOperation.RequiredAuthority(); required != nil || route != "" {
		t.Fatalf("opaque HTTP compaction changed routing: %v/%q", required, route)
	}
	if !bytes.Equal(payload, wire) {
		t.Fatal("opaque HTTP compaction bytes changed")
	}

	candidate, applied, finalURL := fixtureCandidate(t, candidateSpec{})
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = finalURL
	attempt, err := httpOperation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	sse := []byte("event: response.compaction\r\ndata: future non-json wire\r\n\r\n")
	gate := attempt.NewSSEGate()
	gate.Append(sse)
	event, ready, err := gate.PrepareNext(context.Background(), false)
	if err != nil || !ready || !bytes.Equal(event.ReplayBytes(), sse) {
		t.Fatalf("opaque SSE compaction = ready:%v bytes:%q err:%v", ready, event.ReplayBytes(), err)
	}

	wsRequest := fixtureRequest(http.MethodGet, "opaque-client", nil)
	wsOperation, err := fixture.ws.Begin(context.Background(), wsRequest, testAPIType, operationID("opaque-ws", 1))
	if err != nil {
		t.Fatal(err)
	}
	textFrame := wsOperation.ClassifyClientFrame(context.Background(), true, payload)
	binaryFrame := wsOperation.ClassifyClientFrame(context.Background(), false, []byte{0x00, 0xff, 0x10})
	for _, frame := range []*codexws.ClientFramePermit{textFrame, binaryFrame} {
		if frame.Disposition() != codexws.ClientFrameForward || frame.Trace().Kind != codexws.ClientFrameOpaque ||
			!frame.ReplayEligible() || !frame.ReplacementEligible() {
			t.Fatalf("opaque WS permit = %#v, err %v", frame.Trace(), frame.Rejection())
		}
	}
	if rows := countContinuityBindings(t, fixture); rows != beforeRows {
		t.Fatalf("opaque content changed continuity rows: before=%d after=%d", beforeRows, rows)
	}
}

func TestOrdinaryWebSocketReselectionEndsOnlyAtVisibleCommit(t *testing.T) {
	first, firstApplied, firstURL := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-first", requestURL: "https://first.example.test/v1/responses",
	})
	second, secondApplied, secondURL := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-second", requestURL: "https://second.example.test/v1/responses",
	})
	sameAuthorityRoute, sameAuthorityApplied, _ := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-same-authority", requestURL: "https://first.example.test/v1/responses",
	})

	t.Run("ordinary 101 and failed projection remain replaceable", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		request := fixtureRequest(http.MethodGet, "replace-before-visible", nil)
		operation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operationID("replace-101", 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := operation.PrepareDial(context.Background(), request.Header.Clone(), first, firstApplied, websocketURL(t, firstURL)); err != nil {
			t.Fatal(err)
		}
		if err := operation.ApplyHandshake(websocketURL(t, firstURL), http.Header{
			"Set-Cookie": {"abandoned=first; Path=/; Secure"},
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := operation.PrepareServerHeaders(context.Background(), http.Header{
			"X-Codex-Turn-State": {"", "invalid-duplicate"},
		}); err == nil {
			t.Fatal("malformed projected Turn State unexpectedly succeeded")
		}
		if _, err := operation.PrepareDial(context.Background(), request.Header.Clone(), second, secondApplied, websocketURL(t, secondURL)); err != nil {
			t.Fatal("ordinary 101 or failed projection pinned the abandoned provider:", err)
		}
	})

	t.Run("committed Turn State pins its RouteTarget", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		request := fixtureRequest(http.MethodGet, "turn-state-visible", nil)
		operation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operationID("visible-state", 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := operation.PrepareDial(context.Background(), request.Header.Clone(), first, firstApplied, websocketURL(t, firstURL)); err != nil {
			t.Fatal(err)
		}
		permit, projected, err := operation.PrepareServerHeaders(context.Background(), http.Header{
			"X-Codex-Turn-State": {"visible-turn-state"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if projected.Get("X-Codex-Turn-State") != "visible-turn-state" || !permit.PinsRouteTarget() {
			t.Fatalf("Turn State projection = %#v, pins=%v", projected, permit.PinsRouteTarget())
		}
		if err := permit.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := operation.CommitVisibility(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := operation.PrepareDial(
			context.Background(), request.Header.Clone(), sameAuthorityRoute, sameAuthorityApplied, websocketURL(t, firstURL),
		); codexws.Classify(err) != codexws.FailureIdentity {
			t.Fatalf("post-visible same-authority RouteTarget changed: class=%q err=%v", codexws.Classify(err), err)
		}
	})

	t.Run("failed first-frame write remains replaceable; successful write pins", func(t *testing.T) {
		fixture := newRuntimeFixture(t, fixtureOptions{})
		failedRequest := fixtureRequest(http.MethodGet, "failed-frame", nil)
		failed, err := fixture.ws.Begin(context.Background(), failedRequest, testAPIType, operationID("failed-frame", 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := failed.PrepareDial(context.Background(), failedRequest.Header.Clone(), first, firstApplied, websocketURL(t, firstURL)); err != nil {
			t.Fatal(err)
		}
		if _, err := failed.PrepareServerFrame(context.Background(), true, []byte(`{"type":"future.server.event"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := failed.PrepareDial(context.Background(), failedRequest.Header.Clone(), second, secondApplied, websocketURL(t, secondURL)); err != nil {
			t.Fatal("failed first-frame write pinned the provider:", err)
		}

		visibleRequest := fixtureRequest(http.MethodGet, "visible-frame", nil)
		visible, err := fixture.ws.Begin(context.Background(), visibleRequest, testAPIType, operationID("visible-frame", 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := visible.PrepareDial(context.Background(), visibleRequest.Header.Clone(), first, firstApplied, websocketURL(t, firstURL)); err != nil {
			t.Fatal(err)
		}
		frame, err := visible.PrepareServerFrame(context.Background(), true, []byte(`{"type":"future.server.event"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := frame.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := visible.CommitVisibility(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := visible.PrepareDial(context.Background(), visibleRequest.Header.Clone(), second, secondApplied, websocketURL(t, secondURL)); codexws.Classify(err) != codexws.FailureIdentity {
			t.Fatalf("successful first-frame write did not pin provider: class=%q err=%v", codexws.Classify(err), err)
		}
	})
}

func TestProbeClassifierPreservesPreCreateFramesInOrder(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	request := fixtureRequest(http.MethodGet, "probe-order-client", nil)
	operation, err := fixture.ws.Begin(context.Background(), request, testAPIType, operationID("probe-order", 1))
	if err != nil {
		t.Fatal(err)
	}
	frames := []struct {
		text    bool
		payload []byte
	}{
		{text: true, payload: []byte("future non-json probe preface")},
		{text: false, payload: []byte{0x00, 0xff, 0x80}},
		{text: true, payload: []byte(`{"type":"future.probe.event","extension":{"x":1}}`)},
		{text: true, payload: readProtocolGolden(t, "ws-client-response-create-warmup.json")},
	}
	replayed := make([][]byte, 0, len(frames))
	for index, input := range frames {
		wire := append([]byte(nil), input.payload...)
		permit := operation.ClassifyClientFrame(context.Background(), input.text, input.payload)
		if permit.Disposition() != codexws.ClientFrameForward || !permit.ReplayEligible() || !permit.ReplacementEligible() {
			t.Fatalf("Probe frame %d permit = %#v, err %v", index, permit.Trace(), permit.Rejection())
		}
		if permit.IsResponseCreate() != (index == len(frames)-1) {
			t.Fatalf("Probe frame %d create=%v", index, permit.IsResponseCreate())
		}
		if !bytes.Equal(input.payload, wire) {
			t.Fatalf("Probe frame %d bytes changed during classification", index)
		}
		replayed = append(replayed, wire)
	}
	for index := range frames {
		if !bytes.Equal(replayed[index], frames[index].payload) {
			t.Fatalf("Probe replay order changed at frame %d", index)
		}
	}
	if rows := countContinuityBindings(t, fixture); rows != 0 {
		t.Fatalf("Probe classification created %d owners before delivery", rows)
	}
}

func TestOwnerPreferenceConflictIsPermutationInvariantAcrossHTTPAndWebSocket(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	routeA, routeAApplied, finalURL := fixtureCandidate(t, candidateSpec{routeTarget: "route-a"})
	routeB, routeBApplied, _ := fixtureCandidate(t, candidateSpec{routeTarget: "route-b"})
	owners := []struct {
		header    string
		value     string
		candidate codexidentity.CandidateSnapshot
		applied   codexidentity.AppliedIdentity
	}{
		{header: "Thread-Id", value: "owner-thread-a", candidate: routeA, applied: routeAApplied},
		{header: "Session-Id", value: "owner-session-b", candidate: routeB, applied: routeBApplied},
		{header: "Conversation-Id", value: "owner-conversation-a", candidate: routeA, applied: routeAApplied},
	}
	for index, owner := range owners {
		seedContinuityHeader(t, fixture, "owner-permutation-client", owner.header, owner.value, owner.candidate, owner.applied, finalURL, index)
	}

	for index, permutation := range permutationsOfThree() {
		headers := make(http.Header)
		for _, ownerIndex := range permutation {
			headers.Set(owners[ownerIndex].header, owners[ownerIndex].value)
		}
		httpOperation, err := fixture.http.Begin(
			context.Background(), fixtureRequest(http.MethodPost, "owner-permutation-client", headers),
			testAPIType, operationID("owner-permutation-http", index), testHTTPClientEvidence(nil, nil),
		)
		if err != nil {
			t.Fatal(err)
		}
		assertConflictedRoutePreference(t, "HTTP", httpOperation.RequiredAuthority, routeA.Authority())

		wsOperation, err := fixture.ws.Begin(
			context.Background(), fixtureRequest(http.MethodGet, "owner-permutation-client", headers),
			testAPIType, operationID("owner-permutation-ws", index),
		)
		if err != nil {
			t.Fatal(err)
		}
		assertConflictedRoutePreference(t, "WebSocket", wsOperation.RequiredAuthority, routeA.Authority())
	}
}

func readProtocolGolden(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "headers", "testdata", protocolFixtureDirectory, name))
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func countContinuityBindings(t *testing.T, fixture *runtimeFixture) int64 {
	t.Helper()
	var count int64
	if err := fixture.db.Table("codex_continuity_bindings").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func seedContinuityHeader(
	t *testing.T,
	fixture *runtimeFixture,
	client, header, value string,
	candidate codexidentity.CandidateSnapshot,
	applied codexidentity.AppliedIdentity,
	finalURL *url.URL,
	sequence int,
) {
	t.Helper()
	operation, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, client, http.Header{header: {value}}),
		testAPIType, operationID("owner-seed", sequence), testHTTPClientEvidence(nil, nil),
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
	if err := attempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func permutationsOfThree() [][3]int {
	return [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
}

func assertConflictedRoutePreference(
	t *testing.T,
	carrier string,
	constraint func() (*codexidentity.UpstreamAuthority, string),
	want codexidentity.UpstreamAuthority,
) {
	t.Helper()
	authority, route := constraint()
	if authority == nil || !authority.Equal(want) || route != "" {
		t.Fatalf("%s conflicted owner preference = %v/%q, want shared authority with no route", carrier, authority, route)
	}
}
