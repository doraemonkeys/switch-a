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
	legacyMaxRetriesConfigKey    = "max_retries"
	globalMaxAttemptsConfigKey   = "global_max_attempts"
	providersTableName           = "providers"
	providerCredentialDataColumn = "credential_data"
	requestLogsTableName         = "request_logs"
	legacyWebSocketColumnName    = "is_web_socket"
	webSocketColumnName          = "is_websocket"
	sessionCommittedColumnName   = "session_committed"
	stickyWrittenColumnName      = "sticky_written"
	probeOutcomeColumnName       = "probe_outcome"
	terminalCauseColumnName      = "terminal_cause"
	commitSourceColumnName       = "commit_source"
)

const (
	legacySuccessValue = 1
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

// migrateGlobalMaxAttemptsConfig renames the legacy max_retries runtime setting.
// The runtime only reads global_max_attempts, so leaving the stale key behind
// makes the admin surface and the active config diverge.
func migrateGlobalMaxAttemptsConfig(db *gorm.DB) error {
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

// migrateWebSocketColumn copies data from the legacy is_web_socket column
// (GORM's default snake_case for IsWebSocket) to the explicit is_websocket column.
// Idempotent: skips if the legacy column does not exist.
func migrateWebSocketColumn(db *gorm.DB) error {
	hasLegacyColumn, err := requestLogsColumnExists(db, legacyWebSocketColumnName)
	if err != nil {
		return err
	}
	if !hasLegacyColumn {
		return nil // No legacy column; nothing to migrate.
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			fmt.Sprintf(
				`UPDATE %s SET %s = %s WHERE %s = ?`,
				requestLogsTableName,
				webSocketColumnName,
				legacyWebSocketColumnName,
				legacyWebSocketColumnName,
			),
			legacySuccessValue,
		).Error; err != nil {
			return err
		}
		if err := tx.Exec(
			fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, requestLogsTableName, legacyWebSocketColumnName),
		).Error; err != nil {
			return err
		}
		return nil
	})
}

// migrateRequestLogLifecycleFields backfills only the historical WebSocket rows
// whose lifecycle cannot be reconstructed from the pre-refactor schema, while
// clearing lifecycle pollution from regular HTTP/SSE rows.
func migrateRequestLogLifecycleFields(db *gorm.DB) error {
	hasSessionCommitted, err := requestLogsColumnExists(db, sessionCommittedColumnName)
	if err != nil {
		return err
	}
	hasStickyWritten, err := requestLogsColumnExists(db, stickyWrittenColumnName)
	if err != nil {
		return err
	}
	hasProbeOutcome, err := requestLogsColumnExists(db, probeOutcomeColumnName)
	if err != nil {
		return err
	}
	hasTerminalCause, err := requestLogsColumnExists(db, terminalCauseColumnName)
	if err != nil {
		return err
	}
	hasCommitSource, err := requestLogsColumnExists(db, commitSourceColumnName)
	if err != nil {
		return err
	}
	if !hasSessionCommitted && !hasStickyWritten && !hasProbeOutcome && !hasTerminalCause && !hasCommitSource {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := migrateOptionalWebSocketLifecycleColumn(
			tx,
			hasSessionCommitted,
			sessionCommittedColumnName,
			true,
			fmt.Sprintf("success = ? AND %s IS NULL", sessionCommittedColumnName),
			legacySuccessValue,
		); err != nil {
			return err
		}
		if err := migrateOptionalWebSocketLifecycleColumn(
			tx,
			hasStickyWritten,
			stickyWrittenColumnName,
			false,
			fmt.Sprintf("%s IS NULL", stickyWrittenColumnName),
		); err != nil {
			return err
		}
		if err := migrateOptionalWebSocketLifecycleColumn(
			tx,
			hasProbeOutcome,
			probeOutcomeColumnName,
			model.WebSocketProbeOutcomeUnknown,
			fmt.Sprintf("(%s IS NULL OR %s = '')", probeOutcomeColumnName, probeOutcomeColumnName),
		); err != nil {
			return err
		}
		if err := migrateOptionalWebSocketLifecycleColumn(
			tx,
			hasTerminalCause,
			terminalCauseColumnName,
			model.TerminalUnknown,
			fmt.Sprintf("(%s IS NULL OR %s = '')", terminalCauseColumnName, terminalCauseColumnName),
		); err != nil {
			return err
		}
		if err := migrateOptionalWebSocketLifecycleColumn(
			tx,
			hasCommitSource,
			commitSourceColumnName,
			model.CommitUnknown,
			fmt.Sprintf("(%s IS NULL OR %s = '')", commitSourceColumnName, commitSourceColumnName),
		); err != nil {
			return err
		}
		return nil
	})
}

