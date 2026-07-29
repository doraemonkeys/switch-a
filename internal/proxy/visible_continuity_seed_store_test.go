package proxy

import (
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestVisibleContinuitySeedStoreLookupAndConsume(t *testing.T) {
	baseTime := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clock := &mockClock{current: baseTime}
	store := NewVisibleContinuitySeedStoreWithClock(clock)

	key := model.StickyKey{IP: "127.0.0.1", User: "user-1", APIType: "codex"}
	store.Store(model.VisibleContinuitySeed{
		SeedID:              "seed-1",
		ContinuityKey:       key,
		OriginProviderID:    "provider-origin",
		OriginVendor:        "vendor-a",
		ContaminatedVendors: []string{"vendor-a"},
		StrictestScope:      model.ScopeVendor,
		ObservedAt:          baseTime,
	})

	clock.current = baseTime.Add(2 * time.Second)
	candidate, found := store.Lookup(key)
	if !found {
		t.Fatal("expected continuity seed lookup to succeed before TTL expiry")
	}
	if candidate.SeedID != "seed-1" {
		t.Fatalf("candidate.SeedID = %q, want %q", candidate.SeedID, "seed-1")
	}
	if candidate.Age != 2*time.Second {
		t.Fatalf("candidate.Age = %s, want %s", candidate.Age, 2*time.Second)
	}

	consumed, consumedOK := store.CompareAndConsume(key, "seed-1")
	if !consumedOK {
		t.Fatal("expected compare-and-consume to succeed for matching seed ID")
	}
	if consumed.OriginProviderID != "provider-origin" {
		t.Fatalf("OriginProviderID = %q, want %q", consumed.OriginProviderID, "provider-origin")
	}
	if _, found := store.Lookup(key); found {
		t.Fatal("expected consumed seed to be removed from the shared store")
	}
}

func TestVisibleContinuitySeedStoreRequiresMatchingSeedID(t *testing.T) {
	baseTime := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clock := &mockClock{current: baseTime}
	store := NewVisibleContinuitySeedStoreWithClock(clock)

	key := model.StickyKey{IP: "127.0.0.1", User: "user-1", APIType: "codex"}
	store.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-1",
		ContinuityKey:    key,
		OriginProviderID: "provider-origin",
		ObservedAt:       baseTime,
	})

	if _, ok := store.CompareAndConsume(key, "seed-other"); ok {
		t.Fatal("expected compare-and-consume to reject mismatched seed IDs")
	}
	if _, found := store.Lookup(key); !found {
		t.Fatal("expected mismatched compare-and-consume to leave the seed intact")
	}
}

func TestVisibleContinuitySeedStoreExpiresSeeds(t *testing.T) {
	baseTime := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clock := &mockClock{current: baseTime}
	store := NewVisibleContinuitySeedStoreWithClock(clock)

	key := model.StickyKey{IP: "127.0.0.1", User: "user-1", APIType: "codex"}
	store.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-1",
		ContinuityKey:    key,
		OriginProviderID: "provider-origin",
		ObservedAt:       baseTime,
	})

	clock.current = baseTime.Add(model.VisibleContinuitySeedTTL + time.Millisecond)
	if _, found := store.Lookup(key); found {
		t.Fatal("expected lookup to evict expired seeds")
	}
	if store.Len() != 0 {
		t.Fatalf("Len() = %d, want 0 after expiry cleanup", store.Len())
	}
}

func TestVisibleContinuitySeedStoreKeepsNewestObservedAt(t *testing.T) {
	baseTime := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	clock := &mockClock{current: baseTime}
	store := NewVisibleContinuitySeedStoreWithClock(clock)

	key := model.StickyKey{IP: "127.0.0.1", User: "user-1", APIType: "codex"}
	store.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-newer",
		ContinuityKey:    key,
		OriginProviderID: "provider-newer",
		ObservedAt:       baseTime.Add(2 * time.Second),
	})
	store.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-older",
		ContinuityKey:    key,
		OriginProviderID: "provider-older",
		ObservedAt:       baseTime,
	})

	clock.current = baseTime.Add(3 * time.Second)
	candidate, found := store.Lookup(key)
	if !found {
		t.Fatal("expected lookup to keep the newest seed for a continuity key")
	}
	if candidate.SeedID != "seed-newer" {
		t.Fatalf("candidate.SeedID = %q, want %q", candidate.SeedID, "seed-newer")
	}
}
