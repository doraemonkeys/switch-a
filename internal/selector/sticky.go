package selector

import (
	"sync"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
)

// Compile-time interface check.
var _ internal.StickyCache = (*MemoryStickyCache)(nil)

// stickyEntry represents a cached sticky session entry.
type stickyEntry struct {
	providerID string
	expiresAt  time.Time
}

// MemoryStickyCache implements StickyCache using in-memory storage.
// Note: The caller is responsible for scheduling periodic calls to Cleanup()
// to prevent memory growth from expired entries. Alternatively, use
// StartCleanupLoop() to automatically clean up expired entries.
type MemoryStickyCache struct {
	mu      sync.RWMutex
	entries map[model.StickyKey]*stickyEntry
	clock   internal.Clock
}

// NewMemoryStickyCache creates a new in-memory sticky cache.
func NewMemoryStickyCache(clock internal.Clock) *MemoryStickyCache {
	return &MemoryStickyCache{
		entries: make(map[model.StickyKey]*stickyEntry),
		clock:   clock,
	}
}

// Get retrieves a cached provider ID for the given key.
func (c *MemoryStickyCache) Get(key model.StickyKey) (providerID string, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}

	// Check if entry has expired
	if c.clock.Now().After(entry.expiresAt) {
		return "", false
	}

	return entry.providerID, true
}

// Set stores a provider ID for the given key with TTL.
func (c *MemoryStickyCache) Set(key model.StickyKey, providerID string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &stickyEntry{
		providerID: providerID,
		expiresAt:  c.clock.Now().Add(ttl),
	}
}

// Delete removes a cached entry.
func (c *MemoryStickyCache) Delete(key model.StickyKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
}

// Cleanup removes expired entries. Call periodically to prevent memory bloat.
// Note: Get() already returns empty for expired entries, but they remain in
// memory until Cleanup() is called. For automatic cleanup, use StartCleanupLoop().
func (c *MemoryStickyCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

// StartCleanupLoop spawns a goroutine that periodically calls Cleanup().
// Returns a stop function to terminate the cleanup loop.
// The stop function waits for the cleanup goroutine to fully exit before returning.
// Example: stop := cache.StartCleanupLoop(5 * time.Minute); defer stop()
func (c *MemoryStickyCache) StartCleanupLoop(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Cleanup()
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}

// Len returns the number of entries in the cache (for testing).
func (c *MemoryStickyCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
