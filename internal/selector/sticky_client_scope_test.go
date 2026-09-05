package selector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func stickyTestClientScope(t *testing.T, version, credential string) codexidentity.ClientScope {
	t.Helper()
	scope, err := codexidentity.ClientScopeFromDigest(version, sha256.Sum256([]byte(credential)))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestBuildContinuityKeyCodexClientScope(t *testing.T) {
	req := &model.SelectRequest{ClientIP: "127.0.0.1", User: "same-user", APIType: "codex", Model: "gpt", StickyMode: model.StickyModeModel}
	req.ClientScope = stickyTestClientScope(t, "v1", "client-a")
	keyA := BuildContinuityKey(req)
	encoded, err := req.ClientScope.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if keyA.ClientScope != hex.EncodeToString(encoded) || keyA.ClientScope == "" {
		t.Fatal("sticky scope must encode the full versioned digest")
	}
	req.ClientScope = stickyTestClientScope(t, "v1", "client-b")
	keyB := BuildContinuityKey(req)
	if keyA == keyB {
		t.Fatal("separate client credentials share affinity")
	}
	req.ClientScope = stickyTestClientScope(t, "v2", "client-a")
	if BuildContinuityKey(req) == keyA {
		t.Fatal("key versions share affinity")
	}
	req.APIType = "chat"
	if key := BuildContinuityKey(req); key.ClientScope != "" || key.Model != "gpt" {
		t.Fatalf("non-Codex dimensions changed: %#v", key)
	}
	req.APIType = "codex"
	req.ClientScope = codexidentity.ClientScope{}
	if key := BuildContinuityKey(req); key.ClientScope != "" {
		t.Fatal("uninitialized scope must not invent an identity")
	}
	if key := BuildContinuityKey(nil); key != (model.StickyKey{}) {
		t.Fatalf("nil request key = %#v", key)
	}
}

func TestCodexSoftStickyRestoredSelectionAndFallback(t *testing.T) {
	for _, scenario := range []string{"eligible", "off", "miss", "expired", "disabled", "unhealthy", "missing-provider", "other-client", "legacy-unscoped"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now()
			clock := &mockClock{now: now}
			req := &model.SelectRequest{
				ClientIP: "127.0.0.1", User: "alice", APIType: "codex", Model: "gpt",
				StickyMode: model.StickyModeModel, ClientScope: stickyTestClientScope(t, "v1", "client-a"),
			}
			strategyProvider := authorityTestProvider("strategy", "https://a.example.test", "account-a", 0)
			stickyProvider := authorityTestProvider("sticky", "https://b.example.test", "account-b", 100)
			entry := model.StickyEntry{Key: BuildContinuityKey(req), ProviderID: stickyProvider.ID, ExpiresAt: now.Add(time.Minute)}
			switch scenario {
			case "off":
				req.StickyMode = model.StickyModeOff
			case "expired":
				entry.ExpiresAt = now
			case "disabled":
				stickyProvider.Enabled = false
			case "missing-provider":
				entry.ProviderID = "removed"
			case "other-client":
				req.ClientScope = stickyTestClientScope(t, "v1", "client-b")
			case "legacy-unscoped":
				entry.Key.ClientScope = ""
			}
			persistence := newPersistentStickyTestStore(entry)
			if scenario == "miss" {
				persistence = newPersistentStickyTestStore()
			}
			cache := NewPersistentStickyCache(persistence, clock, nil)
			defer cache.Close(context.Background())
			store := newMockStore()
			store.providers = []model.Provider{strategyProvider, stickyProvider}
			health := newMockHealthChecker()
			if scenario == "unhealthy" {
				health.available[stickyProvider.ID] = false
			}
			selector := NewSelector(Config{Store: store, StickyCache: cache, HealthChecker: health})
			result, err := selector.SelectWithMetadata(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			defer result.Lease.Release()
			want := strategyProvider.ID
			if scenario == "eligible" {
				want = stickyProvider.ID
			}
			if got := result.Provider().ID; got != want {
				t.Fatalf("selected %s, want %s", got, want)
			}
		})
	}
}

func TestPersistentStickyConcurrentCompletionsRestoreMemoryWinner(t *testing.T) {
	const writers = 16
	const completions = 100
	clock := &mockClock{now: time.Now()}
	persistence := newPersistentStickyTestStore()
	cache := NewPersistentStickyCache(persistence, clock, nil)
	req := &model.SelectRequest{APIType: "codex", StickyMode: model.StickyModeAPIType, ClientScope: stickyTestClientScope(t, "v1", "a")}
	keyA := BuildContinuityKey(req)
	req.ClientScope = stickyTestClientScope(t, "v1", "b")
	keyB := BuildContinuityKey(req)
	keys := []model.StickyKey{keyA, keyB}
	var completed sync.WaitGroup
	start := make(chan struct{})
	for writer := range writers {
		completed.Go(func() {
			<-start
			key := keys[writer%len(keys)]
			for completion := range completions {
				cache.Set(key, fmt.Sprintf("provider-%d-%d", writer, completion), time.Minute)
				if completion%2 == 0 {
					cache.Delete(key)
				}
			}
		})
	}
	close(start)
	completed.Wait()
	if err := cache.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := NewPersistentStickyCache(persistence, clock, nil)
	defer restarted.Close(context.Background())
	for _, key := range keys {
		want, wantFound := cache.Get(key)
		got, gotFound := restarted.Get(key)
		if gotFound != wantFound || got != want {
			t.Fatalf("restart changed concurrent completion winner: got %q/%v, want %q/%v", got, gotFound, want, wantFound)
		}
	}
}

func TestPersistentStickyClientScopesRemainIndependentAcrossRestart(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	persistence := newPersistentStickyTestStore()
	cache := NewPersistentStickyCache(persistence, clock, nil)
	req := &model.SelectRequest{APIType: "codex", StickyMode: model.StickyModeAPIType, ClientScope: stickyTestClientScope(t, "v1", "a")}
	keyA := BuildContinuityKey(req)
	req.ClientScope = stickyTestClientScope(t, "v1", "b")
	keyB := BuildContinuityKey(req)
	cache.Set(keyA, "provider-a", time.Minute)
	cache.Set(keyB, "provider-b", time.Minute)
	cache.Set(keyA, "provider-c", time.Minute)
	if err := cache.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := NewPersistentStickyCache(persistence, clock, nil)
	defer restarted.Close(context.Background())
	for key, want := range map[model.StickyKey]string{keyA: "provider-c", keyB: "provider-b"} {
		if got, ok := restarted.Get(key); !ok || got != want {
			t.Fatalf("restored %q, want %q", got, want)
		}
	}
	restarted.Delete(keyA)
	if got, ok := restarted.Get(keyB); !ok || got != "provider-b" {
		t.Fatal("deleting one client removed another client's affinity")
	}
	if stickyKeyOrder(keyA, keyB) == stickyKeyOrder(keyB, keyA) {
		t.Fatal("scope must participate in deterministic persistence ordering")
	}
}
