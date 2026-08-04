package websocketproxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap/zaptest"
)

type leaseLifecycleSessions struct {
	accept      bool
	registered  map[string]ActiveSession
	unregisters int
}

func (*leaseLifecycleSessions) NewLiveTraffic() LiveTraffic { return nil }

func (s *leaseLifecycleSessions) Register(session ActiveSession, _ <-chan struct{}, _ LiveTraffic) bool {
	if !s.accept || session.RequestID == "" || session.Lease == nil || !session.Lease.Held() {
		return false
	}
	if s.registered == nil {
		s.registered = make(map[string]ActiveSession)
	}
	s.registered[session.RequestID] = session
	return true
}

func (s *leaseLifecycleSessions) Unregister(requestID string) bool {
	session, found := s.registered[requestID]
	if !found {
		return false
	}
	delete(s.registered, requestID)
	s.unregisters++
	return session.Lease.Release()
}

func (*leaseLifecycleSessions) UpdateModel(string, string) {}
func (*leaseLifecycleSessions) MarkDataReceived(string)    {}

func (s *leaseLifecycleSessions) FindActiveLeaseForRequest(*model.SelectRequest) (ProviderLease, bool) {
	for _, session := range s.registered {
		return session.Lease, true
	}
	return nil, false
}

type malformedProviderLease struct {
	ProviderLease
	providerID string
	identity   uintptr
}

func (l malformedProviderLease) ProviderID() string          { return l.providerID }
func (l malformedProviderLease) CapabilityIdentity() uintptr { return l.identity }

func TestNormalizeProviderSelectionOwnsEveryRejectedCapability(t *testing.T) {
	provider := routingTestProvider("provider-a")
	wantErr := errors.New("selection failed")

	tests := []struct {
		name  string
		build func() ProviderSelection
		err   error
	}{
		{
			name: "selector result plus error",
			build: func() ProviderSelection {
				return routingTestSelection(&provider, "", 1)
			},
			err: wantErr,
		},
		{
			name: "provider identity mismatch",
			build: func() ProviderSelection {
				selection := routingTestSelection(&provider, "", 1)
				selection.Lease = malformedProviderLease{
					ProviderLease: selection.Lease,
					providerID:    "different-provider",
					identity:      selection.Lease.CapabilityIdentity(),
				}
				return selection
			},
		},
		{
			name: "zero capability identity",
			build: func() ProviderSelection {
				selection := routingTestSelection(&provider, "", 1)
				selection.Lease = malformedProviderLease{
					ProviderLease: selection.Lease,
					providerID:    provider.ID,
				}
				return selection
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selection := tt.build()
			lease := selection.Lease
			result, err := normalizeProviderSelection(selection, tt.err)
			if result.Provider() != nil || err == nil {
				t.Fatalf("normalizeProviderSelection() = (%#v, %v), want rejection", result, err)
			}
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if lease.Held() {
				t.Fatal("rejected capability remained held")
			}
		})
	}
}

func TestWebSocketOrchestratorTransfersOrRetainsLeaseOwnershipExplicitly(t *testing.T) {
	provider := routingTestProvider("provider-a")

	tests := []struct {
		name       string
		registered bool
		switching  bool
	}{
		{name: "registry terminal cleanup", registered: true},
		{name: "registry provider switch", registered: true, switching: true},
		{name: "registration rejection retains cleanup", registered: false},
		{name: "registryless provider switch", switching: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selection := routingTestSelection(&provider, "", 7)
			var sessions *leaseLifecycleSessions
			var activeSessions ActiveSessions
			if tt.registered || tt.name != "registryless provider switch" {
				sessions = &leaseLifecycleSessions{accept: tt.registered}
				activeSessions = sessions
			}
			handler := &Gateway{activeSessions: activeSessions, logger: zaptest.NewLogger(t)}
			orchestrator := &WebSocketSessionOrchestrator{
				handler:           handler,
				requestID:         "request-a",
				selectReq:         &model.SelectRequest{APIType: APITypeCodex},
				currentProvider:   &provider,
				currentLease:      selection.Lease,
				excludedProviders: make(map[string]bool),
				startTime:         time.Now(),
			}
			orchestrator.trackCurrentAttempt(selection)
			if orchestrator.activeRegistered != tt.registered {
				t.Fatalf("activeRegistered = %v, want %v", orchestrator.activeRegistered, tt.registered)
			}

			if tt.switching {
				orchestrator.excludeCurrentProvider()
				if !orchestrator.excludedProviders[provider.ID] {
					t.Fatal("provider switch did not preserve exclusion")
				}
			} else {
				orchestrator.cleanup()
			}
			if selection.Lease.Held() {
				t.Fatal("terminal ownership edge did not release the exact lease")
			}
			if sessions != nil && tt.registered && sessions.unregisters != 1 {
				t.Fatalf("registry unregisters = %d, want 1", sessions.unregisters)
			}
			orchestrator.cleanup()
			if sessions != nil && tt.registered && sessions.unregisters != 1 {
				t.Fatalf("duplicate cleanup unregistered %d times", sessions.unregisters)
			}
		})
	}
}

