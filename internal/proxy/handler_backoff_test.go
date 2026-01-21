package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

func TestBackoff_AppliesDelayOnSameProviderRetry(t *testing.T) {
	// Track timing between calls to verify backoff delay
	var callTimes []time.Time
	var callTimesMu sync.Mutex
	var callCount atomic.Int32

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimesMu.Lock()
		callTimes = append(callTimes, time.Now())
		callTimesMu.Unlock()
		count := callCount.Add(1)
		if count < 3 {
			// First two attempts fail with 500 (transient, retryable)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server error"}`))
			return
		}
		// Third attempt succeeds
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
			BaseURL:    upstreamServer.URL,
			APIKey:     "key1",
			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 2, // Allow 2 retries (3 attempts total)
			Backoff: model.BackoffPolicy{
				InitialDelay: model.Duration(50 * time.Millisecond),
				MaxDelay:     model.Duration(200 * time.Millisecond),
				Multiplier:   2.0,
				Jitter:       false, // Disable jitter for predictable timing
			},
		},
	}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	startTime := time.Now()
	handler.ServeHTTP(w, req)
	totalDuration := time.Since(startTime)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify we had 3 attempts
	if callCount.Load() != 3 {
		t.Fatalf("expected 3 upstream calls, got %d", callCount.Load())
	}

	// Verify backoff delays were applied:
	// First retry: 50ms delay
	// Second retry: 100ms delay (50ms * 2)
	// Total minimum delay: 150ms
	minExpectedDuration := 150 * time.Millisecond
	if totalDuration < minExpectedDuration {
		t.Errorf("total duration %v < expected minimum %v - backoff may not be applied", totalDuration, minExpectedDuration)
	}

	// Verify timing gaps between calls
	callTimesMu.Lock()
	callTimesCopy := make([]time.Time, len(callTimes))
	copy(callTimesCopy, callTimes)
	callTimesMu.Unlock()

	if len(callTimesCopy) >= 3 {
		gap1 := callTimesCopy[1].Sub(callTimesCopy[0])
		gap2 := callTimesCopy[2].Sub(callTimesCopy[1])

		// First retry should have ~50ms delay (allow some tolerance)
		if gap1 < 40*time.Millisecond {
			t.Errorf("first retry gap %v < 40ms - expected ~50ms backoff", gap1)
		}
		// Second retry should have ~100ms delay
		if gap2 < 80*time.Millisecond {
			t.Errorf("second retry gap %v < 80ms - expected ~100ms backoff", gap2)
		}
	}
}

func TestBackoff_NoDelayWhenBackoffNotConfigured(t *testing.T) {
	var callCount atomic.Int32

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server error"}`))
			return
		}
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
			BaseURL:    upstreamServer.URL,
			APIKey:     "key1",
			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 2,
			// No Backoff configured (zero value)
		},
	}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	startTime := time.Now()
	handler.ServeHTTP(w, req)
	totalDuration := time.Since(startTime)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Without backoff, retries should happen immediately
	// Total duration should be much less than 100ms (no artificial delays)
	if totalDuration > 100*time.Millisecond {
		t.Errorf("total duration %v > 100ms - unexpected delay without backoff configured", totalDuration)
	}
}

func TestBackoff_RespectsMaxDelay(t *testing.T) {
	var callTimes []time.Time
	var callTimesMu sync.Mutex
	var callCount atomic.Int32

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimesMu.Lock()
		callTimes = append(callTimes, time.Now())
		callTimesMu.Unlock()
		count := callCount.Add(1)
		if count < 5 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server error"}`))
			return
		}
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
			BaseURL:    upstreamServer.URL,
			APIKey:     "key1",
			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 4, // Allow 4 retries (5 attempts total)
			Backoff: model.BackoffPolicy{
				InitialDelay: model.Duration(50 * time.Millisecond),
				MaxDelay:     model.Duration(100 * time.Millisecond), // Cap at 100ms
				Multiplier:   2.0,
				Jitter:       false,
			},
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

	// Verify delays are capped at MaxDelay (100ms)
	// Retry 1: 50ms, Retry 2: 100ms (capped from 100ms), Retry 3: 100ms (capped from 200ms), Retry 4: 100ms (capped)
	callTimesMu.Lock()
	callTimesCopy := make([]time.Time, len(callTimes))
	copy(callTimesCopy, callTimes)
	callTimesMu.Unlock()

	if len(callTimesCopy) >= 4 {
		// Third and fourth retries should have delays capped at ~100ms
		gap3 := callTimesCopy[3].Sub(callTimesCopy[2])
		gap4 := callTimesCopy[4].Sub(callTimesCopy[3])

		// Both should be around 100ms (not exceeding MaxDelay significantly)
		maxExpected := 150 * time.Millisecond // Allow some tolerance
		if gap3 > maxExpected || gap4 > maxExpected {
			t.Errorf("delays exceeded MaxDelay: gap3=%v, gap4=%v", gap3, gap4)
		}
	}
}

