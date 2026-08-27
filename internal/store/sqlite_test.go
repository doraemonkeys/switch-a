package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath, internal.RealClock{}, nil)
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

func TestNewSQLiteStoreEnablesForeignKeysOnEveryPooledConnection(t *testing.T) {
	store := setupTestStore(t)
	database, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	const poolSize = 4
	database.SetMaxOpenConns(poolSize)
	database.SetMaxIdleConns(poolSize)

	connections := make([]*sql.Conn, 0, poolSize)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range poolSize {
		connection, err := database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for index, connection := range connections {
		var enabled int
		if err := connection.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
			t.Fatalf("connection %d PRAGMA: %v", index, err)
		}
		if enabled != 1 {
			t.Fatalf("connection %d foreign_keys = %d, want 1", index, enabled)
		}
	}
}

func TestSQLiteForeignKeyConnectionConfigurationFailsClosed(t *testing.T) {
	if got := sqliteDSN("file:test.db?mode=memory"); !strings.Contains(got, "&_pragma=foreign_keys(1)") {
		t.Fatalf("sqliteDSN(existing query) = %q", got)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := assertSQLiteForeignKeys(db); err == nil {
		t.Fatal("assertSQLiteForeignKeys accepted disabled enforcement")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := assertSQLiteForeignKeys(db); err == nil {
		t.Fatal("assertSQLiteForeignKeys accepted a closed database")
	}
}

func TestNewSQLiteStore_CreatesCredentialSessionAndRoutingPolicyTables(t *testing.T) {
	store := setupTestStore(t)

	testCases := []struct {
		name  string
		model any
	}{
		{name: "credential_sessions", model: "credential_sessions"},
		{name: "route_target_credentials", model: "route_target_credentials"},
		{name: "routing_policies", model: &model.RoutingPolicy{}},
		{name: "routing_policy_groups", model: &model.RoutingPolicyGroup{}},
		{name: "routing_policy_vendors", model: &model.RoutingPolicyVendor{}},
	}

	for _, tc := range testCases {
		if !store.db.Migrator().HasTable(tc.model) {
			t.Fatalf("table %s was not created", tc.name)
		}
	}
	for _, obsolete := range []string{"provider_credentials", "provider_auth_states"} {
		if store.db.Migrator().HasTable(obsolete) {
			t.Fatalf("obsolete provider-owned credential table %s still exists", obsolete)
		}
	}
}

func TestNewSQLiteStoreCreatesNarrowRequestLogAnalyticsIndexes(t *testing.T) {
	store := setupTestStore(t)
	wantColumns := map[string][]string{
		requestLogCreatedAtUnixNanoIndex:         {requestLogCreatedAtUnixNanoColumn},
		requestLogProviderCreatedAtUnixNanoIndex: {"provider_id", requestLogCreatedAtUnixNanoColumn},
		requestLogModelCreatedAtUnixNanoIndex:    {"model", requestLogCreatedAtUnixNanoColumn},
		requestLogAPITypeCreatedAtUnixNanoIndex:  {"api_type", requestLogCreatedAtUnixNanoColumn},
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
	for _, legacyIndexName := range legacyRequestLogAnalyticsIndexes {
		if store.db.Migrator().HasIndex(&model.RequestLog{}, legacyIndexName) {
			t.Errorf("legacy lexical analytics index %s still exists", legacyIndexName)
		}
	}
}
