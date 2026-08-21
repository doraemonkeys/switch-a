package store

import (
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
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

func TestNewSQLiteStore_CreatesProviderStateAndRoutingPolicyTables(t *testing.T) {
	store := setupTestStore(t)

	testCases := []struct {
		name  string
		model any
	}{
		{name: "provider_credentials", model: &model.ProviderCredential{}},
		{name: "provider_auth_states", model: &model.ProviderAuthState{}},
		{name: "routing_policies", model: &model.RoutingPolicy{}},
		{name: "routing_policy_groups", model: &model.RoutingPolicyGroup{}},
		{name: "routing_policy_vendors", model: &model.RoutingPolicyVendor{}},
	}

	for _, tc := range testCases {
		if !store.db.Migrator().HasTable(tc.model) {
			t.Fatalf("table %s was not created", tc.name)
		}
	}
}

func TestNewSQLiteStoreCreatesNarrowRequestLogAnalyticsIndexes(t *testing.T) {
	store := setupTestStore(t)
	wantColumns := map[string][]string{
		requestLogProviderCreatedAtIndex: {"provider_id", "created_at"},
		requestLogModelCreatedAtIndex:    {"model", "created_at"},
		requestLogAPITypeCreatedAtIndex:  {"api_type", "created_at"},
	}

	for indexName, expectedColumns := range wantColumns {
		var columns []string
		if err := store.db.Raw(
			"SELECT name FROM pragma_index_info(?) ORDER BY seqno",
			indexName,
		).Scan(&columns).Error; err != nil {
			t.Fatalf("read %s columns: %v", indexName, err)
		}
		if len(columns) != len(expectedColumns) {
			t.Fatalf("%s columns = %v, want %v", indexName, columns, expectedColumns)
		}
		for index := range columns {
			if columns[index] != expectedColumns[index] {
				t.Errorf("%s columns = %v, want %v", indexName, columns, expectedColumns)
				break
			}
		}
	}
}
