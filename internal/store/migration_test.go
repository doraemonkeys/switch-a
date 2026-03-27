package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"switch-a/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if err := db.AutoMigrate(&model.RuntimeConfig{}); err != nil {
		t.Fatalf("auto-migrate runtime_config: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close migration db: %v", closeErr)
		}
	})

	return db
}

func seedConfig(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()

	cfg := model.RuntimeConfig{
		Key:   key,
		Value: value,
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("seed config %q: %v", key, err)
	}
}

func readConfigValue(t *testing.T, db *gorm.DB, key string) string {
	t.Helper()

	var cfg model.RuntimeConfig
	if err := db.First(&cfg, "key = ?", key).Error; err != nil {
		t.Fatalf("read config %q: %v", key, err)
	}
	return cfg.Value
}

func assertConfigMissing(t *testing.T, db *gorm.DB, key string) {
	t.Helper()

	var cfg model.RuntimeConfig
	err := db.First(&cfg, "key = ?", key).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("config %q should be missing, err=%v", key, err)
	}
}

func setupProviderStateMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := setupMigrationTestDB(t)
	if err := db.AutoMigrate(
		&model.Provider{},
		&model.ProviderAPIType{},
		&model.ProviderCredential{},
		&model.ProviderAuthState{},
	); err != nil {
		t.Fatalf("auto-migrate provider state tables: %v", err)
	}
	hasCredentialData, err := tableColumnExists(db, providersTableName, providerCredentialDataColumn)
	if err != nil {
		t.Fatalf("check legacy provider credential column: %v", err)
	}
	if !hasCredentialData {
		if err := db.Exec(
			fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s TEXT DEFAULT ''`, providersTableName, providerCredentialDataColumn),
		).Error; err != nil {
			t.Fatalf("add legacy provider credential column: %v", err)
		}
	}
	return db
}

func insertLegacyProvider(t *testing.T, db *gorm.DB, provider *model.Provider, credentialData string) {
	t.Helper()
	if err := db.Omit("Credential", "AuthState").Create(provider).Error; err != nil {
		t.Fatalf("create legacy provider: %v", err)
	}
	if err := db.Model(&model.Provider{}).
		Where("id = ?", provider.ID).
		Update(providerCredentialDataColumn, credentialData).Error; err != nil {
		t.Fatalf("seed legacy credential shadow for %q: %v", provider.ID, err)
	}
}

func strPtr(value string) *string {
	return &value
}

func TestMigrateStickyConfig_ValueMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		legacy     string
		wantSticky string
	}{
		{name: "true to api_type", legacy: "true", wantSticky: string(model.StickyModeAPIType)},
		{name: "false to off", legacy: "false", wantSticky: string(model.StickyModeOff)},
		{name: "one to api_type", legacy: "1", wantSticky: string(model.StickyModeAPIType)},
		{name: "zero to off", legacy: "0", wantSticky: string(model.StickyModeOff)},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := setupMigrationTestDB(t)
			seedConfig(t, db, legacyStickyEnabledConfigKey, tt.legacy)

			if err := migrateStickyConfig(db); err != nil {
				t.Fatalf("migrateStickyConfig error: %v", err)
			}

			if got := readConfigValue(t, db, stickyModeConfigKey); got != tt.wantSticky {
				t.Fatalf("sticky_mode = %q, want %q", got, tt.wantSticky)
			}
			assertConfigMissing(t, db, legacyStickyEnabledConfigKey)
		})
	}
}

func TestMigrateStickyConfig_NoLegacyKey(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := migrateStickyConfig(db); err != nil {
		t.Fatalf("migrateStickyConfig error: %v", err)
	}

	assertConfigMissing(t, db, stickyModeConfigKey)
}

func TestMigrateStickyConfig_ExistingStickyModeNotOverwritten(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	seedConfig(t, db, legacyStickyEnabledConfigKey, "true")
	seedConfig(t, db, stickyModeConfigKey, string(model.StickyModeOff))

	if err := migrateStickyConfig(db); err != nil {
		t.Fatalf("migrateStickyConfig error: %v", err)
	}

	if got := readConfigValue(t, db, stickyModeConfigKey); got != string(model.StickyModeOff) {
		t.Fatalf("sticky_mode = %q, want %q", got, string(model.StickyModeOff))
	}
	assertConfigMissing(t, db, legacyStickyEnabledConfigKey)
}

func TestMigrateGlobalMaxAttemptsConfig_RenamesLegacyKey(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	seedConfig(t, db, legacyMaxRetriesConfigKey, "7")

	if err := migrateGlobalMaxAttemptsConfig(db); err != nil {
		t.Fatalf("migrateGlobalMaxAttemptsConfig error: %v", err)
	}

	if got := readConfigValue(t, db, globalMaxAttemptsConfigKey); got != "7" {
		t.Fatalf("global_max_attempts = %q, want %q", got, "7")
	}
	assertConfigMissing(t, db, legacyMaxRetriesConfigKey)
}

func TestMigrateGlobalMaxAttemptsConfig_NoLegacyKey(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := migrateGlobalMaxAttemptsConfig(db); err != nil {
		t.Fatalf("migrateGlobalMaxAttemptsConfig error: %v", err)
	}

	assertConfigMissing(t, db, globalMaxAttemptsConfigKey)
}

func TestMigrateGlobalMaxAttemptsConfig_ExistingCurrentKeyNotOverwritten(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	seedConfig(t, db, legacyMaxRetriesConfigKey, "7")
	seedConfig(t, db, globalMaxAttemptsConfigKey, "4")

	if err := migrateGlobalMaxAttemptsConfig(db); err != nil {
		t.Fatalf("migrateGlobalMaxAttemptsConfig error: %v", err)
	}

	if got := readConfigValue(t, db, globalMaxAttemptsConfigKey); got != "4" {
		t.Fatalf("global_max_attempts = %q, want %q", got, "4")
	}
	assertConfigMissing(t, db, legacyMaxRetriesConfigKey)
}

// setupWebSocketMigrationDB creates a DB with the legacy is_web_socket column
// (simulating GORM auto-naming before the explicit column tag was added).
func setupWebSocketMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "ws_migration.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// Create request_logs with the legacy column name (GORM's default for IsWebSocket).
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_web_socket BOOLEAN DEFAULT 0,
		is_websocket BOOLEAN DEFAULT 0,
		provider_id TEXT DEFAULT '',
		created_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close ws migration db: %v", closeErr)
		}
	})

	return db
}

func TestMigrateWebSocketColumn_CopiesData(t *testing.T) {
	t.Parallel()

	db := setupWebSocketMigrationDB(t)

	// Seed data in the legacy column.
	if err := db.Exec(`INSERT INTO request_logs (is_web_socket, is_websocket, provider_id) VALUES (1, 0, 'p1')`).Error; err != nil {
		t.Fatalf("seed ws log: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_logs (is_web_socket, is_websocket, provider_id) VALUES (0, 0, 'p2')`).Error; err != nil {
		t.Fatalf("seed regular log: %v", err)
	}

	if err := migrateWebSocketColumn(db); err != nil {
		t.Fatalf("migrateWebSocketColumn error: %v", err)
	}

	// Verify the WS row was migrated to the new column.
	var wsCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM request_logs WHERE is_websocket = 1`).Scan(&wsCount).Error; err != nil {
		t.Fatalf("count ws: %v", err)
	}
	if wsCount != 1 {
		t.Errorf("is_websocket=1 count = %d, want 1", wsCount)
	}

	// Verify legacy column was dropped.
	var colCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM pragma_table_info('request_logs') WHERE name = 'is_web_socket'`).Scan(&colCount).Error; err != nil {
		t.Fatalf("check column: %v", err)
	}
	if colCount != 0 {
		t.Error("is_web_socket column should have been dropped")
	}
}

