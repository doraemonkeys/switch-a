package store

import (
	"testing"

	"switch-a/internal/model"
)

func TestMigrateBaseURLToAPIType_BackfillsProviderAPITypeRowsAndDropsLegacyColumn(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE providers (
		id TEXT PRIMARY KEY,
		base_url TEXT NOT NULL DEFAULT ''
	)`).Error; err != nil {
		t.Fatalf("create legacy providers table: %v", err)
	}
	if err := db.Exec(`CREATE TABLE provider_api_types (
		provider_id TEXT NOT NULL,
		api_type TEXT NOT NULL,
		base_url TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (provider_id, api_type)
	)`).Error; err != nil {
		t.Fatalf("create provider_api_types table: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO providers (id, base_url) VALUES (?, ?), (?, ?)`,
		"p-legacy", "https://legacy.example",
		"p-empty", "",
	).Error; err != nil {
		t.Fatalf("seed providers: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO provider_api_types (provider_id, api_type, base_url) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		"p-legacy", "codex", "",
		"p-legacy", "claude", "",
		"p-empty", "codex", "https://keep.example",
	).Error; err != nil {
		t.Fatalf("seed provider_api_types: %v", err)
	}

	if err := migrateBaseURLToAPIType(db); err != nil {
		t.Fatalf("migrateBaseURLToAPIType() error = %v", err)
	}

	type providerAPITypeRow struct {
		ProviderID string
		APIType    string
		BaseURL    string
	}

	var rows []providerAPITypeRow
	if err := db.Raw(
		`SELECT provider_id, api_type, base_url FROM provider_api_types ORDER BY provider_id, api_type`,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("load migrated provider_api_types: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	if rows[0].ProviderID != "p-empty" || rows[0].BaseURL != "https://keep.example" {
		t.Fatalf("rows[0] = %+v, want p-empty row to preserve existing base_url", rows[0])
	}
	if rows[1].ProviderID != "p-legacy" || rows[1].BaseURL != "https://legacy.example" {
		t.Fatalf("rows[1] = %+v, want legacy base_url backfilled", rows[1])
	}
	if rows[2].ProviderID != "p-legacy" || rows[2].BaseURL != "https://legacy.example" {
		t.Fatalf("rows[2] = %+v, want legacy base_url backfilled", rows[2])
	}

	hasLegacyColumn, err := tableColumnExists(db, providersTableName, "base_url")
	if err != nil {
		t.Fatalf("check providers.base_url column: %v", err)
	}
	if hasLegacyColumn {
		t.Fatal("providers.base_url column should have been dropped")
	}
}

func TestMigrateBaseURLToAPIType_NoLegacyColumn(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE providers (
		id TEXT PRIMARY KEY
	)`).Error; err != nil {
		t.Fatalf("create providers table: %v", err)
	}
	if err := db.Exec(`CREATE TABLE provider_api_types (
		provider_id TEXT NOT NULL,
		api_type TEXT NOT NULL,
		base_url TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (provider_id, api_type)
	)`).Error; err != nil {
		t.Fatalf("create provider_api_types table: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO provider_api_types (provider_id, api_type, base_url) VALUES (?, ?, ?)`,
		"p-current", "codex", "https://current.example",
	).Error; err != nil {
		t.Fatalf("seed provider_api_types: %v", err)
	}

	if err := migrateBaseURLToAPIType(db); err != nil {
		t.Fatalf("migrateBaseURLToAPIType(no legacy column) error = %v", err)
	}

	var baseURL string
	if err := db.Raw(
		`SELECT base_url FROM provider_api_types WHERE provider_id = ? AND api_type = ?`,
		"p-current", "codex",
	).Scan(&baseURL).Error; err != nil {
		t.Fatalf("load provider_api_types row: %v", err)
	}
	if baseURL != "https://current.example" {
		t.Fatalf("base_url = %q, want existing value preserved", baseURL)
	}
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
