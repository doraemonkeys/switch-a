package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"gorm.io/gorm"
)

func TestProviderCookieSchemaRejectsEveryManifestDamageWithoutRepair(t *testing.T) {
	tests := []struct {
		name   string
		target string
		mutate func(string) string
	}{
		{name: "metadata type", target: schemaTable, mutate: replaceSchema("version INTEGER", "version TEXT")},
		{name: "column type", target: handlesTable, mutate: replaceSchema("handle_digest BLOB", "handle_digest TEXT")},
		{name: "not null", target: handlesTable, mutate: replaceSchema("handle_digest BLOB NOT NULL", "handle_digest BLOB")},
		{name: "primary key ordinal", target: handlesTable, mutate: replaceSchema(
			"PRIMARY KEY (handle_key_version, handle_digest)", "PRIMARY KEY (handle_digest, handle_key_version)")},
		{name: "critical check", target: handlesTable, mutate: replaceSchema(
			"CHECK (length(handle_digest) = 32)", "CHECK (length(handle_digest) > 0)")},
		{name: "absolute cap check", target: handlesTable, mutate: replaceSchema(
			"CHECK (idle_expires_at_ms <= absolute_expires_at_ms)", "CHECK (idle_expires_at_ms > 0)")},
		{name: "without rowid", target: handlesTable, mutate: replaceSchema(" WITHOUT ROWID", "")},
		{name: "unique jar", target: handlesTable, mutate: replaceSchema("jar_id BLOB NOT NULL UNIQUE", "jar_id BLOB NOT NULL")},
		{name: "foreign key", target: authoritiesTable, mutate: replaceSchema(
			"FOREIGN KEY (jar_id) REFERENCES codex_provider_cookie_handles(jar_id) ON DELETE CASCADE",
			"CHECK (jar_id IS NOT NULL)")},
		{name: "authority delete action", target: authoritiesTable, mutate: replaceSchema("ON DELETE CASCADE", "ON DELETE RESTRICT")},
		{name: "entry delete action", target: entriesTable, mutate: replaceSchema("ON DELETE CASCADE", "ON DELETE SET NULL")},
		{name: "handle expiry index", target: "idx_codex_provider_cookie_handles_expiry", mutate: func(string) string { return "" }},
		{name: "authority orphan index", target: "idx_codex_provider_cookie_authorities_orphan", mutate: func(string) string { return "" }},
		{name: "entry expiry index", target: "idx_codex_provider_cookie_entries_expiry", mutate: func(string) string { return "" }},
		{name: "entry eviction index", target: "idx_codex_provider_cookie_entries_eviction", mutate: func(string) string { return "" }},
		{name: "index column order", target: "idx_codex_provider_cookie_handles_expiry", mutate: replaceSchema(
			"(idle_expires_at_ms, absolute_expires_at_ms)", "(absolute_expires_at_ms, idle_expires_at_ms)")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openTestDatabase(t, filepath.Join(t.TempDir(), "damaged.db"))
			createDamagedProviderCookieSchema(t, db, test.target, test.mutate)
			before := sqliteSchemaObjectCount(t, db)
			if err := ValidateSchema(context.Background(), db); !errors.Is(err, providercookie.ErrStorageCorrupt) {
				t.Fatalf("ValidateSchema() error = %v", err)
			}
			if err := Migrate(context.Background(), db); !errors.Is(err, providercookie.ErrStorageCorrupt) {
				t.Fatalf("Migrate() error = %v", err)
			}
			if after := sqliteSchemaObjectCount(t, db); after != before {
				t.Fatalf("damaged schema object count changed from %d to %d", before, after)
			}
		})
	}
}

func TestProviderCookieSchemaVersionStateFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("metadata name collision", func(t *testing.T) {
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "view.db"))
		if err := db.Exec("CREATE VIEW " + schemaTable + " AS SELECT 1 AS id, 1 AS version").Error; err != nil {
			t.Fatal(err)
		}
		if err := Migrate(ctx, db); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("Migrate() error = %v", err)
		}
	})

	t.Run("missing version row", func(t *testing.T) {
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "missing-row.db"))
		createProviderCookieSchemaWithoutVersion(t, db, schemaDefinition)
		if err := Migrate(ctx, db); !errors.Is(err, providercookie.ErrStorageCorrupt) {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := ValidateSchema(ctx, db); !errors.Is(err, providercookie.ErrStorageCorrupt) {
			t.Fatalf("ValidateSchema() error = %v", err)
		}
	})

	t.Run("old version", func(t *testing.T) {
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "old.db"))
		createProviderCookieSchemaWithoutVersion(t, db, schemaDefinition)
		if err := db.Exec("PRAGMA ignore_check_constraints=ON").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO "+schemaTable+" (id, version) VALUES (?, 0)", schemaRowID).Error; err != nil {
			t.Fatal(err)
		}
		if err := Migrate(ctx, db); !errors.Is(err, providercookie.ErrStorageCorrupt) {
			t.Fatalf("Migrate() error = %v", err)
		}
	})

	t.Run("unreadable version", func(t *testing.T) {
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "unreadable.db"))
		brokenMeta := strings.Replace(schemaDefinition,
			"version INTEGER NOT NULL CHECK (version >= 1)",
			"wrong_version INTEGER NOT NULL CHECK (wrong_version >= 1)", 1)
		createProviderCookieSchemaWithoutVersion(t, db, brokenMeta)
		if err := db.Exec("INSERT INTO "+schemaTable+" (id, wrong_version) VALUES (?, 1)", schemaRowID).Error; err != nil {
			t.Fatal(err)
		}
		if err := Migrate(ctx, db); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := ValidateSchema(ctx, db); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("ValidateSchema() error = %v", err)
		}
	})

	t.Run("closed version reader", func(t *testing.T) {
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "closed.db"))
		database, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readSchemaVersion(ctx, db); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("readSchemaVersion() error = %v", err)
		}
	})
}

func createDamagedProviderCookieSchema(t *testing.T, db *gorm.DB, target string, mutate func(string) string) {
	t.Helper()
	statements := append([]string{schemaDefinition}, createSchemaStatements...)
	applied := false
	for _, statement := range statements {
		if !applied && strings.Contains(statement, target) {
			statement = mutate(statement)
			applied = true
		}
		if statement == "" {
			continue
		}
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create damaged schema: %v\n%s", err, statement)
		}
	}
	if !applied {
		t.Fatalf("damage target %q was not found", target)
	}
	if err := db.Exec("INSERT INTO "+schemaTable+" (id, version) VALUES (?, ?)", schemaRowID, CurrentSchemaVersion).Error; err != nil {
		t.Fatal(err)
	}
}

func createProviderCookieSchemaWithoutVersion(t *testing.T, db *gorm.DB, metadata string) {
	t.Helper()
	for _, statement := range append([]string{metadata}, createSchemaStatements...) {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func sqliteSchemaObjectCount(t *testing.T, db *gorm.DB) int {
	t.Helper()
	var count int
	if err := db.Raw(`SELECT COUNT(*) FROM sqlite_master
		WHERE (type = 'table' OR type = 'index') AND name LIKE 'codex_provider_cookie_%'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func replaceSchema(old, replacement string) func(string) string {
	return func(statement string) string {
		return strings.Replace(statement, old, replacement, 1)
	}
}
