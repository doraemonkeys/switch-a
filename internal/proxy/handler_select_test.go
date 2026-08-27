package proxy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

// mockSelector implements the Selector interface for testing.
type mockSelector struct {
	selectWithMetadataFunc      func(ctx context.Context, req *model.SelectRequest) (*selectResult, error)
	selectExcludingFunc         func(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error)
	reserveSameProviderDispatch func(context.Context, providerLease, *model.SelectRequest) (sameProviderDispatchPermit, error)

	mu                  sync.Mutex
	stickyUpdates       []stickyUpdate // Records all UpdateStickyWithTTL calls
	continuityEvictions []string       // Records provider IDs evicted from sticky continuity
}

// stickyUpdate records a single call to UpdateStickyWithTTL.
type stickyUpdate struct {
	ProviderID string
	Model      string
	TTL        time.Duration
}

// selectResult mirrors selector.SelectResult for testing.
type selectResult struct {
	Provider        *model.Provider
	Metadata        selector.SelectionMetadata
	FromStickyCache bool
}

func (m *mockSelector) selectionResult(ctx context.Context, req *model.SelectRequest) (*selectResult, error) {
	if m.selectWithMetadataFunc != nil {
		result, err := m.selectWithMetadataFunc(ctx, req)
		if result == nil {
			return nil, err
		}
		metadata := result.Metadata
		if metadata.Source == "" {
			source := selector.SelectionSourceStrategy
			if result.FromStickyCache {
				source = selector.SelectionSourceStickyContinuity
			}
			metadata = selector.BuildSelectionMetadata(req, source)
		} else {
			enriched := selector.BuildSelectionMetadata(req, metadata.Source)
			if metadata.SwitchMode == "" {
				metadata.SwitchMode = enriched.SwitchMode
			}
			if !metadata.ContinuitySeeded {
				metadata.ContinuitySeeded = enriched.ContinuitySeeded
			}
			if metadata.ContinuityOriginProviderID == "" {
				metadata.ContinuityOriginProviderID = enriched.ContinuityOriginProviderID
			}
			if metadata.ContinuitySeedObservedAt.IsZero() {
				metadata.ContinuitySeedObservedAt = enriched.ContinuitySeedObservedAt
			}
			if metadata.ContinuitySeedAgeAtSelectionMs == nil {
				metadata.ContinuitySeedAgeAtSelectionMs = enriched.ContinuitySeedAgeAtSelectionMs
			}
		}
		return &selectResult{
			Provider: result.Provider,
			Metadata: metadata,
		}, err
	}
	return nil, nil
}

func (m *mockSelector) SelectInitial(ctx context.Context, req *model.SelectRequest) (*providerSelection, error) {
	result, err := m.selectionResult(ctx, req)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Provider == nil {
		return nil, internal.ErrNoProvider
	}
	return &providerSelection{
		provider: result.Provider,
		lease:    newLocalProviderLease(result.Provider),
		metadata: result.Metadata,
	}, nil
}

func (m *mockSelector) SelectActive(
	ctx context.Context,
	req *model.SelectRequest,
	active providerLease,
) (*providerSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if active == nil || !active.Held() || active.Provider() == nil {
		return nil, internal.ErrNoProvider
	}
	return &providerSelection{
		provider: active.Provider(),
		lease:    active,
		metadata: selector.BuildSelectionMetadata(req, selector.SelectionSourceActiveContinuity),
	}, nil
}

type mockRetryPermit struct {
	provider          *model.Provider
	ledger            errorrule.RetryLedger
	ruleKey           errorrule.ProviderRuleKey
	globalMaxAttempts uint
	released          bool
}

func (p *mockRetryPermit) Provider() *model.Provider { return p.provider }
func (p *mockRetryPermit) Activate() (errorrule.RetryLedger, error) {
	if p.released {
		return errorrule.RetryLedger{}, internal.ErrNoProvider
	}
	p.released = true
	return p.ledger.StartRuleRetry(p.ruleKey, p.globalMaxAttempts)
}
func (p *mockRetryPermit) Release() bool {
	if p.released {
		return false
	}
	p.released = true
	return true
}

func (m *mockSelector) ReserveSameProviderRetry(
	ctx context.Context,
	input sameProviderRetryReservation,
) (retryPermit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.current == nil || !input.current.Held() || input.current.Provider() == nil {
		return nil, internal.ErrNoProvider
	}
	return &mockRetryPermit{
		provider:          input.current.Provider(),
		ledger:            input.ledger,
		ruleKey:           input.ruleKey,
		globalMaxAttempts: input.globalMaxAttempts,
	}, nil
}

func (m *mockSelector) ReserveSameProviderDispatch(
	ctx context.Context,
	current providerLease,
	request *model.SelectRequest,
) (sameProviderDispatchPermit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.reserveSameProviderDispatch != nil {
		return m.reserveSameProviderDispatch(ctx, current, request)
	}
	if current == nil || !current.Held() || current.Provider() == nil {
		return nil, internal.ErrNoProvider
	}
	return newLocalSameProviderDispatchPermit(current.Provider(), current), nil
}

