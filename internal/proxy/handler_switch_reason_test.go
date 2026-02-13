package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

func TestTryIncrementAndExhaustsProvider_ReturnsCorrectSwitchReason(t *testing.T) {
	tests := []struct {
		name             string
		statusCode       int
		providerAttempt  int
		maxRetries       int
		isAvailable      bool
		wantExhausted    bool
		wantSwitchReason string
	}{
		{
			name:             "permanent_error_401",
			statusCode:       401,
			providerAttempt:  0,
			maxRetries:       2,
			isAvailable:      true,
			wantExhausted:    true,
			wantSwitchReason: "permanent_error_401",
		},
		{
			name:             "permanent_error_402",
			statusCode:       402,
			providerAttempt:  0,
			maxRetries:       2,
			isAvailable:      true,
			wantExhausted:    true,
			wantSwitchReason: "permanent_error_402",
		},
		{
			name:             "permanent_error_403",
			statusCode:       403,
			providerAttempt:  0,
			maxRetries:       2,
			isAvailable:      true,
			wantExhausted:    true,
			wantSwitchReason: "permanent_error_403",
		},
		{
			name:             "circuit_breaker_triggered",
			statusCode:       500,
			providerAttempt:  0,
			maxRetries:       2,
			isAvailable:      false, // Circuit breaker tripped
			wantExhausted:    true,
			wantSwitchReason: SwitchReasonCircuitBreakerTriggered,
		},
		{
			name:             "max_retries_exhausted",
			statusCode:       500,
			providerAttempt:  2, // Already used all retries (0, 1, 2)
			maxRetries:       2,
			isAvailable:      true,
			wantExhausted:    true,
			wantSwitchReason: SwitchReasonMaxRetriesExhausted,
		},
		{
			name:             "retries_remaining_no_switch",
			statusCode:       500,
			providerAttempt:  0,
			maxRetries:       2,
			isAvailable:      true,
			wantExhausted:    false,
			wantSwitchReason: "",
		},
		{
			name:             "429_with_retries_remaining",
			statusCode:       429,
			providerAttempt:  0,
			maxRetries:       2,
			isAvailable:      true,
			wantExhausted:    false,
			wantSwitchReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthMgr := newMockHealthManager()
			healthMgr.availableProviders["test-provider"] = tt.isAvailable

			handler := &Handler{
				logger: zap.NewNop(),
				health: healthMgr,
			}

			state := &retryState{
				statusCode:      tt.statusCode,
				providerAttempt: tt.providerAttempt,
				currentProvider: &model.Provider{
					ID:         "test-provider",
					MaxRetries: tt.maxRetries,
				},
			}

			exhausted, switchReason := handler.tryIncrementAndExhaustProvider(context.Background(), state)

			if exhausted != tt.wantExhausted {
				t.Errorf("exhausted = %v, want %v", exhausted, tt.wantExhausted)
			}
			if switchReason != tt.wantSwitchReason {
				t.Errorf("switchReason = %q, want %q", switchReason, tt.wantSwitchReason)
			}
		})
	}
}

func TestFormatPermanentErrorReason(t *testing.T) {
	tests := []struct {
		statusCode int
		want       string
	}{
		{401, "permanent_error_401"},
		{402, "permanent_error_402"},
		{403, "permanent_error_403"},
	}

	for _, tt := range tests {
		got := formatPermanentErrorReason(tt.statusCode)
		if got != tt.want {
			t.Errorf("formatPermanentErrorReason(%d) = %q, want %q", tt.statusCode, got, tt.want)
		}
	}
}

