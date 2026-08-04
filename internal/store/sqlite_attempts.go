package store

import (
	"context"
	"fmt"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"gorm.io/gorm"
)

// InsertAttempts inserts multiple provider-attempt records in a batch.
func (s *SQLiteStore) InsertAttempts(ctx context.Context, attempts []model.RequestAttempt) error {
	if len(attempts) == 0 {
		return nil
	}

	// GORM represents default-tagged fields that vary between batch rows with
	// DEFAULT inside VALUES tuples, which SQLite does not support. Separate
	// statements avoid that dialect mismatch; the transaction keeps the caller's
	// batch atomic if any later row fails.
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range attempts {
			if err := tx.Create(&attempts[i]).Error; err != nil {
				return fmt.Errorf(
					"insert attempt row %d/%d for request %q: %w",
					i+1,
					len(attempts),
					attempts[i].RequestID,
					err,
				)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("insert attempts: %w", err)
	}
	return nil
}

// GetAttemptsByRequestID retrieves all provider-attempt records for a request ID.
func (s *SQLiteStore) GetAttemptsByRequestID(ctx context.Context, requestID string) ([]model.RequestAttempt, error) {
	var attempts []model.RequestAttempt
	err := s.db.WithContext(ctx).
		Where("request_id = ?", requestID).
		// `attempt` remains the primary lifecycle ordering key even after adding
		// switch-mode and continuity provenance columns; `id` keeps ties stable so
		// new metadata never changes the timeline order users already rely on.
		Order("attempt ASC, id ASC").
		Find(&attempts).Error
	if err != nil {
		return nil, fmt.Errorf("get attempts by request id %s: %w", requestID, err)
	}
	return attempts, nil
}

// CleanOldAttempts removes attempts older than the specified time.
// Returns the number of deleted records.
func (s *SQLiteStore) CleanOldAttempts(ctx context.Context, before time.Time) (int64, error) {
	result := s.db.WithContext(ctx).
		Where("created_at < ?", before).
		Delete(&model.RequestAttempt{})
	return result.RowsAffected, result.Error
}
