// Package store provides data storage implementations.
package store

import (
	"context"
	"sync"
	"time"

	"switch-a/internal"
)

// Default cache configuration values.
const (
	// DefaultConfigCacheTTL is how long cached config values remain valid.
	// Short enough to pick up changes reasonably quickly, long enough to reduce DB load.
	DefaultConfigCacheTTL = 5 * time.Second
)

// CachedStoreConfig holds configuration for the cached store wrapper.
type CachedStoreConfig struct {
	// Store is the underlying store to wrap.
	Store internal.Store
	// CacheTTL is how long cached config values remain valid.
	// Defaults to DefaultConfigCacheTTL if <= 0.
	CacheTTL time.Duration
	// Clock is used for time operations (for testing).
	Clock internal.Clock
}

// configEntry holds a cached config value with expiration.
type configEntry struct {
	value     string
	expiresAt time.Time
}

// CachedStore wraps a Store with in-memory caching for config values.
// This reduces database pressure for high-frequency config reads during proxy requests.
//
// Design tradeoffs:
//   - GetAllConfig is NOT cached (returns fresh values), while GetConfig uses cache.
//     This may cause brief inconsistency (up to 5s TTL), acceptable for proxy use.
//   - No stampede protection on cache expiry. Acceptable for low-cardinality config
//     keys with 5s TTL. Consider singleflight if this becomes a bottleneck.
//   - TOCTOU gap: concurrent requests for the same expired key may redundantly fetch
//     from DB. Not a correctness bug, just slightly wasteful under high concurrency.
type CachedStore struct {
	internal.Store
	cache    map[string]configEntry
	mu       sync.RWMutex
	cacheTTL time.Duration
	clock    internal.Clock
}

// NewCachedStore creates a new cached store wrapper.
// It caches GetConfig calls to reduce database pressure under high QPS.
// Panics if cfg.Store is nil (indicates programming error).
func NewCachedStore(cfg CachedStoreConfig) *CachedStore {
	if cfg.Store == nil {
		panic("cached_store: cfg.Store must not be nil")
	}

	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = DefaultConfigCacheTTL
	}

	clock := cfg.Clock
	if clock == nil {
		clock = internal.RealClock{}
	}

	return &CachedStore{
		Store:    cfg.Store,
		cache:    make(map[string]configEntry),
		cacheTTL: ttl,
		clock:    clock,
	}
}

// GetConfig retrieves a config value, using cache when available and not expired.
func (s *CachedStore) GetConfig(ctx context.Context, key string) (string, error) {
	now := s.clock.Now()

	// Check cache first with read lock
	s.mu.RLock()
	if entry, found := s.cache[key]; found && now.Before(entry.expiresAt) {
		s.mu.RUnlock()
		return entry.value, nil
	}
	s.mu.RUnlock()

	// Cache miss or expired - fetch from underlying store
	value, err := s.Store.GetConfig(ctx, key)
	if err != nil {
		return "", err
	}

	// Cache the value with write lock
	// Use fresh timestamp after DB call for accurate TTL
	s.mu.Lock()
	s.cache[key] = configEntry{
		value:     value,
		expiresAt: s.clock.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	return value, nil
}

// SetConfig sets a config value and invalidates the cache.
func (s *CachedStore) SetConfig(ctx context.Context, key, value string) error {
	// Update underlying store first
	if err := s.Store.SetConfig(ctx, key, value); err != nil {
		return err
	}

	// Invalidate cache entry to ensure next read gets fresh value
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()

	return nil
}

// SetConfigs sets multiple config values and invalidates their cache entries.
func (s *CachedStore) SetConfigs(ctx context.Context, configs map[string]string) error {
	// Update underlying store first
	if err := s.Store.SetConfigs(ctx, configs); err != nil {
		return err
	}

	// Invalidate all updated keys
	s.mu.Lock()
	for key := range configs {
		delete(s.cache, key)
	}
	s.mu.Unlock()

	return nil
}

// InvalidateConfig manually invalidates a cache entry.
// This can be called when config is known to have changed externally.
func (s *CachedStore) InvalidateConfig(key string) {
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()
}

// InvalidateAllConfig clears the entire config cache.
// This forces all subsequent reads to hit the database.
func (s *CachedStore) InvalidateAllConfig() {
	s.mu.Lock()
	s.cache = make(map[string]configEntry)
	s.mu.Unlock()
}

// InitDefaultConfig initializes default config values and invalidates the cache.
// This override ensures cache consistency even if called after startup.
func (s *CachedStore) InitDefaultConfig(ctx context.Context) error {
	if err := s.Store.InitDefaultConfig(ctx); err != nil {
		return err
	}
	s.InvalidateAllConfig()
	return nil
}
