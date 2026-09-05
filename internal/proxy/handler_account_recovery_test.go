package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestHTTPAccountRecoveryThreeAccountsPreservesWireAndBudget(t *testing.T) {
	for _, budget := range []int{2, 3} {
		t.Run(string(rune('0'+budget)), func(t *testing.T) {
			events := &x3EventLog{}
			a, b, c := x3Provider("recovery-a"), x3Provider("recovery-b"), x3Provider("recovery-c")
			selection := &x3Selector{initial: a, initialLease: x3NewLease(a, events), alternates: []*model.Provider{b, c}, events: events}
			rules, err := errorrule.CompileRuleSet(71, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire := []byte("{ \"type\":\"response.create\", \"model\":\"x3-model\", \"previous_response_id\":\"previous\", \"input\":[] }")
			headers := http.Header{
				"Thread-Id": {"thread"}, "Session-Id": {"session"}, "X-Codex-Window-Id": {"thread:2"},
				"X-Codex-Turn-State": {"old-turn"}, "X-Oai-Attestation": {"opaque-attestation"}, "Accept-Encoding": {"identity"},
			}
			var attempts []string
			checkRequest := func(request *http.Request) {
				payload, err := io.ReadAll(request.Body)
				if err != nil || !bytes.Equal(payload, wire) {
					t.Errorf("request body changed: %q, %v", payload, err)
				}
				for name, values := range headers {
					if !reflect.DeepEqual(request.Header.Values(name), values) {
						t.Errorf("%s changed", name)
					}
				}
				if request.Header.Get("Authorization") != "Bearer secret" {
					t.Errorf("credentials = %q", request.Header.Get("Authorization"))
				}
				if request.Header.Get("Cookie") != "" {
					t.Error("provider cookie crossed authority")
				}
				attempts = append(attempts, request.URL.Host)
			}
			failure := func() x3TransportStep {
				return x3TransportStep{err: errors.New("disclosed provider transport failure"), disclosure: upstreamtransport.RequestDisclosureConfirmed, onRequest: checkRequest}
			}
			accepted := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"new-response\"}}\n\n")
			success := x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", x3NewTrackedBody(accepted, "close:success", events), len(accepted))
			success.onRequest = checkRequest
			success.header.Set("X-Codex-Turn-State", "old-turn")
			transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{failure(), failure(), success}}
			logCore, logs := observer.New(zap.DebugLevel)
			response, pctx := x3Execute(t, x3ExecutionConfig{
				providers: []*model.Provider{a, b, c}, selector: selection, transport: transport, requestBody: wire, requestHeaders: headers,
				recoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation, stickyMode: model.StickyModeAPIType,
				rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(), stats: &x3RuleStats{},
				globalMaxAttempts: budget, logger: zap.New(logCore),
			})
			if transport.Count() != budget || len(attempts) != budget {
				t.Fatalf("attempt count = %d, calls=%d", transport.Count(), len(attempts))
			}
			for _, request := range selection.alternateRequests {
				if request.RequiredAuthority != nil || request.PreferredRouteTargetID != "" || request.ProviderContinuityContext != nil || request.FailoverContext != nil || request.SwitchMode != model.SwitchModeReplacement {
					t.Fatalf("provenance constrained replacement: %#v", request)
				}
			}
			if budget == 3 {
				if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), accepted) {
					t.Fatalf("response=%d %q", response.Code, response.Body.Bytes())
				}
				if !reflect.DeepEqual(selection.stickyProviders, []string{c.ID}) {
					t.Fatalf("sticky = %v", selection.stickyProviders)
				}
			} else if response.Code == http.StatusOK || len(selection.stickyProviders) > 0 {
				t.Fatalf("exhausted response=%d sticky=%v", response.Code, selection.stickyProviders)
			}
			if pctx.selectReq.ClientScope.KeyVersion() == "" {
				t.Fatal("selection lost client scope")
			}
			switchLogs := logs.FilterMessage("proxy.provider_switch").All()
			if len(switchLogs) != budget-1 {
				t.Fatalf("switch log count = %d", len(switchLogs))
			}
			for _, entry := range switchLogs {
				fields := entry.ContextMap()
				if fields["conversation_recovery_policy"] != string(model.ConversationRecoverySwitchAccountPreserveConversation) || fields["client_visible"] != false || fields["switch_reason"] == "" {
					t.Fatalf("switch diagnostics = %#v", fields)
				}
			}
		})
	}
}

