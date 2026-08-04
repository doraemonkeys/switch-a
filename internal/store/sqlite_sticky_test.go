package store

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestSQLiteStore_StickyEntriesSurviveReloadAndRespectExpiry(t *testing.T) {
	store := setupTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := model.StickyKey{IP: "10.0.0.1", User: "alice", APIType: "chat", Model: "model-a"}
	entry := model.StickyEntry{Key: key, ProviderID: "provider-a", ExpiresAt: now.Add(time.Minute)}
	ctx := context.Background()

	if err := store.UpsertStickyEntry(ctx, entry); err != nil {
		t.Fatalf("upsert sticky entry: %v", err)
	}
	entries, err := store.LoadStickyEntries(ctx, now)
	if err != nil {
		t.Fatalf("load sticky entries: %v", err)
	}
	if len(entries) != 1 || entries[0] != entry {
		t.Fatalf("unexpected loaded entries: %#v", entries)
	}

	updated := entry
	updated.ProviderID = "provider-b"
	if err := store.UpsertStickyEntry(ctx, updated); err != nil {
		t.Fatalf("upsert sticky replacement: %v", err)
	}
	entries, err = store.LoadStickyEntries(ctx, now)
	if err != nil || len(entries) != 1 || entries[0].ProviderID != "provider-b" {
		t.Fatalf("expected replacement to be loaded, entries=%#v err=%v", entries, err)
	}

	if err := store.DeleteExpiredStickyEntries(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("delete expired sticky entries: %v", err)
	}
	entries, err = store.LoadStickyEntries(ctx, now)
	if err != nil {
		t.Fatalf("load after expiry cleanup: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expired entry should not be loaded, got %#v", entries)
	}
}

func TestSQLiteStore_StickyEntriesDeleteByProvider(t *testing.T) {
	store := setupTestStore(t)
	now := time.Now().UTC().Add(time.Minute)
	keyA := model.StickyKey{IP: "10.0.0.1", APIType: "chat"}
	keyB := model.StickyKey{IP: "10.0.0.2", APIType: "chat"}
	ctx := context.Background()
	for _, entry := range []model.StickyEntry{
		{Key: keyA, ProviderID: "provider-a", ExpiresAt: now},
		{Key: keyB, ProviderID: "provider-b", ExpiresAt: now},
	} {
		if err := store.UpsertStickyEntry(ctx, entry); err != nil {
			t.Fatalf("upsert sticky entry: %v", err)
		}
	}

	if err := store.DeleteStickyEntriesByProvider(ctx, "provider-a"); err != nil {
		t.Fatalf("delete provider sticky entries: %v", err)
	}
	entries, err := store.LoadStickyEntries(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("load remaining sticky entries: %v", err)
	}
	if len(entries) != 1 || entries[0].ProviderID != "provider-b" {
		t.Fatalf("expected provider-b to remain, got %#v", entries)
	}
}

func TestSQLiteStore_StickyEntriesBestEffortMethodsReportClosedDatabase(t *testing.T) {
	store := setupTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	ctx := context.Background()
	key := model.StickyKey{IP: "10.0.0.1", APIType: "chat"}
	entry := model.StickyEntry{Key: key, ProviderID: "provider-a", ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := store.LoadStickyEntries(ctx, time.Now()); err == nil {
		t.Fatal("load should report a closed database")
	}
	if err := store.UpsertStickyEntry(ctx, entry); err == nil {
		t.Fatal("upsert should report a closed database")
	}
	if err := store.DeleteStickyEntry(ctx, key); err == nil {
		t.Fatal("delete should report a closed database")
	}
	if err := store.DeleteStickyEntriesByProvider(ctx, "provider-a"); err == nil {
		t.Fatal("provider eviction should report a closed database")
	}
	if err := store.DeleteStickyEntriesByProvider(ctx, ""); err != nil {
		t.Fatalf("empty provider eviction should be a no-op: %v", err)
	}
	if err := store.DeleteExpiredStickyEntries(ctx, time.Now()); err == nil {
		t.Fatal("cleanup should report a closed database")
	}
}
