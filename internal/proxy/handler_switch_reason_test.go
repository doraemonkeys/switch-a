package proxy

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

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
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Provider 1",

			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 1,
			Priority:   1,
			APITypes:   []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "key1"),
		withTestStaticCredential(model.Provider{
			ID:   "p2",
			Name: "Provider 2",

			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 0,
			Priority:   0,
			APITypes:   []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "key2"),
	}

	handler := newProxyCodexTestHandler(t, Config{
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
				withTestStaticCredential(model.Provider{
					ID:   "p1",
					Name: "Provider 1",

					AuthMode:   "bearer",
					Enabled:    true,
					MaxRetries: 2,
					Priority:   1,
					APITypes:   []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
				}, "", "key1"),
				withTestStaticCredential(model.Provider{
					ID:   "p2",
					Name: "Provider 2",

					AuthMode:   "bearer",
					Enabled:    true,
					MaxRetries: 0,
					Priority:   0,
					APITypes:   []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: upstreamServer.URL}},
				}, "", "key2"),
			}

			handler := newProxyCodexTestHandler(t, Config{
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

func TestHandler_RecordsUsageLimitSwitchReasonAndSuspendsConfiguredProvider(t *testing.T) {
	resetAt := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	resetAtEpoch := resetAt.Unix()
	callCount := 0
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set(headerCodexPrimaryUsedPercent, "100")
			w.Header().Set(headerCodexSecondaryUsedPercent, "67")
			w.Header().Set(headerCodexPrimaryResetAt, strconv.FormatInt(resetAtEpoch, 10))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(resetAtEpoch, 10) + `}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"success"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Provider 1",

			AuthMode:         "bearer",
			UsageLimitPolicy: model.ProviderUsageLimitPolicySuspend,
			Enabled:          true,
			MaxRetries:       2,
			Priority:         1,
			APITypes:         []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "key1"),
		withTestStaticCredential(model.Provider{
			ID:   "p2",
			Name: "Provider 2",

			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 0,
			Priority:   0,
			APITypes:   []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "key2"),
	}
	healthMgr := newMockHealthManager()
	healthMgr.availableProviders["p1"] = true
	healthMgr.availableProviders["p2"] = true

	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Health: healthMgr,
		Logger: zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	waitFor(t, func() bool {
		return store.AttemptsLen() >= 2
	}, testPollTimeout)

	attempts := store.LastAttempts(2)
	if len(attempts) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", len(attempts))
	}

	if attempts[0].SwitchReason != SwitchReasonUsageLimitReached {
		t.Fatalf("first attempt switch_reason = %q, want %q", attempts[0].SwitchReason, SwitchReasonUsageLimitReached)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 upstream calls (immediate switch on usage limit), got %d", callCount)
	}
	if got := healthMgr.suspendReasons["p1"]; got != usageLimitAutoDisableReason {
		t.Fatalf("suspend reason = %q, want %q", got, usageLimitAutoDisableReason)
	}
	if got := healthMgr.suspendedUntil["p1"]; !got.Equal(resetAt) {
		t.Fatalf("suspended until = %v, want %v", got, resetAt)
	}
}

func TestHandler_RecordsUsageLimitSwitchReasonWithoutSuspendingSwitchOnlyProvider(t *testing.T) {
	resetAt := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	resetAtEpoch := resetAt.Unix()
	callCount := 0
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set(headerCodexPrimaryUsedPercent, "100")
			w.Header().Set(headerCodexSecondaryUsedPercent, "67")
			w.Header().Set(headerCodexPrimaryResetAt, strconv.FormatInt(resetAtEpoch, 10))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(resetAtEpoch, 10) + `}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"success"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		withTestStaticCredential(model.Provider{
			ID:   "p1",
			Name: "Provider 1",

			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 2,
			Priority:   1,
			APITypes:   []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "key1"),
		withTestStaticCredential(model.Provider{
			ID:   "p2",
			Name: "Provider 2",

			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 0,
			Priority:   0,
			APITypes:   []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude", BaseURL: upstreamServer.URL}},
		}, "", "key2"),
	}
	healthMgr := newMockHealthManager()
	healthMgr.availableProviders["p1"] = true
	healthMgr.availableProviders["p2"] = true

	handler := newProxyCodexTestHandler(t, Config{
		Store:  store,
		Health: healthMgr,
		Logger: zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	waitFor(t, func() bool {
		return store.AttemptsLen() >= 2
	}, testPollTimeout)

	attempts := store.LastAttempts(2)
	if len(attempts) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", len(attempts))
	}

	if attempts[0].SwitchReason != SwitchReasonUsageLimitReached {
		t.Fatalf("first attempt switch_reason = %q, want %q", attempts[0].SwitchReason, SwitchReasonUsageLimitReached)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 upstream calls (immediate switch on usage limit), got %d", callCount)
	}
	if got := healthMgr.suspendReasons["p1"]; got != "" {
		t.Fatalf("suspend reason = %q, want empty", got)
	}
	if got := healthMgr.suspendedUntil["p1"]; !got.IsZero() {
		t.Fatalf("suspended until = %v, want zero time", got)
	}
}
