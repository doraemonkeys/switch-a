// Package internal defines the core interfaces for switch-a.
package internal

import (
	"context"
	"net/http"
	"time"

	"switch-a/internal/model"
)

// Store defines the data storage interface.
type Store interface {
	// Provider operations
	ListProviders(ctx context.Context) ([]model.Provider, error)
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetProvider(ctx context.Context, id string) (*model.Provider, error)
	CreateProvider(ctx context.Context, p *model.Provider, options ...model.ProviderWriteOptions) error
	UpdateProvider(ctx context.Context, p *model.Provider, options ...model.ProviderWriteOptions) error
	UpdateProviderCredential(ctx context.Context, id string, credentialType model.ProviderCredentialType, credentialData string) error
	DeleteProvider(ctx context.Context, id string) error

	// Group operations
	ListGroups(ctx context.Context) ([]model.Group, error)
	GetGroup(ctx context.Context, id string) (*model.Group, error)
	CreateGroup(ctx context.Context, g *model.Group) error
	UpdateGroup(ctx context.Context, g *model.Group) error
	DeleteGroup(ctx context.Context, id string) error

	// Health state operations
	GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error)
	GetHealthStatesByProviderIDs(ctx context.Context, providerIDs []string) (map[string]*model.HealthState, error)
	UpdateHealthState(ctx context.Context, state *model.HealthState) error
	ListHealthStates(ctx context.Context) ([]model.HealthState, error)
	// IncrementSuccessCount atomically increments success_count and sets available=true
	// (unless manually disabled). Returns the updated state.
	IncrementSuccessCount(ctx context.Context, providerID string, now time.Time) (*model.HealthState, error)
	// IncrementFailCount atomically increments fail_count, sets last_failure and last_error.
	// Returns the updated state.
	IncrementFailCount(ctx context.Context, providerID string, now time.Time, lastError string) (*model.HealthState, error)
	// AutoDisableUntil atomically marks a provider unavailable until the given time.
	// The reason identifies the automatic recovery policy that put the provider on hold.
	AutoDisableUntil(ctx context.Context, providerID string, disabledUntil time.Time, reason string) error
	// AtomicRecoverIfExpired atomically checks if a provider's auto-disable period has expired
	// and recovers it. Returns true if recovery was performed, false otherwise.
	AtomicRecoverIfExpired(ctx context.Context, providerID string, now time.Time) (bool, error)

	// Config operations
	GetConfig(ctx context.Context, key string) (string, error)
	GetAllConfig(ctx context.Context) (map[string]string, error)
	SetConfig(ctx context.Context, key, value string) error
	SetConfigs(ctx context.Context, configs map[string]string) error
	InitDefaultConfig(ctx context.Context) error

	// Log operations
	// RequestLog remains the single source of truth for the canonical request/session
	// assessment, while semantics_version keeps legacy rows explicit.
	InsertLog(ctx context.Context, log *model.RequestLog) error
	ListLogs(ctx context.Context, filter model.LogFilter) ([]model.RequestLog, error)
	CountLogs(ctx context.Context, filter model.LogFilter) (int64, error)
	GetLogStats(ctx context.Context, startTime, endTime time.Time) (*model.LogStats, error)
	GetLogTimeSeries(ctx context.Context, startTime, endTime time.Time, granularity time.Duration) ([]model.TimeSeriesPoint, error)
	CleanOldLogs(ctx context.Context, beforeDays int) error
	GetLogByID(ctx context.Context, id uint) (*model.RequestLog, error)

	// Request attempt operations only persist provider-attempt evidence; final
	// request/session assessment never moves off RequestLog.
	InsertAttempts(ctx context.Context, attempts []model.RequestAttempt) error
	GetAttemptsByRequestID(ctx context.Context, requestID string) ([]model.RequestAttempt, error)
	CleanOldAttempts(ctx context.Context, before time.Time) (int64, error)

	// Close closes the store and releases resources.
	Close() error
}

// Selector defines the minimal provider selection interface.
// This is the core interface that all selector implementations must satisfy.
//
// Note: The proxy handler (internal/proxy) defines an extended Selector interface
// with additional methods (SelectExcluding, UpdateStickyWithTTL, ReleaseConcurrency,
// ClearConcurrency) for advanced features like retries, sticky sessions, and
// concurrency management. The concrete selector.Selector implements both interfaces.
type Selector interface {
	// Select chooses an available provider based on the request.
	Select(ctx context.Context, req *model.SelectRequest) (*model.Provider, error)
}

// HealthManager defines the health management interface.
type HealthManager interface {
	// MarkSuccess marks a successful request for the provider.
	MarkSuccess(ctx context.Context, providerID string)
	// MarkFailure marks a failed request, returns true if circuit breaker triggered.
	MarkFailure(ctx context.Context, providerID string, err error) bool
	// RecoverIfExpired checks if a provider's auto-disable period has expired
	// and performs recovery if so. Returns true if recovery was performed.
	// This method has side effects - it updates the provider's health state.
	RecoverIfExpired(ctx context.Context, providerID string) bool
	// IsAvailable checks if the provider is currently available.
	// This is a pure query with no side effects.
	IsAvailable(ctx context.Context, providerID string) bool
	// SuspendUntil marks a provider unavailable until the given time using automatic
	// recovery semantics so it becomes selectable again after the cooldown expires.
	SuspendUntil(ctx context.Context, providerID string, disabledUntil time.Time, reason string) error
	// ManualDisable manually disables a provider.
	ManualDisable(ctx context.Context, providerID string, reason string) error
	// ManualEnable manually enables a provider (clears disabled state).
	ManualEnable(ctx context.Context, providerID string) error
	// ResetCircuitBreaker clears the in-memory failure history for a provider.
	// Call this when a provider is deleted to prevent memory leaks.
	ResetCircuitBreaker(providerID string)
}

// StickyCache defines the sticky session cache interface.
type StickyCache interface {
	// Get retrieves a cached provider ID for the given key.
	Get(key model.StickyKey) (providerID string, found bool)
	// Set stores a provider ID for the given key with TTL.
	Set(key model.StickyKey, providerID string, ttl time.Duration)
	// Delete removes a cached entry.
	Delete(key model.StickyKey)
	// EvictProvider removes every continuity key that currently points at the
	// provider so suspension can invalidate affinity immediately instead of
	// waiting for lazy revalidation on the next request.
	EvictProvider(providerID string)
}

// Clock provides time-related functions for testing.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) *time.Ticker
}

// HTTPDoer performs HTTP requests for testing.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// RealClock implements Clock using the real system time.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time {
	return time.Now()
}

// NewTicker returns a new time.Ticker.
func (RealClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}
