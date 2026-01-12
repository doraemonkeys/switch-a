package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

// Health state operations

func (s *SQLiteStore) GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error) {
	var state model.HealthState
	err := s.db.WithContext(ctx).First(&state, "provider_id = ?", providerID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Return default available state if not found
		return &model.HealthState{
			ProviderID: providerID,
			Available:  true,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get health state for provider %q: %w", providerID, err)
	}
	return &state, nil
}

// GetHealthStatesByProviderIDs fetches health states for multiple providers in a single query.
// Returns a map of provider ID to health state. For providers without stored state,
// returns a default available state.
func (s *SQLiteStore) GetHealthStatesByProviderIDs(ctx context.Context, providerIDs []string) (map[string]*model.HealthState, error) {
	if len(providerIDs) == 0 {
		return make(map[string]*model.HealthState), nil
	}

	var states []model.HealthState
	err := s.db.WithContext(ctx).Where("provider_id IN ?", providerIDs).Find(&states).Error
	if err != nil {
		return nil, fmt.Errorf("get health states by provider IDs: %w", err)
	}

	// Build result map with fetched states
	result := make(map[string]*model.HealthState, len(providerIDs))
	for i := range states {
		result[states[i].ProviderID] = &states[i]
	}

	// Add default available state for providers not found in the database
	for _, id := range providerIDs {
		if _, ok := result[id]; !ok {
			result[id] = &model.HealthState{
				ProviderID: id,
				Available:  true,
			}
		}
	}

	return result, nil
}

func (s *SQLiteStore) UpdateHealthState(ctx context.Context, state *model.HealthState) error {
	// Use raw SQL upsert to properly handle zero-value boolean fields
	err := s.db.WithContext(ctx).Exec(`
		INSERT INTO health_states (provider_id, available, success_count, fail_count, last_success, last_failure, last_error, disabled_until, disabled_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id) DO UPDATE SET
			available = excluded.available,
			success_count = excluded.success_count,
			fail_count = excluded.fail_count,
			last_success = excluded.last_success,
			last_failure = excluded.last_failure,
			last_error = excluded.last_error,
			disabled_until = excluded.disabled_until,
			disabled_reason = excluded.disabled_reason
	`, state.ProviderID, state.Available, state.SuccessCount, state.FailCount,
		state.LastSuccess, state.LastFailure, state.LastError, state.DisabledUntil, state.DisabledReason).Error
	if err != nil {
		return fmt.Errorf("update health state for provider %q: %w", state.ProviderID, err)
	}
	return nil
}

// IncrementSuccessCount atomically increments success_count and sets available=true
// (unless manually disabled or auto-disabled with unexpired disabled_until).
// Returns the updated state.
func (s *SQLiteStore) IncrementSuccessCount(ctx context.Context, providerID string, now time.Time) (*model.HealthState, error) {
	// Use atomic SQL UPDATE to prevent race conditions.
	// The CASE expression preserves:
	// - manual disable state (disabled_reason LIKE 'manual:%')
	// - auto-disable state when disabled_until has not expired yet
	err := s.db.WithContext(ctx).Exec(`
		INSERT INTO health_states (provider_id, available, success_count, fail_count, last_success, last_failure, last_error, disabled_until, disabled_reason)
		VALUES (?, true, 1, 0, ?, NULL, '', NULL, '')
		ON CONFLICT(provider_id) DO UPDATE SET
			success_count = health_states.success_count + 1,
			last_success = excluded.last_success,
			available = CASE 
				WHEN health_states.disabled_reason LIKE 'manual:%' THEN health_states.available 
				WHEN health_states.disabled_reason LIKE 'auto:%' AND health_states.disabled_until > ? THEN health_states.available
				ELSE true 
			END
	`, providerID, now, now).Error
	if err != nil {
		return nil, fmt.Errorf("increment success count for provider %q: %w", providerID, err)
	}
	return s.GetHealthState(ctx, providerID)
}

// IncrementFailCount atomically increments fail_count, sets last_failure and last_error.
// Returns the updated state.
func (s *SQLiteStore) IncrementFailCount(ctx context.Context, providerID string, now time.Time, lastError string) (*model.HealthState, error) {
	// Use atomic SQL UPDATE to prevent race conditions.
	err := s.db.WithContext(ctx).Exec(`
		INSERT INTO health_states (provider_id, available, success_count, fail_count, last_success, last_failure, last_error, disabled_until, disabled_reason)
		VALUES (?, true, 0, 1, NULL, ?, ?, NULL, '')
		ON CONFLICT(provider_id) DO UPDATE SET
			fail_count = health_states.fail_count + 1,
			last_failure = excluded.last_failure,
			last_error = excluded.last_error
	`, providerID, now, lastError).Error
	if err != nil {
		return nil, fmt.Errorf("increment fail count for provider %q: %w", providerID, err)
	}
	return s.GetHealthState(ctx, providerID)
}

// TriggerCircuitBreaker atomically sets available=false, disabled_until, and disabled_reason.
// This is designed to not be overwritten by concurrent MarkSuccess calls.
func (s *SQLiteStore) TriggerCircuitBreaker(ctx context.Context, providerID string, disabledUntil time.Time, reason string) error {
	// Use atomic SQL UPDATE to set circuit breaker state.
	// This is called after IncrementFailCount, so the row should already exist.
	err := s.db.WithContext(ctx).Exec(`
		UPDATE health_states 
		SET available = false,
			disabled_until = ?,
			disabled_reason = ?
		WHERE provider_id = ?
	`, disabledUntil, reason, providerID).Error
	if err != nil {
		return fmt.Errorf("trigger circuit breaker for provider %q: %w", providerID, err)
	}
	return nil
}

// AtomicRecoverIfExpired atomically checks if a provider's auto-disable period has expired
// and recovers it in a single SQL operation. This prevents race conditions where concurrent
// calls could overwrite each other's state updates.
// Returns true if recovery was performed, false otherwise.
func (s *SQLiteStore) AtomicRecoverIfExpired(ctx context.Context, providerID string, now time.Time) (bool, error) {
	// Use atomic SQL UPDATE with WHERE clause that checks the expiration condition.
	// Only auto-disabled providers (with disabled_until set and available=false) are recovered.
	// Manual disables (disabled_reason LIKE 'manual:%') are excluded.
	result := s.db.WithContext(ctx).Exec(`
		UPDATE health_states 
		SET available = true,
			disabled_until = NULL,
			disabled_reason = ''
		WHERE provider_id = ?
			AND available = false
			AND disabled_until IS NOT NULL
			AND disabled_until < ?
			AND disabled_reason NOT LIKE 'manual:%'
	`, providerID, now)
	if result.Error != nil {
		return false, fmt.Errorf("atomic recover for provider %q: %w", providerID, result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (s *SQLiteStore) ListHealthStates(ctx context.Context) ([]model.HealthState, error) {
	var states []model.HealthState
	if err := s.db.WithContext(ctx).Find(&states).Error; err != nil {
		return nil, fmt.Errorf("list health states: %w", err)
	}
	return states, nil
}
