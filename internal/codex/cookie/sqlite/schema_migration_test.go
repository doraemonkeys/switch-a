package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"gorm.io/gorm"
)

const v2HandlesDefinition = `CREATE TABLE codex_provider_cookie_handles (
		handle_key_version TEXT NOT NULL CHECK (length(handle_key_version) BETWEEN 1 AND 32),
		handle_digest BLOB NOT NULL CHECK (length(handle_digest) = 32),
		jar_id BLOB NOT NULL UNIQUE CHECK (length(jar_id) = 32),
		client_scope_key_version TEXT NOT NULL CHECK (length(client_scope_key_version) BETWEEN 1 AND 32),
		client_scope_digest BLOB NOT NULL CHECK (length(client_scope_digest) = 32),
		created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
		last_access_at_ms INTEGER NOT NULL CHECK (last_access_at_ms >= created_at_ms),
		idle_expires_at_ms INTEGER NOT NULL CHECK (idle_expires_at_ms > last_access_at_ms),
		absolute_expires_at_ms INTEGER NOT NULL CHECK (absolute_expires_at_ms > created_at_ms),
		PRIMARY KEY (handle_key_version, handle_digest),
		UNIQUE (client_scope_key_version, client_scope_digest),
		CHECK (idle_expires_at_ms <= absolute_expires_at_ms)
) WITHOUT ROWID`

func setupLegacySchema(t *testing.T, db *gorm.DB, version int, handlesSQL string) {
	t.Helper()
	statements := []string{
		schemaDefinition,
		handlesSQL,
		handlesExpiryIndex,
		authoritiesDefinition,
		authoritiesOrphanIndex,
		entriesDefinition,
		entriesExpiryIndex,
		entriesEvictionIndex,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("setup legacy schema: %v\n%s", err, statement)
		}
	}
	if err := db.Exec("INSERT INTO "+schemaTable+" (id, version) VALUES (?, ?)", schemaRowID, version).Error; err != nil {
		t.Fatalf("insert legacy version %d: %v", version, err)
	}
}

func insertTestCookieData(t *testing.T, db *gorm.DB, jarID []byte, handleDigest []byte, scopeDigest []byte) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := int64(1725000000000)
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO codex_provider_cookie_handles
		(handle_key_version, handle_digest, jar_id, client_scope_key_version, client_scope_digest, created_at_ms, last_access_at_ms, idle_expires_at_ms, absolute_expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"h1", handleDigest, jarID, "h1", scopeDigest, now, now, now+3600000, now+7200000,
	); err != nil {
		t.Fatalf("insert handle: %v", err)
	}

	auth := []byte("https://api.openai.com")
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO codex_provider_cookie_authorities
		(jar_id, authority, created_at_ms, last_access_at_ms, unreachable_since_ms)
		VALUES (?, ?, ?, ?, ?)`,
		jarID, auth, now, now, nil,
	); err != nil {
		t.Fatalf("insert authority: %v", err)
	}

	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO codex_provider_cookie_entries
		(jar_id, authority, cookie_name, cookie_domain, cookie_path, value_key_version, value_nonce, value_ciphertext,
		 host_only, secure, http_only, quoted, session, same_site, expires_at_ms, created_at_ms, last_access_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jarID, auth, "session", "openai.com", "/", "a1", make([]byte, 12), make([]byte, 32),
		1, 1, 1, 0, 0, 1, now+3600000, now, now,
	); err != nil {
		t.Fatalf("insert entry: %v", err)
	}
}

func TestProviderCookieSchemaMigrationFromVersion1(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "v1.db"))
	setupLegacySchema(t, db, 1, handlesDefinition)

	jarID := make([]byte, 32)
	jarID[0] = 0x01
	handleDigest := make([]byte, 32)
	handleDigest[0] = 0x02
	scopeDigest := make([]byte, 32)
	scopeDigest[0] = 0x03
	insertTestCookieData(t, db, jarID, handleDigest, scopeDigest)

	if err := Migrate(ctx, db); err != nil {
		var pe *providercookie.PersistenceError
		if errors.As(err, &pe) {
			t.Logf("cause: %v", pe.Cause)
		}
		t.Fatalf("Migrate from v1 failed: %v", err)
	}
	if err := ValidateSchema(ctx, db); err != nil {
		t.Fatalf("ValidateSchema after v1 migration failed: %v", err)
	}

	var count int
	if err := db.Raw("SELECT COUNT(*) FROM codex_provider_cookie_handles WHERE jar_id = ?", jarID).Scan(&count).Error; err != nil {
		t.Fatalf("count handles: %v", err)
	}
	if count != 1 {
		t.Fatalf("handle count = %d; want 1", count)
	}

	if err := db.Raw("SELECT COUNT(*) FROM codex_provider_cookie_entries WHERE jar_id = ?", jarID).Scan(&count).Error; err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("entry count = %d; want 1", count)
	}
}

func TestProviderCookieSchemaMigrationFromVersion2(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "v2.db"))
	setupLegacySchema(t, db, 2, v2HandlesDefinition)

	jarID := make([]byte, 32)
	jarID[0] = 0x11
	handleDigest := make([]byte, 32)
	handleDigest[0] = 0x12
	scopeDigest := make([]byte, 32)
	scopeDigest[0] = 0x13
	insertTestCookieData(t, db, jarID, handleDigest, scopeDigest)

	if err := Migrate(ctx, db); err != nil {
		var pe *providercookie.PersistenceError
		if errors.As(err, &pe) {
			t.Logf("cause: %v", pe.Cause)
		}
		t.Fatalf("Migrate from v2 failed: %v", err)
	}
	if err := ValidateSchema(ctx, db); err != nil {
		t.Fatalf("ValidateSchema after v2 migration failed: %v", err)
	}

	// Verify data preservation
	var count int
	if err := db.Raw("SELECT COUNT(*) FROM codex_provider_cookie_handles WHERE jar_id = ?", jarID).Scan(&count).Error; err != nil {
		t.Fatalf("count handles: %v", err)
	}
	if count != 1 {
		t.Fatalf("handle count = %d; want 1", count)
	}

	// Verify that multiple handles can now share the same client_scope_digest without UNIQUE constraint violation
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	jarID2 := make([]byte, 32)
	jarID2[0] = 0x21
	handleDigest2 := make([]byte, 32)
	handleDigest2[0] = 0x22
	now := int64(1725000000000)
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO codex_provider_cookie_handles
		(handle_key_version, handle_digest, jar_id, client_scope_key_version, client_scope_digest, created_at_ms, last_access_at_ms, idle_expires_at_ms, absolute_expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"h1", handleDigest2, jarID2, "h1", scopeDigest, now, now, now+3600000, now+7200000,
	); err != nil {
		t.Fatalf("insert second handle with same client_scope failed: %v", err)
	}
}

func TestProviderCookieSchemaMigrationUnsupportedVersion(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "v99.db"))
	setupLegacySchema(t, db, 99, handlesDefinition)

	if err := Migrate(ctx, db); err == nil {
		t.Fatal("Migrate accepted future version 99")
	}

	if err := migrateSchema(db, 0); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("migrateSchema(0) = %v; want ErrStorageCorrupt", err)
	}
}

func TestRebuildHandlesTableErrors(t *testing.T) {
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "rebuild-error.db"))
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rebuildHandlesTable(db); err == nil {
		t.Fatal("rebuildHandlesTable on closed database succeeded")
	}
}