func TestHandler_RecordsSwitchReasonInAttempts(t *testing.T) {
	// This test verifies that switch_reason is correctly recorded in attempt records
	// when a provider switch occurs due to max retries exhausted.
	failCount := 0
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failCount++
		if failCount <= 2 {
			// First two attempts from provider 1 fail with 500
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server error"}`))
			return
		}
		// Third attempt (from provider 2) succeeds
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"success"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:         "p1",
			Name:       "Provider 1",
			APIKey:     "key1",
			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 1, // Allow 1 retry (2 attempts total)
			Priority:   1,
			APITypes:   []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		},
		{
			ID:         "p2",
			Name:       "Provider 2",
			APIKey:     "key2",
			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 0,
			Priority:   0, // Lower priority, used after p1 exhausted
			APITypes:   []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: upstreamServer.URL}},
		},
	}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Wait for async log
	waitFor(t, func() bool {
		return store.AttemptsLen() >= 3
	}, testPollTimeout)

	attempts := store.LastAttempts(3)
	if len(attempts) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", len(attempts))
	}

	// The second attempt from p1 (index 1) should have switch_reason = "max_retries_exhausted"
	// because after this attempt, p1's retries are exhausted and we switch to p2.
	foundSwitchReason := false
	for _, attempt := range attempts {
		if attempt.SwitchReason == SwitchReasonMaxRetriesExhausted {
			foundSwitchReason = true
			break
		}
	}
	if !foundSwitchReason {
		t.Errorf("expected to find attempt with switch_reason=%q, got attempts: %+v",
			SwitchReasonMaxRetriesExhausted, attempts)
	}

	// Last successful attempt should have empty switch_reason
	lastAttempt := attempts[len(attempts)-1]
	if lastAttempt.SwitchReason != "" {
		t.Errorf("last successful attempt switch_reason = %q, want empty", lastAttempt.SwitchReason)
	}
}

func TestHandler_RecordsPermanentErrorSwitchReason(t *testing.T) {
	// Test that permanent errors (401, 402, 403) record the correct switch_reason
	tests := []struct {
		name             string
		statusCode       int
		wantSwitchReason string
	}{
		{"401_unauthorized", 401, "permanent_error_401"},
		{"402_payment_required", 402, "permanent_error_402"},
		{"403_forbidden", 403, "permanent_error_403"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if callCount == 1 {
					// First provider returns permanent error
					w.WriteHeader(tt.statusCode)
					_, _ = w.Write([]byte(`{"error":"permanent error"}`))
					return
				}
				// Second provider succeeds
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"response":"success"}`))
			}))
			defer upstreamServer.Close()

			store := newMockStore()
			store.providers = []model.Provider{
				{
					ID:         "p1",
					Name:       "Provider 1",
					APIKey:     "key1",
					AuthMode:   "bearer",
					Enabled:    true,
					MaxRetries: 2, // Even with retries, permanent error should switch immediately
					Priority:   1,
					APITypes:   []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
				},
				{
					ID:         "p2",
					Name:       "Provider 2",
					APIKey:     "key2",
					AuthMode:   "bearer",
					Enabled:    true,
					MaxRetries: 0,
					Priority:   0,
					APITypes:   []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: upstreamServer.URL}},
				},
			}

			handler := NewHandler(Config{
				Store:  store,
				Logger: zap.NewNop(),
			})

			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Wait for async log
			waitFor(t, func() bool {
				return store.AttemptsLen() >= 2
			}, testPollTimeout)

			attempts := store.LastAttempts(2)
			if len(attempts) < 1 {
				t.Fatalf("expected at least 1 attempt, got %d", len(attempts))
			}

			// First attempt should have the permanent error switch_reason
			firstAttempt := attempts[0]
			if firstAttempt.SwitchReason != tt.wantSwitchReason {
				t.Errorf("first attempt switch_reason = %q, want %q", firstAttempt.SwitchReason, tt.wantSwitchReason)
			}

			// Verify we switched to p2 (only 2 calls to upstream despite p1 having MaxRetries=2)
			if callCount != 2 {
				t.Errorf("expected 2 upstream calls (immediate switch on permanent error), got %d", callCount)
			}
		})
	}
}
