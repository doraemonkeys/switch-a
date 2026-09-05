package proxy

import (
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

// mockClock implements internal.Clock for testing.
type mockClock struct {
	current time.Time
}

func (c *mockClock) Now() time.Time {
	return c.current
}

func (c *mockClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}

func TestNewActiveRequestRegistry(t *testing.T) {
	r := NewActiveRequestRegistry()
	if r == nil {
		t.Fatal("NewActiveRequestRegistry returned nil")
	}
	if r.requests == nil {
		t.Error("requests map should be initialized")
	}
	if len(r.requests) != 0 {
		t.Error("requests map should be empty")
	}
	if r.keyIndex == nil {
		t.Error("keyIndex map should be initialized")
	}
	if r.stickyIndex == nil {
		t.Error("stickyIndex map should be initialized")
	}
}

func TestNewActiveRequestRegistryWithClock(t *testing.T) {
	clock := &mockClock{current: time.Now()}
	r := NewActiveRequestRegistryWithClock(clock)
	if r == nil {
		t.Fatal("NewActiveRequestRegistryWithClock returned nil")
	}
	if r.clock != clock {
		t.Error("expected injected clock to be set")
	}
	if r.keyIndex == nil {
		t.Error("keyIndex map should be initialized")
	}
	if r.stickyIndex == nil {
		t.Error("stickyIndex map should be initialized")
	}
}

func TestActiveRequestRegistry_RemovalHook(t *testing.T) {
	t.Run("unregister runs hook for tracked provider", func(t *testing.T) {
		removed := make([]ActiveRequest, 0, 1)
		reasons := make([]ActiveRequestRemovalReason, 0, 1)
		registry := NewActiveRequestRegistryWithHook(func(req ActiveRequest, reason ActiveRequestRemovalReason) {
			removed = append(removed, req)
			reasons = append(reasons, reason)
		})

		registry.Register(&ActiveRequest{
			RequestID:  "req-1",
			ProviderID: "provider-1",
		})

		registry.Unregister("req-1")

		if len(removed) != 1 {
			t.Fatalf("len(removed) = %d, want 1", len(removed))
		}
		if removed[0].ProviderID != "provider-1" {
			t.Fatalf("ProviderID = %q, want %q", removed[0].ProviderID, "provider-1")
		}
		if reasons[0] != ActiveRequestRemovalReasonExplicit {
			t.Fatalf("reason = %q, want %q", reasons[0], ActiveRequestRemovalReasonExplicit)
		}
	})

	t.Run("cleanup orphaned runs hook for removed provider", func(t *testing.T) {
		baseTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
		clock := &mockClock{current: baseTime}
		removed := make([]ActiveRequest, 0, 1)
		reasons := make([]ActiveRequestRemovalReason, 0, 1)
		registry := NewActiveRequestRegistryWithClockAndHook(clock, func(req ActiveRequest, reason ActiveRequestRemovalReason) {
			removed = append(removed, req)
			reasons = append(reasons, reason)
		})
		requestDone := make(chan struct{})

		registry.RegisterWithDone(&ActiveRequest{
			RequestID:  "req-stale",
			ProviderID: "provider-stale",
			StartedAt:  baseTime.Add(-45 * time.Minute),
		}, requestDone)
		close(requestDone)

		cleaned := registry.CleanupStale(30 * time.Minute)

		if cleaned != 1 {
			t.Fatalf("cleaned = %d, want 1", cleaned)
		}
		if len(removed) != 1 {
			t.Fatalf("len(removed) = %d, want 1", len(removed))
		}
		if removed[0].ProviderID != "provider-stale" {
			t.Fatalf("ProviderID = %q, want %q", removed[0].ProviderID, "provider-stale")
		}
		if reasons[0] != ActiveRequestRemovalReasonOrphaned {
			t.Fatalf("reason = %q, want %q", reasons[0], ActiveRequestRemovalReasonOrphaned)
		}
	})

	t.Run("provider handoff releases displaced provider once", func(t *testing.T) {
		removed := make([]ActiveRequest, 0, 1)
		reasons := make([]ActiveRequestRemovalReason, 0, 1)
		registry := NewActiveRequestRegistryWithHook(func(req ActiveRequest, reason ActiveRequestRemovalReason) {
			removed = append(removed, req)
			reasons = append(reasons, reason)
		})

		registry.Register(&ActiveRequest{
			RequestID:  "req-handoff",
			ProviderID: "provider-old",
		})
		registry.Register(&ActiveRequest{
			RequestID:  "req-handoff",
			ProviderID: "provider-new",
		})

		if len(removed) != 1 {
			t.Fatalf("len(removed) = %d, want 1", len(removed))
		}
		if removed[0].ProviderID != "provider-old" {
			t.Fatalf("ProviderID = %q, want %q", removed[0].ProviderID, "provider-old")
		}
		if reasons[0] != ActiveRequestRemovalReasonProviderHandoff {
			t.Fatalf("reason = %q, want %q", reasons[0], ActiveRequestRemovalReasonProviderHandoff)
		}
	})

	t.Run("quiet request with open lifecycle is not stale-cleaned", func(t *testing.T) {
		baseTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
		clock := &mockClock{current: baseTime}
		removed := make([]ActiveRequest, 0, 1)
		registry := NewActiveRequestRegistryWithClockAndHook(clock, func(req ActiveRequest, reason ActiveRequestRemovalReason) {
			removed = append(removed, req)
		})
		requestDone := make(chan struct{})

		registry.RegisterWithDone(&ActiveRequest{
			RequestID:  "req-live",
			ProviderID: "provider-live",
			StartedAt:  baseTime.Add(-45 * time.Minute),
		}, requestDone)

		cleaned := registry.CleanupStale(30 * time.Minute)

		if cleaned != 0 {
			t.Fatalf("cleaned = %d, want 0", cleaned)
		}
		if len(removed) != 0 {
			t.Fatalf("len(removed) = %d, want 0", len(removed))
		}
		list := registry.List()
		if len(list) != 1 {
			t.Fatalf("len(list) = %d, want 1", len(list))
		}
	})
}

func TestActiveRequestRegistry_Register(t *testing.T) {
	t.Run("register valid request", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		req := &ActiveRequest{
			RequestID:  "req-1",
			ProviderID: "provider-1",
			Model:      "claude-3-opus",
			APIType:    APITypeClaude,
			UserID:     "user-1",
			ClientIP:   "192.168.1.1",
			IsSSE:      false,
			StartedAt:  time.Now(),
		}

		r.Register(req)

		list := r.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 request, got %d", len(list))
		}
		if list[0].RequestID != "req-1" {
			t.Errorf("expected RequestID 'req-1', got %q", list[0].RequestID)
		}
	})

	t.Run("register nil request", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		r.Register(nil)

		list := r.List()
		if len(list) != 0 {
			t.Errorf("expected 0 requests after registering nil, got %d", len(list))
		}
	})

	t.Run("register duplicate overwrites", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		req1 := &ActiveRequest{
			RequestID:  "req-1",
			ProviderID: "provider-1",
			Model:      "model-a",
		}
		req2 := &ActiveRequest{
			RequestID:  "req-1",
			ProviderID: "provider-2",
			Model:      "model-b",
		}

		r.Register(req1)
		r.Register(req2)

		list := r.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 request after duplicate, got %d", len(list))
		}
		if list[0].ProviderID != "provider-2" {
			t.Errorf("expected ProviderID 'provider-2', got %q", list[0].ProviderID)
		}
		if list[0].Model != "model-b" {
			t.Errorf("expected Model 'model-b', got %q", list[0].Model)
		}
	})
}

