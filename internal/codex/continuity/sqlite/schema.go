// Package sqlite persists Codex continuity owner bindings without depending on
// the application's broad Store or migration registrar.
package sqlite

import (
	"context"
	"errors"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/sqliteschema"
	"gorm.io/gorm"
)

const (
	CurrentSchemaVersion = 1
	schemaRowID          = 1
	schemaMetaTable      = "codex_continuity_schema_meta"
	bindingsTable        = "codex_continuity_bindings"
)

const schemaMetaDefinition = `CREATE TABLE codex_continuity_schema_meta (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL CHECK (version > 0)
)`

const bindingsDefinition = `(
	kind TEXT NOT NULL CHECK (kind IN ('thread_id','session_id','conversation_id','window_id','turn_state','turn_metadata','response_reference')),
	opaque_key_version TEXT NOT NULL CHECK (length(opaque_key_version) BETWEEN 1 AND 32),
	opaque_digest BLOB NOT NULL CHECK (length(opaque_digest) = 32),
	client_key_version TEXT NOT NULL CHECK (length(client_key_version) BETWEEN 1 AND 32),
	client_digest BLOB NOT NULL CHECK (length(client_digest) = 32),
	protocol_vendor TEXT NOT NULL CHECK (length(protocol_vendor) > 0),
	protocol_origin TEXT NOT NULL CHECK (length(protocol_origin) > 0),
	protocol_subject_kind TEXT NOT NULL CHECK (protocol_subject_kind IN ('account','keyed-digest')),
	protocol_subject_account TEXT NULL,
	protocol_subject_key_version TEXT NULL,
	protocol_subject_digest BLOB NULL,
	protocol_api_type TEXT NOT NULL CHECK (length(protocol_api_type) > 0),
	route_target_hint TEXT NOT NULL,
	claim_operation_id TEXT NOT NULL CHECK (length(claim_operation_id) BETWEEN 1 AND 128),
	lifecycle TEXT NOT NULL CHECK (lifecycle IN ('pending','committed','tombstone')),
	created_at_ns INTEGER NOT NULL,
	updated_at_ns INTEGER NOT NULL,
	committed_at_ns INTEGER NULL,
	expires_at_ns INTEGER NOT NULL,
	tombstone_until_ns INTEGER NULL,
	PRIMARY KEY (kind, opaque_key_version, opaque_digest),
	CHECK (
		(protocol_subject_kind = 'account' AND protocol_subject_account IS NOT NULL
			AND length(protocol_subject_account) > 0 AND protocol_subject_key_version IS NULL
			AND protocol_subject_digest IS NULL) OR
		(protocol_subject_kind = 'keyed-digest' AND protocol_subject_account IS NULL
			AND protocol_subject_key_version IS NOT NULL
			AND length(protocol_subject_key_version) BETWEEN 1 AND 32
			AND protocol_subject_digest IS NOT NULL AND length(protocol_subject_digest) = 32)
	),
	CHECK (
		(lifecycle = 'pending' AND committed_at_ns IS NULL AND tombstone_until_ns IS NULL) OR
		(lifecycle = 'committed' AND committed_at_ns IS NOT NULL AND tombstone_until_ns IS NULL) OR
		(lifecycle = 'tombstone' AND tombstone_until_ns IS NOT NULL)
	)
)`

const bindingsCreateStatement = "CREATE TABLE " + bindingsTable + " " + bindingsDefinition + " WITHOUT ROWID"

const (
	expiryIndexStatement    = "CREATE INDEX IF NOT EXISTS idx_codex_continuity_expiry ON codex_continuity_bindings(lifecycle, expires_at_ns)"
	tombstoneIndexStatement = "CREATE INDEX IF NOT EXISTS idx_codex_continuity_tombstone ON codex_continuity_bindings(tombstone_until_ns)"
	versionsIndexStatement  = "CREATE INDEX IF NOT EXISTS idx_codex_continuity_versions ON codex_continuity_bindings(opaque_key_version, client_key_version, protocol_subject_key_version)"
)

