package health

import (
	"context"
	"strconv"
	"time"

	"switch-a/internal"
	"switch-a/internal/defaults"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Compile-time interface check.
var _ internal.HealthManager = (*Manager)(nil)

// Default values - derived from centralized defaults package.
const (
	DefaultCircuitFailure = defaults.CircuitFailure
	DefaultCircuitWindow  = defaults.CircuitWindowSec
	DefaultCircuitDisable = defaults.CircuitDisableSec
)

// DisabledReason prefixes for distinguishing automatic vs manual disables.
const (
	DisabledReasonPrefixAuto   = "auto: "
	DisabledReasonPrefixManual = "manual: "
)

// Reason strings for disabled state.
const (
	ReasonCircuitBreakerTriggered = "circuit breaker triggered"
)

// Store defines the minimal storage interface needed by the health manager.
type Store interface {
	GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error)
	UpdateHealthState(ctx context.Context, state *model.HealthState) error
	GetConfig(ctx context.Context, key string) (string, error)
	// IncrementSuccessCount atomically increments success_count and sets available=true
	// (unless manually disabled). Returns the updated state.
	IncrementSuccessCount(ctx context.Context, providerID string, now time.Time) (*model.HealthState, error)
	// IncrementFailCount atomically increments fail_count, sets last_failure and last_error.
	// Returns the updated state.
	IncrementFailCount(ctx context.Context, providerID string, now time.Time, lastError string) (*model.HealthState, error)
	// TriggerCircuitBreaker atomically sets available=false, disabled_until, and disabled_reason.
	TriggerCircuitBreaker(ctx context.Context, providerID string, disabledUntil time.Time, reason string) error
	// AtomicRecoverIfExpired atomically checks if a provider's auto-disable period has expired
	// and recovers it. Returns true if recovery was performed, false otherwise.
	AtomicRecoverIfExpired(ctx context.Context, providerID string, now time.Time) (bool, error)
}

// Config holds health manager configuration.
type Config struct {
	Store  Store
	Clock  internal.Clock
	Logger *zap.Logger
}

// Manager manages provider health status and circuit breaking.
type Manager struct {
	store   Store
	circuit *CircuitBreaker
	clock   internal.Clock
	logger  *zap.Logger
}

// NewManager creates a new health manager.
func NewManager(cfg Config) *Manager {
	return &Manager{
		store:   cfg.Store,
		circuit: NewCircuitBreaker(cfg.Clock),
		clock:   cfg.Clock,
		logger:  cfg.Logger,
	}
}

// StartCleanupLoop starts a background goroutine that periodically cleans up
// old failure records in the circuit breaker to prevent memory leaks.
// Returns a stop function to terminate the cleanup loop.
// Example: stop := mgr.StartCleanupLoop(5 * time.Minute, 10 * time.Minute); defer stop()
func (m *Manager) StartCleanupLoop(interval, maxAge time.Duration) (stop func()) {
	return m.circuit.StartCleanupLoop(interval, maxAge)
}

// MarkSuccess marks a successful request for the provider.
func (m *Manager) MarkSuccess(ctx context.Context, providerID string) {
	now := m.clock.Now()

	// Use atomic increment to avoid race conditions.
	// This prevents lost updates when concurrent requests complete simultaneously.
	_, err := m.store.IncrementSuccessCount(ctx, providerID, now)
	if err != nil {
		m.logger.Error("failed to increment success count", zap.String("provider_id", providerID), zap.Error(err))
		return
	}

	// Reset circuit breaker on success
	m.circuit.Reset(providerID)
}

// MarkFailure marks a failed request, returns true if circuit breaker triggered.
func (m *Manager) MarkFailure(ctx context.Context, providerID string, err error) bool {
	now := m.clock.Now()

	// Extract error message
	var lastError string
	if err != nil {
		lastError = err.Error()
	}

	// Use atomic increment to avoid race conditions.
	// This prevents lost updates when concurrent requests fail simultaneously.
	_, stateErr := m.store.IncrementFailCount(ctx, providerID, now, lastError)
	if stateErr != nil {
		m.logger.Error("failed to increment fail count", zap.String("provider_id", providerID), zap.Error(stateErr))
		return false
	}

	// Get circuit breaker config
	circuitFailure := m.getConfigInt(ctx, "circuit_failure", DefaultCircuitFailure)
	circuitWindow := time.Duration(m.getConfigInt(ctx, "circuit_window", DefaultCircuitWindow)) * time.Second
	circuitDisable := time.Duration(m.getConfigInt(ctx, "circuit_disable", DefaultCircuitDisable)) * time.Second

	// Record failure in in-memory circuit breaker and check if threshold reached
	triggered := m.circuit.RecordFailure(providerID, circuitWindow, circuitFailure)
	if triggered {
		disableUntil := now.Add(circuitDisable)
		reason := DisabledReasonPrefixAuto + ReasonCircuitBreakerTriggered

		// Use atomic update to set circuit breaker state.
		// This prevents a concurrent MarkSuccess from overwriting the disabled state.
		if triggerErr := m.store.TriggerCircuitBreaker(ctx, providerID, disableUntil, reason); triggerErr != nil {
			m.logger.Error("failed to trigger circuit breaker", zap.String("provider_id", providerID), zap.Error(triggerErr))
		}

		m.logger.Warn("circuit breaker triggered",
			zap.String("provider_id", providerID),
			zap.Int("threshold", circuitFailure),
			zap.Duration("disable_duration", circuitDisable),
		)
	}

	return triggered
}

