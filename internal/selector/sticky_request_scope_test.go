package selector

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuation"
	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDeniedConversationPreservesClientStickyAcrossRestart(t *testing.T) {
	for _, transport := range []string{"http", "websocket"} {
		for _, excluding := range []bool{false, true} {
			name := transport + "/initial"
			if excluding {
				name = transport + "/excluding"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				source := transportProvider("old-conversation", transport, 100)
				source.Enabled = false
				source.CodexContinuation.Outbound = continuation.None
				healthy := transportProvider("healthy-sticky", transport, 10)
				healthy.CodexContinuation.Inbound = continuation.Any
				fallback := transportProvider("priority-first", transport, 0)
				fallback.CodexContinuation.Inbound = continuation.Any
				store := newMockStore()
				store.providers = []model.Provider{fallback, healthy, source}
				clock := &mockClock{now: time.Now()}
				persistence := newPersistentStickyTestStore()
				cache := NewPersistentStickyCache(persistence, clock, nil)
				t.Cleanup(func() { _ = cache.Close(ctx) })
				core, logs := observer.New(zap.DebugLevel)
				selector := NewSelector(Config{Store: store, StickyCache: cache, Clock: clock, Logger: zap.New(core)})
				stateless := model.SelectRequest{
					APIType: "codex", Transport: transport, ClientIP: "127.0.0.1",
					Model: unknownModelSentinel, StickyMode: model.StickyModeAPIType,
					ClientScope: stickyTestClientScope(t, "v1", "same-client"),
				}
				selector.UpdateStickyWithTTL(&stateless, healthy.ID, time.Hour)

				target, err := url.Parse(source.APITypes[0].BaseURL)
				if err != nil {
					t.Fatal(err)
				}
				candidate, err := codexidentity.NewAuthorityResolver().Resolve(source.CredentialSessions[0], "codex", target)
				if err != nil {
					t.Fatal(err)
				}
				service := &continuation.Service{Policy: func(_ context.Context, id string) (continuation.Policy, bool, error) {
					return source.CodexContinuation, id == source.ID, nil
				}}
				session := service.Begin("same-client", "blocked-conversation")
				err = session.Observe(ctx,
					codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("existing-thread")},
					codexcontinuity.Resolution{Owner: &codexcontinuity.Owner{RouteTargetHint: source.ID, ProtocolScope: candidate.ProtocolScope()}},
				)
				if err != nil {
					t.Fatal(err)
				}
				conversation := stateless
				conversation.OperationID = "blocked-conversation"
				conversation.Model = "conversation-model"
				conversation.CodexContinuation = session
				var selected *SelectResult
				if excluding {
					selected, err = selector.SelectExcludingWithMetadata(ctx, &conversation, nil)
				} else {
					selected, err = selector.SelectWithMetadata(ctx, &conversation)
				}
				if selected != nil && selected.Lease != nil {
					selected.Lease.Release()
				}
				if !errors.Is(err, internal.ErrNoProvider) {
					t.Fatalf("blocked conversation must remain rejected: %v", err)
				}
				assertStickyRequestSkipped(t, logs, &conversation, healthy.ID, errorrule.ReasonRoutingChanged)
				assertHealthyStickySelected(t, selector, &stateless, healthy.ID)

				// The same denial must not enqueue a durable deletion that only
				// becomes visible after a restart.
				if err := cache.Close(ctx); err != nil {
					t.Fatal(err)
				}
				restored := NewPersistentStickyCache(persistence, clock, nil)
				t.Cleanup(func() { _ = restored.Close(ctx) })
				restarted := NewSelector(Config{Store: store, StickyCache: restored, Clock: clock})
				assertHealthyStickySelected(t, restarted, &stateless, healthy.ID)
			})
		}
	}
}