func TestActiveRequestRegistry_Unregister(t *testing.T) {
	t.Run("unregister existing request", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		req := &ActiveRequest{RequestID: "req-1"}
		r.Register(req)

		r.Unregister("req-1")

		list := r.List()
		if len(list) != 0 {
			t.Errorf("expected 0 requests after unregister, got %d", len(list))
		}
	})

	t.Run("unregister non-existent request", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		req := &ActiveRequest{RequestID: "req-1"}
		r.Register(req)

		// Unregister a request that doesn't exist - should be a no-op
		r.Unregister("req-nonexistent")

		list := r.List()
		if len(list) != 1 {
			t.Errorf("expected 1 request after unregistering non-existent, got %d", len(list))
		}
	})
}

func TestActiveRequestRegistry_UpdateSSE(t *testing.T) {
	t.Run("update existing request", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		req := &ActiveRequest{
			RequestID: "req-1",
			IsSSE:     false,
		}
		r.Register(req)

		r.UpdateSSE("req-1", true)

		list := r.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 request, got %d", len(list))
		}
		if !list[0].IsSSE {
			t.Error("expected IsSSE to be true after update")
		}
	})

	t.Run("update non-existent request", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		req := &ActiveRequest{
			RequestID: "req-1",
			IsSSE:     false,
		}
		r.Register(req)

		// Update a non-existent request - should be a no-op
		r.UpdateSSE("req-nonexistent", true)

		list := r.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 request, got %d", len(list))
		}
		if list[0].IsSSE {
			t.Error("expected IsSSE to remain false for existing request")
		}
	})
}

