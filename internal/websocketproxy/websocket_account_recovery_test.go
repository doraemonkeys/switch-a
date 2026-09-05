package websocketproxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"go.uber.org/zap/zaptest"
)

type accountRecoverySeedStore struct {
	mu       sync.Mutex
	seed     *model.VisibleContinuitySeed
	consumed int
}

func (s *accountRecoverySeedStore) Store(seed model.VisibleContinuitySeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seed = seed.Clone()
}
func (s *accountRecoverySeedStore) Lookup(key model.StickyKey) (*model.VisibleContinuitySeedCandidate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seed == nil || s.seed.ContinuityKey != key {
		return nil, false
	}
	return s.seed.Candidate(time.Now()), true
}
func (s *accountRecoverySeedStore) CompareAndConsume(key model.StickyKey, id string) (*model.VisibleContinuitySeed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seed == nil || s.seed.ContinuityKey != key || s.seed.SeedID != id {
		return nil, false
	}
	seed := s.seed.Clone()
	s.seed = nil
	s.consumed++
	return seed, true
}
func (s *accountRecoverySeedStore) snapshot() (*model.VisibleContinuitySeed, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seed.Clone(), s.consumed
}

type recoverySelectionObservation struct {
	mode          model.SwitchMode
	excluded      map[string]bool
	continuity    bool
	continuityKey model.StickyKey
}
type accountRecoverySelector struct {
	gateway     *Gateway
	providers   []model.Provider
	mu          sync.Mutex
	selections  []recoverySelectionObservation
	sticky      []string
	activeCalls int
}

func (s *accountRecoverySelector) selectProvider(req *model.SelectRequest, excluded map[string]bool) (ProviderSelection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyExcluded := make(map[string]bool)
	for id, v := range excluded {
		copyExcluded[id] = v
	}
	s.selections = append(s.selections, recoverySelectionObservation{req.SwitchMode, copyExcluded, req.ProviderContinuityContext != nil, selector.BuildContinuityKey(req)})
	for i := range s.providers {
		if !excluded[s.providers[i].ID] {
			return ProviderSelection{Lease: s.gateway.newFallbackProviderLease(&s.providers[i], APITypeCodex), Metadata: selector.BuildSelectionMetadataAt(req, selector.SelectionSourceStrategy, time.Now())}, nil
		}
	}
	return ProviderSelection{}, internal.ErrNoProvider
}
func (s *accountRecoverySelector) SelectInitial(_ context.Context, req *model.SelectRequest) (ProviderSelection, error) {
	return s.selectProvider(req, nil)
}
func (s *accountRecoverySelector) SelectAlternate(_ context.Context, req *model.SelectRequest, excluded map[string]bool) (ProviderSelection, error) {
	return s.selectProvider(req, excluded)
}
func (s *accountRecoverySelector) SelectActive(context.Context, *model.SelectRequest, ProviderLease) (ProviderSelection, error) {
	s.mu.Lock()
	s.activeCalls++
	s.mu.Unlock()
	return ProviderSelection{}, internal.ErrNoProvider
}
func (s *accountRecoverySelector) UpdateStickyWithTTL(_ *model.SelectRequest, id string, _ time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sticky = append(s.sticky, id)
}
func (*accountRecoverySelector) EvictProviderContinuity(string) {}
func (s *accountRecoverySelector) snapshot() ([]recoverySelectionObservation, []string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recoverySelectionObservation(nil), s.selections...), append([]string(nil), s.sticky...), s.activeCalls
}