func TestMigrateWebSocketColumn_NoLegacyColumn(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "ws_no_legacy.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// Create table WITHOUT the legacy column.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_websocket BOOLEAN DEFAULT 0
	)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	// Should be a no-op.
	if err := migrateWebSocketColumn(db); err != nil {
		t.Fatalf("migrateWebSocketColumn error: %v", err)
	}
}

func setupRequestLogLifecycleMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "request_log_lifecycle_migration.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	createLegacyTableSQL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_websocket BOOLEAN DEFAULT 0,
		provider_id TEXT DEFAULT '',
		success BOOLEAN DEFAULT 0,
		created_at DATETIME
	)`, requestLogsTableName)
	if err := db.Exec(createLegacyTableSQL).Error; err != nil {
		t.Fatalf("create request_logs: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close request log lifecycle db: %v", closeErr)
		}
	})

	return db
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
		StickyWritten    sql.NullBool
		TerminalCause    sql.NullString
		CommitSource     sql.NullString
	}

	var rows []lifecycleRow
	querySQL := fmt.Sprintf(
		`SELECT provider_id, session_committed, sticky_written, terminal_cause, commit_source FROM %s ORDER BY provider_id`,
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
	if rows[0].TerminalCause.Valid {
		t.Fatalf("regular row terminal_cause = %+v, want NULL", rows[0].TerminalCause)
	}
	if rows[0].CommitSource.Valid {
		t.Fatalf("regular row commit_source = %+v, want NULL", rows[0].CommitSource)
	}

	if !rows[1].SessionCommitted.Valid || !rows[1].SessionCommitted.Bool {
		t.Fatalf("websocket row session_committed = %+v, want true", rows[1].SessionCommitted)
	}
	if !rows[1].StickyWritten.Valid || rows[1].StickyWritten.Bool {
		t.Fatalf("websocket row sticky_written = %+v, want false", rows[1].StickyWritten)
	}
	if !rows[1].TerminalCause.Valid || rows[1].TerminalCause.String != string(model.TerminalUnknown) {
		t.Fatalf("websocket row terminal_cause = %+v, want %q", rows[1].TerminalCause, model.TerminalUnknown)
	}
	if !rows[1].CommitSource.Valid || rows[1].CommitSource.String != string(model.CommitUnknown) {
		t.Fatalf("websocket row commit_source = %+v, want %q", rows[1].CommitSource, model.CommitUnknown)
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
			TerminalCause:    terminalCausePtr(model.TerminalCleanClose),
			CommitSource:     commitSourcePtr(model.CommitSemantic),
		},
		{
			IsWebSocket:      true,
			ProviderID:       "semantic-error",
			Success:          false,
			StickyWritten:    boolPtr(false),
			SessionCommitted: &uncommitted,
			TerminalCause:    terminalCausePtr(model.TerminalUpstreamSemanticError),
			CommitSource:     commitSourcePtr(model.CommitUnknown),
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
	if persisted[0].TerminalCause == nil || *persisted[0].TerminalCause != model.TerminalCleanClose {
		t.Fatalf("clean-close terminal_cause = %v, want %q", persisted[0].TerminalCause, model.TerminalCleanClose)
	}
	if persisted[0].CommitSource == nil || *persisted[0].CommitSource != model.CommitSemantic {
		t.Fatalf("clean-close commit_source = %v, want %q", persisted[0].CommitSource, model.CommitSemantic)
	}

	if persisted[1].SessionCommitted == nil || *persisted[1].SessionCommitted {
		t.Fatalf("semantic-error session_committed = %v, want false", persisted[1].SessionCommitted)
	}
	if persisted[1].TerminalCause == nil || *persisted[1].TerminalCause != model.TerminalUpstreamSemanticError {
		t.Fatalf("semantic-error terminal_cause = %v, want %q", persisted[1].TerminalCause, model.TerminalUpstreamSemanticError)
	}
	if persisted[1].CommitSource == nil || *persisted[1].CommitSource != model.CommitUnknown {
		t.Fatalf("semantic-error commit_source = %v, want %q", persisted[1].CommitSource, model.CommitUnknown)
	}
}

func TestMigrateProviderStateTables_BackfillsChatGPTCredentialAndAuthState(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	now := time.Date(2026, time.March, 27, 4, 0, 0, 0, time.UTC)
	resetAt := now.Add(2 * time.Hour)
	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "pro",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
		Usage: &model.ProviderUsageSnapshot{
			FetchedAt: &now,
			FiveHour: &model.ProviderUsageWindow{
				UsedPercent:   50,
				WindowSeconds: 5 * 60 * 60,
				ResetAt:       &resetAt,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode credential data: %v", err)
	}

	provider := &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, provider, credentialData)

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables error: %v", err)
	}
	hasCredentialData, err := tableColumnExists(db, providersTableName, providerCredentialDataColumn)
	if err != nil {
		t.Fatalf("check legacy provider credential column after migration: %v", err)
	}
	if hasCredentialData {
		t.Fatal("providers.credential_data column still exists after migration")
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider credential: %v", err)
	}
	if credential.SecretData != credentialData {
		t.Fatalf("SecretData = %q, want original payload", credential.SecretData)
	}
	if credential.BindingAccountID == nil || *credential.BindingAccountID != "acct_test" {
		t.Fatalf("BindingAccountID = %v, want acct_test", credential.BindingAccountID)
	}
	if credential.Version != 1 {
		t.Fatalf("Version = %d, want 1", credential.Version)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusActive)
	}
	if authState.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", authState.Email)
	}
	if authState.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", authState.AccountID)
	}
	if authState.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want pro", authState.PlanType)
	}
	if authState.UsageSnapshot == nil || authState.UsageSnapshot.FiveHour == nil {
		t.Fatalf("UsageSnapshot = %#v, want migrated snapshot", authState.UsageSnapshot)
	}
}

func TestMigrateProviderStateTables_BackfillsNotConnectedForIncompleteChatGPTLogin(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccountID: "acct_test",
		Email:     "user@example.com",
	})
	if err != nil {
		t.Fatalf("encode credential data: %v", err)
	}

	provider := &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, provider, credentialData)

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables error: %v", err)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusNotConnected)
	}
	if authState.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", authState.AccountID)
	}
}

func TestMigrateProviderStateTables_DoesNotOverwriteExistingRows(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	legacyCredentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "legacy-access",
		RefreshToken: "legacy-refresh",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("encode legacy credential data: %v", err)
	}

	provider := &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, provider, legacyCredentialData)

	existingAccountID := "acct_other"
	existingCredential := &model.ProviderCredential{
		ProviderID:       provider.ID,
		SecretData:       "new-secret",
		BindingAccountID: &existingAccountID,
		Version:          7,
	}
	if err := db.Create(existingCredential).Error; err != nil {
		t.Fatalf("create provider credential: %v", err)
	}

	transitionAt := time.Date(2026, time.March, 27, 4, 10, 0, 0, time.UTC)
	existingAuthState := &model.ProviderAuthState{
		ProviderID:       provider.ID,
		Status:           model.ProviderAuthStatusReauthRequired,
		StatusReason:     "invalid_grant",
		LastError:        "refresh_token_reused",
		LastTransitionAt: &transitionAt,
	}
	if err := db.Create(existingAuthState).Error; err != nil {
		t.Fatalf("create provider auth state: %v", err)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables error: %v", err)
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider credential: %v", err)
	}
	if credential.SecretData != "new-secret" {
		t.Fatalf("SecretData = %q, want existing row to remain", credential.SecretData)
	}
	if credential.Version != 7 {
		t.Fatalf("Version = %d, want 7", credential.Version)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusReauthRequired)
	}
	if authState.LastError != "refresh_token_reused" {
		t.Fatalf("LastError = %q, want refresh_token_reused", authState.LastError)
	}
}

func TestMigrateProviderStateTables_BackfillsLegacyProviders(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)

	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "team",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
		Usage: &model.ProviderUsageSnapshot{
			PlanType: "team",
		},
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential() error: %v", err)
	}

	providers := []model.Provider{
		{ID: "api", Name: "API Provider", APIKey: "key", CredentialType: model.ProviderCredentialTypeAPIKey, Enabled: true},
		{ID: "gpt-active", Name: "GPT Active", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true},
		{ID: "gpt-pending", Name: "GPT Pending", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true},
	}
	for i := range providers {
		shadow := ""
		if providers[i].ID == "gpt-active" {
			shadow = credentialData
		}
		insertLegacyProvider(t, db, &providers[i], shadow)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables() error: %v", err)
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", "gpt-active").Error; err != nil {
		t.Fatalf("read gpt-active credential: %v", err)
	}
	if credential.SecretData != credentialData {
		t.Fatalf("SecretData = %q, want original payload", credential.SecretData)
	}
	if credential.BindingAccountID == nil || *credential.BindingAccountID != "acct_test" {
		t.Fatalf("BindingAccountID = %v, want acct_test", credential.BindingAccountID)
	}
	if credential.Version != 1 {
		t.Fatalf("Version = %d, want 1", credential.Version)
	}

	var apiAuthState model.ProviderAuthState
	if err := db.First(&apiAuthState, "provider_id = ?", "api").Error; err != nil {
		t.Fatalf("read api auth state: %v", err)
	}
	if apiAuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("api auth status = %q, want %q", apiAuthState.Status, model.ProviderAuthStatusActive)
	}

	var activeAuthState model.ProviderAuthState
	if err := db.First(&activeAuthState, "provider_id = ?", "gpt-active").Error; err != nil {
		t.Fatalf("read gpt-active auth state: %v", err)
	}
	if activeAuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("gpt-active auth status = %q, want %q", activeAuthState.Status, model.ProviderAuthStatusActive)
	}
	if activeAuthState.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", activeAuthState.AccountID)
	}
	if activeAuthState.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", activeAuthState.Email)
	}
	if activeAuthState.UsageSnapshot == nil || activeAuthState.UsageSnapshot.PlanType != "team" {
		t.Fatalf("UsageSnapshot = %+v, want team snapshot", activeAuthState.UsageSnapshot)
	}

	var pendingAuthState model.ProviderAuthState
	if err := db.First(&pendingAuthState, "provider_id = ?", "gpt-pending").Error; err != nil {
		t.Fatalf("read gpt-pending auth state: %v", err)
	}
	if pendingAuthState.Status != model.ProviderAuthStatusNotConnected {
		t.Fatalf("gpt-pending auth status = %q, want %q", pendingAuthState.Status, model.ProviderAuthStatusNotConnected)
	}
}

func TestMigrateProviderStateTables_PreservesExistingRows(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)

	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential() error: %v", err)
	}

	provider := model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, &provider, credentialData)
	if err := db.Create(&model.ProviderCredential{
		ProviderID:       "gpt",
		SecretData:       "preserved-secret",
		BindingAccountID: strPtr("acct_preserved"),
		Version:          7,
	}).Error; err != nil {
		t.Fatalf("seed provider credential: %v", err)
	}
	if err := db.Create(&model.ProviderAuthState{
		ProviderID:   "gpt",
		Status:       model.ProviderAuthStatusReauthRequired,
		StatusReason: "refresh_token_reused",
		LastError:    "terminal oauth error",
	}).Error; err != nil {
		t.Fatalf("seed provider auth state: %v", err)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables() error: %v", err)
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", "gpt").Error; err != nil {
		t.Fatalf("read provider credential: %v", err)
	}
	if credential.SecretData != "preserved-secret" {
		t.Fatalf("SecretData = %q, want preserved-secret", credential.SecretData)
	}
	if credential.Version != 7 {
		t.Fatalf("Version = %d, want 7", credential.Version)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", "gpt").Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusReauthRequired)
	}
	if authState.StatusReason != "refresh_token_reused" {
		t.Fatalf("StatusReason = %q, want refresh_token_reused", authState.StatusReason)
	}
}

func TestRoutingPolicySchema_RejectsDuplicateMatchCombination(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.AutoMigrate(&model.RoutingPolicy{}, &model.RoutingPolicyGroup{}, &model.RoutingPolicyVendor{}); err != nil {
		t.Fatalf("auto-migrate routing policy tables: %v", err)
	}

	first := model.RoutingPolicy{
		APIType:         "responses",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5",
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first routing policy: %v", err)
	}

	duplicate := model.RoutingPolicy{
		APIType:         "responses",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5",
	}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate routing policy insert succeeded, want unique constraint failure")
	}
}