func TestActiveRequestRegistry_List(t *testing.T) {
	t.Run("empty registry", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		list := r.List()
		if list == nil {
			t.Error("List should return non-nil slice")
		}
		if len(list) != 0 {
			t.Errorf("expected 0 requests, got %d", len(list))
		}
	})

	t.Run("multiple requests", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		r.Register(&ActiveRequest{RequestID: "req-1", Model: "model-a"})
		r.Register(&ActiveRequest{RequestID: "req-2", Model: "model-b"})
		r.Register(&ActiveRequest{RequestID: "req-3", Model: "model-c"})

		list := r.List()
		if len(list) != 3 {
			t.Fatalf("expected 3 requests, got %d", len(list))
		}

		// Verify all requests are present (order not guaranteed due to map iteration)
		ids := make(map[string]bool)
		for _, req := range list {
			ids[req.RequestID] = true
		}
		for _, id := range []string{"req-1", "req-2", "req-3"} {
			if !ids[id] {
				t.Errorf("expected request %q in list", id)
			}
		}
	})

	t.Run("list returns copy", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		r.Register(&ActiveRequest{RequestID: "req-1", Model: "original"})

		list := r.List()
		// Modify the returned slice
		list[0].Model = "modified"

		// Original should be unchanged
		newList := r.List()
		if newList[0].Model != "original" {
			t.Error("modifying returned list should not affect registry")
		}
	})
}

func TestActiveRequestRegistry_CleanupStale(t *testing.T) {
	t.Run("cleanup old requests", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		oldTime := time.Now().Add(-1 * time.Hour)
		recentTime := time.Now()

		r.Register(&ActiveRequest{RequestID: "old-1", StartedAt: oldTime})
		r.Register(&ActiveRequest{RequestID: "old-2", StartedAt: oldTime})
		r.Register(&ActiveRequest{RequestID: "recent", StartedAt: recentTime})

		removed := r.CleanupStale(30 * time.Minute)

		if removed != 2 {
			t.Errorf("expected 2 removed, got %d", removed)
		}

		list := r.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 request remaining, got %d", len(list))
		}
		if list[0].RequestID != "recent" {
			t.Errorf("expected 'recent' request to remain, got %q", list[0].RequestID)
		}
	})

	t.Run("keep recent requests", func(t *testing.T) {
		r := NewActiveRequestRegistry()
		recentTime := time.Now()

		r.Register(&ActiveRequest{RequestID: "req-1", StartedAt: recentTime})
		r.Register(&ActiveRequest{RequestID: "req-2", StartedAt: recentTime})

		removed := r.CleanupStale(30 * time.Minute)

		if removed != 0 {
			t.Errorf("expected 0 removed, got %d", removed)
		}

		list := r.List()
		if len(list) != 2 {
			t.Errorf("expected 2 requests remaining, got %d", len(list))
		}
	})

	t.Run("empty registry", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		removed := r.CleanupStale(30 * time.Minute)

		if removed != 0 {
			t.Errorf("expected 0 removed from empty registry, got %d", removed)
		}
	})

	t.Run("cleanup with mock clock for deterministic testing", func(t *testing.T) {
		// Use mock clock for precise time control without test flakiness.
		// This addresses BE-006: CleanupStale uses injected clock for testability.
		baseTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
		clock := &mockClock{current: baseTime}
		r := NewActiveRequestRegistryWithClock(clock)

		// Register requests at various times
		r.Register(&ActiveRequest{
			RequestID: "old-req",
			StartedAt: baseTime.Add(-45 * time.Minute), // 45 minutes old
		})
		r.Register(&ActiveRequest{
			RequestID: "recent-req",
			StartedAt: baseTime.Add(-15 * time.Minute), // 15 minutes old
		})

		// Cleanup requests older than 30 minutes
		removed := r.CleanupStale(30 * time.Minute)

		if removed != 1 {
			t.Errorf("expected 1 removed, got %d", removed)
		}

		list := r.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 request remaining, got %d", len(list))
		}
		if list[0].RequestID != "recent-req" {
			t.Errorf("expected 'recent-req' to remain, got %q", list[0].RequestID)
		}

		// Advance clock by 20 minutes
		clock.current = baseTime.Add(20 * time.Minute)

		// Now the "recent" request is 35 minutes old and should be cleaned
		removed = r.CleanupStale(30 * time.Minute)

		if removed != 1 {
			t.Errorf("expected 1 removed after clock advance, got %d", removed)
		}

		list = r.List()
		if len(list) != 0 {
			t.Errorf("expected 0 requests remaining after second cleanup, got %d", len(list))
		}
	})
}

