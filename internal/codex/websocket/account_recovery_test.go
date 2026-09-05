package codexws

import (
	"context"
	"net/http"
	"testing"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func seedRecoveryTurn(t *testing.T, service *codexcontinuity.Service, client codexidentity.ClientScope, candidate codexidentity.CandidateSnapshot, value string) codexcontinuity.Evidence {
	t.Helper()
	discovery := codexheaders.DecideServerHeaders(http.Header{"X-Codex-Turn-State": {value}}, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerUnknown })
	item := evidence(discovery.Decisions()[0].Candidate())
	lease, err := service.PrepareVisible(context.Background(), codexcontinuity.ClaimRequest{Evidence: item, Scope: codexcontinuity.Scope{CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client}, ProtocolScope: candidate.ProtocolScope(), RouteTargetHint: candidate.RouteTargetID()}, OperationID: "seed-" + value})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestAccountRecoveryMixedOwnersDisclosureAndResponseProvenance(t *testing.T) {
	ctx := context.Background()
	service := newTestContinuity(t)
	runtime := testRuntime(t, service)
	client := testClientScope(t, "recovery-client")
	first, _ := testCandidate(t, "a", "https://a.example.test")
	second, secondApplied := testCandidate(t, "b", "https://b.example.test")
	third, thirdApplied := testCandidate(t, "c", "https://c.example.test")
	claimFixtureEvidence(t, service, client, first, "thread-seed", http.Header{"Thread-Id": {"thread-a"}})
	oldState := seedRecoveryTurn(t, service, client, second, "state-b")
	request := testRequest("recovery-client")
	request.Header.Set("Thread-Id", "thread-a")
	request.Header.Set("X-Codex-Turn-State", "state-b")
	request.Header.Set("X-Oai-Attestation", "opaque-attestation")
	if _, err := runtime.Begin(ctx, request, codexAPIType, "strict-mixed", model.ConversationRecoveryPreserveConversation); Classify(err) != FailureIdentity {
		t.Fatalf("strict mixed-owner err=%v", err)
	}
	op, err := runtime.Begin(ctx, request, codexAPIType, "recovery", model.ConversationRecoverySwitchAccountPreserveConversation)
	if err != nil {
		t.Fatal(err)
	}
	if authority, route := op.RequiredAuthority(); authority != nil || route != "" {
		t.Fatal("provenance constrained recovery selection")
	}
	if !op.ClientScope().Equal(client) {
		t.Fatal("client scope differs from ingress credential")
	}
	frame := []byte(`{"type":"response.create","previous_response_id":"unknown-prior","model":"gpt-5"}`)
	for _, attempt := range []struct {
		candidate codexidentity.CandidateSnapshot
		applied   codexidentity.AppliedIdentity
	}{{second, secondApplied}, {third, thirdApplied}} {
		permit, err := op.PrepareDial(ctx, request.Header.Clone(), attempt.candidate, attempt.applied, mustURL(t, attempt.candidate.Authority().Origin().String()))
		if err != nil {
			t.Fatal(err)
		}
		if err := permit.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := op.OpenConnection(); err != nil {
			t.Fatal(err)
		}
		delivery, err := op.PrepareClientFrame(ctx, true, frame)
		if err != nil {
			t.Fatal(err)
		}
		if err := delivery.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if authority, _ := op.RequiredAuthority(); authority != nil {
			t.Fatal("pre-visible disclosure leaked routing pin")
		}
		if attempt.candidate.RouteTargetID() == "b" {
			if err := op.ReplacePhysicalAttempt(); err != nil {
				t.Fatal(err)
			}
		}
	}
	echoed, projected, err := op.PrepareServerHeaders(ctx, http.Header{"X-Codex-Turn-State": {"state-b"}})
	if err != nil {
		t.Fatal(err)
	}
	if projected.Get("X-Codex-Turn-State") != "state-b" {
		t.Fatal("echo was rewritten")
	}
	if err := echoed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := service.ResolveOwner(ctx, codexcontinuity.ResolveRequest{Evidence: oldState, ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "check-old"})
	if err != nil || !owner.Owner.ProtocolScope.Equal(second.ProtocolScope()) {
		t.Fatalf("old provenance migrated: %v", err)
	}
	newHeaders := http.Header{"X-Codex-Turn-State": {"state-c"}}
	produced, _, err := op.PrepareServerHeaders(ctx, newHeaders)
	if err != nil {
		t.Fatal(err)
	}
	if err := produced.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	newDiscovery := codexheaders.DecideServerHeaders(newHeaders, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerCurrent })
	newOwner, err := service.ResolveOwner(ctx, codexcontinuity.ResolveRequest{Evidence: evidence(newDiscovery.Decisions()[0].Candidate()), ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "check-new"})
	if err != nil || !newOwner.Owner.ProtocolScope.Equal(third.ProtocolScope()) {
		t.Fatalf("new provenance not actual account: %v", err)
	}
	if err := op.CommitVisibility(ctx); err != nil {
		t.Fatal(err)
	}
	if err := op.ReplacePhysicalAttempt(); err == nil {
		t.Fatal("replaced after visibility")
	}
	other := request.Clone(ctx)
	other.Header.Set("Authorization", "Bearer other-client")
	if _, err := runtime.Begin(ctx, other, codexAPIType, "other-client", model.ConversationRecoverySwitchAccountPreserveConversation); Classify(err) != FailureIdentity {
		t.Fatalf("cross-client provenance err=%v", err)
	}
}