func (m *mockSelector) ReserveAlternate(
	ctx context.Context,
	req *model.SelectRequest,
	excluded map[string]bool,
) (alternateProviderReservation, error) {
	if m.selectExcludingFunc == nil {
		return nil, internal.ErrNoProvider
	}
	provider, err := m.selectExcludingFunc(ctx, req, excluded)
	if err != nil {
		return nil, err
	}
	if provider == nil || excluded[provider.ID] {
		return nil, internal.ErrNoProvider
	}
	return &localAlternateReservation{
		provider: provider,
		lease:    newLocalProviderLease(provider),
		metadata: selector.BuildSelectionMetadata(req, selector.SelectionSourceAlternate),
	}, nil
}

func (m *mockSelector) UpdateStickyWithTTL(req *model.SelectRequest, providerID string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	update := stickyUpdate{ProviderID: providerID, TTL: ttl}
	if req != nil {
		update.Model = req.Model
	}
	m.stickyUpdates = append(m.stickyUpdates, update)
}

func (m *mockSelector) EvictProviderContinuity(providerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.continuityEvictions = append(m.continuityEvictions, providerID)
}

// StickyUpdatesLen returns the number of sticky updates in a thread-safe manner.
func (m *mockSelector) StickyUpdatesLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stickyUpdates)
}

// LastStickyUpdate returns the latest sticky update in a thread-safe manner.
func (m *mockSelector) LastStickyUpdate() (stickyUpdate, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.stickyUpdates) == 0 {
		return stickyUpdate{}, false
	}
	return m.stickyUpdates[len(m.stickyUpdates)-1], true
}

func (m *mockSelector) ContinuityEvictions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.continuityEvictions))
	copy(result, m.continuityEvictions)
	return result
}

// mockHealthManager implements the HealthManager interface for testing.
type mockHealthManager struct {
	availableProviders map[string]bool
	recoverCalled      map[string]bool
	suspendedUntil     map[string]time.Time
	suspendReasons     map[string]string
}

func newMockHealthManager() *mockHealthManager {
	return &mockHealthManager{
		availableProviders: make(map[string]bool),
		recoverCalled:      make(map[string]bool),
		suspendedUntil:     make(map[string]time.Time),
		suspendReasons:     make(map[string]string),
	}
}

func (m *mockHealthManager) MarkSuccess(_ context.Context, _ string) {}

func (m *mockHealthManager) MarkFailure(_ context.Context, _ string, _ error) bool {
	return false
}

func (m *mockHealthManager) RecoverIfExpired(_ context.Context, providerID string) bool {
	m.recoverCalled[providerID] = true
	return false
}

func (m *mockHealthManager) IsAvailable(_ context.Context, providerID string) bool {
	available, ok := m.availableProviders[providerID]
	if !ok {
		return true // Default to available
	}
	return available
}

func (m *mockHealthManager) ManualDisable(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockHealthManager) SuspendUntil(_ context.Context, providerID string, disabledUntil time.Time, reason string) error {
	m.availableProviders[providerID] = false
	m.suspendedUntil[providerID] = disabledUntil
	m.suspendReasons[providerID] = reason
	return nil
}

func (m *mockHealthManager) ManualEnable(_ context.Context, _ string) error {
	return nil
}

func (m *mockHealthManager) ResetCircuitBreaker(_ string) {}

// TestSelectProviderWithTracking_NoSelector tests fallback mode when no selector is configured.
func TestHandler_SuspendProviderUntilEvictsContinuity(t *testing.T) {
	t.Parallel()

	mockSel := &mockSelector{}
	healthMgr := newMockHealthManager()
	handler := newProxyCodexTestHandler(t, Config{
		Store:    newMockStore(),
		Selector: mockSel,
		Health:   healthMgr,
		Logger:   zap.NewNop(),
	})

	disabledUntil := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)
	handler.suspendProviderUntil(context.Background(), "p1", disabledUntil, usageLimitAutoDisableReason)

	if got := healthMgr.suspendReasons["p1"]; got != usageLimitAutoDisableReason {
		t.Fatalf("suspend reason = %q, want %q", got, usageLimitAutoDisableReason)
	}
	if got := healthMgr.suspendedUntil["p1"]; !got.Equal(disabledUntil) {
		t.Fatalf("suspended until = %v, want %v", got, disabledUntil)
	}
	evictions := mockSel.ContinuityEvictions()
	if len(evictions) != 1 || evictions[0] != "p1" {
		t.Fatalf("continuity evictions = %v, want [p1]", evictions)
	}
}

func TestSelectProviderFallback_NoProviders(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{} // Empty
	logger := zap.NewNop()

	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	selectReq := &model.SelectRequest{
		APIType: "claude",
	}

	provider, err := handler.selectProviderFallback(ctx, selectReq, 0, nil)
	if err == nil {
		t.Fatal("expected error for no providers")
	}
	if provider != nil {
		t.Error("expected nil provider")
	}
}

