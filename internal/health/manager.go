package health

import (
	"context"
	"strconv"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Compile-time interface check.
var _ internal.HealthManager = (*Manager)(nil)

// Default values.
const (
	DefaultCircuitFailure = 3
	DefaultCircuitWindow  = 60  // seconds
	DefaultCircuitDisable = 300 // seconds
)

// Store defines the minimal storage interface needed by the health manager.
type Store interface {
	GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error)
	UpdateHealthState(ctx context.Context, state *model.HealthState) error
	GetConfig(ctx context.Context, key string) (string, error)
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

// MarkSuccess marks a successful request for the provider.
func (m *Manager) MarkSuccess(ctx context.Context, providerID string) {
	state, err := m.store.GetHealthState(ctx, providerID)
	if err != nil {
		m.logger.Error("failed to get health state", zap.String("provider_id", providerID), zap.Error(err))
		return
	}

	now := m.clock.Now()
	state.SuccessCount++
	state.LastSuccess = &now
	state.Available = true

	// Reset circuit breaker on success
	m.circuit.Reset(providerID)

	if err := m.store.UpdateHealthState(ctx, state); err != nil {
		m.logger.Error("failed to update health state", zap.String("provider_id", providerID), zap.Error(err))
	}
}

// MarkFailure marks a failed request, returns true if circuit breaker triggered.
func (m *Manager) MarkFailure(ctx context.Context, providerID string, err error) bool {
	state, stateErr := m.store.GetHealthState(ctx, providerID)
	if stateErr != nil {
		m.logger.Error("failed to get health state", zap.String("provider_id", providerID), zap.Error(stateErr))
		return false
	}

	now := m.clock.Now()
	state.FailCount++
	state.LastFailure = &now
	if err != nil {
		state.LastError = err.Error()
	}

	// Get circuit breaker config
	circuitFailure := m.getConfigInt(ctx, "circuit_failure", DefaultCircuitFailure)
	circuitWindow := time.Duration(m.getConfigInt(ctx, "circuit_window", DefaultCircuitWindow)) * time.Second
	circuitDisable := time.Duration(m.getConfigInt(ctx, "circuit_disable", DefaultCircuitDisable)) * time.Second

	// Record failure and check if threshold reached
	triggered := m.circuit.RecordFailure(providerID, circuitWindow, circuitFailure)
	if triggered {
		disableUntil := now.Add(circuitDisable)
		state.DisabledUntil = &disableUntil
		state.DisabledReason = "auto: circuit breaker triggered"
		state.Available = false

		m.logger.Warn("circuit breaker triggered",
			zap.String("provider_id", providerID),
			zap.Int("threshold", circuitFailure),
			zap.Duration("disable_duration", circuitDisable),
		)
	}

	if stateErr := m.store.UpdateHealthState(ctx, state); stateErr != nil {
		m.logger.Error("failed to update health state", zap.String("provider_id", providerID), zap.Error(stateErr))
	}

	return triggered
}

// IsAvailable checks if the provider is available.
func (m *Manager) IsAvailable(ctx context.Context, providerID string) bool {
	state, err := m.store.GetHealthState(ctx, providerID)
	if err != nil {
		m.logger.Error("failed to get health state", zap.String("provider_id", providerID), zap.Error(err))
		return false // Fail safe: treat as unavailable if we can't check
	}

	// Check if auto-disabled and if disable period has expired
	if state.DisabledUntil != nil && !state.Available {
		if m.clock.Now().After(*state.DisabledUntil) {
			// Disable period expired, auto-recover
			state.Available = true
			state.DisabledUntil = nil
			state.DisabledReason = ""
			m.circuit.Reset(providerID)

			if err := m.store.UpdateHealthState(ctx, state); err != nil {
				m.logger.Error("failed to update health state on auto-recovery",
					zap.String("provider_id", providerID), zap.Error(err))
			}
			m.logger.Info("provider auto-recovered", zap.String("provider_id", providerID))
			return true
		}
		return false
	}

	return state.Available
}

// ManualDisable manually disables a provider.
func (m *Manager) ManualDisable(ctx context.Context, providerID string, reason string) error {
	state, err := m.store.GetHealthState(ctx, providerID)
	if err != nil {
		return err
	}

	state.Available = false
	state.DisabledReason = "manual: " + reason
	// No DisabledUntil for manual disable - requires manual enable

	return m.store.UpdateHealthState(ctx, state)
}

// ManualEnable manually enables a provider (clears disabled state).
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