func TestHTTPRecoveryUpstreamErrorEventsNeverUpdateSticky(t *testing.T) {
	for _, matchedRule := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmatched", true: "matched"}[matchedRule], func(t *testing.T) {
			events := &x3EventLog{}
			provider := x3Provider("error-events")
			s := &x3Selector{initial: provider, initialLease: x3NewLease(provider, events), events: events}
			rules, err := errorrule.CompileRuleSet(72, nil)
			if err != nil {
				t.Fatal(err)
			}
			if matchedRule {
				rules = x3CompiledRuleSet(t, 73, errorrule.NewPassthroughAction(), "failed-request")
			}
			wire := []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"message\":\"failed-request\"}}\n\n")
			transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", x3NewTrackedBody(wire, "error-close", events), len(wire))}}
			response, _ := x3Execute(t, x3ExecutionConfig{providers: []*model.Provider{provider}, selector: s, transport: transport,
				recoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation, stickyMode: model.StickyModeAPIType,
				rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(), stats: &x3RuleStats{}, globalMaxAttempts: 1})
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), wire) {
				t.Fatalf("response=%d %q", response.Code, response.Body.Bytes())
			}
			if len(s.stickyProviders) != 0 {
				t.Fatalf("error event updated sticky: %v", s.stickyProviders)
			}
		})
	}
}

func TestHTTPStickyRequiresCompletedClientVisibleSuccess(t *testing.T) {
	for _, test := range []struct {
		name   string
		result forwardResult
		want   bool
	}{
		{"success", forwardResult{success: true, responseCommitted: true}, true},
		{"uncommitted", forwardResult{success: true}, false},
		{"semantic error", forwardResult{responseCommitted: true}, false},
		{"write failure", forwardResult{success: true, responseCommitted: true, failureKind: attemptFailureWrite}, false},
		{"read failure", forwardResult{success: true, responseCommitted: true, failureKind: attemptFailureRead}, false},
		{"cancelled", forwardResult{success: true, responseCommitted: true, clientTermination: clientTerminationDisconnect}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &x3Selector{}
			h := &Handler{selector: s}
			pctx := &proxyContext{apiType: APITypeCodex, cfg: &runtimeConfig{stickyMode: model.StickyModeAPIType, ConversationRecoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation}, selectReq: &model.SelectRequest{}}
			state := &retryState{currentProvider: x3Provider("completed")}
			h.finalizeCommittedResponse(pctx, state, &test.result)
			if (len(s.stickyProviders) == 1) != test.want {
				t.Fatalf("sticky = %v", s.stickyProviders)
			}
		})
	}
}

func TestGlobalRecoveryDoesNotChangeOtherAPIContinuity(t *testing.T) {
	request := &model.SelectRequest{APIType: "claude", Model: "model", StickyMode: model.StickyModeAPIType}
	strategy, active := x3Provider("strategy"), x3Provider("active")
	s := &mockSelector{selectWithMetadataFunc: func(context.Context, *model.SelectRequest) (*selectResult, error) {
		return &selectResult{Provider: strategy}, nil
	}}
	registry := NewActiveRequestRegistry()
	activeLease := newLocalProviderLease(active)
	defer activeLease.Release()
	registry.Register(&ActiveRequest{RequestID: "active", ContinuityKey: selector.BuildContinuityKey(request), HasReceivedData: true, lease: activeLease})
	h := &Handler{httpSelector: s, selector: s, activeRegistry: registry, logger: zap.NewNop()}
	selected, err := h.selectInitialProvider(context.Background(), request, 0, nil, model.ConversationRecoverySwitchAccountPreserveConversation)
	if err != nil || selected.provider.ID != active.ID {
		t.Fatalf("other API active continuity=%v, %v", selected, err)
	}
	pctx := &proxyContext{apiType: "claude", selectReq: request, cfg: &runtimeConfig{stickyMode: model.StickyModeAPIType, ConversationRecoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation}}
	state := &retryState{currentProvider: active}
	h.finalizeCommittedResponse(pctx, state, &forwardResult{responseCommitted: true, clientTermination: clientTerminationDisconnect})
	if s.StickyUpdatesLen() != 1 {
		t.Fatal("other API's existing committed-response sticky behavior changed")
	}
}

