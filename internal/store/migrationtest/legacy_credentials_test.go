package migrationtest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const fixtureFaultDriverName = "switch-a-migration-fixture-fault"

var registerFixtureFaultDriver sync.Once

type fixtureProviderRow struct {
	ID             string
	APIKey         string
	CredentialType string
	Enabled        bool
}

type fixtureCredentialRow struct {
	ProviderID       string
	SecretData       string
	BindingAccountID *string
	Version          int64
}

type fixtureAPITypeRow struct {
	APIType string
	APIKey  string
}

func TestLegacyCredentialFixtureSchemaIsFrozenBeforeCredentialSession(t *testing.T) {
	t.Parallel()

	db := openLegacyCredentialDatabase(t)
	wantColumns := map[string][]string{
		"providers": {
			"id", "name", "api_key", "auth_mode", "credential_type", "usage_limit_policy",
			"group_id", "weight", "priority", "concurrency", "max_retries", "backoff_initial_delay",
			"backoff_max_delay", "backoff_multiplier", "backoff_jitter", "vendor", "failover_scope",
			"accept_failover", "enabled", "created_at", "updated_at",
		},
		"provider_api_types": {"provider_id", "api_type", "base_url", "api_key"},
		"provider_credentials": {
			"provider_id", "secret_data", "binding_account_id", "version", "created_at", "updated_at",
		},
		"provider_auth_states": {
			"provider_id", "status", "status_reason", "last_error", "last_transition_at", "email",
			"account_id", "plan_type", "expires_at", "last_refresh_at", "usage_snapshot",
			"refresh_fail_count", "last_refresh_failure_at", "created_at", "updated_at",
		},
	}
	for table, want := range wantColumns {
		if got := tableColumns(t, db, table); !equalStrings(got, want) {
			t.Fatalf("%s columns = %v, want frozen legacy columns %v", table, got, want)
		}
	}

	for _, table := range []string{"credential_sessions", "route_target_credentials", "continuity_bindings", "provider_cookie_entries"} {
		if tableExists(t, db, table) {
			t.Fatalf("future table %q must not exist in %s", table, LegacyCredentialSchemaVersion)
		}
	}

	var uniqueIndexSQL string
	if err := db.Raw(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
		"idx_provider_credentials_binding_account_id",
	).Scan(&uniqueIndexSQL).Error; err != nil {
		t.Fatalf("read legacy binding index: %v", err)
	}
	if !strings.HasPrefix(strings.ToUpper(uniqueIndexSQL), "CREATE UNIQUE INDEX") {
		t.Fatalf("legacy binding index = %q, want UNIQUE", uniqueIndexSQL)
	}
}

