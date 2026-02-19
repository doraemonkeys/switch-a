package store

import (
	"errors"
	"fmt"
	"strings"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

const (
	legacyStickyEnabledConfigKey = "sticky_enabled"
	stickyModeConfigKey          = "sticky_mode"
)

// migrateBaseURLToAPIType moves base_url from the providers table to provider_api_types.
// Idempotent: skips if providers.base_url column no longer exists.
func migrateBaseURLToAPIType(db *gorm.DB) error {
	// Check whether the old column still exists.
	var count int64
	err := db.Raw(`SELECT COUNT(*) FROM pragma_table_info('providers') WHERE name = 'base_url'`).Scan(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return nil // Already migrated.
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// Copy each provider's base_url into its associated api_type rows.
		// Only propagate non-empty base_url values to prevent creating rows
		// with empty base_url that would cause proxy routing failures at runtime.
		if err := tx.Exec(`
			UPDATE provider_api_types
			SET base_url = (
				SELECT providers.base_url
				FROM providers
				WHERE providers.id = provider_api_types.provider_id
				  AND providers.base_url != ''
			)
			WHERE (base_url = '' OR base_url IS NULL)
			  AND EXISTS (
				SELECT 1 FROM providers
				WHERE providers.id = provider_api_types.provider_id
				  AND providers.base_url != ''
			)
		`).Error; err != nil {
			return err
		}

		// Drop the old column from providers.
		if err := tx.Exec(`ALTER TABLE providers DROP COLUMN base_url`).Error; err != nil {
			return err
		}

		return nil
	})
}

// migrateStickyConfig converts legacy sticky_enabled values to sticky_mode.
// It deletes sticky_enabled after migration to prevent stale keys in config exports.
func migrateStickyConfig(db *gorm.DB) error {
	var cfg model.RuntimeConfig
	err := db.First(&cfg, "key = ?", legacyStickyEnabledConfigKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", legacyStickyEnabledConfigKey, err)
	}

	var mode string
	switch strings.ToLower(cfg.Value) {
	case "false", "0":
		mode = string(model.StickyModeOff)
	case "true", "1":
		mode = string(model.StickyModeAPIType)
	default:
		return nil
	}

	result := db.Where("key = ?", stickyModeConfigKey).
		FirstOrCreate(&model.RuntimeConfig{Key: stickyModeConfigKey, Value: mode})
	if result.Error != nil {
		return fmt.Errorf("upsert %s: %w", stickyModeConfigKey, result.Error)
	}

	if err := db.Where("key = ?", legacyStickyEnabledConfigKey).
		Delete(&model.RuntimeConfig{}).Error; err != nil {
		return fmt.Errorf("delete %s: %w", legacyStickyEnabledConfigKey, err)
	}

	return nil
}
