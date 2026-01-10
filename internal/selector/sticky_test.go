package selector

import (
	"testing"
	"time"

	"switch-a/internal/model"
)

// mockClock implements internal.Clock for testing.
type mockClock struct {
	now time.Time
}

func (c *mockClock) Now() time.Time {
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestMemoryStickyCache_GetSet(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cache := NewMemoryStickyCache(clock)

	key := model.StickyKey{IP: "192.168.1.1", User: "user1", APIType: "claude"}

	// Get on empty cache
	providerID, found := cache.Get(key)
	if found {
		t.Error("expected not found on empty cache")
	}
	if providerID != "" {
		t.Errorf("expected empty providerID, got %q", providerID)
	}

	// Set and get
	cache.Set(key, "provider1", 5*time.Minute)
	providerID, found = cache.Get(key)
	if !found {
		t.Error("expected to find cached value")
	}
	if providerID != "provider1" {
		t.Errorf("expected provider1, got %q", providerID)
	}
}

func TestMemoryStickyCache_Expiration(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cache := NewMemoryStickyCache(clock)

	key := model.StickyKey{IP: "192.168.1.1", User: "user1", APIType: "claude"}
	cache.Set(key, "provider1", 5*time.Minute)

	// Should be found before expiration
	_, found := cache.Get(key)
	if !found {
		t.Error("expected to find cached value before expiration")
	}

	// Advance time past expiration
	clock.Advance(6 * time.Minute)

	// Should not be found after expiration
	_, found = cache.Get(key)
	if found {
		t.Error("expected not to find cached value after expiration")
	}
}

func TestMemoryStickyCache_Delete(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cache := NewMemoryStickyCache(clock)

	key := model.StickyKey{IP: "192.168.1.1", User: "user1", APIType: "claude"}
	cache.Set(key, "provider1", 5*time.Minute)

	// Verify it's cached
	_, found := cache.Get(key)
	if !found {
		t.Fatal("expected to find cached value")
	}

	// Delete
	cache.Delete(key)

	// Should not be found
	_, found = cache.Get(key)
	if found {
		t.Error("expected not to find deleted value")
	}
}

func TestMemoryStickyCache_Cleanup(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cache := NewMemoryStickyCache(clock)

	// Add entries with different TTLs
	key1 := model.StickyKey{IP: "192.168.1.1", User: "user1", APIType: "claude"}
	key2 := model.StickyKey{IP: "192.168.1.2", User: "user2", APIType: "claude"}

	cache.Set(key1, "provider1", 5*time.Minute)
	cache.Set(key2, "provider2", 10*time.Minute)

	if cache.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", cache.Len())
	}

	// Advance time to expire first entry
	clock.Advance(6 * time.Minute)
	cache.Cleanup()

	if cache.Len() != 1 {
		t.Errorf("expected 1 entry after cleanup, got %d", cache.Len())
	}

	// Advance time to expire second entry
	clock.Advance(5 * time.Minute)
	cache.Cleanup()

	if cache.Len() != 0 {
		t.Errorf("expected 0 entries after second cleanup, got %d", cache.Len())
	}
}

func TestMemoryStickyCache_DifferentKeys(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cache := NewMemoryStickyCache(clock)

	key1 := model.StickyKey{IP: "192.168.1.1", User: "user1", APIType: "claude"}
	key2 := model.StickyKey{IP: "192.168.1.1", User: "user1", APIType: "codex"}
	key3 := model.StickyKey{IP: "192.168.1.1", User: "user2", APIType: "claude"}

	cache.Set(key1, "provider1", 5*time.Minute)
	cache.Set(key2, "provider2", 5*time.Minute)
	cache.Set(key3, "provider3", 5*time.Minute)

	p1, _ := cache.Get(key1)
	p2, _ := cache.Get(key2)
	p3, _ := cache.Get(key3)

	if p1 != "provider1" || p2 != "provider2" || p3 != "provider3" {
		t.Errorf("expected different values for different keys, got %q, %q, %q", p1, p2, p3)
	}
}
