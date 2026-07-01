package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

func TestHandler_ServeHTTP_SameProviderRetryRevalidatesFreshAuthStateBeforeReuse(t *testing.T) {
	t.Parallel()

	var (
		primaryAttempts  atomic.Int32
		fallbackAttempts atomic.Int32
		retrySelections  atomic.Int32
	)

	store := newMockStore()
	primaryID := "retry-auth-primary"
	fallbackID := "retry-auth-fallback"

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts.Add(1)
		store.mu.Lock()
		store.authStates[primaryID] = &model.ProviderAuthState{
			ProviderID: primaryID,
			Status:     model.ProviderAuthStatusReauthRequired,
		}
		store.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"retryable"}`))
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"provider":"fallback"}`))
	}))
	defer fallbackServer.Close()

	storePrimary := model.Provider{
		ID:         primaryID,
		Name:       "Retry Auth Primary",
		APIKey:     "primary-key",
		AuthMode:   "bearer",
		Enabled:    true,
		MaxRetries: 1,
		APITypes:   []model.ProviderAPIType{{ProviderID: primaryID, APIType: "claude", BaseURL: primaryServer.URL}},
	}
	selectedPrimary := storePrimary
	selectedPrimary.AuthState = &model.ProviderAuthState{
		ProviderID: primaryID,
		Status:     model.ProviderAuthStatusActive,
	}
	fallbackProvider := &model.Provider{
		ID:       fallbackID,
		Name:     "Retry Auth Fallback",
		APIKey:   "fallback-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: fallbackID, APIType: "claude", BaseURL: fallbackServer.URL}},
	}

	store.providers = []model.Provider{storePrimary, *fallbackProvider}
	store.authStates[primaryID] = &model.ProviderAuthState{
		ProviderID: primaryID,
		Status:     model.ProviderAuthStatusActive,
	}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: &selectedPrimary, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			retrySelections.Add(1)
			if !excludeIDs[primaryID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryID)
			}
			return fallbackProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: mockSel,
		Logger:   zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "retry-user")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, `"provider":"fallback"`) {
		t.Fatalf("body = %q, want fallback response", body)
	}

	waitFor(t, func() bool { return store.AttemptsLen() >= 2 }, testPollTimeout)

	if got := primaryAttempts.Load(); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := fallbackAttempts.Load(); got != 1 {
		t.Fatalf("fallback attempts = %d, want 1", got)
	}
	if got := retrySelections.Load(); got != 1 {
		t.Fatalf("retry selections = %d, want 1", got)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].ProviderID != primaryID || attempts[1].ProviderID != fallbackID {
		t.Fatalf("attempt provider order = [%s %s], want [%s %s]", attempts[0].ProviderID, attempts[1].ProviderID, primaryID, fallbackID)
	}
}

func TestHandler_ServeHTTP_SameProviderRetryRevalidatesExactProviderPolicyBeforeReuse(t *testing.T) {
	t.Parallel()

	var (
		primaryAttempts  atomic.Int32
		fallbackAttempts atomic.Int32
		retrySelections  atomic.Int32
	)

	store := newMockStore()
	primaryID := "retry-policy-primary"
	fallbackID := "retry-policy-fallback"

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts.Add(1)
		store.mu.Lock()
		store.routingPolicies = []model.RoutingPolicy{{
			APIType:          "claude",
			Enabled:          true,
			TargetProviderID: stringPtr(fallbackID),
		}}
		store.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"retryable"}`))
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"provider":"fallback"}`))
	}))
	defer fallbackServer.Close()

	primaryProvider := &model.Provider{
		ID:         primaryID,
		Name:       "Retry Policy Primary",
		APIKey:     "primary-key",
		AuthMode:   "bearer",
		Enabled:    true,
		MaxRetries: 1,
		APITypes:   []model.ProviderAPIType{{ProviderID: primaryID, APIType: "claude", BaseURL: primaryServer.URL}},
	}
	fallbackProvider := &model.Provider{
		ID:       fallbackID,
		Name:     "Retry Policy Fallback",
		APIKey:   "fallback-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: fallbackID, APIType: "claude", BaseURL: fallbackServer.URL}},
	}

	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}

	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			retrySelections.Add(1)
			if !excludeIDs[primaryID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryID)
			}
			return fallbackProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: mockSel,
		Logger:   zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "retry-user")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, `"provider":"fallback"`) {
		t.Fatalf("body = %q, want fallback response", body)
	}

	waitFor(t, func() bool { return store.AttemptsLen() >= 2 }, testPollTimeout)

	if got := primaryAttempts.Load(); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := fallbackAttempts.Load(); got != 1 {
		t.Fatalf("fallback attempts = %d, want 1", got)
	}
	if got := retrySelections.Load(); got != 1 {
		t.Fatalf("retry selections = %d, want 1", got)
	}

	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].ProviderID != primaryID || attempts[1].ProviderID != fallbackID {
		t.Fatalf("attempt provider order = [%s %s], want [%s %s]", attempts[0].ProviderID, attempts[1].ProviderID, primaryID, fallbackID)
	}
}
