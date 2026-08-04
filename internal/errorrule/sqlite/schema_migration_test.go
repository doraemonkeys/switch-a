package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrateUpgradesLegacyRetryConstraint(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-rules.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// This fixture intentionally models a database created before the DDL
	// version table existed. CREATE TABLE IF NOT EXISTS alone cannot change its
	// old CHECK constraint, so the migration must rebuild this table in place.
	legacyCreate := strings.Replace(
		createRulesTableStatement(internalErrorRulesTableName, true),
		fmt.Sprintf("BETWEEN 0 AND %d", errorrule.MaxRuleRetries),
		"BETWEEN 0 AND 10",
		1,
	)
	if legacyCreate == createRulesTableStatement(internalErrorRulesTableName, true) {
		t.Fatal("legacy fixture did not retain the pre-v2 retry limit")
	}
	legacyStatements := append([]string{legacyCreate}, schemaStatements[1:5]...)
	for _, statement := range legacyStatements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}

	const (
		legacyRuleID     = "11111111-1111-4111-8111-111111111111"
		legacyGeneration = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	)
	createdAt := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	if err := db.Exec(`
		INSERT INTO internal_error_rules (
			id, generation, name, enabled, target_kind, provider_id, api_type,
			keywords_json, match_mode, action_type, max_retries,
			backoff_initial_delay, backoff_max_delay, backoff_multiplier,
			backoff_jitter, position, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		legacyRuleID, legacyGeneration, "legacy", 1, "global", nil, nil,
		`["error"]`, "any", "retry_only", 10, 0, 0, 0, 0, 0, createdAt, createdAt,
	).Error; err != nil {
		t.Fatalf("seed legacy rule: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO internal_error_rule_stats (rule_id, generation, hit_count, last_hit_at)
		VALUES (?, ?, ?, ?)`, legacyRuleID, legacyGeneration, "7", createdAt).Error; err != nil {
		t.Fatalf("seed legacy stats: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO internal_error_rule_set_meta (id, revision) VALUES (?, ?)",
		metaRowID, 4,
	).Error; err != nil {
		t.Fatalf("seed legacy revision: %v", err)
	}

	usesCurrent, err := rulesTableUsesCurrentRetryLimit(db)
	if err != nil {
		t.Fatal(err)
	}
	if usesCurrent {
		t.Fatal("legacy fixture unexpectedly uses the current retry constraint")
	}

	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	usesCurrent, err = rulesTableUsesCurrentRetryLimit(db)
	if err != nil {
		t.Fatal(err)
	}
	if !usesCurrent {
		t.Fatal("migrated rule table still uses the legacy retry constraint")
	}

	var migratedVersion int
	if err := db.Raw("SELECT version FROM internal_error_schema_meta WHERE id = ?", schemaVersionRowID).Scan(&migratedVersion).Error; err != nil {
		t.Fatal(err)
	}
	if migratedVersion != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", migratedVersion, currentSchemaVersion)
	}

	var preserved struct {
		Name       string
		Generation string
		MaxRetries int
		Position   int
	}
	if err := db.Raw(`
		SELECT name, generation, max_retries, position
		FROM internal_error_rules WHERE id = ?`, legacyRuleID).Scan(&preserved).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.Name != "legacy" || preserved.Generation != legacyGeneration || preserved.MaxRetries != 10 || preserved.Position != 0 {
		t.Fatalf("preserved rule = %#v", preserved)
	}
	var preservedHits string
	if err := db.Raw(`
		SELECT hit_count FROM internal_error_rule_stats
		WHERE rule_id = ? AND generation = ?`, legacyRuleID, legacyGeneration).Scan(&preservedHits).Error; err != nil {
		t.Fatal(err)
	}
	if preservedHits != "7" {
		t.Fatalf("preserved stats hit_count = %q", preservedHits)
	}

	var indexCount int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND tbl_name = ?
		  AND name IN (?, ?)`, internalErrorRulesTableName,
		"idx_internal_error_rules_position", "idx_internal_error_rules_provider").Scan(&indexCount).Error; err != nil {
		t.Fatal(err)
	}
	if indexCount != 2 {
		t.Fatalf("migrated rule indexes = %d, want 2", indexCount)
	}

	repository, err := Open(context.Background(), Config{DB: db})
	if err != nil {
		t.Fatalf("open migrated repository: %v", err)
	}
	maxAction, err := errorrule.NewRetryOnlyAction(errorrule.MaxRuleRetries, model.BackoffPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateRule(context.Background(), 4, errorrule.RuleSpec{
		Name: "new-max", Enabled: true, Target: errorrule.NewGlobalTarget(),
		Keywords: []string{"new"}, MatchMode: errorrule.MatchAny, Action: maxAction,
	})
	if err != nil {
		t.Fatalf("persist max-retry rule after migration: %v", err)
	}
	if created.Revision != 5 || len(created.Rules) != 2 {
		t.Fatalf("created result = revision %s, %d rules", created.Revision, len(created.Rules))
	}
	var persistedMax int
	if err := db.Raw("SELECT max_retries FROM internal_error_rules WHERE name = ?", "new-max").Scan(&persistedMax).Error; err != nil {
		t.Fatal(err)
	}
	if persistedMax != errorrule.MaxRuleRetries {
		t.Fatalf("persisted max_retries = %d, want %d", persistedMax, errorrule.MaxRuleRetries)
	}

	reopened, err := Open(context.Background(), Config{DB: db})
	if err != nil {
		t.Fatalf("reopen migrated repository: %v", err)
	}
	revision, rules := reopened.ListRules()
	if revision != 5 || len(rules) != 2 {
		t.Fatalf("reopened result = revision %s, %d rules", revision, len(rules))
	}
	for _, rule := range rules {
		if rule.Name != "new-max" {
			continue
		}
		policy, ok := rule.Action.RetryPolicy()
		if !ok || policy.MaxRetries != errorrule.MaxRuleRetries {
			t.Fatalf("reopened max rule policy = %#v, ok=%v", policy, ok)
		}
		return
	}
	t.Fatal("reopened max-retry rule was not found")
}