func TestLegacyCredentialFixtureCoversMigrationScenarios(t *testing.T) {
	t.Parallel()

	db := openLegacyCredentialDatabase(t)

	var providers []fixtureProviderRow
	if err := db.Raw(`SELECT id, api_key, credential_type, enabled FROM providers ORDER BY id`).Scan(&providers).Error; err != nil {
		t.Fatalf("read fixture providers: %v", err)
	}
	if len(providers) != 8 {
		t.Fatalf("provider count = %d, want 8", len(providers))
	}
	for _, provider := range providers {
		if provider.APIKey != "" && (!strings.HasPrefix(provider.APIKey, "fixture-") || !strings.HasSuffix(provider.APIKey, "-not-secret")) {
			t.Fatalf("provider %q API key lacks explicit synthetic markers", provider.ID)
		}
	}

	primary := findProvider(t, providers, StaticProviderID)
	if primary.APIKey != "fixture-static-primary-not-secret" || primary.CredentialType != "api_key" {
		t.Fatalf("static provider = %+v, want provider-level synthetic key", primary)
	}

	var overrides []fixtureAPITypeRow
	if err := db.Raw(
		`SELECT api_type, api_key FROM provider_api_types WHERE provider_id = ? ORDER BY api_type`,
		APITypeOverrideProviderID,
	).Scan(&overrides).Error; err != nil {
		t.Fatalf("read API-type overrides: %v", err)
	}
	codexKey, hasCodex := apiTypeKey(overrides, "codex")
	claudeKey, hasClaude := apiTypeKey(overrides, "claude")
	if len(overrides) != 2 || !hasCodex || !hasClaude || codexKey != "fixture-static-override-not-secret" || claudeKey != "" {
		t.Fatalf("API-type override rows = %+v, want one override and one inherited key", overrides)
	}

	sameSecretA := findProvider(t, providers, SameSecretStaticProviderAID)
	sameSecretB := findProvider(t, providers, SameSecretStaticProviderBID)
	if sameSecretA.APIKey == "" || sameSecretA.APIKey != sameSecretB.APIKey {
		t.Fatalf("same-secret static candidates = (%q, %q), want same non-empty key", sameSecretA.APIKey, sameSecretB.APIKey)
	}
	var sameSecretOrigins []string
	if err := db.Raw(
		`SELECT base_url FROM provider_api_types WHERE provider_id IN (?, ?) ORDER BY provider_id`,
		SameSecretStaticProviderAID,
		SameSecretStaticProviderBID,
	).Scan(&sameSecretOrigins).Error; err != nil {
		t.Fatalf("read same-secret static origins: %v", err)
	}
	if len(sameSecretOrigins) != 2 || sameSecretOrigins[0] != sameSecretOrigins[1] {
		t.Fatalf("same-secret static origins = %v, want identical authority candidate", sameSecretOrigins)
	}
	deletionTarget := findProvider(t, providers, ProviderDeletionTargetID)
	if deletionTarget.Enabled {
		t.Fatalf("deletion target Enabled = true, want disabled legacy target")
	}

	var credentials []fixtureCredentialRow
	if err := db.Raw(
		`SELECT provider_id, secret_data, binding_account_id, version FROM provider_credentials ORDER BY provider_id`,
	).Scan(&credentials).Error; err != nil {
		t.Fatalf("read fixture credentials: %v", err)
	}
	if len(credentials) != 3 {
		t.Fatalf("credential count = %d, want 3 ChatGPT records", len(credentials))
	}
	for _, credential := range credentials {
		assertSyntheticChatGPTSecret(t, credential.SecretData)
	}

	owner := findCredential(t, credentials, DuplicateAccountOwnerID)
	repair := findCredential(t, credentials, DuplicateAccountRepairID)
	if owner.BindingAccountID == nil || *owner.BindingAccountID != HistoricalDuplicateAccount {
		t.Fatalf("duplicate owner binding = %v, want %q", owner.BindingAccountID, HistoricalDuplicateAccount)
	}
	if repair.BindingAccountID != nil {
		t.Fatalf("repair credential binding = %v, want nil historical loser", repair.BindingAccountID)
	}
	var repairAccount, repairStatus, repairReason string
	if err := db.Raw(
		`SELECT account_id, status, status_reason FROM provider_auth_states WHERE provider_id = ?`,
		DuplicateAccountRepairID,
	).Row().Scan(&repairAccount, &repairStatus, &repairReason); err != nil {
		t.Fatalf("read duplicate repair auth state: %v", err)
	}
	if repairAccount != HistoricalDuplicateAccount || repairStatus != "reauth_required" || repairReason != "legacy_duplicate_account_binding" {
		t.Fatalf("duplicate repair state = (%q, %q, %q)", repairAccount, repairStatus, repairReason)
	}
}

