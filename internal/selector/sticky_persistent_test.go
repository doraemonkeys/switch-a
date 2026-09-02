package selector

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

type persistentStickyTestStore struct {
	mu         sync.Mutex
	entries    map[model.StickyKey]model.StickyEntry
	loadErr    error
	upsertErr  error
	deleteErr  error
	evictErr   error
	cleanupErr error
}

func newPersistentStickyTestStore(entries ...model.StickyEntry) *persistentStickyTestStore {
	result := &persistentStickyTestStore{entries: make(map[model.StickyKey]model.StickyEntry)}
	for _, entry := range entries {
		result.entries[entry.Key] = entry
	}
	return result
}

func (s *persistentStickyTestStore) LoadStickyEntries(_ context.Context, now time.Time) ([]model.StickyEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	result := make([]model.StickyEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		if entry.ExpiresAt.After(now) {
			result = append(result, entry)
		}
	}
	return result, nil
}

func (s *persistentStickyTestStore) UpsertStickyEntry(_ context.Context, entry model.StickyEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.entries[entry.Key] = entry
	return nil
}

func (s *persistentStickyTestStore) DeleteStickyEntry(_ context.Context, key model.StickyKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.entries, key)
	return nil
}

func (s *persistentStickyTestStore) DeleteStickyEntriesByProvider(_ context.Context, providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.evictErr != nil {
		return s.evictErr
	}
	for key, entry := range s.entries {
		if entry.ProviderID == providerID {
			delete(s.entries, key)
		}
	}
	return nil
}

func (s *persistentStickyTestStore) DeleteExpiredStickyEntries(_ context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanupErr != nil {
		return s.cleanupErr
	}
	for key, entry := range s.entries {
		if !entry.ExpiresAt.After(now) {
			delete(s.entries, key)
		}
	}
	return nil
}

func (s *persistentStickyTestStore) has(key model.StickyKey) (model.StickyEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	return entry, ok
}

func (s *persistentStickyTestStore) setErrors(upsert, delete, evict, cleanup error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertErr = upsert
	s.deleteErr = delete
	s.evictErr = evict
	s.cleanupErr = cleanup
}

func TestPersistentStickyCache_RestoresAndFlushesAcrossInstances(t *testing.T) {
	now := time.Now()
	clock := &mockClock{now: now}
	liveKey := model.StickyKey{IP: "10.0.0.1", User: "alice", APIType: "chat"}
	expiredKey := model.StickyKey{IP: "10.0.0.2", User: "bob", APIType: "chat"}
	store := newPersistentStickyTestStore(
		model.StickyEntry{Key: liveKey, ProviderID: "provider-a", ExpiresAt: now.Add(time.Minute)},
		model.StickyEntry{Key: expiredKey, ProviderID: "provider-b", ExpiresAt: now.Add(-time.Second)},
	)

	cache := NewPersistentStickyCache(store, clock, nil)
	if providerID, found := cache.Get(liveKey); !found || providerID != "provider-a" {
		t.Fatalf("expected restored live binding, got %q (found=%v)", providerID, found)
	}
	if _, found := cache.Get(expiredKey); found {
		t.Fatal("expired durable binding must not be restored")
	}

	newKey := model.StickyKey{IP: "10.0.0.3", User: "carol", APIType: "chat"}
	cache.Set(newKey, "provider-c", time.Minute)
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("close flush failed: %v", err)
	}

	if entry, ok := store.has(newKey); !ok || entry.ProviderID != "provider-c" {
		t.Fatalf("expected set binding to be durable, got %#v (found=%v)", entry, ok)
	}

	restarted := NewPersistentStickyCache(store, clock, nil)
	defer restarted.Close(context.Background())
	if providerID, found := restarted.Get(newKey); !found || providerID != "provider-c" {
		t.Fatalf("expected binding after restart, got %q (found=%v)", providerID, found)
	}

	restarted.Delete(newKey)
	if err := restarted.Close(context.Background()); err != nil {
		t.Fatalf("second close flush failed: %v", err)
	}
	if _, ok := store.has(newKey); ok {
		t.Fatal("deleted binding should not remain durable")
	}
}

func TestPersistentStickyCache_EvictionAndCleanupAreDurable(t *testing.T) {
	now := time.Now()
	clock := &mockClock{now: now}
	keyA := model.StickyKey{IP: "10.0.0.1", APIType: "chat"}
	keyB := model.StickyKey{IP: "10.0.0.2", APIType: "chat"}
	store := newPersistentStickyTestStore(
		model.StickyEntry{Key: keyA, ProviderID: "provider-a", ExpiresAt: now.Add(time.Minute)},
		model.StickyEntry{Key: keyB, ProviderID: "provider-b", ExpiresAt: now.Add(time.Minute)},
	)
	cache := NewPersistentStickyCache(store, clock, nil)
	cache.EvictProvider("provider-a")
	clock.Advance(2 * time.Minute)
	cache.Cleanup()
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("close flush failed: %v", err)
	}
	if _, ok := store.has(keyA); ok {
		t.Fatal("evicted provider binding should be durable-deleted")
	}
	if _, ok := store.has(keyB); ok {
		t.Fatal("expired provider binding should be durable-deleted")
	}
}

