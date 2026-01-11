// Package store provides data storage implementations.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Compile-time interface check.
var _ internal.Store = (*SQLiteStore)(nil)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// getDefaultConfigs returns the default runtime configuration values.
// Returns a new map each time to prevent mutation of shared state.
func getDefaultConfigs() map[string]string {
	return map[string]string{
		"auth_mode":                DefaultAuthMode,
		"user_header":              DefaultUserHeader,
		"trust_proxy_headers":      DefaultTrustProxyHeaders,
		"upstream_connect_timeout": DefaultUpstreamConnectTimeout,
		"upstream_read_timeout":    DefaultUpstreamReadTimeout,
		"sse_idle_timeout":         DefaultSSEIdleTimeout,
		"sticky_enabled":           DefaultStickyEnabled,
		"sticky_ttl":               DefaultStickyTTL,
		"circuit_failure":          DefaultCircuitFailure,
		"circuit_window":           DefaultCircuitWindow,
		"circuit_disable":          DefaultCircuitDisable,
		"max_body_size":            DefaultMaxBodySize,
		"max_retries":              DefaultMaxRetries,
		"log_retention_days":       DefaultLogRetentionDays,
		"inter_group_strategy":     DefaultInterGroupStrategy,
	}
}

// SQLiteStore implements the Store interface using GORM and SQLite.
type SQLiteStore struct {
	db    *gorm.DB
	clock internal.Clock
}

// NewSQLiteStore creates a new SQLite store with the given database path and clock.
func NewSQLiteStore(dbPath string, clock internal.Clock) (*SQLiteStore, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	// Enable WAL mode for better concurrency
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil { // coverage-ignore -- PRAGMA rarely fails on valid connection
		return nil, err
	}

	// Auto migrate tables
	if err := db.AutoMigrate(
		&model.Group{},
		&model.Provider{},
		&model.ProviderAPIType{},
		&model.HealthState{},
		&model.RuntimeConfig{},
		&model.RequestLog{},
	); err != nil { // coverage-ignore -- AutoMigrate rarely fails on valid schema
		return nil, err
	}

	return &SQLiteStore{db: db, clock: clock}, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil { // coverage-ignore -- DB() rarely fails on valid gorm.DB
		return err
	}
	return sqlDB.Close()
}

// InitDefaultConfig initializes default runtime configuration values.
func (s *SQLiteStore) InitDefaultConfig(ctx context.Context) error {
	for key, value := range getDefaultConfigs() {
		err := s.db.WithContext(ctx).Exec(
			"INSERT OR IGNORE INTO runtime_configs (key, value, updated_at) VALUES (?, ?, ?)",
			key, value, s.clock.Now(),
		).Error
		if err != nil { // coverage-ignore -- INSERT OR IGNORE rarely fails on valid schema
			return fmt.Errorf("init default config %q: %w", key, err)
		}
	}
	return nil
}

// Provider operations

func (s *SQLiteStore) ListProviders(ctx context.Context) ([]model.Provider, error) {
	var providers []model.Provider
	if err := s.db.WithContext(ctx).Preload("APITypes").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	return providers, nil
}

func (s *SQLiteStore) ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error) {
	var providers []model.Provider
	err := s.db.WithContext(ctx).
		Preload("APITypes").
		Distinct().
		Joins("JOIN provider_api_types ON provider_api_types.provider_id = providers.id").
		Where("provider_api_types.api_type = ? AND providers.enabled = ?", apiType, true).
		Find(&providers).Error
	if err != nil {
		return nil, fmt.Errorf("list providers by api type %q: %w", apiType, err)
	}
	return providers, nil
}

func (s *SQLiteStore) GetProvider(ctx context.Context, id string) (*model.Provider, error) {
	var provider model.Provider
	err := s.db.WithContext(ctx).Preload("APITypes").First(&provider, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", id, err)
	}
	return &provider, nil
}

