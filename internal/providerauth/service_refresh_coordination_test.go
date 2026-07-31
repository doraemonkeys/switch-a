package providerauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

const refreshCoordinationTestTimeout = time.Second

type credentialMutationOwnershipTestKey struct{}

type blockingCredentialMutationStore struct {
	mu sync.Mutex

	provider *model.Provider
	permit   chan struct{}
	acquired chan struct{}
	released chan struct{}
	once     sync.Once
	held     bool

	credentialUpdates int
	authStateUpdates  int
}

func newBlockingCredentialMutationStore(provider *model.Provider, permit chan struct{}) *blockingCredentialMutationStore {
	return &blockingCredentialMutationStore{
		provider: cloneProviderForRefreshCoordinationTest(provider),
		permit:   permit,
		acquired: make(chan struct{}),
		released: make(chan struct{}),
	}
}

func (s *blockingCredentialMutationStore) WithProviderCredentialMutations(
	ctx context.Context,
	providerIDs []string,
) (context.Context, func(), error) {
	if len(providerIDs) != 1 || strings.TrimSpace(providerIDs[0]) == "" {
		return nil, nil, fmt.Errorf("unexpected provider IDs: %#v", providerIDs)
	}
	s.once.Do(func() { close(s.acquired) })
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-s.permit:
	}
	s.mu.Lock()
	s.held = true
	s.mu.Unlock()
	ownedCtx := context.WithValue(ctx, credentialMutationOwnershipTestKey{}, s)
	return ownedCtx, func() {
		s.mu.Lock()
		s.held = false
		s.mu.Unlock()
		select {
		case <-s.released:
		default:
			close(s.released)
		}
	}, nil
}

func (s *blockingCredentialMutationStore) GetProvider(ctx context.Context, id string) (*model.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.held || ctx.Value(credentialMutationOwnershipTestKey{}) != s {
		return nil, fmt.Errorf("GetProvider(%q) called outside credential mutation lease", id)
	}
	if s.provider == nil || s.provider.ID != id {
		return nil, fmt.Errorf("provider %q not found", id)
	}
	return cloneProviderForRefreshCoordinationTest(s.provider), nil
}

func (s *blockingCredentialMutationStore) UpdateProviderCredential(
	ctx context.Context,
	_ string,
	_ model.ProviderCredentialType,
	_ string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.held || ctx.Value(credentialMutationOwnershipTestKey{}) != s {
		return fmt.Errorf("credential update ran outside credential mutation lease")
	}
	s.credentialUpdates++
	return nil
}

func (s *blockingCredentialMutationStore) UpdateProviderAuthState(
	ctx context.Context,
	_ string,
	_ *model.ProviderAuthState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.held || ctx.Value(credentialMutationOwnershipTestKey{}) != s {
		return fmt.Errorf("auth-state update ran outside credential mutation lease")
	}
	s.authStateUpdates++
	return nil
}

func (s *blockingCredentialMutationStore) replaceProvider(provider *model.Provider) {
	s.mu.Lock()
	s.provider = cloneProviderForRefreshCoordinationTest(provider)
	s.mu.Unlock()
}

func (s *blockingCredentialMutationStore) isHeld() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

func cloneProviderForRefreshCoordinationTest(provider *model.Provider) *model.Provider {
	if provider == nil {
		return nil
	}
	cloned := *provider
	cloned.Credential = provider.Credential.Clone()
	cloned.AuthState = provider.AuthState.Clone()
	cloned.APITypes = append([]model.ProviderAPIType(nil), provider.APITypes...)
	return &cloned
}

func chatGPTProviderForRefreshCoordinationTest(
	t *testing.T,
	providerID string,
	credential model.ChatGPTProviderCredential,
) *model.Provider {
	t.Helper()
	provider := &model.Provider{ID: providerID, CredentialType: model.ProviderCredentialTypeChatGPT}
	if err := applyChatGPTCredential(provider, &credential); err != nil {
		t.Fatalf("applyChatGPTCredential returned error: %v", err)
	}
	return provider
}

