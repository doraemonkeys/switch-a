package codexhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuation"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestHTTPContinuationChecksBothProvidersAndPreservesState(t *testing.T) {
	for _, tc := range []struct {
		name              string
		outbound, inbound continuation.Boundary
		allowed           bool
	}{
		{"defaults", "", "", false},
		{"accepted", continuation.Any, continuation.Any, true},
		{"source veto", continuation.None, continuation.Any, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testClientScope(t, "client")
			source, _ := testCandidate(t, "source", "source.test", "account-a")
			target, applied := testCandidate(t, "target", "target.test", "account-b")
			recorder := &continuityRecorder{resolveBinding: codexcontinuity.Binding{Owner: codexcontinuity.Owner{ClientScope: client, ProtocolScope: source.ProtocolScope(), RouteTargetHint: "source"}}}
			runtime := newContinuityTestRuntime(t, client, recorder)
			runtime.continuation = &continuation.Service{Policy: func(_ context.Context, id string) (continuation.Policy, bool, error) {
				if id == "source" {
					return continuation.Policy{Outbound: tc.outbound}, true, nil
				}
				return continuation.Policy{Inbound: tc.inbound}, true, nil
			}}
			request := httptest.NewRequest(http.MethodPost, "http://gateway/responses", nil)
			request.Header.Set("Authorization", "Bearer client")
			request.Header.Set("Thread-Id", "thread")
			request.Header.Set("X-Codex-Turn-State", "opaque")
			op, err := runtime.Begin(ctx, request, codexAPIType, tc.name, model.ConversationRecoverySwitchAccountPreserveConversation, testClientEvidence(nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer op.Discard()
			if op.Continuation().PreferredProviderID() != "source" {
				t.Fatal("missing serving preference")
			}
			upstream := httptest.NewRequest(http.MethodPost, "https://target.test/responses", nil)
			upstream.Header = request.Header.Clone()
			attempt, err := op.PrepareAttempt(ctx, upstream, target, applied)
			if !tc.allowed {
				if !continuation.IsDenied(err) {
					t.Fatalf("admission = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if upstream.Header.Get("Thread-Id") != "thread" || upstream.Header.Get("X-Codex-Turn-State") != "opaque" {
				t.Fatal("wire state changed")
			}
			visible, err := attempt.PrepareVisible(ctx, http.Header{})
			if err != nil {
				t.Fatal(err)
			}
			if err := visible.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if op.Continuation().PreferredProviderID() != "target" {
				t.Fatal("visible route not committed")
			}
			if recorder.resolveBinding.Owner.RouteTargetHint != "source" {
				t.Fatal("original provenance changed")
			}
		})
	}
}