func TestApplyLegacyCredentialFixtureIsRepeatable(t *testing.T) {
	t.Parallel()

	db := openLegacyCredentialDatabase(t)
	if err := db.Exec(`UPDATE providers SET name = 'mutated' WHERE id = ?`, StaticProviderID).Error; err != nil {
		t.Fatalf("mutate fixture: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		for _, table := range []string{"provider_auth_states", "provider_api_types"} {
			if err := tx.Exec(`DELETE FROM `+table+` WHERE provider_id = ?`, ProviderDeletionTargetID).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`DELETE FROM providers WHERE id = ?`, ProviderDeletionTargetID).Error
	}); err != nil {
		t.Fatalf("delete fixture scenario: %v", err)
	}
	if err := ApplyLegacyCredentialFixture(db); err != nil {
		t.Fatalf("reapply fixture: %v", err)
	}

	var name string
	if err := db.Raw(`SELECT name FROM providers WHERE id = ?`, StaticProviderID).Scan(&name).Error; err != nil {
		t.Fatalf("read reapplied fixture: %v", err)
	}
	if name != "Legacy static primary" {
		t.Fatalf("reapplied provider name = %q, want deterministic seed", name)
	}
	var deletionTargetCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM providers WHERE id = ?`, ProviderDeletionTargetID).Scan(&deletionTargetCount).Error; err != nil {
		t.Fatalf("read restored deletion target: %v", err)
	}
	if deletionTargetCount != 1 {
		t.Fatalf("restored deletion target count = %d, want 1", deletionTargetCount)
	}
}

func TestCreateLegacyCredentialDatabaseReturnsClosedReusableFile(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), LegacyCredentialSchemaVersion+".db")
	if err := CreateLegacyCredentialDatabase(dbPath); err != nil {
		t.Fatalf("create fixture database: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("stat fixture database: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("reopen fixture database: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := closeGORMDatabase(db); closeErr != nil {
			t.Logf("close reopened fixture database: %v", closeErr)
		}
	})

	var count int64
	if err := db.Raw(`SELECT COUNT(*) FROM providers`).Scan(&count).Error; err != nil {
		t.Fatalf("count providers in reopened fixture: %v", err)
	}
	if count != 8 {
		t.Fatalf("provider count = %d, want 8", count)
	}
}

func TestLegacyCredentialDatabaseReportsUnusablePath(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "missing-parent", "fixture.db")
	if db, err := OpenLegacyCredentialDatabase(dbPath); err == nil || db != nil || !strings.Contains(err.Error(), "open legacy credential fixture") {
		t.Fatalf("OpenLegacyCredentialDatabase(unusable path) = (%v, %v), want contextual open failure", db, err)
	}
	if err := CreateLegacyCredentialDatabase(dbPath); err == nil || !strings.Contains(err.Error(), "open legacy credential fixture") {
		t.Fatalf("CreateLegacyCredentialDatabase(unusable path) error = %v, want contextual open failure", err)
	}
}

func TestApplyLegacyCredentialFixtureRejectsNilDatabase(t *testing.T) {
	t.Parallel()

	if err := ApplyLegacyCredentialFixture(nil); err == nil {
		t.Fatal("ApplyLegacyCredentialFixture(nil) error = nil, want explicit failure")
	}
	if err := ApplyLegacyCredentialFixture(&gorm.DB{}); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("ApplyLegacyCredentialFixture(invalid) error = %v, want explicit invalid-handle failure", err)
	}
	if err := ApplyLegacyCredentialFixture(&gorm.DB{Config: &gorm.Config{}}); err == nil || !strings.Contains(err.Error(), "get legacy credential fixture database") {
		t.Fatalf("ApplyLegacyCredentialFixture(disconnected) error = %v, want database context", err)
	}
}

func TestApplyLegacyCredentialFixtureRejectsClosedDatabase(t *testing.T) {
	t.Parallel()

	db, err := openSQLite(t.TempDir() + "/closed.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := closeGORMDatabase(db); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if err := ApplyLegacyCredentialFixture(db); err == nil || !strings.Contains(err.Error(), "reserve legacy credential fixture connection") {
		t.Fatalf("ApplyLegacyCredentialFixture(closed) error = %v, want closed-connection context", err)
	}
}

func TestApplyLegacyCredentialFixtureRollsBackInvalidFixtureAndRestoresConnection(t *testing.T) {
	db, err := openSQLite(t.TempDir() + "/rollback.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := closeGORMDatabase(db); closeErr != nil {
			t.Logf("close rollback database: %v", closeErr)
		}
	})

	originalSQL := legacyCredentialFixtureSQL
	t.Cleanup(func() { legacyCredentialFixtureSQL = originalSQL })
	legacyCredentialFixtureSQL = `
		BEGIN IMMEDIATE;
		CREATE TABLE fixture_partial_write (id TEXT PRIMARY KEY);
		INSERT INTO fixture_partial_write (id) VALUES ('must-rollback');
		THIS IS NOT VALID SQL;
		COMMIT;
	`
	if err := ApplyLegacyCredentialFixture(db); err == nil || !strings.Contains(err.Error(), "execute "+LegacyCredentialSchemaVersion+" fixture") {
		t.Fatalf("ApplyLegacyCredentialFixture(invalid SQL) error = %v, want fixture execution context", err)
	}
	if opened, err := OpenLegacyCredentialDatabase(filepath.Join(t.TempDir(), "invalid-fixture.db")); err == nil || opened != nil || !strings.Contains(err.Error(), "apply legacy credential fixture") {
		t.Fatalf("OpenLegacyCredentialDatabase(invalid SQL) = (%v, %v), want contextual apply failure", opened, err)
	}
	if tableExists(t, db, "fixture_partial_write") {
		t.Fatal("partial fixture table survived failed transaction")
	}

	var foreignKeys int
	if err := db.Raw(`PRAGMA foreign_keys`).Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d after failed fixture, want restored", foreignKeys)
	}

	legacyCredentialFixtureSQL = originalSQL
	if err := ApplyLegacyCredentialFixture(db); err != nil {
		t.Fatalf("apply valid fixture after rollback: %v", err)
	}
	if !tableExists(t, db, "providers") {
		t.Fatal("connection was not reusable after fixture rollback")
	}
}

func TestApplyLegacyCredentialFixtureReportsPragmaFailures(t *testing.T) {
	t.Parallel()

	disableFailure := openFixtureFaultDatabase(t, "disable")
	if err := ApplyLegacyCredentialFixture(disableFailure); err == nil || !strings.Contains(err.Error(), "disable fixture foreign keys") {
		t.Fatalf("ApplyLegacyCredentialFixture(disable failure) error = %v, want pragma context", err)
	}

	restoreFailure := openFixtureFaultDatabase(t, "restore")
	if err := ApplyLegacyCredentialFixture(restoreFailure); err == nil || !strings.Contains(err.Error(), "restore fixture foreign keys") {
		t.Fatalf("ApplyLegacyCredentialFixture(restore failure) error = %v, want pragma context", err)
	}
}

func TestCloseGORMDatabaseHandlesAbsentAndInvalidHandles(t *testing.T) {
	t.Parallel()

	if err := closeGORMDatabase(nil); err != nil {
		t.Fatalf("closeGORMDatabase(nil) error = %v", err)
	}
	if err := closeGORMDatabase(&gorm.DB{}); err == nil {
		t.Fatal("closeGORMDatabase(invalid) error = nil, want explicit failure")
	}
	if err := closeGORMDatabase(&gorm.DB{Config: &gorm.Config{}}); err == nil {
		t.Fatal("closeGORMDatabase(disconnected) error = nil, want explicit failure")
	}
}

func TestOpenLegacyCredentialDatabaseCallerOwnsClose(t *testing.T) {
	t.Parallel()

	db, err := OpenLegacyCredentialDatabase(filepath.Join(t.TempDir(), "caller-close.db"))
	if err != nil {
		t.Fatalf("open legacy credential database: %v", err)
	}
	if err := closeGORMDatabase(db); err != nil {
		t.Fatalf("caller close database: %v", err)
	}
}

func tableColumns(t *testing.T, db *gorm.DB, table string) []string {
	t.Helper()
	type columnRow struct {
		Name string
	}
	var rows []columnRow
	if err := db.Raw(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table).Scan(&rows).Error; err != nil {
		t.Fatalf("read %s columns: %v", table, err)
	}
	columns := make([]string, len(rows))
	for i := range rows {
		columns[i] = rows[i].Name
	}
	return columns
}

func tableExists(t *testing.T, db *gorm.DB, table string) bool {
	t.Helper()
	var count int64
	if err := db.Raw(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		table,
	).Scan(&count).Error; err != nil {
		t.Fatalf("check table %q: %v", table, err)
	}
	return count != 0
}

func findProvider(t *testing.T, providers []fixtureProviderRow, id string) fixtureProviderRow {
	t.Helper()
	for _, provider := range providers {
		if provider.ID == id {
			return provider
		}
	}
	t.Fatalf("provider %q is missing", id)
	return fixtureProviderRow{}
}

func apiTypeKey(rows []fixtureAPITypeRow, apiType string) (string, bool) {
	for _, row := range rows {
		if row.APIType == apiType {
			return row.APIKey, true
		}
	}
	return "", false
}

func findCredential(t *testing.T, credentials []fixtureCredentialRow, providerID string) fixtureCredentialRow {
	t.Helper()
	for _, credential := range credentials {
		if credential.ProviderID == providerID {
			return credential
		}
	}
	t.Fatalf("credential for provider %q is missing", providerID)
	return fixtureCredentialRow{}
}

func assertSyntheticChatGPTSecret(t *testing.T, raw string) {
	t.Helper()
	var secret map[string]string
	if err := json.Unmarshal([]byte(raw), &secret); err != nil {
		t.Fatalf("decode synthetic ChatGPT secret: %v", err)
	}
	for _, field := range []string{"access_token", "refresh_token", "id_token"} {
		value := secret[field]
		if !strings.HasPrefix(value, "fixture-") || !strings.HasSuffix(value, "-not-secret") {
			t.Fatalf("%s = %q, want explicit synthetic marker", field, value)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func openFixtureFaultDatabase(t *testing.T, mode string) *gorm.DB {
	t.Helper()
	registerFixtureFaultDriver.Do(func() {
		sql.Register(fixtureFaultDriverName, fixtureFaultDriver{})
	})
	sqlDB, err := sql.Open(fixtureFaultDriverName, mode)
	if err != nil {
		t.Fatalf("open fixture fault database: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close fixture fault database: %v", closeErr)
		}
	})
	return &gorm.DB{Config: &gorm.Config{ConnPool: sqlDB}}
}

func openLegacyCredentialDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := OpenLegacyCredentialDatabase(filepath.Join(t.TempDir(), LegacyCredentialSchemaVersion+".db"))
	if err != nil {
		t.Fatalf("open legacy credential database: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := closeGORMDatabase(db); closeErr != nil {
			t.Logf("close legacy credential database: %v", closeErr)
		}
	})
	return db
}

type fixtureFaultDriver struct{}

func (fixtureFaultDriver) Open(mode string) (driver.Conn, error) {
	return &fixtureFaultConnection{mode: mode}, nil
}

type fixtureFaultConnection struct {
	mode string
}

func (c *fixtureFaultConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fixture fault driver does not prepare statements")
}

func (c *fixtureFaultConnection) Close() error {
	return nil
}

func (c *fixtureFaultConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("fixture fault driver does not begin transactions")
}

func (c *fixtureFaultConnection) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if (c.mode == "disable" && query == "PRAGMA foreign_keys = OFF") ||
		(c.mode == "restore" && query == "PRAGMA foreign_keys = ON") {
		return nil, errors.New("injected pragma failure")
	}
	return driver.RowsAffected(0), nil
}
