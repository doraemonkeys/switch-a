package sqlite

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"gorm.io/gorm"
)

func (r *Repository) ApplyRuleStatDeltas(
	ctx context.Context,
	deltas []statistics.Delta,
) (statistics.ApplyResult, error) {
	result := statistics.ApplyResult{}
	if len(deltas) == 0 {
		return result, nil
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, delta := range deltas {
			missing, err := applyRuleStatDelta(tx, delta)
			if err != nil {
				return err
			}
			if missing {
				result.Missing = append(result.Missing, delta.Handle)
			}
		}
		return nil
	})
	if err != nil {
		return statistics.ApplyResult{}, err
	}
	return result, nil
}

func applyRuleStatDelta(tx *gorm.DB, delta statistics.Delta) (missing bool, err error) {
	if err := delta.Handle.Validate(); err != nil {
		return false, err
	}
	var row statsRow
	err = tx.Where(
		"rule_id = ? AND generation = ?",
		delta.Handle.RuleID,
		delta.Handle.Generation.String(),
	).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("load stats for rule %q: %w", delta.Handle.RuleID, err)
	}

	current, err := strconv.ParseUint(row.HitCount, 10, 64)
	if err != nil || strconv.FormatUint(current, 10) != row.HitCount {
		return false, fmt.Errorf("rule %q has invalid persisted hit count", delta.Handle.RuleID)
	}
	next := current
	if math.MaxUint64-current < delta.HitCount {
		next = math.MaxUint64
	} else {
		next += delta.HitCount
	}
	lastHitAt := delta.LastHitAt.UTC()
	if row.LastHitAt != nil && row.LastHitAt.After(lastHitAt) {
		lastHitAt = row.LastHitAt.UTC()
	}
	updated := tx.Model(&statsRow{}).
		Where("rule_id = ? AND generation = ?", row.RuleID, row.Generation).
		Updates(map[string]any{
			"hit_count":   strconv.FormatUint(next, 10),
			"last_hit_at": lastHitAt,
		})
	if updated.Error != nil {
		return false, fmt.Errorf("update stats for rule %q: %w", delta.Handle.RuleID, updated.Error)
	}
	return updated.RowsAffected == 0, nil
}

func (r *Repository) ListStats(ctx context.Context) ([]errorrule.RuleStats, error) {
	_, stats, err := r.ListStatsSnapshot(ctx)
	return stats, err
}

// ListStatsSnapshot reads the persisted revision and its ordered stats through
// one transaction and one joined statement. Admin callers can therefore reject
// a commit-to-publication window without ever pairing rows from one revision
// with the immutable snapshot from another.
func (r *Repository) ListStatsSnapshot(
	ctx context.Context,
) (errorrule.Revision, []errorrule.RuleStats, error) {
	type snapshotStatsRow struct {
		Revision  int64      `gorm:"column:revision"`
		RuleID    *string    `gorm:"column:rule_id"`
		HitCount  *string    `gorm:"column:hit_count"`
		LastHitAt *time.Time `gorm:"column:last_hit_at"`
	}
	var rows []snapshotStatsRow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Raw(`
			SELECT meta.revision, rules.id AS rule_id, stats.hit_count, stats.last_hit_at
			FROM internal_error_rule_set_meta AS meta
			LEFT JOIN internal_error_rules AS rules ON 1 = 1
			LEFT JOIN internal_error_rule_stats AS stats
			  ON stats.rule_id = rules.id AND stats.generation = rules.generation
			WHERE meta.id = ?
			ORDER BY rules.position ASC
		`, metaRowID).Scan(&rows).Error
	})
	if err != nil {
		return 0, nil, fmt.Errorf("read internal-error stats snapshot: %w", err)
	}
	if len(rows) == 0 || rows[0].Revision < 0 {
		return 0, nil, fmt.Errorf("internal-error stats snapshot has no valid revision")
	}
	revision := errorrule.Revision(rows[0].Revision)
	result := make([]errorrule.RuleStats, 0, len(rows))
	for _, row := range rows {
		if row.Revision != rows[0].Revision {
			return 0, nil, fmt.Errorf("internal-error stats snapshot contains mixed revisions")
		}
		if row.RuleID == nil {
			continue
		}
		if row.HitCount == nil {
			return 0, nil, fmt.Errorf("rule %q has no persisted stats row", *row.RuleID)
		}
		hitCount, err := strconv.ParseUint(*row.HitCount, 10, 64)
		if err != nil || strconv.FormatUint(hitCount, 10) != *row.HitCount {
			return 0, nil, fmt.Errorf("rule %q has invalid persisted hit count", *row.RuleID)
		}
		if row.LastHitAt != nil {
			utc := row.LastHitAt.UTC()
			row.LastHitAt = &utc
		}
		result = append(result, errorrule.RuleStats{
			RuleID:    errorrule.RuleID(*row.RuleID),
			HitCount:  hitCount,
			LastHitAt: row.LastHitAt,
		})
	}
	return revision, result, nil
}
