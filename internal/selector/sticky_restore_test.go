package selector

import (
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestRestoreStickyPreservesActiveAndPendingLocalMutations(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cache := NewPersistentStickyCache(nil, clock, nil)
	key := func(ip string) model.StickyKey { return model.StickyKey{Transport: "http", IP: ip, APIType: "codex"} }
	cache.Set(key("live"), "local", time.Minute)
	cache.pending.deletes[key("deleted")] = struct{}{}
	cache.pending.providerEvictions["evicted"] = struct{}{}
	var entries []model.StickyEntry
	for _, id := range []string{"live", "restored", "deleted", "evicted", "expired"} {
		at := clock.Now().Add(time.Minute)
		if id == "expired" {
			at = clock.Now().Add(-time.Minute)
		}
		entries = append(entries, model.StickyEntry{Key: key(id), ProviderID: id, ExpiresAt: at})
	}
	cache.MergeRestoredEntries(entries)
	for id, want := range map[string]string{"live": "local", "restored": "restored", "deleted": "", "evicted": "", "expired": ""} {
		got, ok := cache.Get(key(id))
		if got != want || ok != (want != "") {
			t.Fatalf("%s = %s %v", id, got, ok)
		}
	}
	var absent *PersistentStickyCache
	absent.MergeRestoredEntries(entries)
	(&PersistentStickyCache{}).MergeRestoredEntries(entries)
}