var schemaManifest = []sqliteschema.Table{
	{
		Name: schemaMetaTable,
		SQL:  schemaMetaDefinition,
		Columns: []sqliteschema.Column{
			{Name: "id", Type: "INTEGER", PrimaryKeyOrdinal: 1},
			{Name: "version", Type: "INTEGER", NotNull: true},
		},
	},
	{
		Name: bindingsTable,
		SQL:  bindingsCreateStatement,
		Columns: []sqliteschema.Column{
			{Name: "kind", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 1},
			{Name: "opaque_key_version", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 2},
			{Name: "opaque_digest", Type: "BLOB", NotNull: true, PrimaryKeyOrdinal: 3},
			{Name: "client_key_version", Type: "TEXT", NotNull: true},
			{Name: "client_digest", Type: "BLOB", NotNull: true},
			{Name: "protocol_vendor", Type: "TEXT", NotNull: true},
			{Name: "protocol_origin", Type: "TEXT", NotNull: true},
			{Name: "protocol_subject_kind", Type: "TEXT", NotNull: true},
			{Name: "protocol_subject_account", Type: "TEXT"},
			{Name: "protocol_subject_key_version", Type: "TEXT"},
			{Name: "protocol_subject_digest", Type: "BLOB"},
			{Name: "protocol_api_type", Type: "TEXT", NotNull: true},
			{Name: "route_target_hint", Type: "TEXT", NotNull: true},
			{Name: "claim_operation_id", Type: "TEXT", NotNull: true},
			{Name: "lifecycle", Type: "TEXT", NotNull: true},
			{Name: "created_at_ns", Type: "INTEGER", NotNull: true},
			{Name: "updated_at_ns", Type: "INTEGER", NotNull: true},
			{Name: "committed_at_ns", Type: "INTEGER"},
			{Name: "expires_at_ns", Type: "INTEGER", NotNull: true},
			{Name: "tombstone_until_ns", Type: "INTEGER"},
		},
		Indexes: []sqliteschema.Index{
			{Name: "idx_codex_continuity_expiry", Columns: []string{"lifecycle", "expires_at_ns"}, SQL: expiryIndexStatement},
			{Name: "idx_codex_continuity_tombstone", Columns: []string{"tombstone_until_ns"}, SQL: tombstoneIndexStatement},
			{Name: "idx_codex_continuity_versions", Columns: []string{"opaque_key_version", "client_key_version", "protocol_subject_key_version"}, SQL: versionsIndexStatement},
		},
	},
}

// Migrate is the M2 registrar seam. DB2 calls it after the credential-session
// migration; this package intentionally does not register itself globally.
func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("continuity schema database is required")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := sqliteschema.ExistingTableCount(tx, []string{schemaMetaTable, bindingsTable})
		if err != nil {
			return fmt.Errorf("inspect continuity schema: %w", err)
		}
		if existing != 0 && existing != len(schemaManifest) {
			return fmt.Errorf("continuity schema is partial: found %d of %d tables", existing, len(schemaManifest))
		}
		if existing == 0 {
			if err := tx.Exec(schemaMetaDefinition).Error; err != nil {
				return fmt.Errorf("create continuity schema metadata: %w", err)
			}
			if err := tx.Exec(bindingsCreateStatement).Error; err != nil {
				return fmt.Errorf("create continuity bindings: %w", err)
			}
			if err := createIndexes(tx); err != nil {
				return err
			}
			if err := tx.Exec(
				"INSERT INTO "+schemaMetaTable+" (id, version) VALUES (?, ?)",
				schemaRowID,
				CurrentSchemaVersion,
			).Error; err != nil {
				return fmt.Errorf("initialize continuity schema version: %w", err)
			}
			return validateSchema(tx)
		}

		version, exists, err := readSchemaVersion(tx)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("continuity schema version row is missing")
		}
		if exists && version > CurrentSchemaVersion {
			return fmt.Errorf(
				"continuity schema version %d is newer than supported version %d",
				version,
				CurrentSchemaVersion,
			)
		}
		if exists && version < CurrentSchemaVersion {
			return fmt.Errorf("unsupported continuity schema upgrade from version %d", version)
		}
		return validateSchema(tx)
	})
}

// ValidateSchema is read-only so startup capability checks cannot mutate a
// partially registered database or hide migration ordering mistakes.
func ValidateSchema(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("continuity schema database is required")
	}
	return validateSchema(db.WithContext(ctx))
}

func validateSchema(configured *gorm.DB) error {
	existing, err := sqliteschema.ExistingTableCount(configured, []string{schemaMetaTable, bindingsTable})
	if err != nil {
		return err
	}
	if existing != len(schemaManifest) {
		return fmt.Errorf("continuity schema is partial: found %d of %d tables", existing, len(schemaManifest))
	}
	version, exists, err := readSchemaVersion(configured)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("continuity schema version row is missing")
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf(
			"continuity schema version %d does not match supported version %d",
			version,
			CurrentSchemaVersion,
		)
	}
	if err := sqliteschema.Validate(configured, schemaManifest); err != nil {
		return fmt.Errorf("validate continuity schema manifest: %w", err)
	}
	return nil
}

func createIndexes(tx *gorm.DB) error {
	statements := []string{
		expiryIndexStatement,
		tombstoneIndexStatement,
		versionsIndexStatement,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create continuity index: %w", err)
		}
	}
	return nil
}

func readSchemaVersion(db *gorm.DB) (int, bool, error) {
	var row struct{ Version int }
	err := db.Raw("SELECT version FROM "+schemaMetaTable+" WHERE id = ?", schemaRowID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read continuity schema version: %w", err)
	}
	return row.Version, true, nil
}

func tableExists(db *gorm.DB, table string) (bool, error) {
	count, err := sqliteschema.ExistingTableCount(db, []string{table})
	if err != nil {
		return false, err
	}
	return count == 1, nil
}
