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