func TestPersistentStickyCache_PersistenceFailureDoesNotBreakMemory(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newPersistentStickyTestStore()
	store.setErrors(errors.New("disk unavailable"), nil, nil, nil)
	cache := NewPersistentStickyCache(store, clock, nil)
	key := model.StickyKey{IP: "10.0.0.1", APIType: "chat"}
	cache.Set(key, "provider-a", time.Minute)
	if providerID, found := cache.Get(key); !found || providerID != "provider-a" {
		t.Fatalf("memory cache should remain usable after persistence failure, got %q (found=%v)", providerID, found)
	}
	if err := cache.Close(context.Background()); err == nil {
		t.Fatal("close should report failed best-effort flush")
	}
}

func TestPersistentStickyCache_RetryFlushAndErrorBranches(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	store := newPersistentStickyTestStore()
	store.setErrors(errors.New("disk unavailable"), nil, nil, nil)
	cache := NewPersistentStickyCache(store, clock, nil)
	keyA := model.StickyKey{IP: "10.0.0.1", APIType: "chat"}
	cache.Set(keyA, "provider-a", time.Minute)
	if err := cache.flush(context.Background()); err == nil {
		t.Fatal("expected flush error")
	}
	store.setErrors(nil, nil, nil, nil)
	keyB := model.StickyKey{IP: "10.0.0.2", APIType: "chat"}
	cache.Set(keyB, "provider-b", time.Minute)
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("retry flush failed: %v", err)
	}
	if _, ok := store.has(keyA); !ok {
		t.Fatal("failed upsert should have been requeued")
	}
	if _, ok := store.has(keyB); !ok {
		t.Fatal("new upsert should have been flushed")
	}

	store.setErrors(nil, errors.New("delete unavailable"), nil, nil)
	cache = NewPersistentStickyCache(store, clock, nil)
	cache.Delete(keyA)
	if err := cache.Close(context.Background()); err == nil {
		t.Fatal("expected delete flush error")
	}
	store.setErrors(nil, nil, nil, nil)
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("delete retry flush failed: %v", err)
	}

	store.setErrors(nil, nil, errors.New("eviction unavailable"), nil)
	cache = NewPersistentStickyCache(store, clock, nil)
	cache.EvictProvider("provider-b")
	if err := cache.Close(context.Background()); err == nil {
		t.Fatal("expected eviction flush error")
	}
	store.setErrors(nil, nil, nil, nil)
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("eviction retry flush failed: %v", err)
	}

	store.setErrors(nil, nil, nil, errors.New("cleanup unavailable"))
	cache = NewPersistentStickyCache(store, clock, nil)
	cache.Cleanup()
	if err := cache.Close(context.Background()); err == nil {
		t.Fatal("expected cleanup flush error")
	}
	store.setErrors(nil, nil, nil, nil)
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("cleanup retry flush failed: %v", err)
	}
}

func TestPersistentStickyCache_StartCleanupLoopAndNilPersistence(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cache := NewPersistentStickyCache(nil, clock, nil)
	key := model.StickyKey{IP: "10.0.0.1", APIType: "chat"}
	cache.Set(key, "provider-a", time.Millisecond)
	stop := cache.StartCleanupLoop(5 * time.Millisecond)
	clock.Advance(time.Second)
	time.Sleep(20 * time.Millisecond)
	stop()
	if cache.Len() != 0 {
		t.Fatal("cleanup loop should remove expired in-memory entries")
	}
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("nil persistence close failed: %v", err)
	}

	var nilCache *PersistentStickyCache
	if err := nilCache.Close(context.Background()); err != nil {
		t.Fatalf("nil cache close failed: %v", err)
	}
	nilCache.StartCleanupLoop(time.Second)()
}

func TestStickyKeyOrderingHelpers(t *testing.T) {
	left := model.StickyKey{IP: "a", User: "b", APIType: "c", Model: "d"}
	right := model.StickyKey{IP: "a", User: "b", APIType: "c", Model: "e"}
	if !stickyKeyOrder(left, right) || stickyKeyOrder(right, left) {
		t.Fatal("sticky key ordering should be lexicographic")
	}
	if got := sortedStrings(map[string]struct{}{"b": {}, "a": {}}); got[0] != "a" {
		t.Fatalf("expected sorted provider IDs, got %v", got)
	}
}
