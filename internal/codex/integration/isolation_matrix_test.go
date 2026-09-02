package codexintegration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type isolationCell struct {
	clientIndex   int
	providerIndex int
	accountIndex  int
	client        string
	threadID      string
	turnMetadata  string
	turnState     string
	responseID    string
	cookieName    string
	cookieValue   string
	handle        string
	candidate     codexidentity.CandidateSnapshot
	applied       codexidentity.AppliedIdentity
	finalURL      *url.URL
	reconnect     codexidentity.CandidateSnapshot
	reconnectAuth codexidentity.AppliedIdentity
	readRoute     codexidentity.CandidateSnapshot
	readAuth      codexidentity.AppliedIdentity
}

func TestTwoClientsTwoProvidersTwoAccountsIsolationMatrix(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{})
	cells := buildIsolationCells(t)
	handles := make(map[int]string, 2)

	for index := range cells {
		cell := &cells[index]
		cell.handle = seedIsolationCellHTTP(t, fixture, *cell, handles[cell.clientIndex], index)
		handles[cell.clientIndex] = cell.handle
		seedIsolationResponseOnWebSocket(t, fixture, *cell, index)
	}

	for _, kind := range []codexcontinuity.Kind{
		codexcontinuity.KindThreadID,
		codexcontinuity.KindTurnMetadata,
		codexcontinuity.KindTurnState,
		codexcontinuity.KindResponseReference,
	} {
		var count int64
		if err := fixture.db.Table("codex_continuity_bindings").Where("kind = ?", kind).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != int64(len(cells)) {
			t.Fatalf("%s owners = %d, want %d matrix cells", kind, count, len(cells))
		}
	}

	for index := range cells {
		cell := cells[index]
		t.Run(cell.name(), func(t *testing.T) {
			assertIsolationCellRetrievableOverHTTP(t, fixture, cell)
			assertIsolationCellRetrievableAfterWebSocketReconnect(t, fixture, cell)

			wrongAuthority := cells[cell.clientIndex*4+(index+1-cell.clientIndex*4+1)%4]
			assertIsolationCellRejectsOtherAuthority(t, fixture, cell, wrongAuthority)
			assertIsolationCellRejectsOtherClient(t, fixture, cell)
			assertIsolationCellRejectsOtherProtocolScope(t, fixture, cell)
		})
	}
}

func buildIsolationCells(t *testing.T) []isolationCell {
	t.Helper()
	cells := make([]isolationCell, 0, 8)
	for client := range 2 {
		for provider := range 2 {
			for account := range 2 {
				label := fmt.Sprintf("c%d-p%d-a%d", client, provider, account)
				spec := candidateSpec{
					routeTarget: "route-seed-" + label,
					vendor:      fmt.Sprintf("provider-%d", provider),
					subject:     fmt.Sprintf("account-%d", account),
					requestURL:  fmt.Sprintf("https://provider-%d.example.test/v1/responses", provider),
				}
				candidate, applied, finalURL := fixtureCandidate(t, spec)
				spec.routeTarget = "route-reconnect-" + label
				reconnect, reconnectAuth, _ := fixtureCandidate(t, spec)
				spec.routeTarget = "route-read-" + label
				readRoute, readAuth, _ := fixtureCandidate(t, spec)
				cells = append(cells, isolationCell{
					clientIndex: client, providerIndex: provider, accountIndex: account,
					client:       fmt.Sprintf("matrix-client-%d", client),
					threadID:     "thread-" + label,
					turnMetadata: "metadata-" + label,
					turnState:    "state-" + label,
					responseID:   "response-" + label,
					cookieName:   "cookie_" + label,
					cookieValue:  "value-" + label,
					candidate:    candidate, applied: applied, finalURL: finalURL,
					reconnect: reconnect, reconnectAuth: reconnectAuth,
					readRoute: readRoute, readAuth: readAuth,
				})
			}
		}
	}
	return cells
}

func (c isolationCell) name() string {
	return fmt.Sprintf("client_%d/provider_%d/account_%d", c.clientIndex, c.providerIndex, c.accountIndex)
}

