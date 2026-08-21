package migration

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

func TestMigrateRequestLogLifecycleFields_NoRelevantColumnsIsNoOp(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_id TEXT,
		created_at DATETIME
	)`, requestLogsTableName)).Error; err != nil {
		t.Fatalf("create request_logs table: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (provider_id, created_at) VALUES (?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"legacy",
	).Error; err != nil {
		t.Fatalf("seed request_logs: %v", err)
	}

	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields() error = %v", err)
	}

	var count int64
	if err := db.Table(requestLogsTableName).Count(&count).Error; err != nil {
		t.Fatalf("count request_logs rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestMigrateRequestLogLifecycleFields_TagsLegacyRowsAndDropsLegacySemantics(t *testing.T) {
	t.Parallel()

	db := openMigrationSQLiteDB(t, "request_log_semantics_cutover.db")
	createLegacyRequestAssessmentTables(t, db)
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, provider_id, is_websocket, success, status_code, error_msg, terminal_cause, recovery_action, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"req-1",
		"provider-a",
		true,
		true,
		101,
		"",
		"clean_close",
		"none",
	).Error; err != nil {
		t.Fatalf("seed legacy request_log: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, provider_id, attempt, status_code, error, created_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestAttemptsTableName),
		"req-1",
		"provider-a",
		1,
		101,
		"",
	).Error; err != nil {
		t.Fatalf("seed legacy request_attempt: %v", err)
	}

	if err := db.AutoMigrate(&model.RequestLog{}, &model.RequestAttempt{}); err != nil {
		t.Fatalf("auto-migrate request assessment tables: %v", err)
	}
	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields() error = %v", err)
	}

	type requestLogRow struct {
		SemanticsVersion          string
		ClientTransportStatusCode sql.NullInt64
		CompletionState           sql.NullString
		ServiceOutcome            sql.NullString
		TerminationActor          sql.NullString
		TerminationReason         sql.NullString
		ClientAction              sql.NullString
		SessionEvidenceJSON       sql.NullString
	}

	var logRow requestLogRow
	if err := db.Raw(
		fmt.Sprintf(`SELECT
			semantics_version,
			client_transport_status_code,
			completion_state,
			service_outcome,
			termination_actor,
			termination_reason,
			client_action,
			session_evidence_json
		FROM %s WHERE request_id = ?`, requestLogsTableName),
		"req-1",
	).Scan(&logRow).Error; err != nil {
		t.Fatalf("read migrated request_log: %v", err)
	}
	if logRow.SemanticsVersion != string(model.RequestSemanticsVersionLegacyPreAssessment) {
		t.Fatalf("request_log semantics_version = %q, want %q", logRow.SemanticsVersion, model.RequestSemanticsVersionLegacyPreAssessment)
	}
	if logRow.ClientTransportStatusCode.Valid {
		t.Fatalf("client_transport_status_code = %+v, want NULL on legacy row", logRow.ClientTransportStatusCode)
	}
	if logRow.CompletionState.Valid {
		t.Fatalf("completion_state = %+v, want NULL on legacy row", logRow.CompletionState)
	}
	if logRow.ServiceOutcome.Valid {
		t.Fatalf("service_outcome = %+v, want NULL on legacy row", logRow.ServiceOutcome)
	}
	if logRow.TerminationActor.Valid {
		t.Fatalf("termination_actor = %+v, want NULL on legacy row", logRow.TerminationActor)
	}
	if logRow.TerminationReason.Valid {
		t.Fatalf("termination_reason = %+v, want NULL on legacy row", logRow.TerminationReason)
	}
	if logRow.ClientAction.Valid {
		t.Fatalf("client_action = %+v, want NULL on legacy row", logRow.ClientAction)
	}
	if logRow.SessionEvidenceJSON.Valid {
		t.Fatalf("session_evidence_json = %+v, want NULL on legacy row", logRow.SessionEvidenceJSON)
	}

	type requestAttemptRow struct {
		SemanticsVersion    string
		AttemptEvidenceJSON sql.NullString
	}

	var attemptRow requestAttemptRow
	if err := db.Raw(
		fmt.Sprintf(`SELECT semantics_version, attempt_evidence_json FROM %s WHERE request_id = ?`, requestAttemptsTableName),
		"req-1",
	).Scan(&attemptRow).Error; err != nil {
		t.Fatalf("read migrated request_attempt: %v", err)
	}
	if attemptRow.SemanticsVersion != string(model.RequestSemanticsVersionLegacyPreAssessment) {
		t.Fatalf("request_attempt semantics_version = %q, want %q", attemptRow.SemanticsVersion, model.RequestSemanticsVersionLegacyPreAssessment)
	}
	if attemptRow.AttemptEvidenceJSON.Valid {
		t.Fatalf("attempt_evidence_json = %+v, want NULL on legacy row", attemptRow.AttemptEvidenceJSON)
	}

	assertTableColumnMissing(t, db, requestLogsTableName, legacySuccessColumnName)
	assertTableColumnMissing(t, db, requestLogsTableName, legacyStatusCodeColumnName)
	assertTableColumnMissing(t, db, requestLogsTableName, legacyErrorMsgColumnName)
	assertTableColumnMissing(t, db, requestLogsTableName, terminalCauseColumnName)
	assertTableColumnMissing(t, db, requestLogsTableName, recoveryActionColumnName)
}

func TestMigrateRequestLogLifecycleFields_PreservesExplicitWebSocketLifecycleAndClearsRegularRows(t *testing.T) {
	t.Parallel()

	db := setupRequestLogLifecycleMigrationDB(t)
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (
			request_id,
			provider_id,
			semantics_version,
			is_websocket,
			session_committed,
			client_visible,
			sticky_written,
			probe_outcome,
			commit_source,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"regular",
		"provider-regular",
		string(model.RequestSemanticsVersionNormalizedV1),
		false,
		true,
		true,
		true,
		string(model.WebSocketProbeOutcomeTransportFailed),
		string(model.CommitUnknown),
	).Error; err != nil {
		t.Fatalf("seed regular row: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (
			request_id,
			provider_id,
			semantics_version,
			is_websocket,
			session_committed,
			client_visible,
			sticky_written,
			probe_outcome,
			commit_source,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"websocket",
		"provider-websocket",
		string(model.RequestSemanticsVersionNormalizedV1),
		true,
		true,
		true,
		false,
		string(model.WebSocketProbeOutcomeObservedUsableModel),
		string(model.CommitSemantic),
	).Error; err != nil {
		t.Fatalf("seed websocket row: %v", err)
	}

	if err := db.AutoMigrate(&model.RequestLog{}); err != nil {
		t.Fatalf("auto-migrate request_logs: %v", err)
	}
	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields() error = %v", err)
	}

	type lifecycleRow struct {
		RequestID        string
		SemanticsVersion string
		SessionCommitted sql.NullBool
		ClientVisible    sql.NullBool
		CommitSource     sql.NullString
	}

	var rows []lifecycleRow
	if err := db.Raw(
		fmt.Sprintf(`SELECT
			request_id,
			semantics_version,
			session_committed,
			client_visible,
			commit_source
		FROM %s ORDER BY request_id`, requestLogsTableName),
	).Scan(&rows).Error; err != nil {
		t.Fatalf("read lifecycle rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}

	if rows[0].SemanticsVersion != string(model.RequestSemanticsVersionNormalizedV1) {
		t.Fatalf("regular semantics_version = %q, want %q", rows[0].SemanticsVersion, model.RequestSemanticsVersionNormalizedV1)
	}
	if rows[0].SessionCommitted.Valid {
		t.Fatalf("regular session_committed = %+v, want NULL", rows[0].SessionCommitted)
	}
	if rows[0].ClientVisible.Valid {
		t.Fatalf("regular client_visible = %+v, want NULL", rows[0].ClientVisible)
	}
	if rows[0].CommitSource.Valid {
		t.Fatalf("regular commit_source = %+v, want NULL", rows[0].CommitSource)
	}

	if rows[1].SemanticsVersion != string(model.RequestSemanticsVersionNormalizedV1) {
		t.Fatalf("websocket semantics_version = %q, want %q", rows[1].SemanticsVersion, model.RequestSemanticsVersionNormalizedV1)
	}
	if !rows[1].SessionCommitted.Valid || !rows[1].SessionCommitted.Bool {
		t.Fatalf("websocket session_committed = %+v, want true", rows[1].SessionCommitted)
	}
	if !rows[1].ClientVisible.Valid || !rows[1].ClientVisible.Bool {
		t.Fatalf("websocket client_visible = %+v, want true", rows[1].ClientVisible)
	}
	if !rows[1].CommitSource.Valid || rows[1].CommitSource.String != string(model.CommitSemantic) {
		t.Fatalf("websocket commit_source = %+v, want %q", rows[1].CommitSource, model.CommitSemantic)
	}

	assertTableColumnMissing(t, db, requestLogsTableName, stickyWrittenColumnName)
	assertTableColumnMissing(t, db, requestLogsTableName, probeOutcomeColumnName)
}

func TestMigrateRequestLogLifecycleFields_SkipsRetaggingAfterLegacyColumnsAreGone(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.AutoMigrate(&model.RequestLog{}, &model.RequestAttempt{}); err != nil {
		t.Fatalf("auto-migrate request assessment tables: %v", err)
	}

	attemptEvidenceJSON := `{"gateway":{"terminal_status_code":429}}`
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (
			request_id,
			provider_id,
			semantics_version,
			created_at
		) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"req-normalized",
		"provider-a",
		string(model.RequestSemanticsVersionNormalizedV1),
	).Error; err != nil {
		t.Fatalf("seed normalized request_log: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (
			request_id,
			provider_id,
			semantics_version,
			attempt,
			status_code,
			error,
			attempt_evidence_json,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestAttemptsTableName),
		"req-normalized",
		"provider-a",
		string(model.RequestSemanticsVersionNormalizedV1),
		1,
		429,
		"quota",
		attemptEvidenceJSON,
	).Error; err != nil {
		t.Fatalf("seed normalized request_attempt: %v", err)
	}

	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields() error = %v", err)
	}

	var requestLogSemantics string
	if err := db.Raw(
		fmt.Sprintf(`SELECT semantics_version FROM %s WHERE request_id = ?`, requestLogsTableName),
		"req-normalized",
	).Scan(&requestLogSemantics).Error; err != nil {
		t.Fatalf("read normalized request_log: %v", err)
	}
	if requestLogSemantics != string(model.RequestSemanticsVersionNormalizedV1) {
		t.Fatalf("request_log semantics_version = %q, want %q", requestLogSemantics, model.RequestSemanticsVersionNormalizedV1)
	}

	type persistedAttemptRow struct {
		SemanticsVersion    string
		AttemptEvidenceJSON string
	}

	var persistedAttempt persistedAttemptRow
	if err := db.Raw(
		fmt.Sprintf(`SELECT semantics_version, attempt_evidence_json FROM %s WHERE request_id = ?`, requestAttemptsTableName),
		"req-normalized",
	).Scan(&persistedAttempt).Error; err != nil {
		t.Fatalf("read normalized request_attempt: %v", err)
	}
	if persistedAttempt.SemanticsVersion != string(model.RequestSemanticsVersionNormalizedV1) {
		t.Fatalf("request_attempt semantics_version = %q, want %q", persistedAttempt.SemanticsVersion, model.RequestSemanticsVersionNormalizedV1)
	}
	if persistedAttempt.AttemptEvidenceJSON != attemptEvidenceJSON {
		t.Fatalf("attempt_evidence_json = %q, want %q", persistedAttempt.AttemptEvidenceJSON, attemptEvidenceJSON)
	}
}

func TestMigrateRequestLogLifecycleFields_RowLevelTaggingPreservesNormalizedRows(t *testing.T) {
	t.Parallel()

	db := openMigrationSQLiteDB(t, "request_log_row_level_cutover.db")
	createLegacyRequestAssessmentTables(t, db)
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, provider_id, is_websocket, success, status_code, error_msg, terminal_cause, recovery_action, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"req-legacy",
		"provider-a",
		true,
		false,
		429,
		"quota",
		"usage_limit_reached",
		"reconnect_required",
	).Error; err != nil {
		t.Fatalf("seed legacy request_log: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, provider_id, attempt, status_code, error, created_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestAttemptsTableName),
		"req-legacy",
		"provider-a",
		1,
		429,
		"quota",
	).Error; err != nil {
		t.Fatalf("seed legacy request_attempt: %v", err)
	}

	if err := db.AutoMigrate(&model.RequestLog{}, &model.RequestAttempt{}); err != nil {
		t.Fatalf("auto-migrate request assessment tables: %v", err)
	}

	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (
			request_id,
			provider_id,
			semantics_version,
			client_transport_status_code,
			completion_state,
			service_outcome,
			client_action,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"req-normalized",
		"provider-b",
		string(model.RequestSemanticsVersionNormalizedV1),
		http.StatusOK,
		string(model.CompletionStateCompleted),
		string(model.ServiceOutcomeCompleted),
		string(model.ClientActionNone),
	).Error; err != nil {
		t.Fatalf("seed normalized request_log: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (
			request_id,
			provider_id,
			semantics_version,
			attempt,
			status_code,
			created_at
		) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestAttemptsTableName),
		"req-normalized",
		"provider-b",
		string(model.RequestSemanticsVersionNormalizedV1),
		1,
		http.StatusOK,
	).Error; err != nil {
		t.Fatalf("seed normalized request_attempt: %v", err)
	}

	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields() error = %v", err)
	}

	type semanticsRow struct {
		RequestID        string
		SemanticsVersion string
	}

	var requestLogRows []semanticsRow
	if err := db.Raw(
		fmt.Sprintf(`SELECT request_id, semantics_version FROM %s ORDER BY request_id`, requestLogsTableName),
	).Scan(&requestLogRows).Error; err != nil {
		t.Fatalf("read request_log semantics rows: %v", err)
	}
	if len(requestLogRows) != 2 {
		t.Fatalf("request_log row count = %d, want 2", len(requestLogRows))
	}
	if requestLogRows[0].RequestID != "req-legacy" || requestLogRows[0].SemanticsVersion != string(model.RequestSemanticsVersionLegacyPreAssessment) {
		t.Fatalf("legacy request_log = %+v, want req-legacy tagged legacy_pre_assessment", requestLogRows[0])
	}
	if requestLogRows[1].RequestID != "req-normalized" || requestLogRows[1].SemanticsVersion != string(model.RequestSemanticsVersionNormalizedV1) {
		t.Fatalf("normalized request_log = %+v, want req-normalized preserved as normalized_v1", requestLogRows[1])
	}

	var requestAttemptRows []semanticsRow
	if err := db.Raw(
		fmt.Sprintf(`SELECT request_id, semantics_version FROM %s ORDER BY request_id`, requestAttemptsTableName),
	).Scan(&requestAttemptRows).Error; err != nil {
		t.Fatalf("read request_attempt semantics rows: %v", err)
	}
	if len(requestAttemptRows) != 2 {
		t.Fatalf("request_attempt row count = %d, want 2", len(requestAttemptRows))
	}
	if requestAttemptRows[0].RequestID != "req-legacy" || requestAttemptRows[0].SemanticsVersion != string(model.RequestSemanticsVersionLegacyPreAssessment) {
		t.Fatalf("legacy request_attempt = %+v, want req-legacy tagged legacy_pre_assessment", requestAttemptRows[0])
	}
	if requestAttemptRows[1].RequestID != "req-normalized" || requestAttemptRows[1].SemanticsVersion != string(model.RequestSemanticsVersionNormalizedV1) {
		t.Fatalf("normalized request_attempt = %+v, want req-normalized preserved as normalized_v1", requestAttemptRows[1])
	}
}

func TestMigrateRequestLogLifecycleFields_PartialCutoverTagsOnlyReadyTables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		autoMigrateRequestLogs bool
		autoMigrateAttempts    bool
	}{
		{
			name:                   "request logs ready",
			autoMigrateRequestLogs: true,
		},
		{
			name:                "request attempts ready",
			autoMigrateAttempts: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openMigrationSQLiteDB(t, "request_log_partial_cutover.db")
			createLegacyRequestAssessmentTables(t, db)
			if err := db.Exec(
				fmt.Sprintf(`INSERT INTO %s (request_id, provider_id, is_websocket, success, status_code, error_msg, terminal_cause, recovery_action, created_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
				"req-1",
				"provider-a",
				true,
				false,
				429,
				"quota",
				"usage_limit_reached",
				"reconnect_required",
			).Error; err != nil {
				t.Fatalf("seed legacy request_log: %v", err)
			}
			if err := db.Exec(
				fmt.Sprintf(`INSERT INTO %s (request_id, provider_id, attempt, status_code, error, created_at)
					VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, requestAttemptsTableName),
				"req-1",
				"provider-a",
				1,
				429,
				"quota",
			).Error; err != nil {
				t.Fatalf("seed legacy request_attempt: %v", err)
			}

			if tt.autoMigrateRequestLogs {
				if err := db.AutoMigrate(&model.RequestLog{}); err != nil {
					t.Fatalf("auto-migrate request_logs: %v", err)
				}
			}
			if tt.autoMigrateAttempts {
				if err := db.AutoMigrate(&model.RequestAttempt{}); err != nil {
					t.Fatalf("auto-migrate request_attempts: %v", err)
				}
			}

			if err := migrateRequestLogLifecycleFields(db); err != nil {
				t.Fatalf("migrateRequestLogLifecycleFields() error = %v", err)
			}

			requestLogsReady, err := tableColumnExists(db, requestLogsTableName, semanticsVersionColumnName)
			if err != nil {
				t.Fatalf("check request_logs semantics_version column: %v", err)
			}
			if requestLogsReady != tt.autoMigrateRequestLogs {
				t.Fatalf("request_logs semantics_version presence = %t, want %t", requestLogsReady, tt.autoMigrateRequestLogs)
			}
			if tt.autoMigrateRequestLogs {
				var requestLogSemantics string
				if err := db.Raw(
					fmt.Sprintf(`SELECT semantics_version FROM %s WHERE request_id = ?`, requestLogsTableName),
					"req-1",
				).Scan(&requestLogSemantics).Error; err != nil {
					t.Fatalf("read migrated request_log semantics_version: %v", err)
				}
				if requestLogSemantics != string(model.RequestSemanticsVersionLegacyPreAssessment) {
					t.Fatalf("request_log semantics_version = %q, want %q", requestLogSemantics, model.RequestSemanticsVersionLegacyPreAssessment)
				}
			}

			requestAttemptsReady, err := tableColumnExists(db, requestAttemptsTableName, semanticsVersionColumnName)
			if err != nil {
				t.Fatalf("check request_attempts semantics_version column: %v", err)
			}
			if requestAttemptsReady != tt.autoMigrateAttempts {
				t.Fatalf("request_attempts semantics_version presence = %t, want %t", requestAttemptsReady, tt.autoMigrateAttempts)
			}
			if tt.autoMigrateAttempts {
				var requestAttemptSemantics string
				if err := db.Raw(
					fmt.Sprintf(`SELECT semantics_version FROM %s WHERE request_id = ?`, requestAttemptsTableName),
					"req-1",
				).Scan(&requestAttemptSemantics).Error; err != nil {
					t.Fatalf("read migrated request_attempt semantics_version: %v", err)
				}
				if requestAttemptSemantics != string(model.RequestSemanticsVersionLegacyPreAssessment) {
					t.Fatalf("request_attempt semantics_version = %q, want %q", requestAttemptSemantics, model.RequestSemanticsVersionLegacyPreAssessment)
				}
			}

			assertTableColumnMissing(t, db, requestLogsTableName, legacySuccessColumnName)
			assertTableColumnMissing(t, db, requestLogsTableName, legacyStatusCodeColumnName)
			assertTableColumnMissing(t, db, requestLogsTableName, legacyErrorMsgColumnName)
			assertTableColumnMissing(t, db, requestLogsTableName, terminalCauseColumnName)
			assertTableColumnMissing(t, db, requestLogsTableName, recoveryActionColumnName)
		})
	}
}

func TestMigrateOptionalWebSocketLifecycleColumn_ClearsRegularRowsAndBackfillsMissingWebSocketFacts(t *testing.T) {
	t.Parallel()

	db := setupRequestLogLifecycleMigrationDB(t)
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, is_websocket, session_committed, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"regular",
		false,
		true,
	).Error; err != nil {
		t.Fatalf("seed regular row: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, is_websocket, session_committed, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"websocket-explicit",
		true,
		false,
	).Error; err != nil {
		t.Fatalf("seed explicit websocket row: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, is_websocket, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"websocket-missing",
		true,
	).Error; err != nil {
		t.Fatalf("seed missing websocket row: %v", err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		// Missing websocket lifecycle evidence should be backfilled only when the
		// predicate matches, while non-websocket rows are cleared because the
		// normalized model keeps these facts websocket-only.
		return migrateOptionalWebSocketLifecycleColumn(
			tx,
			true,
			sessionCommittedColumnName,
			true,
			fmt.Sprintf("%s IS NULL", sessionCommittedColumnName),
		)
	}); err != nil {
		t.Fatalf("migrateOptionalWebSocketLifecycleColumn() error = %v", err)
	}

	type lifecycleRow struct {
		RequestID        string
		SessionCommitted sql.NullBool
	}

	var rows []lifecycleRow
	if err := db.Raw(
		fmt.Sprintf(`SELECT request_id, session_committed FROM %s ORDER BY request_id`, requestLogsTableName),
	).Scan(&rows).Error; err != nil {
		t.Fatalf("read lifecycle rows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("row count = %d, want 3", len(rows))
	}

	rowsByRequestID := make(map[string]sql.NullBool, len(rows))
	for _, row := range rows {
		rowsByRequestID[row.RequestID] = row.SessionCommitted
	}

	if got := rowsByRequestID["regular"]; got.Valid {
		t.Fatalf("regular session_committed = %+v, want NULL", got)
	}
	if got := rowsByRequestID["websocket-explicit"]; !got.Valid || got.Bool {
		t.Fatalf("websocket-explicit session_committed = %+v, want false", got)
	}
	if got := rowsByRequestID["websocket-missing"]; !got.Valid || !got.Bool {
		t.Fatalf("websocket-missing session_committed = %+v, want true", got)
	}
}

func TestMigrateOptionalWebSocketLifecycleColumn_SkipsWhenColumnIsMarkedAbsent(t *testing.T) {
	t.Parallel()

	db := setupRequestLogLifecycleMigrationDB(t)
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (request_id, is_websocket, session_committed, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, requestLogsTableName),
		"regular",
		false,
		true,
	).Error; err != nil {
		t.Fatalf("seed regular row: %v", err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return migrateOptionalWebSocketLifecycleColumn(
			tx,
			false,
			sessionCommittedColumnName,
			true,
			fmt.Sprintf("%s IS NULL", sessionCommittedColumnName),
		)
	}); err != nil {
		t.Fatalf("migrateOptionalWebSocketLifecycleColumn() error = %v", err)
	}

	var persisted sql.NullBool
	if err := db.Raw(
		fmt.Sprintf(`SELECT session_committed FROM %s WHERE request_id = ?`, requestLogsTableName),
		"regular",
	).Scan(&persisted).Error; err != nil {
		t.Fatalf("read persisted session_committed: %v", err)
	}
	if !persisted.Valid || !persisted.Bool {
		t.Fatalf("session_committed = %+v, want true", persisted)
	}
}

func createLegacyRequestAssessmentTables(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT,
		provider_id TEXT,
		is_websocket BOOLEAN DEFAULT 0,
		success BOOLEAN DEFAULT 0,
		status_code INTEGER DEFAULT 0,
		error_msg TEXT DEFAULT '',
		terminal_cause TEXT,
		recovery_action TEXT,
		created_at DATETIME
	)`, requestLogsTableName)).Error; err != nil {
		t.Fatalf("create legacy request_logs table: %v", err)
	}
	if err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT,
		provider_id TEXT,
		attempt INTEGER DEFAULT 0,
		status_code INTEGER DEFAULT 0,
		error TEXT DEFAULT '',
		created_at DATETIME
	)`, requestAttemptsTableName)).Error; err != nil {
		t.Fatalf("create legacy request_attempts table: %v", err)
	}
}

func assertTableColumnMissing(t *testing.T, db *gorm.DB, tableName, columnName string) {
	t.Helper()

	present, err := tableColumnExists(db, tableName, columnName)
	if err != nil {
		t.Fatalf("check %s.%s: %v", tableName, columnName, err)
	}
	if present {
		t.Fatalf("%s.%s should have been dropped", tableName, columnName)
	}
}
