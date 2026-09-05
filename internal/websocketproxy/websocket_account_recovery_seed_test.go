package websocketproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"go.uber.org/zap/zaptest"
)

func newRecoverySeedTestOrchestrator(t *testing.T, gateway *Gateway, policy model.ConversationRecoveryPolicy, modelName string) *WebSocketSessionOrchestrator {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	request.Header.Set("Authorization", "Bearer recovery-client")
	operation, err := gateway.codex.Begin(context.Background(), request, APITypeCodex, t.Name(), policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(operation.DiscardCookies)
	return newWebSocketSessionOrchestrator(gateway, webSocketSessionOrchestratorConfig{
		apiType: APITypeCodex, requestID: t.Name(), codexOperation: operation, maxAttempts: 3,
		selectReq: &model.SelectRequest{
			APIType: APITypeCodex, Model: modelName, StickyMode: model.StickyModeModel,
			ClientIP: "192.0.2.1", ClientScope: operation.ClientScope(),
		},
	})
}

func TestAccountRecoverySeedPreparationPreservesConsumptionBoundaries(t *testing.T) {
	for _, test := range []struct {
		name          string
		policy        model.ConversationRecoveryPolicy
		purpose       model.VisibleContinuitySeedPurpose
		modelName     string
		wantImmediate bool
		wantCandidate bool
	}{
		{name: "recovery-known", policy: model.ConversationRecoverySwitchAccountPreserveConversation, purpose: model.VisibleContinuitySeedAccountRecovery, modelName: "gpt-5", wantImmediate: true},
		{name: "recovery-unknown", policy: model.ConversationRecoverySwitchAccountPreserveConversation, purpose: model.VisibleContinuitySeedAccountRecovery, modelName: ModelUnknown, wantImmediate: true},
		{name: "ordinary-known", policy: model.ConversationRecoverySwitchAccountPreserveConversation, modelName: "gpt-5", wantCandidate: true},
		{name: "ordinary-unknown", policy: model.ConversationRecoverySwitchAccountPreserveConversation, modelName: ModelUnknown},
		{name: "default-recovery-known", policy: model.ConversationRecoveryPreserveConversation, purpose: model.VisibleContinuitySeedAccountRecovery, modelName: "gpt-5", wantCandidate: true},
		{name: "default-recovery-unknown", policy: model.ConversationRecoveryPreserveConversation, purpose: model.VisibleContinuitySeedAccountRecovery, modelName: ModelUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &accountRecoverySeedStore{}
			gateway := newTestGateway(t, Config{Store: newMockStore(), VisibleContinuitySeedStore: store, Logger: zaptest.NewLogger(t)})
			orchestrator := newRecoverySeedTestOrchestrator(t, gateway, test.policy, test.modelName)
			request := orchestrator.selectReq
			store.Store(model.VisibleContinuitySeed{
				SeedID: "boundary-seed", Purpose: test.purpose, ContinuityKey: selector.BuildContinuityKey(request),
				OriginProviderID: "a", OriginVendor: "vendor-a", StrictestScope: model.ScopeVendor,
				ExcludedProviderIDs: []string{"a"}, ObservedAt: time.Now(),
			})

			orchestrator.prepareSelectionContinuity(context.Background())

			remaining, consumed := store.snapshot()
			if test.wantImmediate {
				if consumed != 1 || remaining != nil || !orchestrator.excludedProviders["a"] ||
					request.SwitchMode != model.SwitchModeFailover || request.ProviderContinuityContext == nil ||
					request.ProviderContinuityContext.VisibleOriginProviderID != "a" ||
					request.ProviderContinuityContext.StrictestScope != model.ScopeVendor ||
					request.VisibleContinuitySeedCandidate != nil {
					t.Fatalf("recovery was not applied before selection: request=%+v consumed=%d remaining=%+v", request, consumed, remaining)
				}
				return
			}
			if consumed != 0 || remaining == nil || len(orchestrator.excludedProviders) != 0 ||
				request.SwitchMode != model.SwitchModeInitial || request.ProviderContinuityContext != nil ||
				(request.VisibleContinuitySeedCandidate != nil) != test.wantCandidate {
				t.Fatalf("ordinary consumption changed: request=%+v consumed=%d remaining=%+v", request, consumed, remaining)
			}
			if test.wantCandidate {
				origin := &model.Provider{ID: "a", Vendor: "vendor-a"}
				orchestrator.switchTracker.recordSelection(origin, selector.SelectionMetadata{Source: selector.SelectionSourceStickyContinuity})
				remaining, consumed = store.snapshot()
				if consumed != 1 || remaining != nil || request.ProviderContinuityContext == nil || len(orchestrator.excludedProviders) != 0 {
					t.Fatalf("ordinary seed did not wait for origin selection: request=%+v consumed=%d", request, consumed)
				}
			}
		})
	}
}