func TestAccountRecoveryGatewayReplayAcrossThreeAuthorities(t *testing.T) {
	for _, probe := range []bool{false, true} {
		t.Run(fmt.Sprintf("probe-%t", probe), func(t *testing.T) {
			const frame = `{ "type":"response.create", "model":"gpt-5", "previous_response_id":"opaque-prior" }`
			const success = `{"type":"response.completed","response":{"id":"new-c","status":"completed","model":"gpt-5"}}`
			var providers []model.Provider
			seen := make(chan string, 3)
			for _, id := range []string{"a", "b", "c"} {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer "+id+"-key" || r.Header.Get("Thread-Id") != "original-thread" || r.Header.Get("X-Oai-Attestation") != "opaque-attestation" || r.Header.Get("Cookie") != "" {
						t.Errorf("upstream %s headers changed: auth=%q thread=%q attestation=%q cookie=%q", id, r.Header.Get("Authorization"), r.Header.Get("Thread-Id"), r.Header.Get("X-Oai-Attestation"), r.Header.Get("Cookie"))
					}
					conn, err := websocket.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					_, data, err := conn.Read(r.Context())
					if err != nil {
						return
					}
					if string(data) != frame {
						t.Errorf("%s replay changed bytes: %q", id, data)
					}
					seen <- id
					payload := `{"type":"error","status":429,"error":{"type":"rate_limit_exceeded","message":"provider unavailable"}}`
					if id == "c" {
						payload = success
					}
					_ = conn.Write(r.Context(), websocket.MessageText, []byte(payload))
					if id == "c" {
						_ = conn.Close(websocket.StatusNormalClosure, "complete")
					} else {
						_, _, _ = conn.Read(r.Context())
					}
				}))
				t.Cleanup(upstream.Close)
				p := routingTestProvider(id)
				p.APITypes[0].BaseURL = upstream.URL
				providers = append(providers, p)
			}
			selection := &accountRecoverySelector{providers: providers}
			gateway := newTestGateway(t, Config{Store: newMockStore(), Selector: selection, Logger: zaptest.NewLogger(t)})
			selection.gateway = gateway
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gateway.Handle(r.Context(), w, r, RequestConfig{ConversationRecoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation, GlobalMaxAttempts: 3, GlobalAuthMode: "bearer", ProbeClientModel: probe, StickyMode: model.StickyModeModel, StickyTTL: time.Minute}, APITypeCodex, "replay-operation", time.Now())
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			options := codexDialOptions()
			options.HTTPHeader.Set("Thread-Id", "original-thread")
			options.HTTPHeader.Set("X-Oai-Attestation", "opaque-attestation")
			conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", options)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				t.Fatal(err)
			}
			_, data, err := conn.Read(ctx)
			if err != nil || string(data) != success {
				t.Fatalf("response=%q err=%v", data, err)
			}
			_, _, _ = conn.Read(ctx)
			for _, want := range []string{"a", "b", "c"} {
				select {
				case got := <-seen:
					if got != want {
						t.Fatalf("attempt=%q want=%q", got, want)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			waitFor(t, func() bool { _, sticky, _ := selection.snapshot(); return len(sticky) == 1 }, testPollTimeout)
			selections, sticky, active := selection.snapshot()
			if len(selections) != 3 || sticky[0] != "c" || active != 0 {
				t.Fatalf("selection=%+v sticky=%v active=%d", selections, sticky, active)
			}
			for i, observed := range selections {
				if observed.continuity || observed.continuityKey.ClientScope == "" || i > 0 && observed.mode != model.SwitchModeReplacement {
					t.Fatalf("owner affected replacement: %+v", observed)
				}
			}
		})
	}
}

type accountRecoveryReconnectCase struct {
	name                 string
	path                 string
	probe                bool
	sticky               model.StickyMode
	modelRule            bool
	replaceBeforeVisible bool
}

func TestAccountRecoveryReconnectPublishesBeforeNoticeAnd1012(t *testing.T) {
	for _, test := range []accountRecoveryReconnectCase{
		{name: "url-model-probe-off", path: "/responses?model=gpt-5", sticky: model.StickyModeModel},
		{name: "url-model-probe-on", path: "/responses?model=gpt-5", probe: true, sticky: model.StickyModeModel},
		{name: "frame-model-probe-off", path: "/responses", sticky: model.StickyModeModel},
		{name: "frame-model-probe-on", path: "/responses", probe: true, sticky: model.StickyModeModel},
		{name: "api-sticky-probe-off", path: "/responses", sticky: model.StickyModeAPIType},
		{name: "api-sticky-probe-on", path: "/responses", probe: true, sticky: model.StickyModeAPIType},
		{name: "sticky-off-probe-off", path: "/responses", sticky: model.StickyModeOff},
		{name: "sticky-off-probe-on", path: "/responses", probe: true, sticky: model.StickyModeOff},
		{name: "model-rule-probe-off", path: "/responses", sticky: model.StickyModeModel, modelRule: true},
		{name: "model-rule-probe-on", path: "/responses", probe: true, sticky: model.StickyModeModel, modelRule: true},
		{name: "replacement-frame-model", path: "/responses", probe: true, sticky: model.StickyModeModel, replaceBeforeVisible: true},
		{name: "replacement-probed-model", path: "/responses", probe: true, sticky: model.StickyModeModel, modelRule: true, replaceBeforeVisible: true},
	} {
		t.Run(test.name, func(t *testing.T) { testAccountRecoveryReconnect(t, test) })
	}
}