func TestActiveRequestRegistry_CleanupStale_PreservesRecentTransportActivity(t *testing.T) {
	const staleAfter = 30 * time.Minute

	baseTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	t.Run("websocket recent activity keeps request active", func(t *testing.T) {
		clock := &mockClock{current: baseTime}
		registry := NewActiveRequestRegistryWithClock(clock)

		const (
			requestID  = "ws-live"
			providerID = "provider-ws"
		)

		registry.Register(&ActiveRequest{
			RequestID:   requestID,
			ProviderID:  providerID,
			IsWebSocket: true,
			StartedAt:   baseTime.Add(-45 * time.Minute),
		})

		tracker := &LiveBytesTracker{}
		tracker.LastActivityAt.Store(baseTime.Add(-5 * time.Minute).UnixMilli())
		registry.RegisterLiveBytes(requestID, tracker)

		removed := registry.CleanupStale(staleAfter)
		if removed != 0 {
			t.Fatalf("removed = %d, want 0", removed)
		}

		list := registry.List()
		if len(list) != 1 {
			t.Fatalf("len(list) = %d, want 1", len(list))
		}
		if list[0].RequestID != requestID {
			t.Fatalf("RequestID = %q, want %q", list[0].RequestID, requestID)
		}
	})

	t.Run("recent body activity keeps long-running response active", func(t *testing.T) {
		clock := &mockClock{current: baseTime}
		registry := NewActiveRequestRegistryWithClock(clock)

		const requestID = "sse-live"

		registry.Register(&ActiveRequest{
			RequestID: requestID,
			IsSSE:     true,
			StartedAt: baseTime.Add(-45 * time.Minute),
		})
		registry.Touch(requestID, baseTime.Add(-2*time.Minute))

		removed := registry.CleanupStale(staleAfter)
		if removed != 0 {
			t.Fatalf("removed = %d, want 0", removed)
		}

		list := registry.List()
		if len(list) != 1 {
			t.Fatalf("len(list) = %d, want 1", len(list))
		}
		if list[0].RequestID != requestID {
			t.Fatalf("RequestID = %q, want %q", list[0].RequestID, requestID)
		}
	})
}

func TestActiveRequestRegistry_StartStopCleanup(t *testing.T) {
	t.Run("start and stop cleanup", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		r.StartCleanup()

		// Verify stopCh is set
		r.mu.RLock()
		hasStopCh := r.stopCh != nil
		r.mu.RUnlock()

		if !hasStopCh {
			t.Error("stopCh should be set after StartCleanup")
		}

		r.StopCleanup()

		// Verify stopCh is cleared
		r.mu.RLock()
		stopChAfterStop := r.stopCh
		r.mu.RUnlock()

		if stopChAfterStop != nil {
			t.Error("stopCh should be nil after StopCleanup")
		}
	})

	t.Run("double start is no-op", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		r.StartCleanup()

		r.mu.RLock()
		firstStopCh := r.stopCh
		r.mu.RUnlock()

		// Second start should be a no-op
		r.StartCleanup()

		r.mu.RLock()
		secondStopCh := r.stopCh
		r.mu.RUnlock()

		if firstStopCh != secondStopCh {
			t.Error("double start should not create new stopCh")
		}

		r.StopCleanup()
	})

	t.Run("double stop is safe", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		r.StartCleanup()
		r.StopCleanup()

		// Second stop should be safe (no-op)
		r.StopCleanup()

		r.mu.RLock()
		stopCh := r.stopCh
		r.mu.RUnlock()

		if stopCh != nil {
			t.Error("stopCh should remain nil after double stop")
		}
	})

	t.Run("stop without start is safe", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		// Stop without starting should be safe
		r.StopCleanup()

		r.mu.RLock()
		stopCh := r.stopCh
		r.mu.RUnlock()

		if stopCh != nil {
			t.Error("stopCh should be nil after stop without start")
		}
	})

	t.Run("concurrent operations", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		var wg sync.WaitGroup
		const numGoroutines = 10

		// Concurrently register, unregister, list, update
		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				reqID := "req-" + string(rune('a'+id))
				r.Register(&ActiveRequest{
					RequestID: reqID,
					StartedAt: time.Now(),
				})
				r.List()
				r.UpdateSSE(reqID, true)
				r.CleanupStale(time.Hour)
				r.Unregister(reqID)
			}(i)
		}

		wg.Wait()
	})

	t.Run("concurrent start stop", func(t *testing.T) {
		r := NewActiveRequestRegistry()

		// Test multiple sequential start/stop cycles
		for range 5 {
			r.StartCleanup()
			r.StopCleanup()
		}

		// Ensure final state is clean
		r.mu.RLock()
		stopCh := r.stopCh
		r.mu.RUnlock()

		if stopCh != nil {
			t.Error("stopCh should be nil after multiple start/stop cycles")
		}
	})
}