func TestAccountRecoveryOpaqueStateRemainsLeaseFreeAndConnectionBound(t *testing.T) {
	ctx := context.Background()
	for _, status := range []codexcontinuity.ResolutionStatus{codexcontinuity.ResolutionUnknown, codexcontinuity.ResolutionExpired, codexcontinuity.ResolutionUnavailable} {
		t.Run(string(status), func(t *testing.T) {
			continuity := &opaqueRecoveryContinuity{Continuity: newTestContinuity(t), status: status}
			runtime := testRuntime(t, continuity)
			request := testRequest("opaque-client")
			request.Header.Set("X-Codex-Turn-State", "opaque-state")
			op, err := runtime.Begin(ctx, request, codexAPIType, "opaque", model.ConversationRecoverySwitchAccountPreserveConversation)
			if err != nil {
				t.Fatal(err)
			}
			candidate, applied := testCandidate(t, "a", "https://a.example.test")
			permit, err := op.PrepareDial(ctx, request.Header.Clone(), candidate, applied, mustURL(t, "wss://a.example.test"))
			if err != nil {
				t.Fatal(err)
			}
			if len(permit.leases) != 0 {
				t.Fatal("opaque input acquired lease")
			}
			if err := permit.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := op.OpenConnection(); err != nil {
				t.Fatal(err)
			}
			echoed, _, err := op.PrepareServerHeaders(ctx, http.Header{"X-Codex-Turn-State": {"opaque-state"}})
			if err != nil || len(echoed.leases) != 0 {
				t.Fatalf("opaque echo lease/error: %v", err)
			}
			if continuity.resolves != 1 {
				t.Fatalf("echo resolved again: %d", continuity.resolves)
			}
			frame := op.ClassifyClientFrame(ctx, true, []byte(`{"type":"response.append"}`))
			if frame.ReplayEligible() || frame.ReplacementEligible() {
				t.Fatal("connection-bound frame became replayable")
			}
			op.CloseConnection()
			if _, err := frame.PrepareDelivery(ctx); err == nil {
				t.Fatal("connection-bound frame accepted after generation closed")
			}
		})
	}
}

type opaqueRecoveryContinuity struct {
	Continuity
	status   codexcontinuity.ResolutionStatus
	resolves int
}

func (c *opaqueRecoveryContinuity) Resolve(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
	c.resolves++
	return codexcontinuity.Resolution{Status: c.status}, nil
}

func TestAccountRecoveryNewServerStateBestEffort(t *testing.T) {
	for _, stage := range []string{"resolve_expired", "resolve_unavailable", "prepare_unavailable", "commit_unavailable", "prepare_conflict"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			continuity := &responseRecoveryContinuity{Continuity: newTestContinuity(t), stage: stage}
			op, err := testRuntime(t, continuity).Begin(ctx, testRequest("new-state-client"), codexAPIType, "new-state", model.ConversationRecoverySwitchAccountPreserveConversation)
			if err != nil {
				t.Fatal(err)
			}
			candidate, applied := testCandidate(t, "b", "https://b.example.test")
			dial, err := op.PrepareDial(ctx, make(http.Header), candidate, applied, mustURL(t, "wss://b.example.test"))
			if err != nil {
				t.Fatal(err)
			}
			if err := dial.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := op.OpenConnection(); err != nil {
				t.Fatal(err)
			}
			permit, err := op.PrepareServerFrame(ctx, true, []byte(`{"type":"response.created","response":{"id":"new-state"}}`))
			if stage == "prepare_conflict" {
				if err == nil {
					t.Fatal("conflict was downgraded")
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
			if stage == "resolve_expired" || stage == "resolve_unavailable" {
				if continuity.prepared != 0 || len(permit.leases) != 0 {
					t.Fatal("opaque server resolution was claimed")
				}
			}
		})
	}
}

type responseRecoveryContinuity struct {
	Continuity
	stage    string
	prepared int
}

func (c *responseRecoveryContinuity) Resolve(ctx context.Context, request codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
	switch c.stage {
	case "resolve_expired":
		return codexcontinuity.Resolution{Status: codexcontinuity.ResolutionExpired}, nil
	case "resolve_unavailable":
		return codexcontinuity.Resolution{Status: codexcontinuity.ResolutionUnavailable}, nil
	}
	return c.Continuity.Resolve(ctx, request)
}
func (c *responseRecoveryContinuity) PrepareVisible(ctx context.Context, request codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error) {
	c.prepared++
	if c.stage == "prepare_unavailable" {
		return codexcontinuity.Lease{}, &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable}
	}
	if c.stage == "prepare_conflict" {
		return codexcontinuity.Lease{}, &codexcontinuity.Error{Kind: codexcontinuity.ErrorConflict}
	}
	return c.Continuity.PrepareVisible(ctx, request)
}
func (c *responseRecoveryContinuity) Commit(ctx context.Context, lease codexcontinuity.Lease) (codexcontinuity.Binding, error) {
	if c.stage == "commit_unavailable" {
		return codexcontinuity.Binding{}, &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable}
	}
	return c.Continuity.Commit(ctx, lease)
}
