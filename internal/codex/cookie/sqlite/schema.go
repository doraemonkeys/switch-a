// Package sqlite persists Codex provider-Cookie handles, authorities, and
// encrypted Cookie entries in an independent schema family.
package sqlite

import (
	"context"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/sqliteschema"
	"gorm.io/gorm"
)

const (
	CurrentSchemaVersion = 2
	schemaRowID          = 1

	schemaTable      = "codex_provider_cookie_schema_meta"
	handlesTable     = "codex_provider_cookie_handles"
	authoritiesTable = "codex_provider_cookie_authorities"
	entriesTable     = "codex_provider_cookie_entries"
)

const schemaDefinition = `CREATE TABLE codex_provider_cookie_schema_meta (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL CHECK (version >= 1)
)`

const handlesDefinition = `CREATE TABLE codex_provider_cookie_handles (
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

const handlesExpiryIndex = `CREATE INDEX idx_codex_provider_cookie_handles_expiry
	ON codex_provider_cookie_handles(idle_expires_at_ms, absolute_expires_at_ms)`

const authoritiesDefinition = `CREATE TABLE codex_provider_cookie_authorities (
		jar_id BLOB NOT NULL CHECK (length(jar_id) = 32),
		authority BLOB NOT NULL CHECK (length(authority) > 0),
		created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
		last_access_at_ms INTEGER NOT NULL CHECK (last_access_at_ms >= created_at_ms),
		unreachable_since_ms INTEGER NULL CHECK (unreachable_since_ms IS NULL OR unreachable_since_ms > 0),
		PRIMARY KEY (jar_id, authority),
		FOREIGN KEY (jar_id) REFERENCES codex_provider_cookie_handles(jar_id) ON DELETE CASCADE
) WITHOUT ROWID`

const authoritiesOrphanIndex = `CREATE INDEX idx_codex_provider_cookie_authorities_orphan
	ON codex_provider_cookie_authorities(unreachable_since_ms)`

const entriesDefinition = `CREATE TABLE codex_provider_cookie_entries (
		jar_id BLOB NOT NULL CHECK (length(jar_id) = 32),
		authority BLOB NOT NULL CHECK (length(authority) > 0),
		cookie_name TEXT NOT NULL CHECK (length(cookie_name) BETWEEN 1 AND 256),
		cookie_domain TEXT NOT NULL CHECK (length(cookie_domain) BETWEEN 1 AND 253),
		cookie_path TEXT NOT NULL CHECK (length(cookie_path) BETWEEN 1 AND 1024),
		value_key_version TEXT NOT NULL CHECK (length(value_key_version) BETWEEN 1 AND 32),
		value_nonce BLOB NOT NULL CHECK (length(value_nonce) = 12),
		value_ciphertext BLOB NOT NULL CHECK (length(value_ciphertext) >= 16),
		host_only INTEGER NOT NULL CHECK (host_only IN (0, 1)),
		secure INTEGER NOT NULL CHECK (secure IN (0, 1)),
		http_only INTEGER NOT NULL CHECK (http_only IN (0, 1)),
		quoted INTEGER NOT NULL CHECK (quoted IN (0, 1)),
		session INTEGER NOT NULL CHECK (session IN (0, 1)),
		same_site INTEGER NOT NULL CHECK (same_site BETWEEN 0 AND 3),
		expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > 0),
		created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
		last_access_at_ms INTEGER NOT NULL CHECK (last_access_at_ms >= created_at_ms),
		PRIMARY KEY (jar_id, authority, cookie_name, cookie_domain, cookie_path),
		FOREIGN KEY (jar_id, authority)
			REFERENCES codex_provider_cookie_authorities(jar_id, authority) ON DELETE CASCADE
) WITHOUT ROWID`

const entriesExpiryIndex = `CREATE INDEX idx_codex_provider_cookie_entries_expiry
	ON codex_provider_cookie_entries(expires_at_ms)`

const entriesEvictionIndex = `CREATE INDEX idx_codex_provider_cookie_entries_eviction
	ON codex_provider_cookie_entries(jar_id, authority, last_access_at_ms, created_at_ms)`

var createSchemaStatements = []string{
	handlesDefinition,
	handlesExpiryIndex,
	authoritiesDefinition,
	authoritiesOrphanIndex,
	entriesDefinition,
	entriesExpiryIndex,
	entriesEvictionIndex,
}

var schemaManifest = []sqliteschema.Table{
	{
		Name: schemaTable,
		SQL:  schemaDefinition,
		Columns: []sqliteschema.Column{
			{Name: "id", Type: "INTEGER", PrimaryKeyOrdinal: 1},
			{Name: "version", Type: "INTEGER", NotNull: true},
		},
	},
	{
		Name: handlesTable,
		SQL:  handlesDefinition,
		Columns: []sqliteschema.Column{
			{Name: "handle_key_version", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 1},
			{Name: "handle_digest", Type: "BLOB", NotNull: true, PrimaryKeyOrdinal: 2},
			{Name: "jar_id", Type: "BLOB", NotNull: true},
			{Name: "client_scope_key_version", Type: "TEXT", NotNull: true},
			{Name: "client_scope_digest", Type: "BLOB", NotNull: true},
			{Name: "created_at_ms", Type: "INTEGER", NotNull: true},
			{Name: "last_access_at_ms", Type: "INTEGER", NotNull: true},
			{Name: "idle_expires_at_ms", Type: "INTEGER", NotNull: true},
			{Name: "absolute_expires_at_ms", Type: "INTEGER", NotNull: true},
		},
		Indexes: []sqliteschema.Index{{
			Name: "idx_codex_provider_cookie_handles_expiry", Columns: []string{"idle_expires_at_ms", "absolute_expires_at_ms"}, SQL: handlesExpiryIndex,
		}},
		UniqueConstraints: [][]string{{"jar_id"}, {"client_scope_key_version", "client_scope_digest"}},
	},
	{
		Name: authoritiesTable,
		SQL:  authoritiesDefinition,
		Columns: []sqliteschema.Column{
			{Name: "jar_id", Type: "BLOB", NotNull: true, PrimaryKeyOrdinal: 1},
			{Name: "authority", Type: "BLOB", NotNull: true, PrimaryKeyOrdinal: 2},
			{Name: "created_at_ms", Type: "INTEGER", NotNull: true},
			{Name: "last_access_at_ms", Type: "INTEGER", NotNull: true},
			{Name: "unreachable_since_ms", Type: "INTEGER"},
		},
		ForeignKeys: []sqliteschema.ForeignKey{{
			Columns: []string{"jar_id"}, ReferenceTable: handlesTable, ReferenceColumns: []string{"jar_id"}, OnUpdate: "NO ACTION", OnDelete: "CASCADE",
		}},
		Indexes: []sqliteschema.Index{{
			Name: "idx_codex_provider_cookie_authorities_orphan", Columns: []string{"unreachable_since_ms"}, SQL: authoritiesOrphanIndex,
		}},
	},
	{
		Name: entriesTable,
		SQL:  entriesDefinition,
		Columns: []sqliteschema.Column{
			{Name: "jar_id", Type: "BLOB", NotNull: true, PrimaryKeyOrdinal: 1},
			{Name: "authority", Type: "BLOB", NotNull: true, PrimaryKeyOrdinal: 2},
			{Name: "cookie_name", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 3},
			{Name: "cookie_domain", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 4},
			{Name: "cookie_path", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 5},
			{Name: "value_key_version", Type: "TEXT", NotNull: true},
			{Name: "value_nonce", Type: "BLOB", NotNull: true},
			{Name: "value_ciphertext", Type: "BLOB", NotNull: true},
			{Name: "host_only", Type: "INTEGER", NotNull: true},
			{Name: "secure", Type: "INTEGER", NotNull: true},
			{Name: "http_only", Type: "INTEGER", NotNull: true},
			{Name: "quoted", Type: "INTEGER", NotNull: true},
			{Name: "session", Type: "INTEGER", NotNull: true},
			{Name: "same_site", Type: "INTEGER", NotNull: true},
			{Name: "expires_at_ms", Type: "INTEGER", NotNull: true},
			{Name: "created_at_ms", Type: "INTEGER", NotNull: true},
			{Name: "last_access_at_ms", Type: "INTEGER", NotNull: true},
		},
		ForeignKeys: []sqliteschema.ForeignKey{{
			Columns: []string{"jar_id", "authority"}, ReferenceTable: authoritiesTable,
			ReferenceColumns: []string{"jar_id", "authority"}, OnUpdate: "NO ACTION", OnDelete: "CASCADE",
		}},
		Indexes: []sqliteschema.Index{
			{Name: "idx_codex_provider_cookie_entries_expiry", Columns: []string{"expires_at_ms"}, SQL: entriesExpiryIndex},
			{Name: "idx_codex_provider_cookie_entries_eviction", Columns: []string{"jar_id", "authority", "last_access_at_ms", "created_at_ms"}, SQL: entriesEvictionIndex},
		},
	},
}

// Migrate owns only the M3 provider-Cookie schema transaction. The global
// registrar remains responsible for calling it after M1 and M2.
func Migrate(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil {
		return &providercookie.ConfigurationError{Field: "database", Reason: "context and database are required"}
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := sqliteschema.ExistingTableCount(tx, []string{schemaTable, handlesTable, authoritiesTable, entriesTable})
		if err != nil {
			return storageError("inspect_schema", err)
		}
		if existing != 0 && existing != len(schemaManifest) {
			return corruptError("migrate_schema", fmt.Errorf("provider-Cookie schema is partial: found %d of %d tables", existing, len(schemaManifest)))
		}
		if existing == 0 {
			if err := tx.Exec(schemaDefinition).Error; err != nil {
				return storageError("create_schema_meta", err)
			}
			for _, statement := range createSchemaStatements {
				if err := tx.Exec(statement).Error; err != nil {
					return storageError("create_schema", err)
				}
			}
			if err := tx.Exec(
				"INSERT INTO "+schemaTable+" (id, version) VALUES (?, ?)",
				schemaRowID,
				CurrentSchemaVersion,
			).Error; err != nil {
				return storageError("publish_schema_version", err)
			}
			return validateSchema(ctx, tx)
		}

		version, exists, err := readSchemaVersion(ctx, tx)
		if err != nil {
			return err
		}
		if !exists {
			return corruptError("migrate_schema", fmt.Errorf("schema version row is missing"))
		}
		if version > CurrentSchemaVersion {
			return corruptError("migrate_schema", fmt.Errorf("schema version %d is newer than supported version %d", version, CurrentSchemaVersion))
		}
		if version != CurrentSchemaVersion {
			return corruptError("migrate_schema", fmt.Errorf("unsupported schema version %d", version))
		}
		return validateSchema(ctx, tx)
	})
	return classifyDatabaseError("migrate_schema", err)
}

func ValidateSchema(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil {
		return &providercookie.ConfigurationError{Field: "database", Reason: "context and database are required"}
	}
	return validateSchema(ctx, db.WithContext(ctx))
}

func validateSchema(ctx context.Context, db *gorm.DB) error {
	existing, err := sqliteschema.ExistingTableCount(db.WithContext(ctx), []string{schemaTable, handlesTable, authoritiesTable, entriesTable})
	if err != nil {
		return storageError("inspect_schema", err)
	}
	if existing != len(schemaManifest) {
		return corruptError("validate_schema", fmt.Errorf("provider-Cookie schema is partial: found %d of %d tables", existing, len(schemaManifest)))
	}
	version, exists, err := readSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if !exists || version != CurrentSchemaVersion {
		return corruptError("validate_schema", fmt.Errorf("schema version is missing or unsupported"))
	}
	if err := sqliteschema.Validate(db.WithContext(ctx), schemaManifest); err != nil {
		return corruptError("validate_schema", err)
	}
	return nil
}

func readSchemaVersion(ctx context.Context, db *gorm.DB) (int, bool, error) {
	var row struct{ Version int }
	result := db.WithContext(ctx).Raw(
		"SELECT version FROM "+schemaTable+" WHERE id = ?",
		schemaRowID,
	).Scan(&row)
	if result.Error != nil {
		return 0, false, storageError("read_schema_version", result.Error)
	}
	if result.RowsAffected == 0 {
		return 0, false, nil
	}
	return row.Version, true, nil
}

func storageError(operation string, cause error) error {
	return &providercookie.PersistenceError{
		Kind:      providercookie.PersistenceUnavailable,
		Operation: operation,
		Cause:     cause,
	}
}

func corruptError(operation string, cause error) error {
	return &providercookie.PersistenceError{
		Kind:      providercookie.PersistenceCorrupt,
		Operation: operation,
		Cause:     cause,
	}
}
