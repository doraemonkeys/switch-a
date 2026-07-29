package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/admin"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	selectorpkg "github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

const (
	activeRequestCleanupMaxAge = 10 * time.Minute
	activeRequestStaleBy       = time.Minute
)

// consistencyMockStore reuses the broad server mock while overriding only the
// request-lifecycle reads this test needs. Keeping proxy and admin backed by
// the same store avoids a fake mismatch caused by divergent test fixtures.
type consistencyMockStore struct {
	*mockStore
	configs      map[string]string
	healthStates map[string]*model.HealthState
	providers    []model.Provider
}

func newConsistencyMockStore(providers []model.Provider) *consistencyMockStore {
	copiedProviders := make([]model.Provider, len(providers))
	copy(copiedProviders, providers)

	return &consistencyMockStore{
		mockStore: &mockStore{},
		configs: map[string]string{
			proxy.ConfigKeyTrustProxyHeaders:      "true",
			proxy.ConfigKeyUserHeader:             "X-User-ID",
			proxy.ConfigKeyMaxBodySize:            "10",
			proxy.ConfigKeyAuthMode:               "auto",
			proxy.ConfigKeyGlobalMaxAttempts:      "0",
			proxy.ConfigKeyUpstreamConnectTimeout: "10",
			proxy.ConfigKeyFirstByteTimeout:       "10",
			proxy.ConfigKeyUpstreamReadTimeout:    "0",
			proxy.ConfigKeySSEIdleTimeout:         "60",
			proxy.ConfigKeyStickyMode:             string(model.StickyModeModel),
			proxy.ConfigKeyStickyTTL:              "300",
		},
		healthStates: make(map[string]*model.HealthState),
		providers:    copiedProviders,
	}
}

func (s *consistencyMockStore) ListProviders(context.Context) ([]model.Provider, error) {
	providers := make([]model.Provider, len(s.providers))
	copy(providers, s.providers)
	return providers, nil
}

func (s *consistencyMockStore) ListProvidersByAPIType(_ context.Context, apiType string) ([]model.Provider, error) {
	providers := make([]model.Provider, 0, len(s.providers))
	for _, provider := range s.providers {
		if _, ok := provider.APITypeConfig(apiType); ok {
			providers = append(providers, provider)
		}
	}
	return providers, nil
}

func (s *consistencyMockStore) GetConfig(_ context.Context, key string) (string, error) {
	return s.configs[key], nil
}

func (s *consistencyMockStore) GetHealthState(_ context.Context, providerID string) (*model.HealthState, error) {
	state, ok := s.healthStates[providerID]
	if !ok || state == nil {
		return nil, nil
	}
	cloned := *state
	return &cloned, nil
}

func TestAdminStatusAndActiveRequestsRemainConsistentAfterActiveRegistryCleanup(t *testing.T) {
	provider := model.Provider{
		ID:          "gpt-example-t8bzwz",
		Name:        "gpt-example",
		Concurrency: 1,
		Enabled:     true,
	}

	store := newConsistencyMockStore([]model.Provider{provider})
	store.healthStates[provider.ID] = &model.HealthState{
		ProviderID:   provider.ID,
		Available:    true,
		SuccessCount: 60,
		FailCount:    29,
	}

	limiter := selectorpkg.NewConcurrencyLimiter()
	activeRegistry := proxy.NewActiveRequestRegistryWithHook(func(req proxy.ActiveRequest, reason proxy.ActiveRequestRemovalReason) {
		if reason == proxy.ActiveRequestRemovalReasonStale {
			return
		}
		limiter.Release(req.ProviderID)
	})

	adminHandler := admin.NewHandler(admin.Config{
		Store:         store,
		Concurrency:   limiter,
		ActiveReqList: activeRegistry,
		Logger:        zap.NewNop(),
	})

	if !limiter.TryAcquire(provider.ID, provider.Concurrency) {
		t.Fatalf("failed to acquire concurrency slot for provider %q", provider.ID)
	}

	requestDone := make(chan struct{})

	activeRegistry.RegisterWithDone(&proxy.ActiveRequest{
		RequestID:   "req-stale-1",
		ProviderID:  provider.ID,
		Model:       "gpt-5.4",
		APIType:     proxy.APITypeCodex,
		IsWebSocket: true,
		StartedAt:   time.Now().Add(-(activeRequestCleanupMaxAge + activeRequestStaleBy)),
	}, requestDone)

	// A quiet request can still be legitimately alive when transport timeouts are
	// disabled, so cleanup must not treat age alone as proof that the request ended.
	if removed := activeRegistry.CleanupStale(activeRequestCleanupMaxAge); removed != 0 {
		t.Fatalf("CleanupStale removed %d request(s), want 0", removed)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	statusRecorder := httptest.NewRecorder()
	adminHandler.GetStatus(statusRecorder, statusReq)

	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", statusRecorder.Code, http.StatusOK)
	}

	var status admin.SystemStatus
	if err := json.NewDecoder(statusRecorder.Body).Decode(&status); err != nil {
		t.Fatalf("decode status response: %v", err)
	}

	activeReq := httptest.NewRequest(http.MethodGet, "/admin/api/requests/active", nil)
	activeRecorder := httptest.NewRecorder()
	adminHandler.GetActiveRequests(activeRecorder, activeReq)

	if activeRecorder.Code != http.StatusOK {
		t.Fatalf("active requests status code = %d, want %d", activeRecorder.Code, http.StatusOK)
	}

	var active admin.ActiveRequestsResponse
	if err := json.NewDecoder(activeRecorder.Body).Decode(&active); err != nil {
		t.Fatalf("decode active requests response: %v", err)
	}

	if len(status.Providers) != 1 {
		t.Fatalf("len(status.Providers) = %d, want 1", len(status.Providers))
	}

	if got, want := status.Providers[0].CurrentRequests, int64(active.Count); got != want {
		t.Fatalf(
			"admin endpoints diverged after active registry cleanup: current_requests=%d active_count=%d",
			got,
			want,
		)
	}
}