func (s *SQLiteStore) CreateProvider(ctx context.Context, p *model.Provider) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Use raw SQL to properly handle boolean false values
		now := s.clock.Now()
		if p.CreatedAt.IsZero() {
			p.CreatedAt = now
		}
		if p.UpdatedAt.IsZero() {
			p.UpdatedAt = now
		}

		if err := tx.Exec(`
			INSERT INTO providers (id, name, base_url, api_key, auth_mode, group_id, weight, priority, concurrency, max_retries, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, p.ID, p.Name, p.BaseURL, p.APIKey, p.AuthMode, p.GroupID, p.Weight, p.Priority, p.Concurrency, p.MaxRetries, p.Enabled, p.CreatedAt, p.UpdatedAt).Error; err != nil { // coverage-ignore -- INSERT rarely fails with valid data
			return err
		}
		// Create API types separately
		for i := range p.APITypes {
			p.APITypes[i].ProviderID = p.ID
			if err := tx.Create(&p.APITypes[i]).Error; err != nil { // coverage-ignore -- transaction error after successful provider insert is rare
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateProvider(ctx context.Context, p *model.Provider) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete existing API types
		if err := tx.Where("provider_id = ?", p.ID).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}
		// Save provider with new API types
		return tx.Save(p).Error
	})
	if err != nil {
		return fmt.Errorf("update provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) DeleteProvider(ctx context.Context, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete API types first
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}
		// Delete health state
		if err := tx.Where("provider_id = ?", id).Delete(&model.HealthState{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}
		// Delete provider
		return tx.Delete(&model.Provider{}, "id = ?", id).Error
	})
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	return nil
}

// Group operations

func (s *SQLiteStore) ListGroups(ctx context.Context) ([]model.Group, error) {
	var groups []model.Group
	if err := s.db.WithContext(ctx).Preload("Providers").Preload("Providers.APITypes").Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return groups, nil
}

func (s *SQLiteStore) GetGroup(ctx context.Context, id string) (*model.Group, error) {
	var group model.Group
	err := s.db.WithContext(ctx).Preload("Providers").Preload("Providers.APITypes").First(&group, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get group %q: %w", id, err)
	}
	return &group, nil
}

func (s *SQLiteStore) CreateGroup(ctx context.Context, g *model.Group) error {
	if err := s.db.WithContext(ctx).Create(g).Error; err != nil {
		return fmt.Errorf("create group %q: %w", g.ID, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateGroup(ctx context.Context, g *model.Group) error {
	// Preserve providers to avoid GORM trying to update the association.
	// This is an ORM implementation detail that should be handled here.
	providers := g.Providers
	g.Providers = nil
	if err := s.db.WithContext(ctx).Save(g).Error; err != nil {
		return fmt.Errorf("update group %q: %w", g.ID, err)
	}
	// Restore providers so the returned Group object has the association
	g.Providers = providers
	return nil
}

func (s *SQLiteStore) DeleteGroup(ctx context.Context, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// First update providers to remove group reference
		if err := tx.Model(&model.Provider{}).
			Where("group_id = ?", id).
			Update("group_id", nil).Error; err != nil { // coverage-ignore -- UPDATE rarely fails within transaction
			return fmt.Errorf("unlink providers: %w", err)
		}
		// Then delete the group
		if err := tx.Delete(&model.Group{}, "id = ?", id).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete group %q: %w", id, err)
	}
	return nil
}

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
// (unless manually disabled). Returns the updated state.
func (s *SQLiteStore) IncrementSuccessCount(ctx context.Context, providerID string, now time.Time) (*model.HealthState, error) {
	// Use atomic SQL UPDATE to prevent race conditions.
	// The CASE expression preserves manual disable state (disabled_reason LIKE 'manual:%').
	err := s.db.WithContext(ctx).Exec(`
		INSERT INTO health_states (provider_id, available, success_count, fail_count, last_success, last_failure, last_error, disabled_until, disabled_reason)
		VALUES (?, true, 1, 0, ?, NULL, '', NULL, '')
		ON CONFLICT(provider_id) DO UPDATE SET
			success_count = health_states.success_count + 1,
			last_success = excluded.last_success,
			available = CASE 
				WHEN health_states.disabled_reason LIKE 'manual:%' THEN health_states.available 
				ELSE true 
			END
	`, providerID, now).Error
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

// Config operations

func (s *SQLiteStore) GetConfig(ctx context.Context, key string) (string, error) {
	var cfg model.RuntimeConfig
	err := s.db.WithContext(ctx).First(&cfg, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Return default value if exists
		if defaultVal, ok := getDefaultConfigs()[key]; ok {
			return defaultVal, nil
		}
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get config %q: %w", key, err)
	}
	return cfg.Value, nil
}

func (s *SQLiteStore) GetAllConfig(ctx context.Context) (map[string]string, error) {
	var configs []model.RuntimeConfig
	if err := s.db.WithContext(ctx).Find(&configs).Error; err != nil { // coverage-ignore -- Find rarely fails on valid table
		return nil, err
	}

	result := make(map[string]string, len(configs))
	for _, cfg := range configs {
		result[cfg.Key] = cfg.Value
	}
	return result, nil
}

func (s *SQLiteStore) SetConfig(ctx context.Context, key, value string) error {
	cfg := model.RuntimeConfig{
		Key:       key,
		Value:     value,
		UpdatedAt: s.clock.Now(),
	}
	if err := s.db.WithContext(ctx).Save(&cfg).Error; err != nil {
		return fmt.Errorf("set config %q: %w", key, err)
	}
	return nil
}

// SetConfigs atomically updates multiple config values in a single transaction.
// If any key fails, the entire batch is rolled back to prevent partial updates.
func (s *SQLiteStore) SetConfigs(ctx context.Context, configs map[string]string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.clock.Now()
		for key, value := range configs {
			cfg := model.RuntimeConfig{
				Key:       key,
				Value:     value,
				UpdatedAt: now,
			}
			if err := tx.Save(&cfg).Error; err != nil {
				return fmt.Errorf("set config %q: %w", key, err)
			}
		}
		return nil
	})
}

// Log operations

func (s *SQLiteStore) InsertLog(ctx context.Context, log *model.RequestLog) error {
	if err := s.db.WithContext(ctx).Create(log).Error; err != nil {
		return fmt.Errorf("insert log: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListLogs(ctx context.Context, limit, offset int) ([]model.RequestLog, error) {
	var logs []model.RequestLog
	err := s.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&logs).Error
	if err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}
	return logs, nil
}

func (s *SQLiteStore) CountLogs(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.RequestLog{}).Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count logs: %w", err)
	}
	return count, nil
}

func (s *SQLiteStore) CleanOldLogs(ctx context.Context, beforeDays int) error {
	cutoff := s.clock.Now().AddDate(0, 0, -beforeDays)
	err := s.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Delete(&model.RequestLog{}).Error
	if err != nil {
		return fmt.Errorf("clean old logs (before %d days): %w", beforeDays, err)
	}
	return nil
}