// TestActiveRequestLifecycle_Integration tests the complete lifecycle:
// request registered -> active in list -> request unregistered -> removed from list.
// This ensures the registry correctly tracks requests through their entire lifecycle.
func TestActiveRequestLifecycle_Integration(t *testing.T) {
	registry := NewActiveRequestRegistry()
	startTime := time.Now()

	// Phase 1: Register active request (simulates proxy handler registering on request start)
	req := &ActiveRequest{
		RequestID:  "test-lifecycle-req",
		ProviderID: "provider-1",
		Model:      "claude-3-opus",
		APIType:    "claude",
		UserID:     "user-1",
		ClientIP:   "192.168.1.100",
		IsSSE:      false,
		StartedAt:  startTime,
	}
	registry.Register(req)

	// Verify request is active
	list := registry.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 active request after register, got %d", len(list))
	}
	if list[0].RequestID != "test-lifecycle-req" {
		t.Errorf("expected RequestID 'test-lifecycle-req', got %q", list[0].RequestID)
	}

	// Phase 2: Update SSE status (simulates proxy determining response type from upstream)
	registry.UpdateSSE("test-lifecycle-req", true)

	list = registry.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 active request after UpdateSSE, got %d", len(list))
	}
	if !list[0].IsSSE {
		t.Error("expected IsSSE to be true after update")
	}

	// Phase 3: Unregister request (simulates proxy handler unregistering on request completion)
	registry.Unregister("test-lifecycle-req")

	// Verify request is removed
	list = registry.List()
	if len(list) != 0 {
		t.Errorf("expected 0 active requests after unregister, got %d", len(list))
	}

	// Phase 4: Verify unregistering again is a no-op (idempotent)
	registry.Unregister("test-lifecycle-req")
	list = registry.List()
	if len(list) != 0 {
		t.Errorf("expected 0 active requests after double unregister, got %d", len(list))
	}
}

func TestFindActiveProvider_ReturnsProviderWithData(t *testing.T) {
	r := NewActiveRequestRegistry()

	// Register request but without data received
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})

	// Should not find (HasReceivedData = false)
	_, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if found {
		t.Error("should not find provider when HasReceivedData is false")
	}

	// Mark data received
	r.MarkDataReceived("req-1")

	// Now should find
	providerID, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if !found {
		t.Error("should find provider when HasReceivedData is true")
	}
	if providerID != "provider-1" {
		t.Errorf("expected provider-1, got %s", providerID)
	}
}

func TestFindActiveProvider_DifferentStickyKeys(t *testing.T) {
	r := NewActiveRequestRegistry()

	// Register requests with different sticky keys
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})
	r.MarkDataReceived("req-1")

	r.Register(&ActiveRequest{
		RequestID:  "req-2",
		ProviderID: "provider-2",
		ClientIP:   "5.6.7.8",
		UserID:     "user-2",
		APIType:    "openai",
	})
	r.MarkDataReceived("req-2")

	// Find by first sticky key
	providerID, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if !found || providerID != "provider-1" {
		t.Errorf("expected provider-1 for first sticky key, got %s (found=%v)", providerID, found)
	}

	// Find by second sticky key
	providerID, found = r.FindActiveProvider("5.6.7.8", "user-2", "openai", "")
	if !found || providerID != "provider-2" {
		t.Errorf("expected provider-2 for second sticky key, got %s (found=%v)", providerID, found)
	}

	// Non-existent sticky key
	_, found = r.FindActiveProvider("9.9.9.9", "user-3", "gemini", "")
	if found {
		t.Error("should not find provider for non-existent sticky key")
	}
}

func TestFindActiveProvider_UsesRegisteredRequestDimensions(t *testing.T) {
	r := NewActiveRequestRegistry()

	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
		Model:      "model-a",
		StickyMode: model.StickyModeAPIType,
	})
	r.MarkDataReceived("req-1")

	providerID, found := r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "claude",
		Model:      "different-model",
		StickyMode: model.StickyModeAPIType,
	})
	if !found || providerID != "provider-1" {
		t.Fatalf("expected provider-1 in api_type mode, got %q (found=%v)", providerID, found)
	}

	if _, found = r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "claude",
		Model:      "different-model",
		StickyMode: model.StickyModeModel,
	}); found {
		t.Fatal("expected miss for different model after switching to model mode")
	}

	r.Register(&ActiveRequest{
		RequestID:  "req-2",
		ProviderID: "provider-2",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
		Model:      "model-a",
		StickyMode: model.StickyModeModel,
	})
	r.MarkDataReceived("req-2")

	providerID, found = r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "claude",
		Model:      "model-a",
		StickyMode: model.StickyModeModel,
	})
	if !found || providerID != "provider-2" {
		t.Fatalf("expected provider-2 for post-switch model registration, got %q (found=%v)", providerID, found)
	}
}

