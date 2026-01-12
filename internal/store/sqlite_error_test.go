package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
)

// setupClosedStore creates a store and immediately closes its underlying connection
// to trigger error paths in database operations.
func setupClosedStore(t *testing.T) *SQLiteStore {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath, internal.RealClock{})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Close the underlying connection to trigger errors
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	return store
}

func TestNewSQLiteStore_InvalidPath(t *testing.T) {
	// Use an invalid path that will cause sqlite to fail
	store, err := NewSQLiteStore("/nonexistent/path/that/cannot/exist/test.db", internal.RealClock{})
	if err == nil {
		store.Close()
		t.Fatal("expected error for invalid path, got nil")
	}
}

func TestListProviders_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.ListProviders(ctx)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestListProvidersByAPIType_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.ListProvidersByAPIType(ctx, "claude")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestGetProvider_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.GetProvider(ctx, "p1")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestCreateProvider_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	p := &model.Provider{
		ID:      "p1",
		Name:    "Test",
		BaseURL: "https://test.com",
		APIKey:  "key",
		Enabled: true,
	}
	err := store.CreateProvider(ctx, p)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestUpdateProvider_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	p := &model.Provider{
		ID:      "p1",
		Name:    "Test",
		BaseURL: "https://test.com",
		APIKey:  "key",
		Enabled: true,
	}
	err := store.UpdateProvider(ctx, p)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestDeleteProvider_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	err := store.DeleteProvider(ctx, "p1")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestListGroups_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.ListGroups(ctx)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestGetGroup_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.GetGroup(ctx, "g1")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestCreateGroup_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	g := &model.Group{
		ID:      "g1",
		Name:    "Test",
		Enabled: true,
	}
	err := store.CreateGroup(ctx, g)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestUpdateGroup_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	g := &model.Group{
		ID:      "g1",
		Name:    "Test",
		Enabled: true,
	}
	err := store.UpdateGroup(ctx, g)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestDeleteGroup_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	err := store.DeleteGroup(ctx, "g1")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestGetHealthState_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.GetHealthState(ctx, "p1")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestUpdateHealthState_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	state := &model.HealthState{
		ProviderID: "p1",
		Available:  true,
	}
	err := store.UpdateHealthState(ctx, state)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestListHealthStates_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.ListHealthStates(ctx)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestGetConfig_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.GetConfig(ctx, "test_key")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestSetConfig_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	err := store.SetConfig(ctx, "test_key", "test_value")
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestInsertLog_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	log := &model.RequestLog{
		ProviderID: "p1",
		APIType:    "claude",
		CreatedAt:  time.Now(),
	}
	err := store.InsertLog(ctx, log)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestListLogs_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	_, err := store.ListLogs(ctx, model.LogFilter{Limit: 10, Offset: 0})
	if err == nil {
		t.Error("expected error on closed connection")
	}
}

func TestCleanOldLogs_Error(t *testing.T) {
	store := setupClosedStore(t)
	ctx := context.Background()

	err := store.CleanOldLogs(ctx, 7)
	if err == nil {
		t.Error("expected error on closed connection")
	}
}
