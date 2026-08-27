package websocketproxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/websocketprotocol"
	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

func TestWebSocketForwarderNegotiatesSameSubprotocolOnBothConnections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		clientOffer    []string
		upstreamOffer  []string
		wantNegotiated string
	}{
		{name: "no protocol"},
		{
			name:           "upstream selection is accepted downstream",
			clientOffer:    []string{"realtime.v2", "realtime.v1"},
			upstreamOffer:  []string{"realtime.v1"},
			wantNegotiated: "realtime.v1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			upstreamSelected := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: test.upstreamOffer})
				if err != nil {
					t.Errorf("accept upstream: %v", err)
					return
				}
				defer conn.CloseNow()
				upstreamSelected <- conn.Subprotocol()
				_, _, _ = conn.Read(r.Context())
			}))
			defer upstream.Close()

			results := make(chan *WebSocketResult, 1)
			forwarder := NewWebSocketForwarder(WebSocketForwarderConfig{Logger: zap.NewNop()})
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result, err := forwarder.Forward(r.Context(), w, r, wsURL(upstream), nil)
				if err != nil {
					t.Errorf("forward websocket: %v", err)
				}
				results <- result
			}))
			defer gateway.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := websocket.Dial(ctx, wsURL(gateway), &websocket.DialOptions{Subprotocols: test.clientOffer})
			if err != nil {
				t.Fatalf("dial gateway: %v", err)
			}
			if got := client.Subprotocol(); got != test.wantNegotiated {
				t.Fatalf("downstream Subprotocol() = %q, want %q", got, test.wantNegotiated)
			}
			if got := <-upstreamSelected; got != test.wantNegotiated {
				t.Fatalf("upstream Subprotocol() = %q, want %q", got, test.wantNegotiated)
			}
			_ = client.Close(websocket.StatusNormalClosure, "done")

			select {
			case result := <-results:
				if result == nil || result.NegotiatedSubprotocol != test.wantNegotiated {
					t.Fatalf("result = %#v, want negotiated protocol %q", result, test.wantNegotiated)
				}
			case <-ctx.Done():
				t.Fatal("forwarder did not finish")
			}
		})
	}
}

func TestGatewayProbeClosesWithProtocolErrorWhenUpstreamDoesNotSelectFixedProtocol(t *testing.T) {
	t.Parallel()
	const clientMessage = `{"type":"response.create","response":{"model":"gpt-5"}}`

	upstreamOffer := make(chan []string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamOffer <- webSocketSubprotocolHeaderValues(r.Header)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream: %v", err)
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
	}))
	defer upstream.Close()

	store := newMockStore()
	store.routingPolicies = []model.RoutingPolicy{{
		Enabled: true, APIType: APITypeCodex,
		ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-",
	}}
	store.providers = []model.Provider{{
		ID: "probe-mismatch", AuthMode: "bearer", Enabled: true,
		APITypes:           []model.ProviderAPIType{{ProviderID: "probe-mismatch", APIType: APITypeCodex, BaseURL: upstream.URL}},
		CredentialSessions: testCredentialSessions("probe-mismatch", APITypeCodex, credentialsession.KindAPIKey, "provider-key"),
	}}
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t)})
	server := newGatewayIntegrationServer(gateway, RequestConfig{
		GlobalAuthMode:    "bearer",
		GlobalMaxAttempts: 1,
		StickyMode:        model.StickyModeModel,
		ProbeClientModel:  true,
	}, "probe-subprotocol-mismatch")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(
		ctx,
		wsURL(server)+"/responses",
		codexDialOptions("realtime.v2", "realtime.v1"),
	)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer client.CloseNow()
	if got := client.Subprotocol(); got != "realtime.v2" {
		t.Fatalf("downstream Subprotocol() = %q, want fixed first offer", got)
	}
	if err := client.Write(ctx, websocket.MessageText, []byte(clientMessage)); err != nil {
		t.Fatalf("write probe message: %v", err)
	}
	_, _, err = client.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusProtocolError {
		t.Fatalf("close status = %v (error %v), want %v", got, err, websocket.StatusProtocolError)
	}
	select {
	case values := <-upstreamOffer:
		if !reflect.DeepEqual(values, []string{"realtime.v2"}) {
			t.Fatalf("upstream offer = %#v, want fixed protocol only", values)
		}
	case <-ctx.Done():
		t.Fatal("upstream dial not observed")
	}
}

