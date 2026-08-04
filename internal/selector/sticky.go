package selector

import (
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
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
	mu           sync.RWMutex
	entries      map[model.StickyKey]*stickyEntry
	providerKeys map[string]map[model.StickyKey]struct{}
	clock        internal.Clock
}

// NewMemoryStickyCache creates a new in-memory sticky cache.
func NewMemoryStickyCache(clock internal.Clock) *MemoryStickyCache {
	return &MemoryStickyCache{
		entries:      make(map[model.StickyKey]*stickyEntry),
		providerKeys: make(map[string]map[model.StickyKey]struct{}),
		clock:        clock,
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
	if !c.clock.Now().Before(entry.expiresAt) {
		return "", false
	}

	return entry.providerID, true
}

// Set stores a provider ID for the given key with TTL.
func (c *MemoryStickyCache) Set(key model.StickyKey, providerID string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setLocked(key, providerID, c.clock.Now().Add(ttl))
}

// restoreEntry repopulates memory with an already-expiring durable entry.
// Keeping the absolute expiry avoids extending a binding every time the
// process restarts or the cache is reloaded.
func (c *MemoryStickyCache) restoreEntry(entry model.StickyEntry) {
	if entry.ProviderID == "" || !entry.ExpiresAt.After(c.clock.Now()) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLocked(entry.Key, entry.ProviderID, entry.ExpiresAt)
}

func (c *MemoryStickyCache) setLocked(key model.StickyKey, providerID string, expiresAt time.Time) {
	if previous, ok := c.entries[key]; ok {
		c.removeProviderKeyLocked(previous.providerID, key)
	}
	c.entries[key] = &stickyEntry{
		providerID: providerID,
		expiresAt:  expiresAt,
	}
	c.addProviderKeyLocked(providerID, key)
}

// Delete removes a cached entry.
func (c *MemoryStickyCache) Delete(key model.StickyKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deleteEntryLocked(key)
}

// Cleanup removes expired entries. Call periodically to prevent memory bloat.
// Note: Get() already returns empty for expired entries, but they remain in
// memory until Cleanup() is called. For automatic cleanup, use StartCleanupLoop().
func (c *MemoryStickyCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			c.removeProviderKeyLocked(entry.providerID, key)
			delete(c.entries, key)
		}
	}
}

// EvictProvider removes every sticky key that currently points at the provider.
func (c *MemoryStickyCache) EvictProvider(providerID string) {
	if providerID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	keys := c.providerKeys[providerID]
	for key := range keys {
		delete(c.entries, key)
	}
	delete(c.providerKeys, providerID)
}

// StartCleanupLoop spawns a goroutine that periodically calls Cleanup().
// Returns a stop function to terminate the cleanup loop.
// The stop function waits for the cleanup goroutine to fully exit before returning.
// Example: stop := cache.StartCleanupLoop(5 * time.Minute); defer stop()
func (c *MemoryStickyCache) StartCleanupLoop(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := c.clock.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Cleanup()
			case <-done:
				return
			}
		}
	})
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

func (c *MemoryStickyCache) addProviderKeyLocked(providerID string, key model.StickyKey) {
	if providerID == "" {
		return
	}
	if c.providerKeys[providerID] == nil {
		c.providerKeys[providerID] = make(map[model.StickyKey]struct{})
	}
	c.providerKeys[providerID][key] = struct{}{}
}

func (c *MemoryStickyCache) removeProviderKeyLocked(providerID string, key model.StickyKey) {
	if providerID == "" {
		return
	}
	keys := c.providerKeys[providerID]
	if keys == nil {
		return
	}
	delete(keys, key)
	if len(keys) == 0 {
		delete(c.providerKeys, providerID)
	}
}

func (c *MemoryStickyCache) deleteEntryLocked(key model.StickyKey) {
	entry, ok := c.entries[key]
	if !ok {
		return
	}
	c.removeProviderKeyLocked(entry.providerID, key)
	delete(c.entries, key)
}