func TestStickyIndex_CleanupUsesRegisteredContinuityKey(t *testing.T) {
	r := NewActiveRequestRegistry()

	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
		Model:      "model-a",
		StickyMode: model.StickyModeModel,
	})
	r.MarkDataReceived("req-1")

	r.UpdateModel("req-1", "model-b")
	r.Unregister("req-1")

	if _, found := r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "claude",
		Model:      "model-a",
		StickyMode: model.StickyModeModel,
	}); found {
		t.Fatal("expected sticky entry to be fully cleaned after unregister")
	}
}

func TestStickyIndex_StaleCleanupUsesRegisteredContinuityKey(t *testing.T) {
	baseTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	clock := &mockClock{current: baseTime}
	r := NewActiveRequestRegistryWithClock(clock)

	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
		Model:      "model-a",
		StickyMode: model.StickyModeModel,
		StartedAt:  baseTime.Add(-45 * time.Minute),
	})
	r.Touch("req-1", baseTime.Add(-45*time.Minute))
	r.MarkDataReceived("req-1")

	r.UpdateModel("req-1", "model-b")
	removed := r.CleanupStale(30 * time.Minute)
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	if _, found := r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "claude",
		Model:      "model-a",
		StickyMode: model.StickyModeModel,
	}); found {
		t.Fatal("expected sticky entry to be fully cleaned after stale cleanup")
	}
}

func TestFindActiveProvider_MultipleRequestsSameStickyKey(t *testing.T) {
	r := NewActiveRequestRegistry()

	// Register multiple requests with the same sticky key
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})
	r.Register(&ActiveRequest{
		RequestID:  "req-2",
		ProviderID: "provider-2",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})

	// Neither has received data
	_, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if found {
		t.Error("should not find provider when no request has received data")
	}

	// Only mark one as having received data
	r.MarkDataReceived("req-2")

	// Should find provider-2
	providerID, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if !found {
		t.Error("should find provider when one request has received data")
	}
	if providerID != "provider-2" {
		t.Errorf("expected provider-2, got %s", providerID)
	}
}

func TestMarkDataReceived_NonExistent(t *testing.T) {
	r := NewActiveRequestRegistry()

	// Should not panic when marking non-existent request
	r.MarkDataReceived("non-existent")

	// Verify registry is still empty
	list := r.List()
	if len(list) != 0 {
		t.Errorf("expected 0 requests, got %d", len(list))
	}
}

func TestStickyIndex_CleanupOnUnregister(t *testing.T) {
	r := NewActiveRequestRegistry()

	// Register request
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})
	r.MarkDataReceived("req-1")

	// Verify it's findable
	_, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if !found {
		t.Error("should find provider before unregister")
	}

	// Unregister
	r.Unregister("req-1")

	// Should not find anymore
	_, found = r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if found {
		t.Error("should not find provider after unregister")
	}
}

func TestStickyIndex_UpdateModelKeepsRegisteredContinuityKey(t *testing.T) {
	r := NewActiveRequestRegistry()

	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "codex",
		Model:      ModelUnknown,
		StickyMode: model.StickyModeModel,
	})
	r.MarkDataReceived("req-1")

	if _, found := r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "codex",
		Model:      ModelUnknown,
		StickyMode: model.StickyModeModel,
	}); !found {
		t.Fatal("should find provider with the original handshake-time key before model update")
	}

	r.UpdateModel("req-1", "gpt-5.4")

	if _, found := r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "codex",
		Model:      "gpt-5.4",
		StickyMode: model.StickyModeModel,
	}); found {
		t.Fatal("should not find provider under a post-handshake model key")
	}

	providerID, found := r.FindActiveProviderForRequest(&model.SelectRequest{
		ClientIP:   "1.2.3.4",
		User:       "user-1",
		APIType:    "codex",
		Model:      ModelUnknown,
		StickyMode: model.StickyModeModel,
	})
	if !found {
		t.Fatal("should still find provider through the original handshake-time key after model update")
	}
	if providerID != "provider-1" {
		t.Fatalf("providerID = %q, want %q", providerID, "provider-1")
	}

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 active request, got %d", len(list))
	}
	if list[0].Model != "gpt-5.4" {
		t.Fatalf("list[0].Model = %q, want %q", list[0].Model, "gpt-5.4")
	}
}