func seedIsolationCellHTTP(
	t *testing.T,
	fixture *runtimeFixture,
	cell isolationCell,
	handle string,
	sequence int,
) string {
	t.Helper()
	request := requestWithHandle(http.MethodPost, cell.client, handle)
	request.Header.Set("Thread-Id", cell.threadID)
	request.Header.Set("X-Codex-Turn-Metadata", cell.turnMetadata)
	operation, err := fixture.http.Begin(
		context.Background(), request, testAPIType, operationID("matrix-http-seed", sequence), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = cell.finalURL
	attempt, err := operation.PrepareAttempt(context.Background(), upstream, cell.candidate, cell.applied)
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.MarkDisclosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	providerCookie := fmt.Sprintf("%s=%s; Path=/; Secure; Max-Age=604800", cell.cookieName, cell.cookieValue)
	head := &upstreamtransport.ResponseHead{
		SourceHeader: http.Header{"Set-Cookie": {providerCookie}},
		Header:       http.Header{"Set-Cookie": {providerCookie}},
	}
	if err := attempt.ObserveResponse(head); err != nil {
		t.Fatal(err)
	}
	if head.Header.Get("Set-Cookie") != "" {
		t.Fatalf("%s exposed raw Provider Set-Cookie", cell.name())
	}
	responseHeaders := http.Header{"X-Codex-Turn-State": {cell.turnState}}
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

func seedIsolationResponseOnWebSocket(
	t *testing.T,
	fixture *runtimeFixture,
	cell isolationCell,
	sequence int,
) {
	t.Helper()
	request := requestWithHandle(http.MethodGet, cell.client, cell.handle)
	request.Header.Set("X-Codex-Turn-State", cell.turnState)
	operation, err := fixture.ws.Begin(
		context.Background(), request, testAPIType, operationID("matrix-ws-seed", sequence),
	)
	if err != nil {
		t.Fatal(err)
	}
	dial, err := operation.PrepareDial(
		context.Background(), request.Header.Clone(), cell.reconnect, cell.reconnectAuth, websocketURL(t, cell.finalURL),
	)
	if err != nil {
		t.Fatal("same-Authority response seed route was rejected:", err)
	}
	if err := dial.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := operation.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	created, err := operation.PrepareServerFrame(
		context.Background(), true,
		[]byte(`{"type":"response.created","response":{"id":"`+cell.responseID+`"}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := operation.CommitVisibility(context.Background()); err != nil {
		t.Fatal(err)
	}
	operation.CloseConnection()
}

func assertIsolationCellRetrievableOverHTTP(t *testing.T, fixture *runtimeFixture, cell isolationCell) {
	t.Helper()
	previous := []byte(`{"type":"response.create","previous_response_id":"` + cell.responseID + `"}`)
	request := isolationCellRequest(http.MethodPost, cell, cell.handle)
	operation, err := fixture.http.Begin(
		context.Background(), request, testAPIType, operationID("matrix-http-read", cell.providerIndex*2+cell.accountIndex),
		previous, previous,
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := operation.RequiredAuthority(); required == nil || !required.Equal(cell.candidate.Authority()) {
		t.Fatalf("HTTP matrix Authority = %v", required)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = cell.finalURL
	if _, err := operation.PrepareAttempt(context.Background(), upstream, cell.readRoute, cell.readAuth); err != nil {
		t.Fatal("same-Authority RouteTarget redefined HTTP response identity:", err)
	}
	if got := upstream.Header.Get("Cookie"); got != cell.cookieName+"="+cell.cookieValue {
		t.Fatalf("HTTP matrix Cookie = %q, want only %s", got, cell.cookieName)
	}
}

func assertIsolationCellRetrievableAfterWebSocketReconnect(t *testing.T, fixture *runtimeFixture, cell isolationCell) {
	t.Helper()
	request := isolationCellRequest(http.MethodGet, cell, cell.handle)
	operation, err := fixture.ws.Begin(
		context.Background(), request, testAPIType, operationID("matrix-ws-reconnect", cell.providerIndex*2+cell.accountIndex),
	)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := operation.RequiredAuthority(); required == nil || !required.Equal(cell.candidate.Authority()) {
		t.Fatalf("WebSocket matrix Authority = %v", required)
	}
	forwarded := request.Header.Clone()
	dial, err := operation.PrepareDial(
		context.Background(), forwarded, cell.readRoute, cell.readAuth, websocketURL(t, cell.finalURL),
	)
	if err != nil {
		t.Fatal("same-Authority RouteTarget redefined WS response identity:", err)
	}
	if got := forwarded.Get("Cookie"); got != cell.cookieName+"="+cell.cookieValue {
		t.Fatalf("WebSocket matrix Cookie = %q, want only %s", got, cell.cookieName)
	}
	if err := dial.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := operation.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	previous := []byte(`{"type":"response.create","previous_response_id":"` + cell.responseID + `"}`)
	frame := operation.ClassifyClientFrame(context.Background(), true, previous)
	if frame.Disposition() != codexws.ClientFrameForward || frame.CurrentConnectionRequired() {
		t.Fatalf("WebSocket reconnect previous response = %#v, err %v", frame.Trace(), frame.Rejection())
	}
	delivery, err := frame.PrepareDelivery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := delivery.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	operation.CloseConnection()
}

func assertIsolationCellRejectsOtherAuthority(
	t *testing.T,
	fixture *runtimeFixture,
	cell isolationCell,
	wrong isolationCell,
) {
	t.Helper()
	previous := []byte(`{"type":"response.create","previous_response_id":"` + cell.responseID + `"}`)
	operation, err := fixture.http.Begin(
		context.Background(), isolationCellRequest(http.MethodPost, cell, cell.handle),
		testAPIType, operationID("matrix-wrong-authority", cell.providerIndex*2+cell.accountIndex),
		previous, previous,
	)
	if err != nil {
		t.Fatal(err)
	}
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = wrong.finalURL
	_, err = operation.PrepareAttempt(context.Background(), upstream, wrong.candidate, wrong.applied)
	requireHTTPError(t, err, codexhttp.ErrorIdentityMismatch)
}

func assertIsolationCellRejectsOtherClient(t *testing.T, fixture *runtimeFixture, cell isolationCell) {
	t.Helper()
	otherClient := fmt.Sprintf("matrix-client-%d", 1-cell.clientIndex)
	previous := []byte(`{"type":"response.create","previous_response_id":"` + cell.responseID + `"}`)
	_, err := fixture.http.Begin(
		context.Background(), fixtureRequest(http.MethodPost, otherClient, nil),
		testAPIType, operationID("matrix-wrong-client", cell.providerIndex*2+cell.accountIndex),
		previous, previous,
	)
	requireHTTPError(t, err, codexhttp.ErrorClientInput)

	request := requestWithHandle(http.MethodGet, otherClient, cell.handle)
	operation, err := fixture.ws.Begin(
		context.Background(), request, testAPIType, operationID("matrix-wrong-cookie-client", cell.providerIndex*2+cell.accountIndex),
	)
	if err != nil {
		t.Fatal(err)
	}
	forwarded := request.Header.Clone()
	if _, err := operation.PrepareDial(
		context.Background(), forwarded, cell.candidate, cell.applied, websocketURL(t, cell.finalURL),
	); err != nil {
		t.Fatal(err)
	}
	if got := forwarded.Get("Cookie"); got != "" {
		t.Fatalf("mismatched handle injected provider Cookie %q", got)
	}
}

func assertIsolationCellRejectsOtherProtocolScope(t *testing.T, fixture *runtimeFixture, cell isolationCell) {
	t.Helper()
	previous := []byte(`{"type":"response.create","previous_response_id":"` + cell.responseID + `"}`)
	operation, err := fixture.http.Begin(
		context.Background(), isolationCellRequest(http.MethodPost, cell, cell.handle),
		testAPIType, operationID("matrix-wrong-protocol", cell.providerIndex*2+cell.accountIndex),
		previous, previous,
	)
	if err != nil {
		t.Fatal(err)
	}
	alt, altApplied, altURL := fixtureCandidate(t, candidateSpec{
		routeTarget: "route-alt-protocol-" + cell.name(),
		vendor:      fmt.Sprintf("provider-%d", cell.providerIndex),
		subject:     fmt.Sprintf("account-%d", cell.accountIndex),
		apiType:     "codex-alt",
		requestURL:  cell.finalURL.String(),
	})
	upstream := fixtureRequest(http.MethodPost, "", nil)
	upstream.URL = altURL
	_, err = operation.PrepareAttempt(context.Background(), upstream, alt, altApplied)
	requireHTTPError(t, err, codexhttp.ErrorIdentityMismatch)
}

func isolationCellRequest(method string, cell isolationCell, handle string) *http.Request {
	request := requestWithHandle(method, cell.client, handle)
	request.Header.Set("Thread-Id", cell.threadID)
	request.Header.Set("X-Codex-Turn-Metadata", cell.turnMetadata)
	request.Header.Set("X-Codex-Turn-State", cell.turnState)
	return request
}
