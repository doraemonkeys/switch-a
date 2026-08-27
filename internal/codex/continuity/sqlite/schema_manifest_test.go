package sqlite

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestContinuitySchemaRejectsEveryManifestDamageWithoutRepair(t *testing.T) {
	tests := []struct {
		name   string
		target string
		mutate func(string) string
	}{
		{name: "metadata type", target: schemaMetaTable, mutate: replaceContinuitySchema("version INTEGER", "version TEXT")},
		{name: "column type", target: bindingsTable, mutate: replaceContinuitySchema("opaque_digest BLOB", "opaque_digest TEXT")},
		{name: "not null", target: bindingsTable, mutate: replaceContinuitySchema("opaque_digest BLOB NOT NULL", "opaque_digest BLOB")},
		{name: "primary key ordinal", target: bindingsTable, mutate: replaceContinuitySchema(
			"PRIMARY KEY (kind, opaque_key_version, opaque_digest)", "PRIMARY KEY (kind, opaque_digest, opaque_key_version)")},
		{name: "kind check", target: bindingsTable, mutate: replaceContinuitySchema(
			"CHECK (kind IN ('thread_id','session_id','conversation_id','window_id','turn_state','turn_metadata','response_reference'))",
			"CHECK (length(kind) > 0)")},
		{name: "subject check", target: bindingsTable, mutate: replaceContinuitySchema(
			"(protocol_subject_kind = 'account' AND protocol_subject_account IS NOT NULL",
			"(protocol_subject_kind = 'account' AND protocol_subject_account IS NULL")},
		{name: "lifecycle check", target: bindingsTable, mutate: replaceContinuitySchema(
			"(lifecycle = 'pending' AND committed_at_ns IS NULL", "(lifecycle = 'pending' AND committed_at_ns IS NOT NULL")},
		{name: "without rowid", target: bindingsTable, mutate: replaceContinuitySchema(" WITHOUT ROWID", "")},
		{name: "expiry index", target: "idx_codex_continuity_expiry", mutate: func(string) string { return "" }},
		{name: "tombstone index", target: "idx_codex_continuity_tombstone", mutate: func(string) string { return "" }},
		{name: "versions index", target: "idx_codex_continuity_versions", mutate: func(string) string { return "" }},
		{name: "index column order", target: "idx_codex_continuity_versions", mutate: replaceContinuitySchema(
			"(opaque_key_version, client_key_version, protocol_subject_key_version)",
			"(client_key_version, opaque_key_version, protocol_subject_key_version)")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, closeDB := openTestDB(t)
			defer closeDB()
			createDamagedContinuitySchema(t, db, test.target, test.mutate)
			before := continuitySchemaObjectCount(t, db)
			if err := ValidateSchema(context.Background(), db); err == nil {
				t.Fatal("ValidateSchema accepted damaged schema")
			}
			if err := Migrate(context.Background(), db); err == nil {
				t.Fatal("Migrate repaired damaged schema")
			}
			if after := continuitySchemaObjectCount(t, db); after != before {
				t.Fatalf("damaged schema object count changed from %d to %d", before, after)
			}
		})
	}
}

func TestContinuitySchemaMigrationRollsBackFreshDDLFailure(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()
	if err := db.Exec("CREATE TABLE idx_codex_continuity_expiry (id INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err == nil {
		t.Fatal("Migrate succeeded despite conflicting schema object")
	}
	for _, table := range []string{schemaMetaTable, bindingsTable} {
		var count int
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed migration left table %q behind", table)
		}
	}
}

func createDamagedContinuitySchema(t *testing.T, db *gorm.DB, target string, mutate func(string) string) {
	t.Helper()
	statements := []string{
		schemaMetaDefinition,
		bindingsCreateStatement,
		expiryIndexStatement,
		tombstoneIndexStatement,
		versionsIndexStatement,
	}
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
	if err := db.Exec("INSERT INTO "+schemaMetaTable+" (id, version) VALUES (?, ?)", schemaRowID, CurrentSchemaVersion).Error; err != nil {
		t.Fatal(err)
	}
}

func replaceContinuitySchema(old, replacement string) func(string) string {
	return func(statement string) string {
		return strings.Replace(statement, old, replacement, 1)
	}
}

func continuitySchemaObjectCount(t *testing.T, db *gorm.DB) int {
	t.Helper()
	var count int
	if err := db.Raw(`SELECT COUNT(*) FROM sqlite_master
		WHERE (type = 'table' OR type = 'index') AND name LIKE 'codex_continuity_%'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
