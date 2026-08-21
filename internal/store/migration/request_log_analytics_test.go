package migration

import (
	"fmt"
	"slices"
	"testing"

	"gorm.io/gorm"
)

func TestMigrateRequestLogAnalyticsIndexesResumesAtFirstIncompleteStage(t *testing.T) {
	db := openMigrationSQLiteDB(t, "request_log_index_restart.db")
	if err := db.Exec(`CREATE TABLE request_logs (
		id INTEGER PRIMARY KEY,
		provider_id TEXT,
		model TEXT,
		api_type TEXT,
		created_at DATETIME,
		created_at_unix_nano INTEGER
	)`).Error; err != nil {
		t.Fatalf("create request_logs: %v", err)
	}
	legacyColumns := []string{"provider_id", "model", "api_type"}
	for index, name := range legacyRequestLogAnalyticsIndexes {
		if err := db.Exec(fmt.Sprintf("CREATE INDEX %s ON request_logs (%s, created_at)", name, legacyColumns[index])).Error; err != nil {
			t.Fatalf("create legacy index %s: %v", name, err)
		}
	}
	if err := db.Exec("CREATE INDEX " + requestLogAPITypeCreatedAtUnixNanoIndex + " ON request_logs (api_type)").Error; err != nil {
		t.Fatalf("create malformed final index: %v", err)
	}

	if err := MigrateRequestLogAnalyticsIndexes(db); err == nil {
		t.Fatal("migration unexpectedly accepted malformed final index")
	}
	for _, index := range requestLogAnalyticsIndexes[:3] {
		assertRequestLogIndexColumns(t, db, index.name, index.columns)
	}
	for _, legacyName := range legacyRequestLogAnalyticsIndexes[:2] {
		if db.Migrator().HasIndex(requestLogsTableName, legacyName) {
			t.Errorf("completed stage retained legacy index %s", legacyName)
		}
	}
	if !db.Migrator().HasIndex(requestLogsTableName, legacyRequestLogAnalyticsIndexes[2]) {
		t.Fatal("incomplete stage dropped its legacy index")
	}

	if err := db.Exec("DROP INDEX " + requestLogAPITypeCreatedAtUnixNanoIndex).Error; err != nil {
		t.Fatalf("drop malformed index: %v", err)
	}
	if err := MigrateRequestLogAnalyticsIndexes(db); err != nil {
		t.Fatalf("resumed migration error = %v", err)
	}
	for _, index := range requestLogAnalyticsIndexes {
		assertRequestLogIndexColumns(t, db, index.name, index.columns)
	}
	for _, legacyName := range legacyRequestLogAnalyticsIndexes {
		if db.Migrator().HasIndex(requestLogsTableName, legacyName) {
			t.Errorf("resumed migration retained legacy index %s", legacyName)
		}
	}
}

func assertRequestLogIndexColumns(t *testing.T, db *gorm.DB, indexName string, want []string) {
	t.Helper()
	var got []string
	if err := db.Raw("SELECT name FROM pragma_index_info(?) ORDER BY seqno", indexName).Scan(&got).Error; err != nil {
		t.Fatalf("read index %s: %v", indexName, err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("index %s columns = %v, want %v", indexName, got, want)
	}
}