func TestStickyIndex_OverwriteExistingRequest(t *testing.T) {
	r := NewActiveRequestRegistry()

	// Register request with one provider
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})
	r.MarkDataReceived("req-1")

	// Overwrite with different provider (same request ID)
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-2",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})
	r.MarkDataReceived("req-1")

	// Should find the new provider
	providerID, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if !found {
		t.Error("should find provider after overwrite")
	}
	if providerID != "provider-2" {
		t.Errorf("expected provider-2 after overwrite, got %s", providerID)
	}
}

func TestStickyIndex_OverwriteWithDifferentStickyKey(t *testing.T) {
	r := NewActiveRequestRegistry()

	// Register request with original sticky key
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
	})
	r.MarkDataReceived("req-1")

	// Verify findable with original key
	_, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if !found {
		t.Error("should find provider with original key")
	}

	// Overwrite with different sticky key (same request ID)
	r.Register(&ActiveRequest{
		RequestID:  "req-1",
		ProviderID: "provider-1",
		ClientIP:   "5.6.7.8", // Different IP
		UserID:     "user-2",  // Different user
		APIType:    "openai",  // Different API type
	})
	r.MarkDataReceived("req-1")

	// Should NOT find with original key anymore
	_, found = r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if found {
		t.Error("should not find provider with original key after overwrite")
	}

	// Should find with new key
	providerID, found := r.FindActiveProvider("5.6.7.8", "user-2", "openai", "")
	if !found {
		t.Error("should find provider with new key")
	}
	if providerID != "provider-1" {
		t.Errorf("expected provider-1, got %s", providerID)
	}
}

// TestActiveRequestLifecycle_MultipleRequests tests concurrent request lifecycles.
func TestActiveRequestLifecycle_MultipleRequests(t *testing.T) {
	registry := NewActiveRequestRegistry()
	startTime := time.Now()

	// Register multiple concurrent requests
	requestIDs := []string{"req-1", "req-2", "req-3"}
	for _, id := range requestIDs {
		registry.Register(&ActiveRequest{
			RequestID:  id,
			ProviderID: "provider-" + id,
			Model:      "model-" + id,
			APIType:    "claude",
			StartedAt:  startTime,
		})
	}

	// Verify all requests are active
	list := registry.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 active requests, got %d", len(list))
	}

	// Complete requests one by one
	registry.Unregister("req-1")
	list = registry.List()
	if len(list) != 2 {
		t.Errorf("expected 2 active requests after first unregister, got %d", len(list))
	}

	registry.Unregister("req-2")
	list = registry.List()
	if len(list) != 1 {
		t.Errorf("expected 1 active request after second unregister, got %d", len(list))
	}

	registry.Unregister("req-3")
	list = registry.List()
	if len(list) != 0 {
		t.Errorf("expected 0 active requests after all unregistered, got %d", len(list))
	}
}

func TestRegisterLiveBytes_PopulatesListFields(t *testing.T) {
	r := NewActiveRequestRegistry()

	r.Register(&ActiveRequest{
		RequestID:   "ws-1",
		IsWebSocket: true,
		StartedAt:   time.Now(),
	})

	tracker := &LiveBytesTracker{}
	tracker.BytesSent.Store(1024)
	tracker.BytesReceived.Store(8192)
	tracker.MsgsSent.Store(5)
	tracker.MsgsReceived.Store(42)
	tracker.LastActivityAt.Store(1700000000000)

	r.RegisterLiveBytes("ws-1", tracker)

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 request, got %d", len(list))
	}

	req := list[0]
	if req.BytesSent != 1024 {
		t.Errorf("BytesSent = %d, want 1024", req.BytesSent)
	}
	if req.BytesReceived != 8192 {
		t.Errorf("BytesReceived = %d, want 8192", req.BytesReceived)
	}
	if req.MsgsSent != 5 {
		t.Errorf("MsgsSent = %d, want 5", req.MsgsSent)
	}
	if req.MsgsReceived != 42 {
		t.Errorf("MsgsReceived = %d, want 42", req.MsgsReceived)
	}
	if req.LastActivityAt != 1700000000000 {
		t.Errorf("LastActivityAt = %d, want 1700000000000", req.LastActivityAt)
	}
}

func TestRegisterLiveBytes_NilTrackerIgnored(t *testing.T) {
	r := NewActiveRequestRegistry()
	r.Register(&ActiveRequest{RequestID: "ws-1", IsWebSocket: true, StartedAt: time.Now()})

	// Should not panic or store anything.
	r.RegisterLiveBytes("ws-1", nil)

	list := r.List()
	if list[0].BytesSent != 0 {
		t.Errorf("expected zero BytesSent without tracker, got %d", list[0].BytesSent)
	}
}