func migrateOptionalWebSocketLifecycleColumn(tx *gorm.DB, present bool, columnName string, defaultValue any, backfillPredicate string, backfillArgs ...any) error {
	if !present {
		return nil
	}
	if err := clearNonWebSocketLifecycleColumn(tx, columnName); err != nil {
		return err
	}
	return backfillWebSocketLifecycleColumn(tx, columnName, defaultValue, backfillPredicate, backfillArgs...)
}

func clearNonWebSocketLifecycleColumn(tx *gorm.DB, columnName string) error {
	return tx.Exec(
		fmt.Sprintf(
			`UPDATE %s SET %s = NULL WHERE %s = 0`,
			requestLogsTableName,
			columnName,
			webSocketColumnName,
		),
	).Error
}

func backfillWebSocketLifecycleColumn(tx *gorm.DB, columnName string, defaultValue any, predicate string, predicateArgs ...any) error {
	args := append([]any{defaultValue}, predicateArgs...)
	return tx.Exec(
		fmt.Sprintf(
			`UPDATE %s SET %s = ? WHERE %s = 1 AND %s`,
			requestLogsTableName,
			columnName,
			webSocketColumnName,
			predicate,
		),
		args...,
	).Error
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

func requestLogsColumnExists(db *gorm.DB, columnName string) (bool, error) {
	return tableColumnExists(db, requestLogsTableName, columnName)
}

// migrateProviderStateTables backfills the new credential/auth tables without
// overwriting rows that were already created by a newer binary, then drops the
// legacy providers.credential_data shadow once split storage is populated.
func migrateProviderStateTables(db *gorm.DB) error {
	hasLegacyCredentialData, err := tableColumnExists(db, providersTableName, providerCredentialDataColumn)
	if err != nil {
		return fmt.Errorf("check providers credential shadow column: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if hasLegacyCredentialData {
			var providers []legacyProviderCredentialShadow
			if err := tx.Table(providersTableName).
				Select("id, credential_type, credential_data").
				Scan(&providers).Error; err != nil {
				return fmt.Errorf("list providers for provider state migration: %w", err)
			}
			for i := range providers {
				if err := backfillProviderState(tx, &providers[i]); err != nil {
					return err
				}
			}
			if err := tx.Exec(
				fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, providersTableName, providerCredentialDataColumn),
			).Error; err != nil {
				return fmt.Errorf("drop providers credential shadow column: %w", err)
			}
		}
		return nil
	})
}

type legacyProviderCredentialShadow struct {
	ID             string
	CredentialType model.ProviderCredentialType
	CredentialData string
}

func backfillProviderState(tx *gorm.DB, provider *legacyProviderCredentialShadow) error {
	if provider == nil {
		return nil
	}

	var credentialCount int64
	if err := tx.Model(&model.ProviderCredential{}).
		Where("provider_id = ?", provider.ID).
		Count(&credentialCount).Error; err != nil {
		return fmt.Errorf("count provider credentials for %q: %w", provider.ID, err)
	}
	if credentialCount == 0 {
		credential := model.ProviderCredentialFromLegacy(
			provider.ID,
			provider.CredentialType,
			provider.CredentialData,
		)
		if credential != nil {
			if err := tx.Create(credential).Error; err != nil {
				return fmt.Errorf("backfill provider credential for %q: %w", provider.ID, err)
			}
		}
	}

	var authStateCount int64
	if err := tx.Model(&model.ProviderAuthState{}).
		Where("provider_id = ?", provider.ID).
		Count(&authStateCount).Error; err != nil {
		return fmt.Errorf("count provider auth states for %q: %w", provider.ID, err)
	}
	if authStateCount == 0 {
		authState := model.ProviderAuthStateFromCredential(
			provider.ID,
			provider.CredentialType,
			model.ProviderCredentialFromLegacy(provider.ID, provider.CredentialType, provider.CredentialData),
		)
		if authState == nil {
			authState = model.NormalizeProviderAuthStateRecord(
				provider.ID,
				provider.CredentialType,
				nil,
			)
		}
		if err := tx.Create(authState).Error; err != nil {
			return fmt.Errorf("backfill provider auth state for %q: %w", provider.ID, err)
		}
	}

	return nil
}
