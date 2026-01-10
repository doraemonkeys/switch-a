package store

import (
	"path/filepath"
	"testing"

	"switch-a/internal"
)

func setupTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath, internal.RealClock{})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("cleanup error: %v", err)
		}
	})

	return store
}

func TestNewSQLiteStore(t *testing.T) {
	store := setupTestStore(t)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}
