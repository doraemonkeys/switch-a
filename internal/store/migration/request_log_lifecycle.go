package migration

import (
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

const (
	requestAttemptsTableName        = "request_attempts"
	legacyWebSocketColumnName       = "is_web_socket"
	webSocketColumnName             = "is_websocket"
	semanticsVersionColumnName      = "semantics_version"
	clientTransportStatusCodeColumn = "client_transport_status_code"
	completionStateColumnName       = "completion_state"
	serviceOutcomeColumnName        = "service_outcome"
	terminationActorColumnName      = "termination_actor"
	terminationReasonColumnName     = "termination_reason"
	clientActionColumnName          = "client_action"
	sessionEvidenceJSONColumnName   = "session_evidence_json"
	attemptEvidenceJSONColumnName   = "attempt_evidence_json"
	sessionCommittedColumnName      = "session_committed"
	clientVisibleColumnName         = "client_visible"
	stickyWrittenColumnName         = "sticky_written"
	probeOutcomeColumnName          = "probe_outcome"
	legacySuccessColumnName         = "success"
	legacyStatusCodeColumnName      = "status_code"
	legacyErrorMsgColumnName        = "error_msg"
	terminalCauseColumnName         = "terminal_cause"
	commitSourceColumnName          = "commit_source"
	recoveryActionColumnName        = "recovery_action"
	legacySuccessValue              = 1
)

// MigrateWebSocketColumn copies data from the legacy is_web_socket column
// (GORM's default snake_case for IsWebSocket) to the explicit is_websocket column.
// Idempotent: skips if the legacy column does not exist.
func MigrateWebSocketColumn(db *gorm.DB) error {
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

// MigrateRequestLogLifecycleFields keeps the cutover explicit: legacy rows are
// tagged once, retired request-log semantics are dropped, and only the
// websocket lifecycle facts that survive in the normalized model remain.
// It deliberately avoids deriving normalized assessment fields from legacy
// booleans or status codes because that would rewrite history with heuristics.
func MigrateRequestLogLifecycleFields(db *gorm.DB) error {
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

// migrateOptionalWebSocketLifecycleColumn accepts inspected presence explicitly
// so callers can preserve the surrounding schema transaction and avoid a second,
// potentially inconsistent metadata read.
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