func TestBackoff_CancelsOnContextDone(t *testing.T) {
	var callCount atomic.Int32

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// Always fail to trigger retry
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:         "p1",
			Name:       "Provider 1",
			BaseURL:    upstreamServer.URL,
			APIKey:     "key1",
			AuthMode:   "bearer",
			Enabled:    true,
			MaxRetries: 10, // Many retries
			Backoff: model.BackoffPolicy{
				InitialDelay: model.Duration(500 * time.Millisecond), // Long delay
				MaxDelay:     model.Duration(5 * time.Second),
				Multiplier:   2.0,
				Jitter:       false,
			},
		},
	}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	// Create request with context that will be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	startTime := time.Now()
	handler.ServeHTTP(w, req)
	totalDuration := time.Since(startTime)

	// Should complete quickly due to context cancellation during backoff wait
	// (not waiting for all retries with 500ms+ delays)
	if totalDuration > 300*time.Millisecond {
		t.Errorf("total duration %v > 300ms - context cancellation may not be working during backoff", totalDuration)
	}

	// Should have made at least 1 attempt before context cancelled
	if callCount.Load() < 1 {
		t.Error("expected at least 1 upstream call")
	}

	// Should have made fewer attempts than MaxRetries (cancelled during backoff)
	if callCount.Load() > 5 {
		t.Errorf("expected fewer calls due to context cancellation, got %d", callCount.Load())
	}
}

func TestBackoff_NoDelayOnProviderSwitch(t *testing.T) {
	// When switching to a different provider, backoff should NOT apply
	// (backoff is only for same-provider retries)
	var callCount atomic.Int32
	var callTimes []time.Time
	var callTimesMu sync.Mutex

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimesMu.Lock()
		callTimes = append(callTimes, time.Now())
		callTimesMu.Unlock()
		count := callCount.Add(1)
		if count < 2 {
			// First provider fails
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server error"}`))
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
			BaseURL:    upstreamServer.URL,
			APIKey:     "key1",
			AuthMode:   "bearer",
			Enabled:    true,
			Priority:   1,
			MaxRetries: 0, // No retries - switch to next provider
			Backoff: model.BackoffPolicy{
				InitialDelay: model.Duration(500 * time.Millisecond), // Would cause long delay if applied
				MaxDelay:     model.Duration(5 * time.Second),
				Multiplier:   2.0,
				Jitter:       false,
			},
		},
		{
			ID:         "p2",
			Name:       "Provider 2",
			BaseURL:    upstreamServer.URL,
			APIKey:     "key2",
			AuthMode:   "bearer",
			Enabled:    true,
			Priority:   0,
			MaxRetries: 0,
		},
	}

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	startTime := time.Now()
	handler.ServeHTTP(w, req)
	totalDuration := time.Since(startTime)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Provider switch should be immediate (no backoff delay)
	// Even though p1 has 500ms backoff, it should not apply when switching to p2
	if totalDuration > 200*time.Millisecond {
		t.Errorf("total duration %v > 200ms - backoff may be incorrectly applied on provider switch", totalDuration)
	}

	callTimesMu.Lock()
	callTimesCopy := make([]time.Time, len(callTimes))
	copy(callTimesCopy, callTimes)
	callTimesMu.Unlock()

	if len(callTimesCopy) >= 2 {
		gap := callTimesCopy[1].Sub(callTimesCopy[0])
		// Gap should be minimal (no backoff on provider switch)
		if gap > 100*time.Millisecond {
			t.Errorf("gap between provider switch %v > 100ms - backoff should not apply", gap)
		}
	}
}
