package store

import (
	"database/sql"
	"fmt"
	"testing"

	"switch-a/internal/model"
)

func TestMigrateOptionalWebSocketLifecycleColumn_PresentClearsRegularRowsAndBackfillsWebSocketRows(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_websocket BOOLEAN DEFAULT 0,
		session_committed BOOLEAN
	)`).Error; err != nil {
		t.Fatalf("create request_logs table: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO request_logs (is_websocket, session_committed) VALUES (?, ?), (?, ?), (?, ?)`,
		true, nil,
		true, true,
		false, true,
	).Error; err != nil {
		t.Fatalf("seed request_logs: %v", err)
	}

	if err := migrateOptionalWebSocketLifecycleColumn(
		db,
		true,
		sessionCommittedColumnName,
		true,
		fmt.Sprintf("%s IS NULL", sessionCommittedColumnName),
	); err != nil {
		t.Fatalf("migrateOptionalWebSocketLifecycleColumn() error = %v", err)
	}

	type lifecycleRow struct {
		ID               int
		IsWebSocket      bool
		SessionCommitted sql.NullBool
	}

	var rows []lifecycleRow
	if err := db.Raw(
		`SELECT id, is_websocket, session_committed FROM request_logs ORDER BY id`,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("load request_logs rows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	if !rows[0].SessionCommitted.Valid || !rows[0].SessionCommitted.Bool {
		t.Fatalf("rows[0].SessionCommitted = %+v, want true backfill", rows[0].SessionCommitted)
	}
	if !rows[1].SessionCommitted.Valid || !rows[1].SessionCommitted.Bool {
		t.Fatalf("rows[1].SessionCommitted = %+v, want preserved true", rows[1].SessionCommitted)
	}
	if rows[2].SessionCommitted.Valid {
		t.Fatalf("rows[2].SessionCommitted = %+v, want NULL for regular row", rows[2].SessionCommitted)
	}
}

