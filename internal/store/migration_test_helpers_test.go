package store

import (
	"errors"
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

func strPtr(value string) *string {
	return &value
}
