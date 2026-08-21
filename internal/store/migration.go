package store

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
	requestLogsTableName              = "request_logs"
	requestAttemptsTableName          = "request_attempts"
	legacyWebSocketColumnName         = "is_web_socket"
	webSocketColumnName               = "is_websocket"
	semanticsVersionColumnName        = "semantics_version"
	clientTransportStatusCodeColumn   = "client_transport_status_code"
	completionStateColumnName         = "completion_state"
	serviceOutcomeColumnName          = "service_outcome"
	terminationActorColumnName        = "termination_actor"
	terminationReasonColumnName       = "termination_reason"
	clientActionColumnName            = "client_action"
	sessionEvidenceJSONColumnName     = "session_evidence_json"
	attemptEvidenceJSONColumnName     = "attempt_evidence_json"
	sessionCommittedColumnName        = "session_committed"
	clientVisibleColumnName           = "client_visible"
	stickyWrittenColumnName           = "sticky_written"
	probeOutcomeColumnName            = "probe_outcome"
	legacySuccessColumnName           = "success"
	legacyStatusCodeColumnName        = "status_code"
	legacyErrorMsgColumnName          = "error_msg"
	terminalCauseColumnName           = "terminal_cause"
	commitSourceColumnName            = "commit_source"
	recoveryActionColumnName          = "recovery_action"
	requestLogProviderCreatedAtIndex  = "idx_request_logs_provider_created_at"
	requestLogModelCreatedAtIndex     = "idx_request_logs_model_created_at"
	requestLogAPITypeCreatedAtIndex   = "idx_request_logs_api_type_created_at"
)

const usageLimitPolicyColumnName = "usage_limit_policy"

const (
	legacySuccessValue = 1
)

var requestLogAnalyticsIndexes = []struct {
	name    string
	columns string
}{
	{name: requestLogProviderCreatedAtIndex, columns: "provider_id, created_at"},
	{name: requestLogModelCreatedAtIndex, columns: "model, created_at"},
	{name: requestLogAPITypeCreatedAtIndex, columns: "api_type, created_at"},
}

// migrateRequestLogAnalyticsIndexes keeps every supported exact filter paired
// with the bounded time range. Separate narrow composites avoid the write cost
// and schema coupling of a token-column covering index.
func migrateRequestLogAnalyticsIndexes(db *gorm.DB) error {
	for _, index := range requestLogAnalyticsIndexes {
		statement := fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON %s (%s)",
			index.name,
			requestLogsTableName,
			index.columns,
		)
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create request-log analytics index %s: %w", index.name, err)
		}
	}
	return nil
}

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

type optionalRequestLogColumn struct {
	name    string
	present bool
}

type requestLogLifecycleMigrationState struct {
	nonWebSocketLifecycleColumns  []optionalRequestLogColumn
	retiredSemanticColumns        []optionalRequestLogColumn
	hasLegacySemanticsColumns     bool
	hasRequestLogSemanticsVersion bool
	hasRequestAttemptSemanticsVer bool
}

func inspectRequestLogLifecycleMigrationState(db *gorm.DB) (requestLogLifecycleMigrationState, error) {
	nonWebSocketLifecycleColumns, err := inspectOptionalRequestLogColumns(
		db,
		sessionCommittedColumnName,
		clientVisibleColumnName,
		commitSourceColumnName,
	)
	if err != nil {
		return requestLogLifecycleMigrationState{}, err
	}

	retiredSemanticColumns, err := inspectOptionalRequestLogColumns(
		db,
		stickyWrittenColumnName,
		probeOutcomeColumnName,
		legacySuccessColumnName,
		legacyStatusCodeColumnName,
		legacyErrorMsgColumnName,
		terminalCauseColumnName,
		recoveryActionColumnName,
	)
	if err != nil {
		return requestLogLifecycleMigrationState{}, err
	}

	hasRequestLogSemanticsVersion, err := requestLogsColumnExists(db, semanticsVersionColumnName)
	if err != nil {
		return requestLogLifecycleMigrationState{}, err
	}
	hasRequestAttemptSemanticsVer, err := requestAttemptsColumnExists(db, semanticsVersionColumnName)
	if err != nil {
		return requestLogLifecycleMigrationState{}, err
	}
	hasLegacySemanticsColumns, err := requestLogLegacySemanticsColumnsPresent(db)
	if err != nil {
		return requestLogLifecycleMigrationState{}, err
	}

	return requestLogLifecycleMigrationState{
		nonWebSocketLifecycleColumns:  nonWebSocketLifecycleColumns,
		retiredSemanticColumns:        retiredSemanticColumns,
		hasLegacySemanticsColumns:     hasLegacySemanticsColumns,
		hasRequestLogSemanticsVersion: hasRequestLogSemanticsVersion,
		hasRequestAttemptSemanticsVer: hasRequestAttemptSemanticsVer,
	}, nil
}

