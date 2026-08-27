package migration

import (
	"errors"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

const (
	legacyStickyEnabledConfigKey      = "sticky_enabled"
	stickyModeConfigKey               = "sticky_mode"
	legacyMaxRetriesConfigKey         = "max_retries"
	globalMaxAttemptsConfigKey        = "global_max_attempts"
	providersTableName                = "providers"
	providerCredentialDataColumn      = "credential_data"
	routingPoliciesTableName          = "routing_policies"
	routingPolicyEnabledColumn        = "enabled"
	routingPolicyTargetProviderColumn = "target_provider_id"
	usageLimitPolicyColumnName        = "usage_limit_policy"
	legacyProviderCredentialAPIKey    = "api_key"
	legacyProviderCredentialChatGPT   = "chatgpt"
)

// MigrateBaseURLToAPIType moves base_url from the providers table to provider_api_types.
// Idempotent: skips if providers.base_url column no longer exists.
func MigrateBaseURLToAPIType(db *gorm.DB) error {
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

// MigrateStickyConfig converts legacy sticky_enabled values to sticky_mode.
// It deletes sticky_enabled after migration to prevent stale keys in config exports.
func MigrateStickyConfig(db *gorm.DB) error {
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

// MigrateGlobalMaxAttemptsConfig renames the legacy max_retries runtime setting.
// The runtime only reads global_max_attempts, so leaving the stale key behind
// makes the admin surface and the active config diverge.
func MigrateGlobalMaxAttemptsConfig(db *gorm.DB) error {
	var cfg model.RuntimeConfig
	err := db.First(&cfg, "key = ?", legacyMaxRetriesConfigKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", legacyMaxRetriesConfigKey, err)
	}

	result := db.Where("key = ?", globalMaxAttemptsConfigKey).
		FirstOrCreate(&model.RuntimeConfig{Key: globalMaxAttemptsConfigKey, Value: cfg.Value})
	if result.Error != nil {
		return fmt.Errorf("upsert %s: %w", globalMaxAttemptsConfigKey, result.Error)
	}

	if err := db.Where("key = ?", legacyMaxRetriesConfigKey).
		Delete(&model.RuntimeConfig{}).Error; err != nil {
		return fmt.Errorf("delete %s: %w", legacyMaxRetriesConfigKey, err)
	}

	return nil
}

func tableColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	var count int64
	err := db.Raw(
		fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?`, tableName),
		columnName,
	).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// MigrateRoutingPolicyLifecycleStorage adds lifecycle/reference columns without
// dropping the natural-key uniqueness that older builds already relied on.
func MigrateRoutingPolicyLifecycleStorage(db *gorm.DB) error {
	hasEnabled, err := tableColumnExists(db, routingPoliciesTableName, routingPolicyEnabledColumn)
	if err != nil {
		return fmt.Errorf("check routing policy enabled column: %w", err)
	}
	hasTargetProviderID, err := tableColumnExists(db, routingPoliciesTableName, routingPolicyTargetProviderColumn)
	if err != nil {
		return fmt.Errorf("check routing policy target provider column: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if !hasEnabled {
			if err := tx.Exec(
				fmt.Sprintf(
					`ALTER TABLE %s ADD COLUMN %s BOOLEAN NOT NULL DEFAULT 1`,
					routingPoliciesTableName,
					routingPolicyEnabledColumn,
				),
			).Error; err != nil {
				return fmt.Errorf("add routing policy enabled column: %w", err)
			}
		}
		if err := tx.Exec(
			fmt.Sprintf(
				`UPDATE %s SET %s = 1 WHERE %s IS NULL`,
				routingPoliciesTableName,
				routingPolicyEnabledColumn,
				routingPolicyEnabledColumn,
			),
		).Error; err != nil {
			return fmt.Errorf("backfill routing policy enabled column: %w", err)
		}
		if !hasTargetProviderID {
			if err := tx.Exec(
				fmt.Sprintf(
					`ALTER TABLE %s ADD COLUMN %s TEXT`,
					routingPoliciesTableName,
					routingPolicyTargetProviderColumn,
				),
			).Error; err != nil {
				return fmt.Errorf("add routing policy target provider column: %w", err)
			}
		}
		return nil
	})
}

// MigrateProviderUsageLimitPolicyStorage rewrites rows that accidentally
// persisted a credential-derived default back to the empty "inherit default"
// representation before M1 removes credential_type. Explicit overrides survive
// the transition to the route-target-independent default.
func MigrateProviderUsageLimitPolicyStorage(db *gorm.DB) error {
	present, err := tableColumnExists(db, providersTableName, usageLimitPolicyColumnName)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	credentialTypePresent, err := tableColumnExists(db, providersTableName, "credential_type")
	if err != nil {
		return err
	}
	if !credentialTypePresent {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return tx.Exec(`
			UPDATE providers
			SET usage_limit_policy = ''
			WHERE TRIM(COALESCE(usage_limit_policy, '')) != ''
			  AND (
				(COALESCE(NULLIF(TRIM(credential_type), ''), ?) = ? AND TRIM(usage_limit_policy) = ?)
				OR
				(COALESCE(NULLIF(TRIM(credential_type), ''), ?) != ? AND TRIM(usage_limit_policy) = ?)
			  )
		`,
			legacyProviderCredentialAPIKey,
			legacyProviderCredentialChatGPT,
			string(model.ProviderUsageLimitPolicySuspend),
			legacyProviderCredentialAPIKey,
			legacyProviderCredentialChatGPT,
			string(model.ProviderUsageLimitPolicySwitchProvider),
		).Error
	})
}