func TestGatewayProbeUsesFixedSubprotocolForUpstream(t *testing.T) {
	t.Parallel()
	const (
		clientMessage = `{"type":"response.create","response":{"model":"gpt-5"}}`
		serverMessage = `{"type":"response.created","response":{"model":"gpt-5"}}`
		subprotocol   = "realtime.v2"
	)

	upstreamOffer := make(chan string, 1)
	upstreamSelected := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamOffer <- r.Header.Get("Sec-WebSocket-Protocol")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{subprotocol}})
		if err != nil {
			t.Errorf("accept upstream: %v", err)
			return
		}
		defer conn.CloseNow()
		upstreamSelected <- conn.Subprotocol()
		messageType, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read replayed probe message: %v", err)
			return
		}
		if messageType != websocket.MessageText || string(payload) != clientMessage {
			t.Errorf("replayed probe message = (%v, %q)", messageType, payload)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(serverMessage)); err != nil {
			t.Errorf("write upstream response: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer upstream.Close()

	store := newMockStore()
	store.routingPolicies = []model.RoutingPolicy{{
		Enabled: true, APIType: APITypeCodex,
		ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-",
	}}
	store.providers = []model.Provider{{
		ID: "probe-match", AuthMode: "bearer", Enabled: true,
		APITypes:           []model.ProviderAPIType{{ProviderID: "probe-match", APIType: APITypeCodex, BaseURL: upstream.URL}},
		CredentialSessions: testCredentialSessions("probe-match", APITypeCodex, credentialsession.KindAPIKey, "provider-key"),
	}}
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t)})
	server := newGatewayIntegrationServer(gateway, RequestConfig{
		GlobalAuthMode:    "bearer",
		GlobalMaxAttempts: 1,
		StickyMode:        model.StickyModeModel,
		ProbeClientModel:  true,
	}, "probe-subprotocol-match")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(
		ctx,
		wsURL(server)+"/responses",
		codexDialOptions(subprotocol, "realtime.v1"),
	)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer client.CloseNow()
	if got := client.Subprotocol(); got != subprotocol {
		t.Fatalf("downstream Subprotocol() = %q, want %q", got, subprotocol)
	}
	if err := client.Write(ctx, websocket.MessageText, []byte(clientMessage)); err != nil {
		t.Fatalf("write probe message: %v", err)
	}
	messageType, payload, err := client.Read(ctx)
	if err != nil || messageType != websocket.MessageText || string(payload) != serverMessage {
		t.Fatalf("upstream response = (%v, %q, %v)", messageType, payload, err)
	}
	if got := <-upstreamOffer; got != subprotocol {
		t.Fatalf("upstream offer = %q, want fixed protocol only", got)
	}
	if got := <-upstreamSelected; got != subprotocol {
		t.Fatalf("upstream Subprotocol() = %q, want %q", got, subprotocol)
	}
	_, _, _ = client.Read(ctx)
}

func TestWebSocketSubprotocolHeaderValuesReadsAnyHeaderCasing(t *testing.T) {
	t.Parallel()
	headers := http.Header{
		"sec-websocket-protocol": {"one, two"},
		"Sec-Websocket-Protocol": {"three"},
		"Unrelated":              {"value"},
	}
	got := webSocketSubprotocolHeaderValues(headers)
	if len(got) != 2 || !containsString(got, "one, two") || !containsString(got, "three") {
		t.Fatalf("header values = %#v, want both casing variants", got)
	}
}

func TestRejectedSwitchingProtocolsResponseStillEnforcesFixedSelection(t *testing.T) {
	t.Parallel()
	offer, err := websocketprotocol.ParseClientOffer([]string{"realtime.v2"})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := &WebSocketSessionOrchestrator{
		handler:     &Gateway{logger: zap.NewNop()},
		subprotocol: websocketprotocol.New(offer).FixForProbe(),
	}

	err = orchestrator.rejectedUpgradeSubprotocolError(DialExchange{
		HandshakeStatusCode:   http.StatusSwitchingProtocols,
		NegotiatedSubprotocol: "unexpected",
	})
	if !errors.Is(err, websocketprotocol.ErrSubprotocolMismatch) {
		t.Fatalf("rejected upgrade error = %v, want subprotocol mismatch", err)
	}
	if err := orchestrator.rejectedUpgradeSubprotocolError(DialExchange{
		HandshakeStatusCode:   http.StatusUnauthorized,
		NegotiatedSubprotocol: "",
	}); err != nil {
		t.Fatalf("ordinary rejected handshake error = %v, want nil", err)
	}
}