func TestAccountRecoverySeedLookupRetainsKeyDimensions(t *testing.T) {
	for _, dimension := range []string{"client-scope", "api-type", "model"} {
		t.Run(dimension, func(t *testing.T) {
			store := &accountRecoverySeedStore{}
			gateway := newTestGateway(t, Config{Store: newMockStore(), VisibleContinuitySeedStore: store, Logger: zaptest.NewLogger(t)})
			orchestrator := newRecoverySeedTestOrchestrator(t, gateway, model.ConversationRecoverySwitchAccountPreserveConversation, ModelUnknown)
			key := selector.BuildContinuityKey(orchestrator.selectReq)
			switch dimension {
			case "client-scope":
				key.ClientScope = "different-client"
			case "api-type":
				key.APIType = "different-api"
			case "model":
				key.Model = "different-model"
			}
			store.Store(model.VisibleContinuitySeed{
				SeedID: "other-seed", Purpose: model.VisibleContinuitySeedAccountRecovery,
				ContinuityKey: key, OriginProviderID: "a", ExcludedProviderIDs: []string{"a"}, ObservedAt: time.Now(),
			})
			orchestrator.prepareSelectionContinuity(context.Background())
			remaining, consumed := store.snapshot()
			if consumed != 0 || remaining == nil || len(orchestrator.excludedProviders) != 0 ||
				orchestrator.selectReq.ProviderContinuityContext != nil || orchestrator.selectReq.SwitchMode != model.SwitchModeInitial {
				t.Fatalf("seed crossed %s: consumed=%d request=%+v", dimension, consumed, orchestrator.selectReq)
			}
		})
	}
}

type racingAccountRecoverySeedStore struct {
	*accountRecoverySeedStore
	lookedUp chan struct{}
	release  chan struct{}
}

func (s *racingAccountRecoverySeedStore) Lookup(key model.StickyKey) (*model.VisibleContinuitySeedCandidate, bool) {
	candidate, found := s.accountRecoverySeedStore.Lookup(key)
	s.lookedUp <- struct{}{}
	<-s.release
	return candidate, found
}

func TestAccountRecoverySeedConcurrentConsumerAddsNoStaleConstraints(t *testing.T) {
	store := &racingAccountRecoverySeedStore{
		accountRecoverySeedStore: &accountRecoverySeedStore{},
		lookedUp:                 make(chan struct{}, 2), release: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(store.release) })
	defer release()
	gateway := newTestGateway(t, Config{Store: newMockStore(), VisibleContinuitySeedStore: store, Logger: zaptest.NewLogger(t)})
	first := newRecoverySeedTestOrchestrator(t, gateway, model.ConversationRecoverySwitchAccountPreserveConversation, ModelUnknown)
	second := newRecoverySeedTestOrchestrator(t, gateway, model.ConversationRecoverySwitchAccountPreserveConversation, ModelUnknown)
	store.Store(model.VisibleContinuitySeed{
		SeedID: "racing-seed", Purpose: model.VisibleContinuitySeedAccountRecovery,
		ContinuityKey:    selector.BuildContinuityKey(first.selectReq),
		OriginProviderID: "a", ExcludedProviderIDs: []string{"a"}, ObservedAt: time.Now(),
	})
	finished := make(chan struct{}, 2)
	for _, orchestrator := range []*WebSocketSessionOrchestrator{first, second} {
		go func() {
			orchestrator.prepareSelectionContinuity(context.Background())
			finished <- struct{}{}
		}()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		select {
		case <-store.lookedUp:
		case <-ctx.Done():
			t.Fatal("consumers did not both look up the same seed")
		}
	}
	release()
	for range 2 {
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("seed consumption did not finish")
		}
	}
	winners := 0
	for _, orchestrator := range []*WebSocketSessionOrchestrator{first, second} {
		request := orchestrator.selectReq
		if request.SwitchMode == model.SwitchModeFailover {
			winners++
			if !orchestrator.excludedProviders["a"] || request.ProviderContinuityContext == nil {
				t.Fatalf("winner lost recovery context: %+v", request)
			}
		} else if request.SwitchMode != model.SwitchModeInitial || request.ProviderContinuityContext != nil ||
			request.VisibleContinuitySeedCandidate != nil || len(orchestrator.excludedProviders) != 0 {
			t.Fatalf("losing consumer retained stale constraints: %+v", request)
		}
	}
	remaining, consumed := store.snapshot()
	if winners != 1 || consumed != 1 || remaining != nil {
		t.Fatalf("winners=%d consumed=%d remaining=%+v", winners, consumed, remaining)
	}
}
