package store

import (
	"context"
	"errors"
	"fmt"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

// GetDefaultConfigs returns the default runtime configuration values.
// Returns a new map each time to prevent mutation of shared state.
// This is exported so the admin API can return defaults separately from user values.
func GetDefaultConfigs() map[string]string {
	return map[string]string{
		"auth_mode":                DefaultAuthMode,
		"user_header":              DefaultUserHeader,
		"trust_proxy_headers":      DefaultTrustProxyHeaders,
		"upstream_connect_timeout": DefaultUpstreamConnectTimeout,
		"first_byte_timeout":       DefaultFirstByteTimeout,
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

func (s *SQLiteStore) GetConfig(ctx context.Context, key string) (string, error) {
	var cfg model.RuntimeConfig
	err := s.db.WithContext(ctx).First(&cfg, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Return default value if exists
		if defaultVal, ok := GetDefaultConfigs()[key]; ok {
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
