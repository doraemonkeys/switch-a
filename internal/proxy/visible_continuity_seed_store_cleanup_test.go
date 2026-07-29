package proxy

import (
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

const visibleContinuitySeedCleanupTestInterval = 5 * time.Millisecond

func TestVisibleContinuitySeedStoreStartCleanupLoopRemovesExpiredSeeds(t *testing.T) {
	baseTime := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clock := newVisibleContinuitySeedStoreCleanupTestClock(baseTime)
	store := NewVisibleContinuitySeedStoreWithClock(clock)

	key := model.StickyKey{IP: "127.0.0.1", User: "user-1", APIType: "codex"}
	store.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-1",
		ContinuityKey:    key,
		OriginProviderID: "provider-origin",
		ObservedAt:       baseTime,
	})

	stop := store.StartCleanupLoop(visibleContinuitySeedCleanupTestInterval)
	t.Cleanup(stop)

	clock.Set(baseTime.Add(model.VisibleContinuitySeedTTL + time.Millisecond))
	waitForVisibleContinuitySeedStoreLen(t, store, 0)
}

func TestVisibleContinuitySeedStoreStartCleanupLoopStopPreventsFurtherCleanup(t *testing.T) {
	baseTime := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clock := newVisibleContinuitySeedStoreCleanupTestClock(baseTime)
	store := NewVisibleContinuitySeedStoreWithClock(clock)

	stop := store.StartCleanupLoop(visibleContinuitySeedCleanupTestInterval)
	stop()

	key := model.StickyKey{IP: "127.0.0.1", User: "user-1", APIType: "codex"}
	store.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-1",
		ContinuityKey:    key,
		OriginProviderID: "provider-origin",
		ObservedAt:       baseTime,
	})

	clock.Set(baseTime.Add(model.VisibleContinuitySeedTTL + time.Millisecond))
	time.Sleep(4 * visibleContinuitySeedCleanupTestInterval)

	if got := store.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 after cleanup loop shutdown", got)
	}
}

func waitForVisibleContinuitySeedStoreLen(t *testing.T, store *MemoryVisibleContinuitySeedStore, want int) {
	t.Helper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := store.Len(); got == want {
			return
		}
		time.Sleep(visibleContinuitySeedCleanupTestInterval)
	}

	t.Fatalf("Len() = %d, want %d before timeout", store.Len(), want)
}

type visibleContinuitySeedStoreCleanupTestClock struct {
	mu      sync.RWMutex
	current time.Time
}

func newVisibleContinuitySeedStoreCleanupTestClock(now time.Time) *visibleContinuitySeedStoreCleanupTestClock {
	return &visibleContinuitySeedStoreCleanupTestClock{current: now}
}

func (c *visibleContinuitySeedStoreCleanupTestClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

func (c *visibleContinuitySeedStoreCleanupTestClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = now
}

func (c *visibleContinuitySeedStoreCleanupTestClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}