func TestOrdinarySubprotocolSelectionCommitsOnlyAfterAcceptedBoundary(t *testing.T) {
	t.Parallel()

	const (
		providerASelection = "realtime.v2"
		providerBSelection = "realtime.v1"
	)
	clientOffer := []string{providerASelection, providerBSelection}
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	request.Header.Set("Sec-WebSocket-Protocol", strings.Join(clientOffer, ", "))
	orchestrator := &WebSocketSessionOrchestrator{
		handler: &Gateway{
			logger:      zaptest.NewLogger(t),
			wsForwarder: NewWebSocketForwarder(WebSocketForwarderConfig{Logger: zaptest.NewLogger(t)}),
		},
		lifecycle:      newWebSocketLifecycleState(),
		codexOperation: testCodexOperation(t),
		requestID:      "ordinary-attempt-local-subprotocol",
	}
	if result := orchestrator.initializeSubprotocol(request); result != nil {
		t.Fatalf("initializeSubprotocol() result = %+v, want nil", result)
	}

	providerANegotiation, err := orchestrator.acceptedSubprotocolNegotiation(
		DialExchange{NegotiatedSubprotocol: providerASelection},
	)
	if err != nil || providerANegotiation.Selected() != providerASelection {
		t.Fatalf("provider A negotiation = (%q, %v)", providerANegotiation.Selected(), err)
	}
	if _, _, err := orchestrator.codexOperation.PrepareServerHeaders(
		context.Background(),
		http.Header{"X-Codex-Turn-State": {"", "invalid-duplicate"}},
	); err == nil {
		t.Fatal("provider A invalid Turn State projection was accepted")
	}
	if got := orchestrator.subprotocol.DialOffer(); !reflect.DeepEqual(got, clientOffer) {
		t.Fatalf("provider B dial offer = %#v, want original full offer %#v", got, clientOffer)
	}

	providerBNegotiation, err := orchestrator.acceptedSubprotocolNegotiation(
		DialExchange{NegotiatedSubprotocol: providerBSelection},
	)
	if err != nil {
		t.Fatalf("provider B negotiation: %v", err)
	}
	selected := make(chan string, 1)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := orchestrator.ensureClientAccepted(w, r, providerBNegotiation); err != nil {
			t.Errorf("accept provider B selection downstream: %v", err)
			return
		}
		selected <- orchestrator.clientConn.Subprotocol()
		_ = orchestrator.clientConn.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer gateway.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, wsURL(gateway), &websocket.DialOptions{Subprotocols: clientOffer})
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer client.CloseNow()
	if got := client.Subprotocol(); got != providerBSelection {
		t.Fatalf("downstream subprotocol = %q, want provider B selection %q", got, providerBSelection)
	}
	select {
	case got := <-selected:
		if got != providerBSelection {
			t.Fatalf("committed subprotocol = %q, want %q", got, providerBSelection)
		}
	case <-ctx.Done():
		t.Fatal("downstream acceptance was not observed")
	}
}

