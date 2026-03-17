package store

import (
	"errors"
	"path/filepath"
	"testing"

	"switch-a/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if err := db.AutoMigrate(&model.RuntimeConfig{}); err != nil {
		t.Fatalf("auto-migrate runtime_config: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close migration db: %v", closeErr)
		}
	})

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

func TestMigrateStickyConfig_ValueMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		legacy     string
		wantSticky string
	}{
		{name: "true to api_type", legacy: "true", wantSticky: string(model.StickyModeAPIType)},
		{name: "false to off", legacy: "false", wantSticky: string(model.StickyModeOff)},
		{name: "one to api_type", legacy: "1", wantSticky: string(model.StickyModeAPIType)},
		{name: "zero to off", legacy: "0", wantSticky: string(model.StickyModeOff)},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := setupMigrationTestDB(t)
			seedConfig(t, db, legacyStickyEnabledConfigKey, tt.legacy)

			if err := migrateStickyConfig(db); err != nil {
				t.Fatalf("migrateStickyConfig error: %v", err)
			}

			if got := readConfigValue(t, db, stickyModeConfigKey); got != tt.wantSticky {
				t.Fatalf("sticky_mode = %q, want %q", got, tt.wantSticky)
			}
			assertConfigMissing(t, db, legacyStickyEnabledConfigKey)
		})
	}
}

func TestMigrateStickyConfig_NoLegacyKey(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := migrateStickyConfig(db); err != nil {
		t.Fatalf("migrateStickyConfig error: %v", err)
	}

	assertConfigMissing(t, db, stickyModeConfigKey)
}

func TestMigrateStickyConfig_ExistingStickyModeNotOverwritten(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	seedConfig(t, db, legacyStickyEnabledConfigKey, "true")
	seedConfig(t, db, stickyModeConfigKey, string(model.StickyModeOff))

	if err := migrateStickyConfig(db); err != nil {
		t.Fatalf("migrateStickyConfig error: %v", err)
	}

	if got := readConfigValue(t, db, stickyModeConfigKey); got != string(model.StickyModeOff) {
		t.Fatalf("sticky_mode = %q, want %q", got, string(model.StickyModeOff))
	}
	assertConfigMissing(t, db, legacyStickyEnabledConfigKey)
}

func TestMigrateGlobalMaxAttemptsConfig_RenamesLegacyKey(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	seedConfig(t, db, legacyMaxRetriesConfigKey, "7")

	if err := migrateGlobalMaxAttemptsConfig(db); err != nil {
		t.Fatalf("migrateGlobalMaxAttemptsConfig error: %v", err)
	}

	if got := readConfigValue(t, db, globalMaxAttemptsConfigKey); got != "7" {
		t.Fatalf("global_max_attempts = %q, want %q", got, "7")
	}
	assertConfigMissing(t, db, legacyMaxRetriesConfigKey)
}

func TestMigrateGlobalMaxAttemptsConfig_NoLegacyKey(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := migrateGlobalMaxAttemptsConfig(db); err != nil {
		t.Fatalf("migrateGlobalMaxAttemptsConfig error: %v", err)
	}

	assertConfigMissing(t, db, globalMaxAttemptsConfigKey)
}

func TestMigrateGlobalMaxAttemptsConfig_ExistingCurrentKeyNotOverwritten(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	seedConfig(t, db, legacyMaxRetriesConfigKey, "7")
	seedConfig(t, db, globalMaxAttemptsConfigKey, "4")

	if err := migrateGlobalMaxAttemptsConfig(db); err != nil {
		t.Fatalf("migrateGlobalMaxAttemptsConfig error: %v", err)
	}

	if got := readConfigValue(t, db, globalMaxAttemptsConfigKey); got != "4" {
		t.Fatalf("global_max_attempts = %q, want %q", got, "4")
	}
	assertConfigMissing(t, db, legacyMaxRetriesConfigKey)
}

// setupWebSocketMigrationDB creates a DB with the legacy is_web_socket column
// (simulating GORM auto-naming before the explicit column tag was added).
func setupWebSocketMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "ws_migration.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// Create request_logs with the legacy column name (GORM's default for IsWebSocket).
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_web_socket BOOLEAN DEFAULT 0,
		is_websocket BOOLEAN DEFAULT 0,
		provider_id TEXT DEFAULT '',
		created_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close ws migration db: %v", closeErr)
		}
	})

	return db
}

func TestMigrateWebSocketColumn_CopiesData(t *testing.T) {
	t.Parallel()

	db := setupWebSocketMigrationDB(t)

	// Seed data in the legacy column.
	if err := db.Exec(`INSERT INTO request_logs (is_web_socket, is_websocket, provider_id) VALUES (1, 0, 'p1')`).Error; err != nil {
		t.Fatalf("seed ws log: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_logs (is_web_socket, is_websocket, provider_id) VALUES (0, 0, 'p2')`).Error; err != nil {
		t.Fatalf("seed regular log: %v", err)
	}

	if err := migrateWebSocketColumn(db); err != nil {
		t.Fatalf("migrateWebSocketColumn error: %v", err)
	}

	// Verify the WS row was migrated to the new column.
	var wsCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM request_logs WHERE is_websocket = 1`).Scan(&wsCount).Error; err != nil {
		t.Fatalf("count ws: %v", err)
	}
	if wsCount != 1 {
		t.Errorf("is_websocket=1 count = %d, want 1", wsCount)
	}

	// Verify legacy column was dropped.
	var colCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM pragma_table_info('request_logs') WHERE name = 'is_web_socket'`).Scan(&colCount).Error; err != nil {
		t.Fatalf("check column: %v", err)
	}
	if colCount != 0 {
		t.Error("is_web_socket column should have been dropped")
	}
}

func TestMigrateWebSocketColumn_NoLegacyColumn(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "ws_no_legacy.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// Create table WITHOUT the legacy column.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_websocket BOOLEAN DEFAULT 0
	)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	// Should be a no-op.
	if err := migrateWebSocketColumn(db); err != nil {
		t.Fatalf("migrateWebSocketColumn error: %v", err)
	}
}
