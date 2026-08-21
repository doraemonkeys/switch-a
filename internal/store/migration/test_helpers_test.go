package migration

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openMigrationSQLiteDB(t *testing.T, dbFileName string) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), dbFileName)
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close sqlite db %q: %v", dbFileName, closeErr)
		}
	})

	return db
}

func setupMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openMigrationSQLiteDB(t, "migration.db")
	if err := db.AutoMigrate(&model.RuntimeConfig{}); err != nil {
		t.Fatalf("auto-migrate runtime_config: %v", err)
	}

	return db
}

func seedConfig(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()

	cfg := model.RuntimeConfig{
		Key:   key,
		Value: value,
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("seed config %q: %v", key, err)
	}
}

func readConfigValue(t *testing.T, db *gorm.DB, key string) string {
	t.Helper()

	var cfg model.RuntimeConfig
	if err := db.First(&cfg, "key = ?", key).Error; err != nil {
		t.Fatalf("read config %q: %v", key, err)
	}

	return cfg.Value
}

func assertConfigMissing(t *testing.T, db *gorm.DB, key string) {
	t.Helper()

	var cfg model.RuntimeConfig
	err := db.First(&cfg, "key = ?", key).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("config %q should be missing, err=%v", key, err)
	}
}

func setupProviderStateMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := setupMigrationTestDB(t)
	if err := db.AutoMigrate(
		&model.Provider{},
		&model.ProviderAPIType{},
		&model.ProviderCredential{},
		&model.ProviderAuthState{},
	); err != nil {
		t.Fatalf("auto-migrate provider state tables: %v", err)
	}

	hasCredentialData, err := tableColumnExists(db, providersTableName, providerCredentialDataColumn)
	if err != nil {
		t.Fatalf("check legacy provider credential column: %v", err)
	}
	if !hasCredentialData {
		if err := db.Exec(
			fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s TEXT DEFAULT ''`, providersTableName, providerCredentialDataColumn),
		).Error; err != nil {
			t.Fatalf("add legacy provider credential column: %v", err)
		}
	}

	return db
}

func insertLegacyProvider(t *testing.T, db *gorm.DB, provider *model.Provider, credentialData string) {
	t.Helper()

	if err := db.Omit("Credential", "AuthState").Create(provider).Error; err != nil {
		t.Fatalf("create legacy provider: %v", err)
	}
	if err := db.Model(&model.Provider{}).
		Where("id = ?", provider.ID).
		Update(providerCredentialDataColumn, credentialData).Error; err != nil {
		t.Fatalf("seed legacy credential shadow for %q: %v", provider.ID, err)
	}
}

func strPtr(value string) *string {
	return &value
}

func setupWebSocketMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openMigrationSQLiteDB(t, "ws_migration.db")
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_web_socket BOOLEAN DEFAULT 0,
		is_websocket BOOLEAN DEFAULT 0,
		provider_id TEXT DEFAULT '',
		created_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create legacy request_logs table: %v", err)
	}

	return db
}

func setupRequestLogLifecycleMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openMigrationSQLiteDB(t, "request_log_lifecycle_migration.db")
	createLegacyTableSQL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT,
		is_websocket BOOLEAN DEFAULT 0,
		provider_id TEXT DEFAULT '',
		semantics_version TEXT DEFAULT 'normalized_v1',
		session_committed BOOLEAN,
		client_visible BOOLEAN,
		sticky_written BOOLEAN,
		probe_outcome TEXT,
		commit_source TEXT,
		created_at DATETIME
	)`, requestLogsTableName)
	if err := db.Exec(createLegacyTableSQL).Error; err != nil {
		t.Fatalf("create request_logs: %v", err)
	}

	return db
}