func TestSubprotocolDecisionLogsExactNegotiationContext(t *testing.T) {
	const (
		requestID      = "server-generated-session-id"
		selectedMarker = "private-client-offer-marker"
		actualMarker   = "private-upstream-selection-marker"
	)
	core, observed := observer.New(zap.DebugLevel)
	newOrchestrator := func() *WebSocketSessionOrchestrator {
		orchestrator := &WebSocketSessionOrchestrator{
			handler:      &Gateway{logger: zap.New(core)},
			requestID:    requestID,
			probeOutcome: webSocketSelectionProbeOutcomeBypassed,
		}
		request := httptest.NewRequest(http.MethodGet, "http://gateway.test/ws", nil)
		request.Header.Set("Sec-WebSocket-Protocol", selectedMarker)
		if result := orchestrator.initializeSubprotocol(request); result != nil {
			t.Fatalf("initializeSubprotocol() result = %+v, want nil", result)
		}
		return orchestrator
	}

	accepted := newOrchestrator()
	if _, err := accepted.acceptedSubprotocolNegotiation(DialExchange{NegotiatedSubprotocol: selectedMarker}); err != nil {
		t.Fatalf("validate matching upstream selection: %v", err)
	}
	mismatched := newOrchestrator()
	mismatched.subprotocol = mismatched.subprotocol.FixForProbe()
	_, mismatchErr := mismatched.acceptedSubprotocolNegotiation(DialExchange{NegotiatedSubprotocol: actualMarker})
	if !errors.Is(mismatchErr, websocketprotocol.ErrSubprotocolMismatch) {
		t.Fatalf("validate mismatched upstream selection = %v, want subprotocol mismatch", mismatchErr)
	}
	missing := newOrchestrator()
	missing.subprotocol = missing.subprotocol.FixForProbe()
	_, missingErr := missing.acceptedSubprotocolNegotiation(DialExchange{})
	if !errors.Is(missingErr, websocketprotocol.ErrSubprotocolMismatch) {
		t.Fatalf("validate empty upstream selection = %v, want subprotocol mismatch", missingErr)
	}
	mismatched.logSubprotocolDecision(
		"websocket.subprotocol_mismatch",
		webSocketSubprotocolPhaseUpstreamSelection,
		websocketprotocol.Peer(selectedMarker),
		actualMarker,
		&websocketprotocol.MismatchError{
			Peer: websocketprotocol.Peer(actualMarker), Reason: websocketprotocol.MismatchReason(selectedMarker),
			Expected: selectedMarker, Actual: actualMarker,
		},
	)

	entries := observed.All()
	if len(entries) != 4 {
		t.Fatalf("subprotocol log count = %d, want 4", len(entries))
	}
	for _, entry := range entries {
		encoded := fmt.Sprintf("%s %+v", entry.Message, entry.ContextMap())
		for _, forbidden := range []string{
			mismatchErr.Error(), missingErr.Error(),
		} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("subprotocol log contains raw negotiation data %q: %s", forbidden, encoded)
			}
		}
	}

	acceptedContext := entries[0].ContextMap()
	wantAccepted := map[string]any{
		"request_id":           requestID,
		"session_id":           requestID,
		"attempt_index":        int64(0),
		"negotiation_phase":    string(webSocketSubprotocolPhaseUpstreamSelection),
		"negotiation_outcome":  string(webSocketSubprotocolOutcomeAccepted),
		"peer":                 string(websocketprotocol.PeerUpstream),
		"client_offer_count":   int64(1),
		"selection_fixed":      true,
		"selected_state":       string(webSocketSubprotocolValuePresent),
		"actual_state":         string(webSocketSubprotocolValuePresent),
		"selected_subprotocol": selectedMarker,
		"actual_subprotocol":   selectedMarker,
	}
	for key, want := range wantAccepted {
		if got := acceptedContext[key]; got != want {
			t.Errorf("accepted log field %s = %#v, want %#v", key, got, want)
		}
	}

	mismatchContext := entries[1].ContextMap()
	wantMismatch := map[string]any{
		"negotiation_phase":   string(webSocketSubprotocolPhaseUpstreamSelection),
		"negotiation_outcome": string(webSocketSubprotocolOutcomeMismatch),
		"peer":                string(websocketprotocol.PeerUpstream),
		"selection_fixed":     true,
		"selected_state":      string(webSocketSubprotocolValuePresent),
		"actual_state":        string(webSocketSubprotocolValuePresent),
		"mismatch_reason":     string(websocketprotocol.MismatchReasonSelectionChanged),
	}
	for key, want := range wantMismatch {
		if got := mismatchContext[key]; got != want {
			t.Errorf("mismatch log field %s = %#v, want %#v", key, got, want)
		}
	}

	emptyContext := entries[2].ContextMap()
	if got := emptyContext["mismatch_reason"]; got != string(websocketprotocol.MismatchReasonMissingSelection) {
		t.Errorf("empty-selection mismatch_reason = %#v, want %q", got, websocketprotocol.MismatchReasonMissingSelection)
	}
	if got := emptyContext["actual_state"]; got != string(webSocketSubprotocolValueEmpty) {
		t.Errorf("empty-selection actual_state = %#v, want %q", got, webSocketSubprotocolValueEmpty)
	}

	unclassifiedContext := entries[3].ContextMap()
	if got := unclassifiedContext["mismatch_reason"]; got != webSocketSubprotocolMismatchUnclassified {
		t.Errorf("unclassified mismatch_reason = %#v, want %q", got, webSocketSubprotocolMismatchUnclassified)
	}
	if got := unclassifiedContext["peer"]; got != "" {
		t.Errorf("unclassified peer = %#v, want empty typed value", got)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
