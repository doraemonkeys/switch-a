package proxy

import (
	"sync"
	"testing"
	"time"
)

// mockClock implements internal.Clock for testing.
type mockClock struct {
	current time.Time
}

func (c *mockClock) Now() time.Time {
	return c.current
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
		for i := 0; i < numGoroutines; i++ {
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
		for i := 0; i < 5; i++ {
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