func TestEnsureFreshChatGPTCredential_ReloadsAfterImportMutation(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	stale := chatGPTProviderForRefreshCoordinationTest(t, "provider", model.ChatGPTProviderCredential{
		AccessToken:   "stale-access",
		RefreshToken:  "stale-refresh",
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
		AccountID:     "acct",
		LastRefresh:   now.Add(-time.Hour),
		ExpiresAt:     now.Add(30 * time.Second),
	})
	imported := chatGPTProviderForRefreshCoordinationTest(t, "provider", model.ChatGPTProviderCredential{
		AccessToken:   "imported-access",
		RefreshToken:  "imported-refresh",
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
		AccountID:     "acct",
		LastRefresh:   now,
		ExpiresAt:     now.Add(2 * time.Hour),
	})
	permit := make(chan struct{})
	store := newBlockingCredentialMutationStore(stale, permit)
	var refreshCalls atomic.Int32
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			refreshCalls.Add(1)
			return nil, fmt.Errorf("refresh should be skipped after reloading imported credential")
		}},
	})

	type result struct {
		credential *model.ChatGPTProviderCredential
		err        error
	}
	resultCh := make(chan result, 1)
	go func() {
		credential, err := service.ensureFreshChatGPTCredential(context.Background(), stale, false)
		resultCh <- result{credential: credential, err: err}
	}()

	select {
	case <-store.acquired:
	case <-time.After(refreshCoordinationTestTimeout):
		t.Fatal("refresh did not wait for the provider credential mutation lease")
	}
	store.replaceProvider(imported)
	close(permit)

	var got result
	select {
	case got = <-resultCh:
	case <-time.After(refreshCoordinationTestTimeout):
		t.Fatal("refresh did not finish after import released the mutation lease")
	}
	if got.err != nil {
		t.Fatalf("ensureFreshChatGPTCredential returned error: %v", got.err)
	}
	if got.credential.RefreshToken != "imported-refresh" || got.credential.AccessToken != "imported-access" {
		t.Fatalf("credential = %#v, want credential committed by import", got.credential)
	}
	if refreshCalls.Load() != 0 {
		t.Fatalf("outbound refresh calls = %d, want 0", refreshCalls.Load())
	}
	decoded, err := DecodeProviderChatGPTCredential(stale)
	if err != nil {
		t.Fatalf("DecodeProviderChatGPTCredential returned error: %v", err)
	}
	if decoded.RefreshToken != "imported-refresh" {
		t.Fatalf("passed provider retained stale refresh token: %#v", decoded)
	}
	select {
	case <-store.released:
	case <-time.After(refreshCoordinationTestTimeout):
		t.Fatal("credential mutation lease was not released")
	}
}