func TestHTTPBodyReadBytesRemainDistinctFromWebSocketTraffic(t *testing.T) {
	registry := NewActiveRequestRegistry()
	registry.Register(&ActiveRequest{RequestID: "http-body", StartedAt: time.Now()})
	tracker := &LiveBytesTracker{}
	tracker.UpstreamBodyReadBytes.Add(3)
	tracker.UpstreamBodyReadBytes.Add(7)
	registry.RegisterLiveBytes("http-body", tracker)
	request := registry.List()[0]
	if request.UpstreamBodyReadBytes != 10 || request.BytesSent != 0 {
		t.Fatalf("HTTP read consumption = %d, WS traffic = %d", request.UpstreamBodyReadBytes, request.BytesSent)
	}
	registry.Unregister("http-body")
	registry.Register(&ActiveRequest{RequestID: "http-body", StartedAt: time.Now()})
	if registry.List()[0].UpstreamBodyReadBytes != 0 {
		t.Fatal("HTTP read counter survived request lifetime")
	}
}

func TestUpdateObservationPublishesWithoutChangingContinuity(t *testing.T) {
	registry := NewActiveRequestRegistry()
	pending := model.ReasoningObservationPending
	registry.Register(&ActiveRequest{RequestID: "observed", Model: "header-model", StartedAt: time.Now(),
		RequestedReasoningObservation: model.RequestedReasoningObservation{State: &pending}})
	before := registry.List()[0]
	final := model.ReasoningObservationCaptured
	effort := "high"
	observation := model.RequestedReasoningObservation{State: &final, Effort: &effort}
	registry.UpdateObservation("missing", "ignored", observation)
	registry.UpdateObservation("observed", "", observation)
	if registry.List()[0].Model != "header-model" {
		t.Fatal("empty observation erased known model")
	}
	registry.UpdateObservation("observed", "body-model", observation)
	after := registry.List()[0]
	if after.Model != "body-model" || *after.State != final || *after.Effort != effort || after.ContinuityKey != before.ContinuityKey || *before.State != pending {
		t.Fatalf("observations before=%+v after=%+v", before, after)
	}
}

func TestRegisterLiveBytes_NonWSRequestUnaffected(t *testing.T) {
	r := NewActiveRequestRegistry()

	// HTTP request without a tracker should have zero live fields.
	r.Register(&ActiveRequest{RequestID: "http-1", StartedAt: time.Now()})

	list := r.List()
	if list[0].BytesSent != 0 || list[0].BytesReceived != 0 || list[0].LastActivityAt != 0 {
		t.Error("expected zero live fields for request without tracker")
	}
}

func TestRegister_OverwriteClearsStaleTracker(t *testing.T) {
	r := NewActiveRequestRegistry()

	// First registration with a tracker.
	r.Register(&ActiveRequest{RequestID: "ws-1", IsWebSocket: true, StartedAt: time.Now()})
	staleTracker := &LiveBytesTracker{}
	staleTracker.BytesSent.Store(9999)
	staleTracker.MsgsSent.Store(42)
	r.RegisterLiveBytes("ws-1", staleTracker)

	// Overwrite the same request ID without attaching a new tracker.
	r.Register(&ActiveRequest{RequestID: "ws-1", ProviderID: "new-provider", IsWebSocket: true, StartedAt: time.Now()})

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 request, got %d", len(list))
	}
	if list[0].BytesSent != 0 {
		t.Errorf("BytesSent = %d, want 0 (stale tracker should be cleared on overwrite)", list[0].BytesSent)
	}
	if list[0].MsgsSent != 0 {
		t.Errorf("MsgsSent = %d, want 0", list[0].MsgsSent)
	}
	if list[0].ProviderID != "new-provider" {
		t.Errorf("ProviderID = %q, want %q", list[0].ProviderID, "new-provider")
	}
}

func TestUnregister_CleansUpLiveBytes(t *testing.T) {
	r := NewActiveRequestRegistry()

	r.Register(&ActiveRequest{RequestID: "ws-1", IsWebSocket: true, StartedAt: time.Now()})
	tracker := &LiveBytesTracker{}
	tracker.BytesSent.Store(100)
	r.RegisterLiveBytes("ws-1", tracker)

	r.Unregister("ws-1")

	// Re-register same ID — should not see stale tracker data.
	r.Register(&ActiveRequest{RequestID: "ws-1", IsWebSocket: true, StartedAt: time.Now()})
	list := r.List()
	if list[0].BytesSent != 0 {
		t.Errorf("expected zero BytesSent after re-register, got %d", list[0].BytesSent)
	}
}