func inspectOptionalRequestLogColumns(db *gorm.DB, columnNames ...string) ([]optionalRequestLogColumn, error) {
	columns := make([]optionalRequestLogColumn, 0, len(columnNames))
	for _, columnName := range columnNames {
		present, err := requestLogsColumnExists(db, columnName)
		if err != nil {
			return nil, err
		}
		columns = append(columns, optionalRequestLogColumn{name: columnName, present: present})
	}
	return columns, nil
}

func (state requestLogLifecycleMigrationState) isNoOp() bool {
	return !state.hasLegacySemanticsColumns &&
		!state.hasRequestLogSemanticsVersion &&
		!state.hasRequestAttemptSemanticsVer &&
		!optionalRequestLogColumnsPresent(state.nonWebSocketLifecycleColumns) &&
		!optionalRequestLogColumnsPresent(state.retiredSemanticColumns)
}

func optionalRequestLogColumnsPresent(columns []optionalRequestLogColumn) bool {
	for _, column := range columns {
		if column.present {
			return true
		}
	}
	return false
}

func applyRequestLogLifecycleMigration(tx *gorm.DB, state requestLogLifecycleMigrationState) error {
	if state.hasLegacySemanticsColumns {
		if err := tagLegacyRequestAssessmentRows(tx, state.hasRequestLogSemanticsVersion, state.hasRequestAttemptSemanticsVer); err != nil {
			return err
		}
	}
	if err := clearOptionalNonWebSocketLifecycleColumns(tx, state.nonWebSocketLifecycleColumns); err != nil {
		return err
	}
	if err := dropOptionalRequestLogColumns(tx, state.retiredSemanticColumns); err != nil {
		return err
	}
	return nil
}

// migrateRequestLogLifecycleFields keeps the cutover explicit: legacy rows are
// tagged once, retired request-log semantics are dropped, and only the
// websocket lifecycle facts that survive in the normalized model remain.
// It deliberately avoids deriving normalized assessment fields from legacy
// booleans or status codes because that would rewrite history with heuristics.
func migrateRequestLogLifecycleFields(db *gorm.DB) error {
	state, err := inspectRequestLogLifecycleMigrationState(db)
	if err != nil {
		return err
	}
	if state.isNoOp() {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		return applyRequestLogLifecycleMigration(tx, state)
	})
}

func clearOptionalNonWebSocketLifecycleColumns(tx *gorm.DB, columns []optionalRequestLogColumn) error {
	for _, column := range columns {
		if err := clearOptionalNonWebSocketLifecycleColumn(tx, column.present, column.name); err != nil {
			return err
		}
	}
	return nil
}

func clearOptionalNonWebSocketLifecycleColumn(tx *gorm.DB, present bool, columnName string) error {
	if !present {
		return nil
	}
	return clearNonWebSocketLifecycleColumn(tx, columnName)
}