func testAccountRecoveryReconnect(t *testing.T, test accountRecoveryReconnectCase) {
	t.Helper()
	const visible = `{"type":"response.created","response":{"id":"a-visible","model":"gpt-5"}}`
	const complete = `{"type":"response.completed","response":{"id":"b-complete","status":"completed","model":"gpt-5"}}`
	var providers []model.Provider
	providerIDs := []string{"a", "b"}
	if test.replaceBeforeVisible {
		providerIDs = append([]string{"pre"}, providerIDs...)
	}
	for _, id := range providerIDs {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			_, _, err = conn.Read(r.Context())
			if err != nil {
				return
			}
			if id == "pre" {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"error","status":429,"error":{"type":"rate_limit_exceeded","message":"provider unavailable"}}`))
				_, _, _ = conn.Read(r.Context())
			} else if id == "a" {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(visible))
				payload := fmt.Sprintf(`{"type":"error","error":{"type":"usage_limit_reached","message":"quota exhausted","resets_at":%d}}`, time.Now().Add(time.Hour).Unix())
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(payload))
				_, _, _ = conn.Read(r.Context())
			} else {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(complete))
				_ = conn.Close(websocket.StatusNormalClosure, "complete")
			}
		}))
		t.Cleanup(upstream.Close)
		p := routingTestProvider(id)
		p.UsageLimitPolicy = model.ProviderUsageLimitPolicySuspend
		p.APITypes[0].BaseURL = upstream.URL
		providers = append(providers, p)
	}
	selection := &accountRecoverySelector{providers: providers}
	seedStore := &accountRecoverySeedStore{}
	health := newTrackingHealthManager()
	persistence := newMockStore()
	if test.modelRule {
		persistence.routingPolicies = []model.RoutingPolicy{{Enabled: true, APIType: APITypeCodex, ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-"}}
	}
	gateway := newTestGateway(t, Config{Store: persistence, Selector: selection, Health: health, VisibleContinuitySeedStore: seedStore, Logger: zaptest.NewLogger(t)})
	selection.gateway = gateway
	var ids atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateway.Handle(r.Context(), w, r, RequestConfig{ConversationRecoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation, GlobalMaxAttempts: 3, GlobalAuthMode: "bearer", ProbeClientModel: test.probe, StickyMode: test.sticky, StickyTTL: time.Minute}, APITypeCodex, fmt.Sprintf("reconnect-%d", ids.Add(1)), time.Now())
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(server)+test.path, codexDialOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5"}`))
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != visible {
		t.Fatalf("visible=%q err=%v", data, err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil || !strings.Contains(string(data), ErrCodeWebSocketReconnect) {
		t.Fatalf("notice=%q err=%v", data, err)
	}
	seed, _ := seedStore.snapshot()
	beforeReconnect, sticky, _ := selection.snapshot()
	if seed == nil || len(beforeReconnect) == 0 || seed.ContinuityKey != beforeReconnect[0].continuityKey || seed.Purpose != model.VisibleContinuitySeedAccountRecovery || len(seed.ExcludedProviderIDs) != 1 || seed.ExcludedProviderIDs[0] != "a" || len(health.getSuspendCalls()) != 1 || len(sticky) != 0 {
		t.Fatalf("state not published before notice: seed=%+v health=%v sticky=%v", seed, health.getSuspendCalls(), sticky)
	}
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusServiceRestart {
		t.Fatalf("close=%v want actual 1012", err)
	}
	next, _, err := websocket.Dial(ctx, wsURL(server)+test.path, codexDialOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer next.CloseNow()
	_ = next.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","previous_response_id":"a-visible"}`))
	_, data, err = next.Read(ctx)
	if err != nil || string(data) != complete {
		t.Fatalf("reconnect=%q err=%v", data, err)
	}
	_, _, _ = next.Read(ctx)
	wantSticky := 1
	if test.sticky == model.StickyModeOff {
		wantSticky = 0
	}
	waitFor(t, func() bool { _, sticky, _ := selection.snapshot(); return len(sticky) == wantSticky }, testPollTimeout)
	observations, sticky, _ := selection.snapshot()
	remaining, consumed := seedStore.snapshot()
	reconnectIndex, wantSelections := 1, 2
	if test.replaceBeforeVisible {
		reconnectIndex, wantSelections = 2, 4
	}
	if len(observations) != wantSelections || !observations[reconnectIndex].excluded["a"] || observations[reconnectIndex].mode != model.SwitchModeFailover || !observations[reconnectIndex].continuity || consumed != 1 || remaining != nil || len(sticky) != wantSticky || wantSticky > 0 && sticky[0] != "b" || len(health.getSuspendCalls()) != 1 {
		t.Fatalf("reconnect state: selections=%+v consumed=%d seed=%+v sticky=%v health=%v", observations, consumed, remaining, sticky, health.getSuspendCalls())
	}
}

func TestAccountRecoveryProjectedHandshakeVisibility(t *testing.T) {
	for _, probe := range []bool{false, true} {
		t.Run(fmt.Sprintf("probe-%t", probe), func(t *testing.T) {
			const turn = "handshake-state-a"
			const completed = `{"type":"response.completed","response":{"status":"completed","id":"b-done","model":"gpt-5"}}`
			var providers []model.Provider
			for _, id := range []string{"a", "b"} {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if id == "a" {
						w.Header().Set("X-Codex-Turn-State", turn)
					}
					conn, err := websocket.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					_, _, err = conn.Read(r.Context())
					if err != nil {
						return
					}
					if id == "a" {
						_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"error","status":429,"error":{"type":"rate_limit_exceeded","message":"quota exhausted"}}`))
						_, _, _ = conn.Read(r.Context())
					} else {
						_ = conn.Write(r.Context(), websocket.MessageText, []byte(completed))
						_ = conn.Close(websocket.StatusNormalClosure, "done")
					}
				}))
				t.Cleanup(upstream.Close)
				p := routingTestProvider(id)
				p.APITypes[0].BaseURL = upstream.URL
				providers = append(providers, p)
			}
			selection := &accountRecoverySelector{providers: providers}
			seeds := &accountRecoverySeedStore{}
			persistence := newMockStore()
			if probe {
				persistence.routingPolicies = []model.RoutingPolicy{{Enabled: true, APIType: APITypeCodex, ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-"}}
			}
			gateway := newTestGateway(t, Config{Store: persistence, Selector: selection, VisibleContinuitySeedStore: seeds, Logger: zaptest.NewLogger(t)})
			selection.gateway = gateway
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gateway.Handle(r.Context(), w, r, RequestConfig{ConversationRecoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation, GlobalMaxAttempts: 2, GlobalAuthMode: "bearer", ProbeClientModel: probe, StickyMode: model.StickyModeModel}, APITypeCodex, "handshake-visibility", time.Now())
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			path := "/responses?model=gpt-5"
			if probe {
				path = "/responses"
			}
			conn, response, err := websocket.Dial(ctx, wsURL(server)+path, codexDialOptions())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			if probe {
				if response.Header.Get("X-Codex-Turn-State") != "" {
					t.Fatal("probe projected upstream state into prior downstream 101")
				}
				observed, _, _ := selection.snapshot()
				if len(observed) != 0 {
					t.Fatal("probe selected provider before downstream accept and model input")
				}
			} else if response.Header.Get("X-Codex-Turn-State") != turn {
				t.Fatal("non-probe omitted projected state")
			}
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5"}`)); err != nil {
				t.Fatal(err)
			}
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if probe {
				if string(data) != completed {
					t.Fatalf("probe should replace before visibility: %q", data)
				}
				_, _, _ = conn.Read(ctx)
				observed, _, _ := selection.snapshot()
				if len(observed) != 2 || observed[1].mode != model.SwitchModeReplacement || observed[1].continuity {
					t.Fatalf("probe sequence=%+v", observed)
				}
			} else {
				if !strings.Contains(string(data), ErrCodeWebSocketReconnect) {
					t.Fatalf("projected handshake did not require reconnect: %q", data)
				}
				_, _, err = conn.Read(ctx)
				if websocket.CloseStatus(err) != websocket.StatusServiceRestart {
					t.Fatalf("handshake-visible close=%v", err)
				}
				observed, sticky, _ := selection.snapshot()
				seed, _ := seeds.snapshot()
				if len(observed) != 1 || len(sticky) != 0 || seed == nil || seed.Purpose != model.VisibleContinuitySeedAccountRecovery || seed.OriginProviderID != "a" {
					t.Fatalf("handshake visible state: selections=%+v sticky=%v seed=%+v", observed, sticky, seed)
				}
			}
		})
	}
}