func TestWebSocketLeaseCleanupIsIndependentWithinOneProviderGeneration(t *testing.T) {
	provider := routingTestProvider("provider-a")
	sessions := &leaseLifecycleSessions{accept: true}
	first := routingTestSelection(&provider, "", 9)
	second := routingTestSelection(&provider, "", 9)
	if first.Lease.CapabilityIdentity() == second.Lease.CapabilityIdentity() {
		t.Fatal("independent sessions shared capability identity")
	}

	if !sessions.Register(ActiveSession{RequestID: "first", Lease: first.Lease}, nil, nil) ||
		!sessions.Register(ActiveSession{RequestID: "second", Lease: second.Lease}, nil, nil) {
		t.Fatal("failed to register independent sessions")
	}
	if !sessions.Unregister("second") || second.Lease.Held() || !first.Lease.Held() {
		t.Fatal("out-of-order second cleanup affected first capability")
	}
	if !sessions.Unregister("first") || first.Lease.Held() {
		t.Fatal("first capability did not release independently")
	}
	if sessions.Unregister("first") {
		t.Fatal("duplicate unregister reported ownership")
	}
}

func TestFallbackProviderLeaseHasCopySafeOpaqueIdentity(t *testing.T) {
	provider := routingTestProvider("fallback")
	gateway := &Gateway{}
	lease := gateway.newFallbackProviderLease(&provider)
	if lease.Provider() != &provider || lease.ProviderID() != provider.ID || lease.Generation() == 0 ||
		lease.CapabilityIdentity() == 0 || !lease.Held() {
		t.Fatalf("fallback lease = %#v", lease)
	}
	copyOfLease := lease
	if !copyOfLease.Release() || lease.Release() || lease.Held() {
		t.Fatal("fallback lease release was not copy-safe and idempotent")
	}

	var nilLease *fallbackProviderLease
	if nilLease.Provider() != nil || nilLease.ProviderID() != "" || nilLease.Generation() != 0 || nilLease.CapabilityIdentity() != 0 ||
		nilLease.Held() || nilLease.Release() {
		t.Fatal("nil fallback lease exposed ownership")
	}
}

func TestTryActiveProviderFallbackRejectsMissingOrReleasedSource(t *testing.T) {
	provider := routingTestProvider("active")
	request := &model.SelectRequest{APIType: APITypeCodex, StickyMode: model.StickyModeModel}
	selection := routingTestSelection(&provider, "", 4)
	selectorSource := &routingTestSelector{active: routingTestSelection(&provider, "", 4)}

	tests := []struct {
		name     string
		sessions ActiveSessions
		prepare  func()
	}{
		{name: "no registry"},
		{name: "not found", sessions: &routingTestActiveSessions{}},
		{
			name:     "released source",
			sessions: &routingTestActiveSessions{lease: selection.Lease, found: true},
			prepare:  func() { selection.Lease.Release() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepare != nil {
				tt.prepare()
			}
			gateway := &Gateway{selector: selectorSource, activeSessions: tt.sessions}
			if result, found := gateway.tryActiveProviderFallback(context.Background(), request); found || result.Lease != nil {
				t.Fatalf("active fallback = (%#v, %v), want none", result, found)
			}
		})
	}
}
