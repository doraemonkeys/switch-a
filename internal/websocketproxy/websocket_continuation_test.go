package websocketproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/continuation"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap/zaptest"
)

func TestContinuationFirstFrameReselectsWithoutDisclosingToRejectedProvider(t *testing.T) {
	const frame = `{ "type":"response.create", "previous_response_id":"prior", "model":"gpt" }`
	const response = `{"type":"response.completed","response":{"id":"continued","status":"completed"}}`
	var rejectedFrames atomic.Int32
	rejected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err == nil {
			rejectedFrames.Add(1)
		}
	}))
	defer rejected.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Error(err)
			return
		}
		if string(data) != frame {
			t.Errorf("original frame changed: %q", data)
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(response))
		_ = conn.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer source.Close()
	a, b := routingTestProvider("rejected"), routingTestProvider("source")
	a.APITypes[0].BaseURL, b.APITypes[0].BaseURL = rejected.URL, source.URL
	config := testCodexRuntimeConfig(t)
	config.Continuation = &continuation.Service{}
	runtime, err := codexws.New(config)
	if err != nil {
		t.Fatal(err)
	}
	selection := &accountRecoverySelector{providers: []model.Provider{a, b}}
	health := newTrackingHealthManager()
	gateway := newTestGateway(t, Config{Store: newMockStore(), Selector: selection, Codex: runtime, Health: health, Logger: zaptest.NewLogger(t)})
	selection.gateway = gateway
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	identity, err := config.ClientIdentities.Resolve(ctx, []byte(strings.TrimPrefix(testClientAuthorization, "Bearer ")))
	if err != nil {
		t.Fatal(err)
	}
	lease := gateway.newFallbackProviderLease(&b, APITypeCodex)
	candidate, ok := lease.CandidateSnapshot()
	if !ok {
		t.Fatal("missing source candidate")
	}
	defer lease.Release()
	issued := codexheaders.DecideServerMessage(codexheaders.InspectServerFrame([]byte(`{"type":"response.created","response":{"id":"prior"}}`)), func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerUnknown })
	if len(issued.Decisions()) != 1 {
		t.Fatal("expected response reference")
	}
	owner, err := config.Continuity.PrepareVisible(ctx, codexcontinuity.ClaimRequest{
		Evidence: codexcontinuity.Evidence{Kind: codexcontinuity.KindResponseReference, DigestInput: issued.Decisions()[0].Candidate().DigestInput()},
		Scope:    codexcontinuity.Scope{CurrentClientScope: identity.Primary, ClientScopeCandidates: identity.Aliases, ProtocolScope: candidate.ProtocolScope(), RouteTargetHint: b.ID}, OperationID: "seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.Continuity.Commit(ctx, owner); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateway.Handle(r.Context(), w, r, RequestConfig{ConversationRecoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation, GlobalMaxAttempts: 3, GlobalAuthMode: "bearer"}, APITypeCodex, "late-continuation", time.Now())
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt", codexDialOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != response {
		t.Fatalf("continuation = %q, %v", data, err)
	}
	_, _, _ = conn.Read(ctx)
	selections, _, _ := selection.snapshot()
	if len(selections) != 2 || selections[1].mode != model.SwitchModeReplacement {
		t.Fatalf("selection transitions = %+v", selections)
	}
	if rejectedFrames.Load() != 0 {
		t.Fatal("disclosed foreign conversation before admission")
	}
	if len(health.getMarkFailureCalls()) != 0 {
		t.Fatal("routing rejection damaged provider health")
	}
}