func TestSelectProviderFallback_FiltersByRoutingPolicyAndAuthState(t *testing.T) {
	allowedGroup := "g-allowed"
	blockedGroup := "g-blocked"

	store := newMockStore()
	allowed := withTestStaticCredential(model.Provider{
		ID: "p-allowed", Name: "Allowed Provider", Enabled: true, GroupID: &allowedGroup,
		APITypes: []model.ProviderAPIType{{ProviderID: "p-allowed", APIType: "codex", BaseURL: "https://allowed.example"}},
	}, "codex", "allowed-key")
	reauth := withTestStaticCredential(model.Provider{
		ID: "p-reauth", Name: "Reauth Provider", Enabled: true, GroupID: &allowedGroup,
		APITypes: []model.ProviderAPIType{{ProviderID: "p-reauth", APIType: "codex", BaseURL: "https://reauth.example"}},
	}, "codex", "reauth-key")
	reauth.CredentialSessions[0].Credential.AuthState.Status = credentialsession.AuthStatusReauthRequired
	outside := withTestStaticCredential(model.Provider{
		ID: "p-outside", Name: "Outside Policy Provider", Enabled: true, GroupID: &blockedGroup,
		APITypes: []model.ProviderAPIType{{ProviderID: "p-outside", APIType: "codex", BaseURL: "https://outside.example"}},
	}, "codex", "outside-key")
	store.providers = []model.Provider{
		allowed, reauth, outside,
	}
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled: true,
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "g-allowed"}},
		},
	}

	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	provider, err := handler.selectProviderFallback(context.Background(), &model.SelectRequest{
		APIType: "codex",
	}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "p-allowed" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p-allowed")
	}
}

func TestSelectProviderFallback_ExactProviderRuleFiltersCandidates(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		withTestStaticCredential(model.Provider{
			ID:       "p-other",
			Name:     "Other Provider",
			Enabled:  true,
			Priority: 0,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-other", APIType: "codex", BaseURL: "https://other.example"}},
		}, "codex", "other-key"),
		withTestStaticCredential(model.Provider{
			ID:       "p-exact",
			Name:     "Exact Provider",
			Enabled:  true,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-exact", APIType: "codex", BaseURL: "https://exact.example"}},
		}, "codex", "exact-key"),
	}
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled:          true,
			APIType:          "codex",
			TargetProviderID: stringPtr("p-exact"),
		},
	}

	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	provider, err := handler.selectProviderFallback(context.Background(), &model.SelectRequest{
		APIType: "codex",
	}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider to be selected")
	}
	if provider.ID != "p-exact" {
		t.Fatalf("provider.ID = %q, want %q", provider.ID, "p-exact")
	}
}

// TestSelectProviderFallback_RoundRobin tests round-robin selection across attempts.
func TestSelectProviderFallback_RoundRobin(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		withTestStaticCredential(model.Provider{ID: "p1", Name: "Provider 1", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://p1.example"}}}, "claude", "p1-key"),
		withTestStaticCredential(model.Provider{ID: "p2", Name: "Provider 2", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: "https://p2.example"}}}, "claude", "p2-key"),
		withTestStaticCredential(model.Provider{ID: "p3", Name: "Provider 3", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p3", APIType: "claude", BaseURL: "https://p3.example"}}}, "claude", "p3-key"),
	}
	logger := zap.NewNop()

	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	selectReq := &model.SelectRequest{
		APIType: "claude",
	}

	// Make multiple selections and verify round-robin behavior
	seen := make(map[string]int)
	for range 9 {
		provider, err := handler.selectProviderFallback(ctx, selectReq, 0, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[provider.ID]++
	}

	// Each provider should be selected roughly equally (3 times each)
	for id, count := range seen {
		if count != 3 {
			t.Errorf("provider %s selected %d times, expected 3", id, count)
		}
	}
}

// TestSelectProviderFallback_AttemptOffset tests that attempts offset the selection.
func TestSelectProviderFallback_AttemptOffset(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		withTestStaticCredential(model.Provider{ID: "p1", Name: "Provider 1", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://p1.example"}}}, "claude", "p1-key"),
		withTestStaticCredential(model.Provider{ID: "p2", Name: "Provider 2", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: "https://p2.example"}}}, "claude", "p2-key"),
	}
	logger := zap.NewNop()

	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Logger: logger,
	})

	ctx := context.Background()
	selectReq := &model.SelectRequest{
		APIType: "claude",
	}

	// Get first provider
	p0, _ := handler.selectProviderFallback(ctx, selectReq, 0, nil)

	// Reset counter to get predictable behavior for test
	handler.fallbackCounter.Store(0)

	// Get provider with attempt offset
	p1, _ := handler.selectProviderFallback(ctx, selectReq, 1, nil)

	// With 2 providers, attempt=0 and attempt=1 should give different providers
	// when starting from the same counter position
	if p0.ID == p1.ID {
		t.Error("attempt offset should select different providers")
	}
}
