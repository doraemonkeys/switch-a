// Package sqlite persists immutable internal-error rule snapshots and their
// generation-qualified statistics in SQLite.
package sqlite

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const metaRowID = 1

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS internal_error_rules (
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
			(action_type IN ('retry_only', 'retry_then_switch') AND max_retries BETWEEN 0 AND 10
				AND backoff_initial_delay IS NOT NULL AND backoff_initial_delay >= 0
				AND backoff_max_delay IS NOT NULL AND backoff_max_delay >= 0
				AND backoff_multiplier IS NOT NULL AND backoff_multiplier >= 0
				AND backoff_jitter IN (0, 1))
		)
	)`,
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
		if err := tx.Exec(
			"INSERT OR IGNORE INTO internal_error_rule_set_meta (id, revision) VALUES (?, 0)",
			metaRowID,
		).Error; err != nil {
			return fmt.Errorf("initialize internal-error revision: %w", err)
		}
		return nil
	})
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