// RecoverIfExpired checks if a provider's auto-disable period has expired
// and performs recovery if so. This method has side effects - it updates
// the provider's health state and resets the circuit breaker.
// Returns true if recovery was performed, false otherwise.
//
// Uses atomic database operation to prevent race conditions where concurrent
// calls could overwrite each other's state updates.
func (m *Manager) RecoverIfExpired(ctx context.Context, providerID string) bool {
	now := m.clock.Now()

	// Use atomic check-and-update to prevent race conditions.
	// This prevents lost updates when concurrent requests call RecoverIfExpired simultaneously.
	recovered, err := m.store.AtomicRecoverIfExpired(ctx, providerID, now)
	if err != nil {
		m.logger.Error("failed to recover provider", zap.String("provider_id", providerID), zap.Error(err))
		return false
	}

	if recovered {
		// Reset in-memory circuit breaker state
		m.circuit.Reset(providerID)
		m.logger.Info("provider auto-recovered", zap.String("provider_id", providerID))
	}

	return recovered
}

// IsAvailable checks if the provider is currently available.
// This is a pure query with no side effects.
// Call RecoverIfExpired first if you want to trigger auto-recovery for expired providers.
func (m *Manager) IsAvailable(ctx context.Context, providerID string) bool {
	state, err := m.store.GetHealthState(ctx, providerID)
	if err != nil {
		m.logger.Error("failed to get health state", zap.String("provider_id", providerID), zap.Error(err))
		return false // Fail safe: treat as unavailable if we can't check
	}

	return state.Available
}

// ManualDisable manually disables a provider.
//
// Race condition note: This method uses read-modify-write pattern which is not atomic.
// However, this is acceptable because manual disable/enable are low-frequency admin
// operations (typically triggered by UI button clicks), not high-concurrency code paths.
// The probability of concurrent admin actions on the same provider is negligible.
func (m *Manager) ManualDisable(ctx context.Context, providerID string, reason string) error {
	state, err := m.store.GetHealthState(ctx, providerID)
	if err != nil {
		return err
	}

	state.Available = false
	// Manual disable must override any previous auto-disable state.
	// Clearing DisabledUntil prevents IsAvailable() from auto-recovering the provider.
	state.DisabledUntil = nil
	state.DisabledReason = DisabledReasonPrefixManual + reason
	// No DisabledUntil for manual disable - requires manual enable

	return m.store.UpdateHealthState(ctx, state)
}

// ManualEnable manually enables a provider (clears disabled state).
//
// Race condition note: This method uses read-modify-write pattern which is not atomic.
// However, this is acceptable because manual disable/enable are low-frequency admin
// operations (typically triggered by UI button clicks), not high-concurrency code paths.
// The probability of concurrent admin actions on the same provider is negligible.
func (m *Manager) ManualEnable(ctx context.Context, providerID string) error {
	state, err := m.store.GetHealthState(ctx, providerID)
	if err != nil {
		return err
	}

	state.Available = true
	state.DisabledUntil = nil
	state.DisabledReason = ""

	// Reset circuit breaker
	m.circuit.Reset(providerID)

	return m.store.UpdateHealthState(ctx, state)
}

// ResetCircuitBreaker clears the in-memory failure history for a provider.
// Call this when a provider is deleted to prevent memory leaks.
// The database health_states are cleaned up separately during deletion.
func (m *Manager) ResetCircuitBreaker(providerID string) {
	m.circuit.Reset(providerID)
}

// getConfigInt retrieves a config value as int with default.
func (m *Manager) getConfigInt(ctx context.Context, key string, defaultVal int) int {
	val, err := m.store.GetConfig(ctx, key)
	if err != nil || val == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return v
}