func dropOptionalRequestLogColumns(tx *gorm.DB, columns []optionalRequestLogColumn) error {
	for _, column := range columns {
		if !column.present {
			continue
		}
		if err := dropOptionalRequestLogColumn(tx, column.name); err != nil {
			return err
		}
	}
	return nil
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

func requestAttemptsColumnExists(db *gorm.DB, columnName string) (bool, error) {
	return tableColumnExists(db, requestAttemptsTableName, columnName)
}

func requestLogLegacySemanticsColumnsPresent(db *gorm.DB) (bool, error) {
	legacyColumns := []string{
		legacySuccessColumnName,
		legacyStatusCodeColumnName,
		legacyErrorMsgColumnName,
		terminalCauseColumnName,
		recoveryActionColumnName,
	}
	for _, columnName := range legacyColumns {
		present, err := requestLogsColumnExists(db, columnName)
		if err != nil {
			return false, err
		}
		if present {
			return true, nil
		}
	}
	return false, nil
}

func tagLegacyRequestAssessmentRows(tx *gorm.DB, requestLogsReady bool, requestAttemptsReady bool) error {
	if requestLogsReady {
		if err := tx.Exec(
			fmt.Sprintf(
				`UPDATE %s SET %s = ? WHERE %s AND %s`,
				requestLogsTableName,
				semanticsVersionColumnName,
				requestAssessmentNeedsLegacyTagPredicate(semanticsVersionColumnName),
				requestLogMissingNormalizedAssessmentPredicate(),
			),
			string(model.RequestSemanticsVersionLegacyPreAssessment),
			string(model.RequestSemanticsVersionNormalizedV1),
		).Error; err != nil {
			return fmt.Errorf("tag legacy request_logs semantics version: %w", err)
		}
	}
	if requestAttemptsReady {
		statement := fmt.Sprintf(
			`UPDATE %s SET %s = ? WHERE %s`,
			requestAttemptsTableName,
			semanticsVersionColumnName,
			requestAssessmentNeedsLegacyTagPredicate(semanticsVersionColumnName),
		)
		args := []any{
			string(model.RequestSemanticsVersionLegacyPreAssessment),
			string(model.RequestSemanticsVersionNormalizedV1),
		}
		if requestLogsReady {
			statement = fmt.Sprintf(
				`%s AND EXISTS (
					SELECT 1 FROM %s
					WHERE %s.request_id = %s.request_id
					  AND %s
				)`,
				statement,
				requestLogsTableName,
				requestLogsTableName,
				requestAttemptsTableName,
				requestLogMissingNormalizedAssessmentPredicate(),
			)
		}
		if err := tx.Exec(statement, args...).Error; err != nil {
			return fmt.Errorf("tag legacy request_attempts semantics version: %w", err)
		}
	}
	return nil
}

func requestAssessmentNeedsLegacyTagPredicate(columnName string) string {
	return fmt.Sprintf("(%s IS NULL OR %s = '' OR %s = ?)", columnName, columnName, columnName)
}

func requestLogMissingNormalizedAssessmentPredicate() string {
	return strings.Join([]string{
		fmt.Sprintf("%s IS NULL", clientTransportStatusCodeColumn),
		fmt.Sprintf("%s IS NULL", completionStateColumnName),
		fmt.Sprintf("%s IS NULL", serviceOutcomeColumnName),
		fmt.Sprintf("%s IS NULL", clientActionColumnName),
	}, " AND ")
}

func dropOptionalRequestLogColumn(tx *gorm.DB, columnName string) error {
	present, err := requestLogsColumnExists(tx, columnName)
	if err != nil {
		return fmt.Errorf("check request_logs.%s: %w", columnName, err)
	}
	if !present {
		return nil
	}
	if err := tx.Exec(
		fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, requestLogsTableName, columnName),
	).Error; err != nil {
		return fmt.Errorf("drop request_logs.%s: %w", columnName, err)
	}
	return nil
}

// migrateRoutingPolicyLifecycleStorage adds lifecycle/reference columns without
// dropping the natural-key uniqueness that older builds already relied on.
func migrateRoutingPolicyLifecycleStorage(db *gorm.DB) error {
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

// migrateProviderUsageLimitPolicyStorage rewrites rows that accidentally
// persisted a credential-derived default back to the empty "inherit default"
// representation so later credential_type changes can recompute the effective
// policy without clobbering explicit overrides.
func migrateProviderUsageLimitPolicyStorage(db *gorm.DB) error {
	present, err := tableColumnExists(db, providersTableName, usageLimitPolicyColumnName)
	if err != nil {
		return err
	}
	if !present {
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
			string(model.ProviderCredentialTypeAPIKey),
			string(model.ProviderCredentialTypeChatGPT),
			string(model.ProviderUsageLimitPolicySuspend),
			string(model.ProviderCredentialTypeAPIKey),
			string(model.ProviderCredentialTypeChatGPT),
			string(model.ProviderUsageLimitPolicySwitchProvider),
		).Error
	})
}
