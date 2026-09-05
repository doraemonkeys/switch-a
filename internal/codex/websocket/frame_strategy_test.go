package codexws

import (
	"context"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestClientFramePermitStrategy(t *testing.T) {
	op, err := testRuntime(t, nil).Begin(context.Background(), testRequest("frame-strategy"), codexAPIType, "frame-strategy", "")
	if err != nil {
		t.Fatal(err)
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if err := op.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	defer op.CloseConnection()

	for _, test := range []struct {
		name              string
		text              bool
		payload           []byte
		kind              ClientFrameKind
		disposition       ClientFrameDisposition
		replay            bool
		replacement       bool
		currentConnection bool
	}{
		{name: "binary opaque", payload: []byte{0x00, 0xff}, kind: ClientFrameOpaque, disposition: ClientFrameForward, replay: true, replacement: true},
		{name: "text opaque", text: true, payload: []byte(`{"type":"future.event"}`), kind: ClientFrameOpaque, disposition: ClientFrameForward, replay: true, replacement: true},
		{name: "create", text: true, payload: []byte(`{"type":"response.create"}`), kind: ClientFrameResponseCreate, disposition: ClientFrameForward, replay: true, replacement: true},
		{name: "append", text: true, payload: []byte(`{"type":"response.append","response_id":"opaque"}`), kind: ClientFrameResponseAppend, disposition: ClientFrameForward, currentConnection: true},
		{name: "inject", text: true, payload: []byte(`{"type":"response.inject","response_id":"opaque"}`), kind: ClientFrameResponseInject, disposition: ClientFrameForward, currentConnection: true},
		{name: "malformed recognized create", text: true, payload: []byte(`{"type":"response.create","previous_response_id":{}}`), kind: ClientFrameResponseCreate, disposition: ClientFrameReject},
	} {
		t.Run(test.name, func(t *testing.T) {
			permit := op.ClassifyClientFrame(context.Background(), test.text, test.payload)
			if permit.Disposition() != test.disposition || permit.Trace().Kind != test.kind ||
				permit.ReplayEligible() != test.replay || permit.ReplacementEligible() != test.replacement ||
				permit.CurrentConnectionRequired() != test.currentConnection {
				t.Fatalf("permit = disposition:%q trace:%#v replay:%v replacement:%v current:%v err:%v",
					permit.Disposition(), permit.Trace(), permit.ReplayEligible(), permit.ReplacementEligible(),
					permit.CurrentConnectionRequired(), permit.Rejection())
			}
			if test.disposition == ClientFrameReject && permit.Rejection() == nil {
				t.Fatal("rejected frame has no typed cause")
			}
			if test.kind == ClientFrameOpaque && permit.Trace().EventType != "" {
				t.Fatalf("opaque trace exposed unbounded event type %q", permit.Trace().EventType)
			}
		})
	}
}

func TestPassableWebSocketHandshakeHeaderProjection(t *testing.T) {
	headers := http.Header{
		"Connection":             {"Upgrade, X-Remove-Me"},
		"Upgrade":                {"websocket"},
		"X-Remove-Me":            {"nominated"},
		"Keep-Alive":             {"timeout=5"},
		"Proxy-Connection":       {"keep-alive"},
		"Authorization":          {"Bearer secret"},
		"X-Api-Key":              {"secret"},
		"Cookie":                 {"secret=1"},
		"Set-Cookie":             {"secret=2"},
		"Sec-WebSocket-Protocol": {"upstream-controlled"},
		"X-Codex-Turn-State":     {"state"},
		"X-Codex-Turn-Metadata":  {"metadata"},
		"X-Oai-Attestation":      {"attestation"},
		"session_id":             {"legacy-alias"},
		"X-Upstream-Trace":       {"passable"},
	}
	projected := projectPassableHandshakeHeaders(headers)
	if len(projected) != 1 || projected.Get("X-Upstream-Trace") != "passable" {
		t.Fatalf("projected headers = %#v", projected)
	}
}

func TestPreviousResponseUsesClientAndProtocolScopeWithoutConnectionOrRoutePin(t *testing.T) {
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	origin, _ := testCandidate(t, "route-a", "https://api.example.test/v1")
	sameProtocolRoute, sameProtocolApplied := testCandidate(t, "route-b", "https://api.example.test/v1")
	otherProtocol, otherProtocolApplied := testCandidate(t, "route-c", "https://other.example.test/v1")
	responseID := "response-from-prior-connection"
	serverFrame := codexheaders.InspectServerFrame([]byte(`{"type":"response.created","response":{"id":"` + responseID + `"}}`))
	discovery := codexheaders.DecideServerMessage(serverFrame, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		return codexheaders.OwnerUnknown
	})
	if len(discovery.Claims()) != 1 {
		t.Fatalf("response seed = %#v", discovery.Decisions())
	}
	client := testClientScope(t, "client-a")
	lease, err := service.PrepareVisible(context.Background(), codexcontinuity.ClaimRequest{
		Evidence: evidence(discovery.Claims()[0].Candidate()),
		Scope: codexcontinuity.Scope{
			CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client},
			ProtocolScope: origin.ProtocolScope(), RouteTargetHint: origin.RouteTargetID(),
		},
		OperationID: "prior-connection",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(context.Background(), lease); err != nil {
		t.Fatal(err)
	}

	op, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "reconnected", "")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"type":"response.create","previous_response_id":"` + responseID + `"}`)
	frame := op.ClassifyClientFrame(context.Background(), true, payload)
	if frame.Disposition() != ClientFrameForward || !frame.ReplayEligible() || frame.CurrentConnectionRequired() {
		t.Fatalf("reconnected previous response permit = %#v err=%v", frame.Trace(), frame.Rejection())
	}
	if _, err := op.PrepareDial(context.Background(), make(http.Header), sameProtocolRoute, sameProtocolApplied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal("same ProtocolScope RouteTarget replacement rejected:", err)
	}
	if _, err := frame.PrepareDelivery(context.Background()); err != nil {
		t.Fatal("previous response required a live connection:", err)
	}
	if _, err := op.PrepareDial(context.Background(), make(http.Header), otherProtocol, otherProtocolApplied, mustURL(t, "wss://other.example.test/v1")); Classify(err) != FailureIdentity {
		t.Fatalf("different ProtocolScope class=%q err=%v", Classify(err), err)
	}

	otherClient, err := runtime.Begin(context.Background(), testRequest("client-b"), codexAPIType, "wrong-client", "")
	if err != nil {
		t.Fatal(err)
	}
	wrongClientFrame := otherClient.ClassifyClientFrame(context.Background(), true, payload)
	if wrongClientFrame.Disposition() != ClientFrameReject || wrongClientFrame.Rejection() == nil {
		t.Fatalf("different ClientScope permit = %#v", wrongClientFrame)
	}
}
