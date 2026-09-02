package store

import (
	"path/filepath"
	"testing"

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

func strPtr(value string) *string {
	return &value
}
