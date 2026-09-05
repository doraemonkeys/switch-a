package selector

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

const stickyPersistenceRetryInterval = time.Second

// StickyPersistence is the small consumer-side port required to recover sticky
// bindings after a process restart. Implementations may be eventually
// consistent; the selector always keeps the in-memory copy authoritative for
// the current process.
type StickyPersistence interface {
	LoadStickyEntries(ctx context.Context, now time.Time) ([]model.StickyEntry, error)
	UpsertStickyEntry(ctx context.Context, entry model.StickyEntry) error
	DeleteStickyEntry(ctx context.Context, key model.StickyKey) error
	DeleteStickyEntriesByProvider(ctx context.Context, providerID string) error
	DeleteExpiredStickyEntries(ctx context.Context, now time.Time) error
}

type stickyPendingMutations struct {
	upserts           map[model.StickyKey]model.StickyEntry
	deletes           map[model.StickyKey]struct{}
	providerEvictions map[string]struct{}
	cleanupBefore     time.Time
}

func newStickyPendingMutations() stickyPendingMutations {
	return stickyPendingMutations{
		upserts:           make(map[model.StickyKey]model.StickyEntry),
		deletes:           make(map[model.StickyKey]struct{}),
		providerEvictions: make(map[string]struct{}),
	}
}

func (m stickyPendingMutations) empty() bool {
	return len(m.upserts) == 0 && len(m.deletes) == 0 &&
		len(m.providerEvictions) == 0 && m.cleanupBefore.IsZero()
}

// PersistentStickyCache keeps the low-latency memory cache and mirrors its
// mutations to durable storage asynchronously. Writes are coalesced by key and
// provider, so sticky updates never make a request wait on SQLite. Close should
// be called during graceful shutdown to flush the remaining best-effort state.
type PersistentStickyCache struct {
	*MemoryStickyCache
	persistence StickyPersistence
	logger      *zap.Logger

	pendingMu sync.Mutex
	pending   stickyPendingMutations
	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	flushMu   sync.Mutex
	closeOnce sync.Once
}

var _ internal.StickyCache = (*PersistentStickyCache)(nil)

