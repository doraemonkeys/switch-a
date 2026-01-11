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
	CreateProvider(ctx context.Context, p *model.Provider) error
	UpdateProvider(ctx context.Context, p *model.Provider) error
	DeleteProvider(ctx context.Context, id string) error

	// Group operations
	ListGroups(ctx context.Context) ([]model.Group, error)
	GetGroup(ctx context.Context, id string) (*model.Group, error)
	CreateGroup(ctx context.Context, g *model.Group) error
	UpdateGroup(ctx context.Context, g *model.Group) error
	DeleteGroup(ctx context.Context, id string) error

	// Health state operations
	GetHealthState(ctx context.Context, providerID string) (*model.HealthState, error)
	UpdateHealthState(ctx context.Context, state *model.HealthState) error
	ListHealthStates(ctx context.Context) ([]model.HealthState, error)

	// Config operations
	GetConfig(ctx context.Context, key string) (string, error)
	GetAllConfig(ctx context.Context) (map[string]string, error)
	SetConfig(ctx context.Context, key, value string) error
	InitDefaultConfig(ctx context.Context) error

	// Log operations
	InsertLog(ctx context.Context, log *model.RequestLog) error
	ListLogs(ctx context.Context, limit, offset int) ([]model.RequestLog, error)
	CountLogs(ctx context.Context) (int64, error)
	CleanOldLogs(ctx context.Context, beforeDays int) error

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
	// ManualDisable manually disables a provider.
	ManualDisable(ctx context.Context, providerID string, reason string) error
	// ManualEnable manually enables a provider (clears disabled state).
	ManualEnable(ctx context.Context, providerID string) error
}

// StickyCache defines the sticky session cache interface.
type StickyCache interface {
	// Get retrieves a cached provider ID for the given key.
	Get(key model.StickyKey) (providerID string, found bool)
	// Set stores a provider ID for the given key with TTL.
	Set(key model.StickyKey, providerID string, ttl time.Duration)
	// Delete removes a cached entry.
	Delete(key model.StickyKey)
}

// Clock provides time-related functions for testing.
type Clock interface {
	Now() time.Time
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
