package migration

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestMigrateRequestLogCreatedAtInstantsBackfillsAndQuarantinesRawStorageClasses(t *testing.T) {
	db := openMigrationSQLiteDB(t, "request_log_timestamp_migration.db")
	if err := db.Exec(`CREATE TABLE request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME,
		created_at_unix_nano INTEGER
	)`).Error; err != nil {
		t.Fatalf("create request_logs: %v", err)
	}

	wantInstant := time.Date(2026, time.August, 21, 13, 45, 3, 700000000, time.UTC)
	validValues := []string{
		"2026-08-21T21:45:03.7+08:00",
		"2026-08-21 13:45:03.700Z",
		"2026-08-21T08:45:03.700000000-05:00",
	}
	for _, createdAt := range validValues {
		if err := db.Exec("INSERT INTO request_logs (created_at) VALUES (?)", createdAt).Error; err != nil {
			t.Fatalf("insert %q: %v", createdAt, err)
		}
	}
	if err := db.Exec(`
		INSERT INTO request_logs (created_at) VALUES
			('not-a-timestamp'),
			(42),
			(3.14),
			(X'323032362D30382D3231'),
			('1500-01-01T00:00:00Z')
	`).Error; err != nil {
		t.Fatalf("insert invalid timestamps: %v", err)
	}
	for index := range requestLogInvalidIDSampleSize {
		if err := db.Exec("INSERT INTO request_logs (created_at) VALUES (?)", fmt.Sprintf("bad-timestamp-%d", index)).Error; err != nil {
			t.Fatalf("insert malformed timestamp %d: %v", index, err)
		}
	}
	if err := db.Exec("INSERT INTO request_logs (created_at) VALUES (NULL)").Error; err != nil {
		t.Fatalf("insert NULL timestamp: %v", err)
	}

	report, err := MigrateRequestLogCreatedAtInstants(db)
	if err != nil {
		t.Fatalf("MigrateRequestLogCreatedAtInstants() error = %v", err)
	}
	if report.BackfilledCount != int64(len(validValues)) || report.InvalidCount != int64(5+requestLogInvalidIDSampleSize) {
		t.Fatalf("migration report = %+v", report)
	}
	if len(report.InvalidIDs) != requestLogInvalidIDSampleSize || report.InvalidIDs[0] != 4 || report.InvalidIDs[len(report.InvalidIDs)-1] != 19 {
		t.Fatalf("bounded invalid ID sample = %v", report.InvalidIDs)
	}
	restartReport, err := MigrateRequestLogCreatedAtInstants(db)
	if err != nil {
		t.Fatalf("restart migration error = %v", err)
	}
	if restartReport.BackfilledCount != 0 || restartReport.InvalidCount != 0 || len(restartReport.InvalidIDs) != 0 {
		t.Fatalf("restart report = %+v, want no repeated work", restartReport)
	}

	var got []struct {
		CreatedAt         sql.NullTime
		CreatedAtUnixNano sql.NullInt64
	}
	if err := db.Raw("SELECT created_at, created_at_unix_nano FROM request_logs ORDER BY id").Scan(&got).Error; err != nil {
		t.Fatalf("read backfilled instants: %v", err)
	}
	wantRowCount := len(validValues) + 5 + requestLogInvalidIDSampleSize + 1
	if len(got) != wantRowCount {
		t.Fatalf("backfilled row count = %d, want %d", len(got), wantRowCount)
	}
	for index := range validValues {
		if !got[index].CreatedAt.Valid || !got[index].CreatedAtUnixNano.Valid || got[index].CreatedAtUnixNano.Int64 != wantInstant.UnixNano() {
			t.Errorf("instant[%d] = %+v, want %d", index, got[index], wantInstant.UnixNano())
		}
	}
	for index := len(validValues); index < len(got); index++ {
		if got[index].CreatedAt.Valid || got[index].CreatedAtUnixNano.Valid {
			t.Errorf("invalid instant[%d] = %+v, want terminal NULL representation", index, got[index])
		}
	}
}

func TestMigrateRequestLogCreatedAtInstantsResumesAfterCommittedBatch(t *testing.T) {
	db := openMigrationSQLiteDB(t, "request_log_timestamp_restart.db")
	if err := db.Exec(`CREATE TABLE request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME,
		created_at_unix_nano INTEGER
	)`).Error; err != nil {
		t.Fatalf("create request_logs: %v", err)
	}
	totalRows := requestLogTimestampBackfillSize + 7
	if err := db.Exec(`
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < ?
		)
		INSERT INTO request_logs (created_at)
		SELECT '2026-08-21T13:45:03.123456789Z' FROM sequence
	`, totalRows).Error; err != nil {
		t.Fatalf("seed request logs: %v", err)
	}
	failingID := requestLogTimestampBackfillSize + 1
	if err := db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_second_timestamp_batch
		BEFORE UPDATE OF created_at_unix_nano ON request_logs
		WHEN OLD.id = %d
		BEGIN SELECT RAISE(ABORT, 'injected restart probe'); END
	`, failingID)).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	partial, err := MigrateRequestLogCreatedAtInstants(db)
	if err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	if partial.BackfilledCount != int64(requestLogTimestampBackfillSize) {
		t.Fatalf("partial report = %+v, want one committed batch", partial)
	}
	var persisted int64
	if err := db.Raw("SELECT COUNT(*) FROM request_logs WHERE created_at_unix_nano IS NOT NULL").Scan(&persisted).Error; err != nil {
		t.Fatalf("count committed batch: %v", err)
	}
	if persisted != int64(requestLogTimestampBackfillSize) {
		t.Fatalf("persisted rows = %d, want %d", persisted, requestLogTimestampBackfillSize)
	}
	if err := db.Exec("DROP TRIGGER fail_second_timestamp_batch").Error; err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}

	resumed, err := MigrateRequestLogCreatedAtInstants(db)
	if err != nil {
		t.Fatalf("resumed migration error = %v", err)
	}
	if resumed.BackfilledCount != int64(totalRows-requestLogTimestampBackfillSize) {
		t.Fatalf("resumed report = %+v", resumed)
	}
}
