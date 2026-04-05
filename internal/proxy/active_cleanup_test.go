package proxy

import (
	"testing"
	"time"
)

func TestCleanupStale_CleansUpLiveBytes(t *testing.T) {
	baseTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	clock := &mockClock{current: baseTime}
	r := NewActiveRequestRegistryWithClock(clock)

	r.Register(&ActiveRequest{
		RequestID:   "ws-old",
		IsWebSocket: true,
		StartedAt:   baseTime.Add(-45 * time.Minute),
	})
	tracker := &LiveBytesTracker{}
	tracker.BytesSent.Store(999)
	r.RegisterLiveBytes("ws-old", tracker)

	removed := r.CleanupStale(30 * time.Minute)
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	// Re-register same ID — tracker should be gone.
	r.Register(&ActiveRequest{RequestID: "ws-old", IsWebSocket: true, StartedAt: baseTime})
	list := r.List()
	if list[0].BytesSent != 0 {
		t.Errorf("expected zero BytesSent after stale cleanup, got %d", list[0].BytesSent)
	}
}

func TestStickyIndex_CleanupOnCleanupStale(t *testing.T) {
	baseTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	clock := &mockClock{current: baseTime}
	r := NewActiveRequestRegistryWithClock(clock)

	// Register old request
	r.Register(&ActiveRequest{
		RequestID:  "old-req",
		ProviderID: "provider-1",
		ClientIP:   "1.2.3.4",
		UserID:     "user-1",
		APIType:    "claude",
		StartedAt:  baseTime.Add(-45 * time.Minute),
	})
	r.Touch("old-req", baseTime.Add(-45*time.Minute))
	r.MarkDataReceived("old-req")

	// Verify it's findable
	_, found := r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if !found {
		t.Error("should find provider before cleanup")
	}

	// Cleanup stale requests
	removed := r.CleanupStale(30 * time.Minute)
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Should not find anymore
	_, found = r.FindActiveProvider("1.2.3.4", "user-1", "claude", "")
	if found {
		t.Error("should not find provider after cleanup")
	}
}
