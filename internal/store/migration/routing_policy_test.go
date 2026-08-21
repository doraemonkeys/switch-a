package migration

import (
	"database/sql"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestMigrateRoutingPolicyLifecycleStorage_AddsColumnsAndBackfillsEnabled(t *testing.T) {
	db := setupMigrationTestDB(t)

	if err := db.Exec(`DROP TABLE IF EXISTS routing_policies`).Error; err != nil {
		t.Fatalf("drop routing_policies: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE routing_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_type TEXT NOT NULL,
			model_match_type TEXT NOT NULL DEFAULT '',
			model_match_value TEXT NOT NULL DEFAULT '',
			created_at DATETIME,
			updated_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("create legacy routing_policies: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO routing_policies (api_type, model_match_type, model_match_value) VALUES (?, ?, ?)`,
		"codex",
		"exact",
		"gpt-5",
	).Error; err != nil {
		t.Fatalf("insert legacy routing policy: %v", err)
	}

	if err := MigrateRoutingPolicyLifecycleStorage(db); err != nil {
		t.Fatalf("migrateRoutingPolicyLifecycleStorage() error: %v", err)
	}

	hasEnabled, err := tableColumnExists(db, routingPoliciesTableName, routingPolicyEnabledColumn)
	if err != nil {
		t.Fatalf("tableColumnExists(enabled) error = %v", err)
	}
	if !hasEnabled {
		t.Fatal("enabled column missing after migration")
	}
	hasTargetProviderID, err := tableColumnExists(db, routingPoliciesTableName, routingPolicyTargetProviderColumn)
	if err != nil {
		t.Fatalf("tableColumnExists(target_provider_id) error = %v", err)
	}
	if !hasTargetProviderID {
		t.Fatal("target_provider_id column missing after migration")
	}

	var row struct {
		Enabled          bool
		TargetProviderID sql.NullString
	}
	if err := db.Table(routingPoliciesTableName).
		Select("enabled, target_provider_id").
		First(&row).Error; err != nil {
		t.Fatalf("load migrated routing policy: %v", err)
	}
	if !row.Enabled {
		t.Fatal("enabled = false, want backfilled true")
	}
	if row.TargetProviderID.Valid {
		t.Fatalf("target_provider_id = %q, want NULL", row.TargetProviderID.String)
	}
}

func TestMigrateRoutingPolicyLifecycleStorage_BackfillsEnabledAndAddsTargetProvider(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`
		CREATE TABLE routing_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_type TEXT NOT NULL,
			model_match_type TEXT NOT NULL DEFAULT '',
			model_match_value TEXT NOT NULL DEFAULT '',
			created_at DATETIME,
			updated_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("create legacy routing_policies table: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO routing_policies (api_type, model_match_type, model_match_value, created_at, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"codex",
		"",
		"",
	).Error; err != nil {
		t.Fatalf("seed legacy routing policy row: %v", err)
	}

	if err := MigrateRoutingPolicyLifecycleStorage(db); err != nil {
		t.Fatalf("migrateRoutingPolicyLifecycleStorage error: %v", err)
	}

	hasEnabled, err := tableColumnExists(db, routingPoliciesTableName, routingPolicyEnabledColumn)
	if err != nil {
		t.Fatalf("check enabled column: %v", err)
	}
	if !hasEnabled {
		t.Fatal("enabled column was not added")
	}
	hasTargetProviderID, err := tableColumnExists(db, routingPoliciesTableName, routingPolicyTargetProviderColumn)
	if err != nil {
		t.Fatalf("check target_provider_id column: %v", err)
	}
	if !hasTargetProviderID {
		t.Fatal("target_provider_id column was not added")
	}

	var row struct {
		Enabled          bool
		TargetProviderID *string
	}
	if err := db.Table(routingPoliciesTableName).
		Select("enabled, target_provider_id").
		First(&row).Error; err != nil {
		t.Fatalf("read migrated routing policy row: %v", err)
	}
	if !row.Enabled {
		t.Fatal("enabled = false, want true after backfill")
	}
	if row.TargetProviderID != nil {
		t.Fatalf("target_provider_id = %v, want nil", row.TargetProviderID)
	}
}

func TestRoutingPolicySchema_RejectsDuplicateMatchCombination(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.AutoMigrate(&model.RoutingPolicy{}, &model.RoutingPolicyGroup{}, &model.RoutingPolicyVendor{}); err != nil {
		t.Fatalf("auto-migrate routing policy tables: %v", err)
	}

	first := model.RoutingPolicy{
		APIType:         "responses",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5",
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first routing policy: %v", err)
	}

	duplicate := model.RoutingPolicy{
		APIType:         "responses",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5",
	}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate routing policy insert succeeded, want unique constraint failure")
	}
}
