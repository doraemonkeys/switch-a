// Package sqlite persists immutable internal-error rule snapshots and their
// generation-qualified statistics in SQLite.
package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"gorm.io/gorm"
)

const (
	metaRowID = 1

	// The schema metadata is separate from the rule-set revision: revision is
	// user-visible configuration state, while this version tracks DDL upgrades.
	schemaVersionRowID                 = 1
	initialSchemaVersion               = 1
	currentSchemaVersion               = 2
	internalErrorRulesTableName        = "internal_error_rules"
	internalErrorRulesUpgradeTableName = "internal_error_rules_upgrade"
	internalErrorSchemaMetaTableName   = "internal_error_schema_meta"
)

const internalErrorRulesTableDefinition = `(
		id TEXT PRIMARY KEY,
		generation TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		target_kind TEXT NOT NULL CHECK (target_kind IN ('global', 'provider')),
		provider_id TEXT NULL,
		api_type TEXT NULL,
		keywords_json TEXT NOT NULL,
		match_mode TEXT NOT NULL CHECK (match_mode IN ('any', 'all')),
		action_type TEXT NOT NULL CHECK (action_type IN ('passthrough', 'retry_only', 'retry_then_switch')),
		max_retries INTEGER NULL,
		backoff_initial_delay INTEGER NULL,
		backoff_max_delay INTEGER NULL,
		backoff_multiplier REAL NULL,
		backoff_jitter INTEGER NULL,
		position INTEGER NOT NULL CHECK (position >= 0),
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		CHECK (
			(target_kind = 'global' AND provider_id IS NULL) OR
			(target_kind = 'provider' AND provider_id IS NOT NULL AND length(provider_id) > 0)
		),
		CHECK (
			(action_type = 'passthrough' AND max_retries IS NULL AND backoff_initial_delay IS NULL
				AND backoff_max_delay IS NULL AND backoff_multiplier IS NULL AND backoff_jitter IS NULL) OR
			(action_type IN ('retry_only', 'retry_then_switch') AND max_retries BETWEEN 0 AND %d
				AND backoff_initial_delay IS NOT NULL AND backoff_initial_delay >= 0
				AND backoff_max_delay IS NOT NULL AND backoff_max_delay >= 0
				AND backoff_multiplier IS NOT NULL AND backoff_multiplier >= 0
				AND backoff_jitter IN (0, 1))
		)
	)`

func createRulesTableStatement(tableName string, ifNotExists bool) string {
	ifNotExistsClause := ""
	if ifNotExists {
		ifNotExistsClause = " IF NOT EXISTS"
	}
	return fmt.Sprintf("CREATE TABLE%s %s "+internalErrorRulesTableDefinition, ifNotExistsClause, tableName, errorrule.MaxRuleRetries)
}

var schemaStatements = []string{
	createRulesTableStatement(internalErrorRulesTableName, true),
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_internal_error_rules_position ON internal_error_rules(position)`,
	`CREATE INDEX IF NOT EXISTS idx_internal_error_rules_provider ON internal_error_rules(provider_id)`,
	`CREATE TABLE IF NOT EXISTS internal_error_rule_stats (
		rule_id TEXT NOT NULL,
		generation TEXT NOT NULL,
		hit_count TEXT NOT NULL DEFAULT '0' CHECK (
			hit_count <> '' AND hit_count NOT GLOB '*[^0-9]*'
			AND (hit_count = '0' OR substr(hit_count, 1, 1) <> '0')
		),
		last_hit_at DATETIME NULL,
		PRIMARY KEY (rule_id, generation)
	)`,
	`CREATE TABLE IF NOT EXISTS internal_error_rule_set_meta (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		revision INTEGER NOT NULL CHECK (revision >= 0)
	)`,
	fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		version INTEGER NOT NULL CHECK (version >= 1)
	)`, internalErrorSchemaMetaTableName),
}

func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("rule repository database is required")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, statement := range schemaStatements {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("migrate internal-error schema: %w", err)
			}
		}
		if err := migrateSchemaVersion(tx); err != nil {
			return fmt.Errorf("migrate internal-error schema version: %w", err)
		}
		if err := tx.Exec(
			"INSERT OR IGNORE INTO internal_error_rule_set_meta (id, revision) VALUES (?, 0)",
			metaRowID,
		).Error; err != nil {
			return fmt.Errorf("initialize internal-error revision: %w", err)
		}
		return nil
	})
}

func migrateSchemaVersion(tx *gorm.DB) error {
	version, err := readSchemaVersion(tx)
	if err != nil {
		return err
	}
	usesCurrentConstraint, err := rulesTableUsesCurrentRetryLimit(tx)
	if err != nil {
		return err
	}
	if version == 0 {
		version = initialSchemaVersion
		if usesCurrentConstraint {
			version = currentSchemaVersion
		}
		if err := tx.Exec(
			fmt.Sprintf("INSERT INTO %s (id, version) VALUES (?, ?)", internalErrorSchemaMetaTableName),
			schemaVersionRowID, version,
		).Error; err != nil {
			return fmt.Errorf("initialize schema version: %w", err)
		}
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("schema version %d is newer than supported version %d", version, currentSchemaVersion)
	}
	if version < currentSchemaVersion || !usesCurrentConstraint {
		if err := rebuildRulesTable(tx); err != nil {
			return err
		}
		version = currentSchemaVersion
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("schema migration stopped at unsupported version %d", version)
	}
	if err := tx.Exec(
		fmt.Sprintf("UPDATE %s SET version = ? WHERE id = ?", internalErrorSchemaMetaTableName),
		version, schemaVersionRowID,
	).Error; err != nil {
		return fmt.Errorf("publish schema version: %w", err)
	}
	return nil
}