func TestMigrateOptionalWebSocketLifecycleColumn_AbsentColumnIsNoOp(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_websocket BOOLEAN DEFAULT 0
	)`).Error; err != nil {
		t.Fatalf("create request_logs table: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_logs (is_websocket) VALUES (?)`, true).Error; err != nil {
		t.Fatalf("seed request_logs: %v", err)
	}

	if err := migrateOptionalWebSocketLifecycleColumn(
		db,
		false,
		sessionCommittedColumnName,
		true,
		"1 = 1",
	); err != nil {
		t.Fatalf("migrateOptionalWebSocketLifecycleColumn(absent) error = %v", err)
	}

	var count int64
	if err := db.Table(requestLogsTableName).Count(&count).Error; err != nil {
		t.Fatalf("count request_logs rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestMigrateRequestLogLifecycleFields_NoLifecycleColumns(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_id TEXT,
		is_websocket BOOLEAN DEFAULT 0,
		success BOOLEAN DEFAULT 0
	)`, requestLogsTableName)).Error; err != nil {
		t.Fatalf("create legacy request_logs table: %v", err)
	}
	if err := db.Exec(
		fmt.Sprintf(`INSERT INTO %s (provider_id, is_websocket, success) VALUES (?, ?, ?)`, requestLogsTableName),
		"legacy",
		true,
		true,
	).Error; err != nil {
		t.Fatalf("seed legacy request log: %v", err)
	}

	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields(no lifecycle columns) error = %v", err)
	}

	var count int64
	if err := db.Table(requestLogsTableName).Count(&count).Error; err != nil {
		t.Fatalf("count request_logs rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestMigrateRequestLogLifecycleFields_BackfillsHistoricalDefaults(t *testing.T) {
	t.Parallel()

	db := setupRequestLogLifecycleMigrationDB(t)

	insertLegacyRowSQL := fmt.Sprintf(
		`INSERT INTO %s (provider_id, is_websocket, success, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		requestLogsTableName,
	)
	if err := db.Exec(insertLegacyRowSQL, "websocket-success", true, true).Error; err != nil {
		t.Fatalf("seed websocket row: %v", err)
	}
	if err := db.Exec(insertLegacyRowSQL, "regular-success", false, true).Error; err != nil {
		t.Fatalf("seed regular row: %v", err)
	}

	if err := db.AutoMigrate(&model.RequestLog{}); err != nil {
		t.Fatalf("auto-migrate request log: %v", err)
	}
	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields error: %v", err)
	}

	type lifecycleRow struct {
		ProviderID       string
		SessionCommitted sql.NullBool
		ClientVisible    sql.NullBool
		StickyWritten    sql.NullBool
		ProbeOutcome     sql.NullString
		TerminalCause    sql.NullString
		CommitSource     sql.NullString
		RecoveryAction   sql.NullString
	}

	var rows []lifecycleRow
	querySQL := fmt.Sprintf(
		`SELECT provider_id, session_committed, client_visible, sticky_written, probe_outcome, terminal_cause, commit_source, recovery_action FROM %s ORDER BY provider_id`,
		requestLogsTableName,
	)
	if err := db.Raw(querySQL).Scan(&rows).Error; err != nil {
		t.Fatalf("read lifecycle rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}

	if rows[0].SessionCommitted.Valid {
		t.Fatalf("regular row session_committed = %+v, want NULL", rows[0].SessionCommitted)
	}
	if rows[0].StickyWritten.Valid {
		t.Fatalf("regular row sticky_written = %+v, want NULL", rows[0].StickyWritten)
	}
	if rows[0].ClientVisible.Valid {
		t.Fatalf("regular row client_visible = %+v, want NULL", rows[0].ClientVisible)
	}
	if rows[0].ProbeOutcome.Valid {
		t.Fatalf("regular row probe_outcome = %+v, want NULL", rows[0].ProbeOutcome)
	}
	if rows[0].TerminalCause.Valid {
		t.Fatalf("regular row terminal_cause = %+v, want NULL", rows[0].TerminalCause)
	}
	if rows[0].CommitSource.Valid {
		t.Fatalf("regular row commit_source = %+v, want NULL", rows[0].CommitSource)
	}
	if rows[0].RecoveryAction.Valid {
		t.Fatalf("regular row recovery_action = %+v, want NULL", rows[0].RecoveryAction)
	}

	if !rows[1].SessionCommitted.Valid || !rows[1].SessionCommitted.Bool {
		t.Fatalf("websocket row session_committed = %+v, want true", rows[1].SessionCommitted)
	}
	if !rows[1].StickyWritten.Valid || rows[1].StickyWritten.Bool {
		t.Fatalf("websocket row sticky_written = %+v, want false", rows[1].StickyWritten)
	}
	if rows[1].ClientVisible.Valid {
		t.Fatalf("websocket row client_visible = %+v, want NULL", rows[1].ClientVisible)
	}
	if !rows[1].ProbeOutcome.Valid || rows[1].ProbeOutcome.String != string(model.WebSocketProbeOutcomeUnknown) {
		t.Fatalf("websocket row probe_outcome = %+v, want %q", rows[1].ProbeOutcome, model.WebSocketProbeOutcomeUnknown)
	}
	if !rows[1].TerminalCause.Valid || rows[1].TerminalCause.String != string(model.TerminalUnknown) {
		t.Fatalf("websocket row terminal_cause = %+v, want %q", rows[1].TerminalCause, model.TerminalUnknown)
	}
	if !rows[1].CommitSource.Valid || rows[1].CommitSource.String != string(model.CommitUnknown) {
		t.Fatalf("websocket row commit_source = %+v, want %q", rows[1].CommitSource, model.CommitUnknown)
	}
	if rows[1].RecoveryAction.Valid {
		t.Fatalf("websocket row recovery_action = %+v, want NULL", rows[1].RecoveryAction)
	}
}

func TestMigrateRequestLogLifecycleFields_PreservesKnownValues(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.AutoMigrate(&model.RequestLog{}); err != nil {
		t.Fatalf("auto-migrate request log: %v", err)
	}

	committed := true
	uncommitted := false
	logs := []model.RequestLog{
		{
			IsWebSocket:      true,
			ProviderID:       "clean-close",
			Success:          true,
			StickyWritten:    boolPtr(true),
			SessionCommitted: &committed,
			ClientVisible:    boolPtr(true),
			ProbeOutcome:     probeOutcomePtr(model.WebSocketProbeOutcomeObservedUsableModel),
			TerminalCause:    terminalCausePtr(model.TerminalCleanClose),
			CommitSource:     commitSourcePtr(model.CommitSemantic),
			RecoveryAction:   recoveryActionPtr(model.RecoveryActionNone),
		},
		{
			IsWebSocket:      true,
			ProviderID:       "semantic-error",
			Success:          false,
			StickyWritten:    boolPtr(false),
			SessionCommitted: &uncommitted,
			ClientVisible:    boolPtr(true),
			ProbeOutcome:     probeOutcomePtr(model.WebSocketProbeOutcomeTransportFailed),
			TerminalCause:    terminalCausePtr(model.TerminalUpstreamSemanticError),
			CommitSource:     commitSourcePtr(model.CommitUnknown),
			RecoveryAction:   recoveryActionPtr(model.RecoveryActionReconnectRequired),
		},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatalf("seed log %q: %v", logs[i].ProviderID, err)
		}
	}

	if err := migrateRequestLogLifecycleFields(db); err != nil {
		t.Fatalf("migrateRequestLogLifecycleFields error: %v", err)
	}

	var persisted []model.RequestLog
	if err := db.Order("provider_id ASC").Find(&persisted).Error; err != nil {
		t.Fatalf("read logs: %v", err)
	}
	if len(persisted) != len(logs) {
		t.Fatalf("log count = %d, want %d", len(persisted), len(logs))
	}

	if persisted[0].SessionCommitted == nil || !*persisted[0].SessionCommitted {
		t.Fatalf("clean-close session_committed = %v, want true", persisted[0].SessionCommitted)
	}
	if persisted[0].StickyWritten == nil || !*persisted[0].StickyWritten {
		t.Fatalf("clean-close sticky_written = %v, want true", persisted[0].StickyWritten)
	}
	if persisted[0].ClientVisible == nil || !*persisted[0].ClientVisible {
		t.Fatalf("clean-close client_visible = %v, want true", persisted[0].ClientVisible)
	}
	if persisted[0].ProbeOutcome == nil || *persisted[0].ProbeOutcome != model.WebSocketProbeOutcomeObservedUsableModel {
		t.Fatalf("clean-close probe_outcome = %v, want %q", persisted[0].ProbeOutcome, model.WebSocketProbeOutcomeObservedUsableModel)
	}
	if persisted[0].TerminalCause == nil || *persisted[0].TerminalCause != model.TerminalCleanClose {
		t.Fatalf("clean-close terminal_cause = %v, want %q", persisted[0].TerminalCause, model.TerminalCleanClose)
	}
	if persisted[0].CommitSource == nil || *persisted[0].CommitSource != model.CommitSemantic {
		t.Fatalf("clean-close commit_source = %v, want %q", persisted[0].CommitSource, model.CommitSemantic)
	}
	if persisted[0].RecoveryAction == nil || *persisted[0].RecoveryAction != model.RecoveryActionNone {
		t.Fatalf("clean-close recovery_action = %v, want %q", persisted[0].RecoveryAction, model.RecoveryActionNone)
	}

	if persisted[1].SessionCommitted == nil || *persisted[1].SessionCommitted {
		t.Fatalf("semantic-error session_committed = %v, want false", persisted[1].SessionCommitted)
	}
	if persisted[1].ClientVisible == nil || !*persisted[1].ClientVisible {
		t.Fatalf("semantic-error client_visible = %v, want true", persisted[1].ClientVisible)
	}
	if persisted[1].ProbeOutcome == nil || *persisted[1].ProbeOutcome != model.WebSocketProbeOutcomeTransportFailed {
		t.Fatalf("semantic-error probe_outcome = %v, want %q", persisted[1].ProbeOutcome, model.WebSocketProbeOutcomeTransportFailed)
	}
	if persisted[1].TerminalCause == nil || *persisted[1].TerminalCause != model.TerminalUpstreamSemanticError {
		t.Fatalf("semantic-error terminal_cause = %v, want %q", persisted[1].TerminalCause, model.TerminalUpstreamSemanticError)
	}
	if persisted[1].CommitSource == nil || *persisted[1].CommitSource != model.CommitUnknown {
		t.Fatalf("semantic-error commit_source = %v, want %q", persisted[1].CommitSource, model.CommitUnknown)
	}
	if persisted[1].RecoveryAction == nil || *persisted[1].RecoveryAction != model.RecoveryActionReconnectRequired {
		t.Fatalf("semantic-error recovery_action = %v, want %q", persisted[1].RecoveryAction, model.RecoveryActionReconnectRequired)
	}
}