func TestRequestConstraintsPreserveStickyForStatelessRequest(t *testing.T) {
	for _, constraint := range []string{"model_policy", "authority", "client_platform", "switch_history", "failover_boundary"} {
		t.Run(constraint, func(t *testing.T) {
			ctx := context.Background()
			healthy := authorityTestProvider("healthy-sticky", "https://healthy.example.test", "healthy-account", 10)
			fallback := authorityTestProvider("priority-first", "https://fallback.example.test", "fallback-account", 0)
			store := newMockStore()
			store.providers = []model.Provider{fallback, healthy}
			core, logs := observer.New(zap.DebugLevel)
			cache := NewMemoryStickyCache(&mockClock{now: time.Now()})
			selector := NewSelector(Config{Store: store, StickyCache: cache, Logger: zap.New(core)})
			stateless := model.SelectRequest{
				APIType: "codex", ClientIP: "127.0.0.1", StickyMode: model.StickyModeAPIType,
				Model: unknownModelSentinel, ClientScope: stickyTestClientScope(t, "v1", "same-client"),
			}
			selector.UpdateStickyWithTTL(&stateless, healthy.ID, time.Hour)
			constrained := stateless
			constrained.OperationID = constraint
			constrained.Model = "conversation-model"
			reason := errorrule.ReasonRoutingChanged
			switch constraint {
			case "model_policy":
				store.routingPolicies = []model.RoutingPolicy{{
					Enabled: true, APIType: "codex",
					ModelMatchType: model.RoutingPolicyModelMatchTypeExact, ModelMatchValue: constrained.Model,
					TargetProviderID: &fallback.ID,
				}}
			case "authority":
				required := authorityForProvider(t, fallback)
				constrained.RequiredAuthority = &required
				reason = errorrule.ReasonAuthUnavailable
			case "client_platform":
				constrained.ClientDisguise = &selectionDisguise{excluded: map[string]bool{healthy.ID: true}}
				reason = errorrule.ReasonClientPlatformExcluded
			case "switch_history":
				constrained.ProviderSwitchHistory = &model.ProviderSwitchHistory{AttemptChain: []string{healthy.ID}}
			case "failover_boundary":
				constrained.SwitchMode = model.SwitchModeFailover
				constrained.ProviderContinuityContext = &model.ProviderContinuityContext{
					VisibleOriginProviderID: "origin", VisibleOriginVendor: "origin-vendor", StrictestScope: model.ScopeNone,
				}
			}
			result, err := selector.SelectWithMetadata(ctx, &constrained)
			if result != nil && result.Lease != nil {
				defer result.Lease.Release()
			}
			if constraint == "failover_boundary" {
				if !errors.Is(err, internal.ErrNoProvider) {
					t.Fatalf("failover boundary must remain enforced: %v", err)
				}
			} else if err != nil || result == nil || result.Provider().ID != fallback.ID {
				t.Fatalf("constraint must reject the cached provider: selection=%v error=%v", result, err)
			}
			assertStickyRequestSkipped(t, logs, &constrained, healthy.ID, reason)
			assertHealthyStickySelected(t, selector, &stateless, healthy.ID)
		})
	}
}

func assertHealthyStickySelected(t *testing.T, selector *Selector, request *model.SelectRequest, providerID string) {
	t.Helper()
	result, err := selector.SelectWithMetadata(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Lease.Release()
	if result.Provider().ID != providerID || result.Metadata.Source != SelectionSourceStickyContinuity {
		t.Fatalf("stateless selection = %q via %q, want sticky %q", result.Provider().ID, result.Metadata.Source, providerID)
	}
}

func assertStickyRequestSkipped(t *testing.T, logs *observer.ObservedLogs, request *model.SelectRequest, providerID string, reason errorrule.DecisionReason) {
	t.Helper()
	entries := logs.FilterMessage("selector.sticky_binding_decision").All()
	if len(entries) != 1 {
		t.Fatalf("sticky decision count = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["operation_id"] != request.OperationID || fields["provider_id"] != providerID ||
		fields["transport"] != reqTransport(request) || fields["rejection_scope"] != string(providerRejectionScopeRequest) ||
		fields["decision"] != string(stickyBindingDecisionSkipped) || fields["reason"] != string(reason) {
		t.Fatalf("sticky skip lacks request-scoped decision context: %#v", fields)
	}
}
