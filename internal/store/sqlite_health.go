package store

import (
	"context"
	"fmt"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/health/state"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func (s *SQLiteStore) GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error) {
	return healthstate.New(s.db, s).GetHealthState(ctx, providerID)
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
	return healthstate.New(s.db, s).UpdateHealthState(ctx, state)
}

// IncrementSuccessCount atomically increments success_count and sets available=true
// (unless manually disabled or auto-disabled with unexpired disabled_until).
// Returns the updated state.
func (s *SQLiteStore) IncrementSuccessCount(ctx context.Context, providerID string, now time.Time) (*model.HealthState, error) {
	return healthstate.New(s.db, s).IncrementSuccessCount(ctx, providerID, now)
}

// IncrementFailCount atomically increments fail_count, sets last_failure and last_error.
// Returns the updated state.
func (s *SQLiteStore) IncrementFailCount(ctx context.Context, providerID string, now time.Time, lastError string) (*model.HealthState, error) {
	return healthstate.New(s.db, s).IncrementFailCount(ctx, providerID, now, lastError)
}

// AutoDisableUntil atomically marks a provider unavailable until the given time.
// This is used by all temporary provider suspensions, including circuit breaking
// and usage-window exhaustion. Manual disables win over automatic state changes,
// and automatic expiries only move later so concurrent failures cannot shrink an
// existing cooldown window.
func (s *SQLiteStore) AutoDisableUntil(ctx context.Context, providerID string, disabledUntil time.Time, reason string) error {
	return healthstate.New(s.db, s).AutoDisableUntil(ctx, providerID, disabledUntil, reason)
}

// AtomicRecoverIfExpired atomically checks if a provider's auto-disable period has expired
// and recovers it in a single SQL operation. This prevents race conditions where concurrent
// calls could overwrite each other's state updates.
// Returns true if recovery was performed, false otherwise.
func (s *SQLiteStore) AtomicRecoverIfExpired(ctx context.Context, providerID string, now time.Time) (bool, error) {
	return healthstate.New(s.db, s).AtomicRecoverIfExpired(ctx, providerID, now)
}

func (s *SQLiteStore) ListHealthStates(ctx context.Context) ([]model.HealthState, error) {
	var states []model.HealthState
	if err := s.db.WithContext(ctx).Find(&states).Error; err != nil {
		return nil, fmt.Errorf("list health states: %w", err)
	}
	return states, nil
}

func (s *SQLiteStore) HealthScope(apiType, transport string) internal.HealthStateStore {
	return healthstate.New(s.db, s).HealthScope(apiType, transport)
}