func readSchemaVersion(tx *gorm.DB) (int, error) {
	var version int
	if err := tx.Raw(
		fmt.Sprintf("SELECT COALESCE((SELECT version FROM %s WHERE id = ?), 0)", internalErrorSchemaMetaTableName),
		schemaVersionRowID,
	).Scan(&version).Error; err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func rulesTableUsesCurrentRetryLimit(tx *gorm.DB) (bool, error) {
	var createSQL string
	if err := tx.Raw(
		"SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = ?",
		internalErrorRulesTableName,
	).Scan(&createSQL).Error; err != nil {
		return false, fmt.Errorf("inspect rule table schema: %w", err)
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(createSQL), " "))
	want := fmt.Sprintf("max_retries between 0 and %d", errorrule.MaxRuleRetries)
	return strings.Contains(normalized, want), nil
}

func rebuildRulesTable(tx *gorm.DB) error {
	if err := tx.Exec("DROP TABLE IF EXISTS " + internalErrorRulesUpgradeTableName).Error; err != nil {
		return fmt.Errorf("clear rule-table upgrade scratch table: %w", err)
	}
	if err := tx.Exec(createRulesTableStatement(internalErrorRulesUpgradeTableName, false)).Error; err != nil {
		return fmt.Errorf("create upgraded rule table: %w", err)
	}
	const columns = "id, generation, name, enabled, target_kind, provider_id, api_type, keywords_json, match_mode, action_type, max_retries, backoff_initial_delay, backoff_max_delay, backoff_multiplier, backoff_jitter, position, created_at, updated_at"
	if err := tx.Exec(
		fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", internalErrorRulesUpgradeTableName, columns, columns, internalErrorRulesTableName),
	).Error; err != nil {
		return fmt.Errorf("copy rules into upgraded table: %w", err)
	}
	for _, index := range []string{"idx_internal_error_rules_position", "idx_internal_error_rules_provider"} {
		if err := tx.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
			return fmt.Errorf("drop old rule index %s: %w", index, err)
		}
	}
	if err := tx.Exec("DROP TABLE " + internalErrorRulesTableName).Error; err != nil {
		return fmt.Errorf("drop old rule table: %w", err)
	}
	if err := tx.Exec("ALTER TABLE " + internalErrorRulesUpgradeTableName + " RENAME TO " + internalErrorRulesTableName).Error; err != nil {
		return fmt.Errorf("rename upgraded rule table: %w", err)
	}
	for _, statement := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_internal_error_rules_position ON internal_error_rules(position)",
		"CREATE INDEX IF NOT EXISTS idx_internal_error_rules_provider ON internal_error_rules(provider_id)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("recreate rule index: %w", err)
		}
	}
	return nil
}

type ruleRow struct {
	ID                  string    `gorm:"column:id;primaryKey"`
	Generation          string    `gorm:"column:generation"`
	Name                string    `gorm:"column:name"`
	Enabled             bool      `gorm:"column:enabled"`
	TargetKind          string    `gorm:"column:target_kind"`
	ProviderID          *string   `gorm:"column:provider_id"`
	APIType             *string   `gorm:"column:api_type"`
	KeywordsJSON        string    `gorm:"column:keywords_json"`
	MatchMode           string    `gorm:"column:match_mode"`
	ActionType          string    `gorm:"column:action_type"`
	MaxRetries          *int      `gorm:"column:max_retries"`
	BackoffInitialDelay *int64    `gorm:"column:backoff_initial_delay"`
	BackoffMaxDelay     *int64    `gorm:"column:backoff_max_delay"`
	BackoffMultiplier   *float64  `gorm:"column:backoff_multiplier"`
	BackoffJitter       *bool     `gorm:"column:backoff_jitter"`
	Position            int64     `gorm:"column:position"`
	CreatedAt           timeValue `gorm:"column:created_at"`
	UpdatedAt           timeValue `gorm:"column:updated_at"`
}

func (ruleRow) TableName() string { return "internal_error_rules" }

// timeValue is an alias rather than a wrapper so GORM keeps its native SQLite
// timestamp codec while row conversion remains explicit.
type timeValue = time.Time

type statsRow struct {
	RuleID     string     `gorm:"column:rule_id;primaryKey"`
	Generation string     `gorm:"column:generation;primaryKey"`
	HitCount   string     `gorm:"column:hit_count"`
	LastHitAt  *time.Time `gorm:"column:last_hit_at"`
}

func (statsRow) TableName() string { return "internal_error_rule_stats" }
