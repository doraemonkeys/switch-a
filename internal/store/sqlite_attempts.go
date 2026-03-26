package store

import (
	"context"
	"fmt"
	"time"

	"switch-a/internal/model"
)

// InsertAttempts inserts multiple provider-attempt records in a batch.
func (s *SQLiteStore) InsertAttempts(ctx context.Context, attempts []model.RequestAttempt) error {
	if len(attempts) == 0 {
		return nil
	}
	if err := s.db.WithContext(ctx).Create(&attempts).Error; err != nil {
		return fmt.Errorf("insert attempts: %w", err)
	}
	return nil
}

// GetAttemptsByRequestID retrieves all provider-attempt records for a request ID.
func (s *SQLiteStore) GetAttemptsByRequestID(ctx context.Context, requestID string) ([]model.RequestAttempt, error) {
	var attempts []model.RequestAttempt
	err := s.db.WithContext(ctx).
		Where("request_id = ?", requestID).
		// WebSocket provider attempts can be recorded by newer orchestration code
		// while older request rows still reuse the shared attempts table. `attempt`
		// remains the primary lifecycle ordering key, and `id` keeps ties stable.
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
