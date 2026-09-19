package codexws

import (
	"context"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuation"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestWebSocketContinuationRequiresSourceAndDestinationAdmission(t *testing.T) {
	for _, tc := range []struct {
		name              string
		outbound, inbound continuation.Boundary
		allowed           bool
	}{
		{"defaults", "", "", false}, {"accepted", continuation.Any, continuation.Any, true}, {"source veto", continuation.None, continuation.Any, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			state := newTestContinuity(t)
			runtime := testRuntime(t, state)
			source, _ := testCandidate(t, "source", "https://source.test")
			target, applied := testCandidate(t, "target", "https://target.test")
			client := testClientScope(t, "client")
			claimFixtureEvidence(t, state, client, source, "seed", http.Header{"Thread-Id": {"thread"}})
			runtime.continuation = &continuation.Service{Policy: func(_ context.Context, id string) (continuation.Policy, bool, error) {
				if id == "source" {
					return continuation.Policy{Outbound: tc.outbound}, true, nil
				}
				return continuation.Policy{Inbound: tc.inbound}, true, nil
			}}
			request := testRequest("client")
			request.Header.Set("Thread-Id", "thread")
			op, err := runtime.Begin(ctx, request, codexAPIType, tc.name, model.ConversationRecoverySwitchAccountPreserveConversation)
			if err != nil {
				t.Fatal(err)
			}
			permit, err := op.PrepareDial(ctx, request.Header.Clone(), target, applied, mustURL(t, "wss://target.test"))
			if !tc.allowed {
				if !continuation.IsDenied(err) {
					t.Fatalf("admission = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := permit.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := op.CommitVisibility(ctx); err != nil {
				t.Fatal(err)
			}
			if op.Continuation().PreferredProviderID() != "target" {
				t.Fatal("visible route not committed")
			}
		})
	}
}

func TestWebSocketFirstFrameCanRequireDifferentProvider(t *testing.T) {
	ctx := context.Background()
	state := newTestContinuity(t)
	runtime := testRuntime(t, state)
	runtime.continuation = &continuation.Service{}
	source, sourceApplied := testCandidate(t, "source", "https://source.test")
	target, targetApplied := testCandidate(t, "target", "https://target.test")
	client := testClientScope(t, "client")
	message := codexheaders.InspectServerFrame([]byte(`{"type":"response.created","response":{"id":"prior-response"}}`))
	discovery := codexheaders.DecideServerMessage(message, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerUnknown })
	if len(discovery.Decisions()) == 0 {
		t.Fatal("response reference not discovered")
	}
	for _, decision := range discovery.Decisions() {
		lease, err := state.PrepareVisible(ctx, codexcontinuity.ClaimRequest{Evidence: evidence(decision.Candidate()), Scope: codexcontinuity.Scope{CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client}, ProtocolScope: source.ProtocolScope(), RouteTargetHint: "source"}, OperationID: "seed"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Commit(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
	request := testRequest("client")
	op, err := runtime.Begin(ctx, request, codexAPIType, "late-evidence", model.ConversationRecoverySwitchAccountPreserveConversation)
	if err != nil {
		t.Fatal(err)
	}
	permit, err := op.PrepareDial(ctx, request.Header.Clone(), target, targetApplied, mustURL(t, "wss://target.test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	frame := op.ClassifyClientFrame(ctx, true, []byte(`{"type":"response.create","previous_response_id":"prior-response"}`))
	if _, err := frame.PrepareDelivery(ctx); !continuation.IsDenied(err) {
		t.Fatalf("late foreign state = %v", err)
	}
	if op.Continuation().PreferredProviderID() != "source" {
		t.Fatal("late preferred source missing")
	}
	if err := op.ReplacePhysicalAttempt(); err != nil {
		t.Fatal(err)
	}
	permit, err = op.PrepareDial(ctx, request.Header.Clone(), source, sourceApplied, mustURL(t, "wss://source.test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := frame.PrepareDelivery(ctx); err != nil {
		t.Fatalf("original immutable frame could not be delivered on source: %v", err)
	}
}
