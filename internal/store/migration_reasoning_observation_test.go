package store

import (
	"database/sql"
	"testing"

	"switch-a/internal/model"
)

func TestRequestLogAutoMigrateAddsReasoningObservationColumns(t *testing.T) {
	t.Parallel()

	db := openMigrationSQLiteDB(t, "reasoning_observation_migration.db")
	if err := db.Exec(`CREATE TABLE request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT,
		provider_id TEXT,
		created_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create existing request_logs table: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_logs (request_id, provider_id, created_at)
		VALUES ('legacy-request', 'legacy-provider', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("seed existing request log: %v", err)
	}

	if err := db.AutoMigrate(&model.RequestLog{}); err != nil {
		t.Fatalf("auto-migrate request_logs: %v", err)
	}

	for _, column := range []string{
		"reasoning_observation_state",
		"reasoning_effort",
		"reasoning_mode",
		"reasoning_budget_tokens",
	} {
		if !db.Migrator().HasColumn(&model.RequestLog{}, column) {
			t.Errorf("request_logs missing column %q", column)
		}
	}

	var row struct {
		RequestID string
		State     sql.NullString `gorm:"column:reasoning_observation_state"`
		Effort    sql.NullString `gorm:"column:reasoning_effort"`
		Mode      sql.NullString `gorm:"column:reasoning_mode"`
		Budget    sql.NullInt64  `gorm:"column:reasoning_budget_tokens"`
	}
	if err := db.Table("request_logs").Where("request_id = ?", "legacy-request").Take(&row).Error; err != nil {
		t.Fatalf("read migrated request log: %v", err)
	}
	if row.RequestID != "legacy-request" {
		t.Fatalf("RequestID = %q, want legacy-request", row.RequestID)
	}
	if row.State.Valid || row.Effort.Valid || row.Mode.Valid || row.Budget.Valid {
		t.Fatalf("legacy reasoning fields = %+v, want all NULL", row)
	}
}