func TestHTTPRecoverySeedConsumedBeforeExcludedInitialSelection(t *testing.T) {
	events := &x3EventLog{}
	failed, healthy := x3Provider("failed"), x3Provider("healthy")
	request := &model.SelectRequest{APIType: APITypeCodex, Model: "gpt", StickyMode: model.StickyModeAPIType}
	seeds := NewVisibleContinuitySeedStore()
	key := selector.BuildContinuityKey(request)
	seeds.Store(model.VisibleContinuitySeed{SeedID: "one-shot", ContinuityKey: key, OriginProviderID: failed.ID, OriginVendor: "source-vendor",
		Purpose: model.VisibleContinuitySeedAccountRecovery, ExcludedProviderIDs: []string{failed.ID}, ObservedAt: time.Now()})
	tracker := newProviderSwitchTracker(request, 3, seeds)
	if !tracker.lookupVisibleContinuityCandidate() {
		t.Fatal("missing seed")
	}
	excluded := map[string]bool{}
	if !tracker.consumeAccountRecoverySeed(model.ConversationRecoverySwitchAccountPreserveConversation, excluded) {
		t.Fatal("seed not consumed")
	}
	if !excluded[failed.ID] || tracker.prepareSelection() != model.SwitchModeFailover || request.ProviderContinuityContext == nil || request.ProviderContinuityContext.VisibleOriginProviderID != failed.ID {
		t.Fatalf("recovery facts missing: %#v", request)
	}
	if _, ok := seeds.Lookup(key); ok {
		t.Fatal("seed still available")
	}
	s := &x3Selector{initial: failed, initialLease: x3NewLease(failed, events), alternate: healthy, events: events}
	h := &Handler{httpSelector: s, logger: zap.NewNop()}
	selected, err := h.selectInitialProvider(context.Background(), request, 0, excluded, model.ConversationRecoverySwitchAccountPreserveConversation)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.lease.Release()
	if selected.provider.ID != healthy.ID || s.initialLease.ReleaseCount() != 0 {
		t.Fatalf("selected failed provider: %#v", selected)
	}
	if s.LastAlternateRequest().SwitchMode != model.SwitchModeFailover {
		t.Fatal("seed did not reach failover selector")
	}
	if tracker.consumeAccountRecoverySeed(model.ConversationRecoverySwitchAccountPreserveConversation, excluded) {
		t.Fatal("seed consumed twice")
	}
}

func TestHTTPRecoverySeedLosingConcurrentConsumerAddsNoConstraints(t *testing.T) {
	store := NewVisibleContinuitySeedStore()
	request := &model.SelectRequest{APIType: APITypeCodex, Model: "model", ClientScope: proxyCodexTestClientScope(t)}
	key := selector.BuildContinuityKey(request)
	store.Store(model.VisibleContinuitySeed{SeedID: "race", ContinuityKey: key, Purpose: model.VisibleContinuitySeedAccountRecovery, OriginProviderID: "failed", ExcludedProviderIDs: []string{"failed"}, ObservedAt: time.Now()})
	first := newProviderSwitchTracker(cloneHTTPSelectRequest(request), 3, store)
	second := newProviderSwitchTracker(cloneHTTPSelectRequest(request), 3, store)
	first.lookupVisibleContinuityCandidate()
	second.lookupVisibleContinuityCandidate()
	if !first.consumeAccountRecoverySeed(model.ConversationRecoverySwitchAccountPreserveConversation, map[string]bool{}) {
		t.Fatal("first consumer lost seed")
	}
	excluded := map[string]bool{}
	if second.consumeAccountRecoverySeed(model.ConversationRecoverySwitchAccountPreserveConversation, excluded) {
		t.Fatal("second consumed stale candidate")
	}
	if len(excluded) != 0 || second.continuityContext != nil || second.continuityCandidate != nil || second.prepareSelection() != model.SwitchModeInitial {
		t.Fatal("stale candidate initialized recovery")
	}
}

func TestRecoverySeedRegistryCopiesExclusions(t *testing.T) {
	seeds := NewVisibleContinuitySeedStore()
	seed := model.VisibleContinuitySeed{SeedID: "seed", ContinuityKey: model.StickyKey{APIType: APITypeCodex}, Purpose: model.VisibleContinuitySeedAccountRecovery, ExcludedProviderIDs: []string{"failed"}, ObservedAt: time.Now()}
	seeds.Store(seed)
	seed.ExcludedProviderIDs[0] = "mutated"
	candidate, ok := seeds.Lookup(seed.ContinuityKey)
	if !ok || candidate.ExcludedProviderIDs[0] != "failed" {
		t.Fatal("store retained caller slice")
	}
	candidate.ExcludedProviderIDs[0] = "mutated-again"
	consumed, ok := seeds.CompareAndConsume(seed.ContinuityKey, seed.SeedID)
	if !ok || consumed.ExcludedProviderIDs[0] != "failed" {
		t.Fatal("lookup exposed stored slice")
	}
}