func TestRefreshProviderUsage_ReloadsAfterImportMutation(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	stale := chatGPTProviderForRefreshCoordinationTest(t, "provider", model.ChatGPTProviderCredential{
		AccessToken:   "stale-access",
		RefreshToken:  "stale-refresh",
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
		AccountID:     "acct",
		LastRefresh:   now.Add(-time.Hour),
		ExpiresAt:     now.Add(time.Hour),
	})
	imported := chatGPTProviderForRefreshCoordinationTest(t, "provider", model.ChatGPTProviderCredential{
		AccessToken:   "imported-access",
		RefreshToken:  "imported-refresh",
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
		AccountID:     "acct",
		LastRefresh:   now,
		ExpiresAt:     now.Add(2 * time.Hour),
	})
	permit := make(chan struct{})
	store := newBlockingCredentialMutationStore(stale, permit)
	var usageCalls atomic.Int32
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(req *http.Request) (*http.Response, error) {
			usageCalls.Add(1)
			if !store.isHeld() {
				t.Fatal("usage request ran outside credential mutation lease")
			}
			if got := req.Header.Get("Authorization"); got != "Bearer imported-access" {
				t.Fatalf("Authorization = %q, want imported credential", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"plan_type":"plus",
					"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":1785416400}}
				}`)),
			}, nil
		}},
	})

	type result struct {
		refreshed bool
		err       error
	}
	resultCh := make(chan result, 1)
	go func() {
		refreshed, err := service.RefreshProviderUsage(context.Background(), stale)
		resultCh <- result{refreshed: refreshed, err: err}
	}()

	select {
	case <-store.acquired:
	case <-time.After(refreshCoordinationTestTimeout):
		t.Fatal("usage refresh did not wait for the provider credential mutation lease")
	}
	store.replaceProvider(imported)
	close(permit)

	select {
	case got := <-resultCh:
		if got.err != nil || !got.refreshed {
			t.Fatalf("RefreshProviderUsage = (%t, %v), want successful refresh", got.refreshed, got.err)
		}
	case <-time.After(refreshCoordinationTestTimeout):
		t.Fatal("usage refresh did not finish after import released the mutation lease")
	}
	if usageCalls.Load() != 1 {
		t.Fatalf("outbound usage calls = %d, want 1", usageCalls.Load())
	}
	decoded, err := DecodeProviderChatGPTCredential(stale)
	if err != nil {
		t.Fatalf("DecodeProviderChatGPTCredential returned error: %v", err)
	}
	if decoded.AccessToken != "imported-access" || stale.AuthState.Status != ProviderAuthStatusActive {
		t.Fatalf("provider after usage refresh = %#v, want imported active credential", stale)
	}
	if stale.AuthState.UsageSnapshot == nil {
		t.Fatal("provider usage snapshot was not applied")
	}
	store.mu.Lock()
	authStateUpdates := store.authStateUpdates
	store.mu.Unlock()
	if authStateUpdates != 1 {
		t.Fatalf("auth-state updates = %d, want 1", authStateUpdates)
	}
}

func TestRefreshProviderCredentials_HoldsMutationLeaseThroughExchangeAndPersistence(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	current := chatGPTProviderForRefreshCoordinationTest(t, "provider", model.ChatGPTProviderCredential{
		AccessToken:   "current-access",
		RefreshToken:  "current-refresh",
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
		AccountID:     "acct",
		LastRefresh:   now.Add(-time.Hour),
		ExpiresAt:     now.Add(30 * time.Second),
	})
	permit := make(chan struct{})
	close(permit)
	store := newBlockingCredentialMutationStore(current, permit)
	newIDToken := chatgptAuthJWT(t, "acct", "user@example.com", "plus", now.Add(time.Hour))
	service := NewService(Config{
		Clock:           fixedClock{now: now},
		CredentialStore: store,
		HTTPClient: stubHTTPDoer{do: func(req *http.Request) (*http.Response, error) {
			if !store.isHeld() {
				t.Fatal("outbound exchange ran outside credential mutation lease")
			}
			payload, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("io.ReadAll returned error: %v", err)
			}
			values, err := url.ParseQuery(string(payload))
			if err != nil {
				t.Fatalf("url.ParseQuery returned error: %v", err)
			}
			if values.Get("refresh_token") != "current-refresh" {
				t.Fatalf("refresh_token = %q, want latest stored token", values.Get("refresh_token"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"access_token":"refreshed-access",
					"refresh_token":"refreshed-refresh",
					"id_token":"` + newIDToken + `"
				}`)),
			}, nil
		}},
	})

	refreshed, err := service.ensureFreshChatGPTCredential(context.Background(), current, true)
	if err != nil {
		t.Fatalf("ensureFreshChatGPTCredential returned error: %v", err)
	}
	if refreshed.RefreshToken != "refreshed-refresh" {
		t.Fatalf("RefreshToken = %q, want refreshed-refresh", refreshed.RefreshToken)
	}
	if store.isHeld() {
		t.Fatal("credential mutation lease remained held after persistence")
	}
	store.mu.Lock()
	credentialUpdates := store.credentialUpdates
	authStateUpdates := store.authStateUpdates
	store.mu.Unlock()
	if credentialUpdates != 1 || authStateUpdates != 1 {
		t.Fatalf("persistence calls = (%d credential, %d auth state), want (1, 1)", credentialUpdates, authStateUpdates)
	}
}

func TestInvalidateProviderCredentialSessions_DetachesSupersededRefreshGeneration(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	service := NewService(Config{Clock: fixedClock{now: now}})
	providerID := "provider-imported"
	oldCall := &inFlightChatGPTRefresh{done: make(chan struct{})}
	service.refreshMu.Lock()
	service.inFlightRefreshes[providerID] = oldCall
	service.recentChatGPTRefreshes[providerID] = recentChatGPTRefresh{
		credential: &model.ChatGPTProviderCredential{AccessToken: "old-access"},
		expiresAt:  now.Add(time.Minute),
	}
	service.refreshMu.Unlock()

	service.InvalidateProviderCredentialSessions([]string{"", " " + providerID + " ", providerID})
	service.refreshMu.Lock()
	if _, exists := service.inFlightRefreshes[providerID]; exists {
		service.refreshMu.Unlock()
		t.Fatal("superseded in-flight generation remained joinable after import")
	}
	if _, exists := service.recentChatGPTRefreshes[providerID]; exists {
		service.refreshMu.Unlock()
		t.Fatal("recent credential remained reusable after import")
	}
	newCall := &inFlightChatGPTRefresh{done: make(chan struct{})}
	service.inFlightRefreshes[providerID] = newCall
	service.refreshMu.Unlock()

	service.finishChatGPTRefresh(providerID, oldCall, nil, errors.New("old generation finished"))
	service.refreshMu.Lock()
	current := service.inFlightRefreshes[providerID]
	service.refreshMu.Unlock()
	if current != newCall {
		t.Fatalf("current in-flight call = %p, want replacement %p", current, newCall)
	}
}