// NewPersistentStickyCache loads non-expired bindings from persistence. An
// unavailable persistence layer is logged and treated as an empty cache so the
// service can still start and rebuild affinity naturally from new traffic.
func NewPersistentStickyCache(
	persistence StickyPersistence,
	clock internal.Clock,
	logger *zap.Logger,
) *PersistentStickyCache {
	if clock == nil {
		clock = internal.RealClock{}
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	cache := &PersistentStickyCache{
		MemoryStickyCache: NewMemoryStickyCache(clock),
		persistence:       persistence,
		logger:            logger,
		pending:           newStickyPendingMutations(),
	}
	if persistence == nil {
		return cache
	}

	ctx := context.Background()
	entries, err := persistence.LoadStickyEntries(ctx, clock.Now())
	if err != nil {
		cache.logPersistenceError("load", err)
	} else {
		for _, entry := range entries {
			cache.restoreEntry(entry)
		}
		logger.Info("sticky cache restored", zap.Int("entry_count", cache.Len()))
	}

	cache.wake = make(chan struct{}, 1)
	cache.stop = make(chan struct{})
	cache.done = make(chan struct{})
	go cache.runPersistenceLoop()
	return cache
}

// MergeRestoredEntries publishes a committed import without replacing live
// affinity or resurrecting entries whose local deletion is still queued.
func (c *PersistentStickyCache) MergeRestoredEntries(entries []model.StickyEntry) {
	if c == nil || c.MemoryStickyCache == nil {
		return
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for _, entry := range entries {
		if _, deleted := c.pending.deletes[entry.Key]; deleted {
			continue
		}
		if _, evicted := c.pending.providerEvictions[entry.ProviderID]; evicted {
			continue
		}
		if _, exists := c.Get(entry.Key); exists {
			continue
		}
		c.restoreEntry(entry)
	}
}

// Set updates memory first and queues a coalesced durable upsert.
func (c *PersistentStickyCache) Set(key model.StickyKey, providerID string, ttl time.Duration) {
	if c == nil || c.MemoryStickyCache == nil {
		return
	}
	// Memory and queued persistence must share one mutation order; otherwise a
	// concurrent completion can restore a different affinity after restart.
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	now := c.clock.Now()
	c.setAt(key, providerID, now.Add(ttl))
	if c.persistence == nil {
		return
	}

	delete(c.pending.deletes, key)
	c.pending.upserts[key] = model.StickyEntry{
		Key:        key,
		ProviderID: providerID,
		ExpiresAt:  now.Add(ttl),
	}
	c.signalPersistence()
}

// Delete removes a binding from memory immediately and queues its durable delete.
func (c *PersistentStickyCache) Delete(key model.StickyKey) {
	if c == nil || c.MemoryStickyCache == nil {
		return
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.MemoryStickyCache.Delete(key)
	if c.persistence == nil {
		return
	}

	delete(c.pending.upserts, key)
	c.pending.deletes[key] = struct{}{}
	c.signalPersistence()
}

// EvictProvider removes all bindings for a provider from memory immediately and
// queues a provider-scoped durable delete. A later Set is applied after the
// provider delete, so a new binding can intentionally re-establish affinity.
func (c *PersistentStickyCache) EvictProvider(providerID string) {
	if c == nil || c.MemoryStickyCache == nil || providerID == "" {
		return
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.MemoryStickyCache.EvictProvider(providerID)
	if c.persistence == nil {
		return
	}

	c.pending.providerEvictions[providerID] = struct{}{}
	for key, entry := range c.pending.upserts {
		if entry.ProviderID == providerID {
			delete(c.pending.upserts, key)
		}
	}
	c.signalPersistence()
}

// Cleanup removes expired bindings from memory and queues a durable sweep. The
// database sweep also handles entries that expired while this process was down.
func (c *PersistentStickyCache) Cleanup() {
	if c == nil || c.MemoryStickyCache == nil {
		return
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	now := c.clock.Now()
	c.MemoryStickyCache.Cleanup()
	if c.persistence == nil {
		return
	}

	if c.pending.cleanupBefore.IsZero() || now.After(c.pending.cleanupBefore) {
		c.pending.cleanupBefore = now
	}
	for key, entry := range c.pending.upserts {
		if !entry.ExpiresAt.After(now) {
			delete(c.pending.upserts, key)
		}
	}
	c.signalPersistence()
}

// StartCleanupLoop periodically sweeps both memory and durable state.
func (c *PersistentStickyCache) StartCleanupLoop(interval time.Duration) (stop func()) {
	if c == nil || c.MemoryStickyCache == nil {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		ticker := c.clock.NewTicker(interval)
		defer ticker.Stop()
		defer close(finished)
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
		<-finished
	}
}

// Close stops the write-behind worker and flushes all queued mutations. The
// caller controls the flush deadline because persistence is advisory during
// shutdown and must not make process termination unbounded.
func (c *PersistentStickyCache) Close(ctx context.Context) error {
	if c == nil || c.persistence == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.closeOnce.Do(func() {
		close(c.stop)
		<-c.done
	})
	return c.flush(ctx)
}

func (c *PersistentStickyCache) setAt(key model.StickyKey, providerID string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLocked(key, providerID, expiresAt)
}

func (c *PersistentStickyCache) signalPersistence() {
	if c.wake == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *PersistentStickyCache) runPersistenceLoop() {
	ticker := c.clock.NewTicker(stickyPersistenceRetryInterval)
	defer ticker.Stop()
	defer close(c.done)
	for {
		select {
		case <-c.wake:
			if err := c.flush(context.Background()); err != nil {
				c.logPersistenceError("flush", err)
			}
		case <-ticker.C:
			if err := c.flush(context.Background()); err != nil {
				c.logPersistenceError("retry", err)
			}
		case <-c.stop:
			return
		}
	}
}

func (c *PersistentStickyCache) flush(ctx context.Context) error {
	if c.persistence == nil {
		return nil
	}
	c.flushMu.Lock()
	defer c.flushMu.Unlock()

	c.pendingMu.Lock()
	batch := c.pending
	c.pending = newStickyPendingMutations()
	c.pendingMu.Unlock()
	if batch.empty() {
		return nil
	}

	if err := c.applyBatch(ctx, batch); err != nil {
		c.requeue(batch)
		return err
	}
	return nil
}

func (c *PersistentStickyCache) applyBatch(ctx context.Context, batch stickyPendingMutations) error {
	for _, providerID := range sortedStrings(batch.providerEvictions) {
		if err := c.persistence.DeleteStickyEntriesByProvider(ctx, providerID); err != nil {
			return err
		}
	}
	for _, key := range sortedStickyKeys(batch.deletes) {
		if err := c.persistence.DeleteStickyEntry(ctx, key); err != nil {
			return err
		}
	}
	for _, entry := range sortedStickyEntries(batch.upserts) {
		if err := c.persistence.UpsertStickyEntry(ctx, entry); err != nil {
			return err
		}
	}
	if !batch.cleanupBefore.IsZero() {
		if err := c.persistence.DeleteExpiredStickyEntries(ctx, batch.cleanupBefore); err != nil {
			return err
		}
	}
	return nil
}

func (c *PersistentStickyCache) requeue(batch stickyPendingMutations) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	for providerID := range batch.providerEvictions {
		c.pending.providerEvictions[providerID] = struct{}{}
	}
	for key := range batch.deletes {
		if _, newerUpsert := c.pending.upserts[key]; newerUpsert {
			continue
		}
		if _, newerDelete := c.pending.deletes[key]; !newerDelete {
			c.pending.deletes[key] = struct{}{}
		}
	}
	for key, entry := range batch.upserts {
		if _, newerUpsert := c.pending.upserts[key]; newerUpsert {
			continue
		}
		if _, newerDelete := c.pending.deletes[key]; newerDelete {
			continue
		}
		if _, evicted := c.pending.providerEvictions[entry.ProviderID]; evicted {
			continue
		}
		c.pending.upserts[key] = entry
	}
	if !batch.cleanupBefore.IsZero() &&
		(c.pending.cleanupBefore.IsZero() || batch.cleanupBefore.After(c.pending.cleanupBefore)) {
		c.pending.cleanupBefore = batch.cleanupBefore
	}
}

func sortedStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedStickyKeys(values map[model.StickyKey]struct{}) []model.StickyKey {
	result := make([]model.StickyKey, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool { return stickyKeyOrder(result[i], result[j]) })
	return result
}

func sortedStickyEntries(values map[model.StickyKey]model.StickyEntry) []model.StickyEntry {
	result := make([]model.StickyEntry, 0, len(values))
	for _, entry := range values {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return stickyKeyOrder(result[i].Key, result[j].Key) })
	return result
}

func stickyKeyOrder(left, right model.StickyKey) bool {
	for _, pair := range [][2]string{
		{left.IP, right.IP},
		{left.User, right.User},
		{left.APIType, right.APIType},
		{left.Model, right.Model},
		{left.ClientScope, right.ClientScope},
	} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return false
}

func (c *PersistentStickyCache) logPersistenceError(operation string, err error) {
	if c == nil || c.logger == nil || err == nil {
		return
	}
	c.logger.Warn("sticky cache persistence degraded",
		zap.String("operation", operation),
		zap.Error(err),
	)
}
