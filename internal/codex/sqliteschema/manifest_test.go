package sqliteschema

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const parentSQL = `CREATE TABLE manifest_parents (
	key TEXT NOT NULL,
	ordinal INTEGER NOT NULL,
	label TEXT,
	PRIMARY KEY (key, ordinal),
	UNIQUE (ordinal, key)
) WITHOUT ROWID`

const childSQL = `CREATE TABLE manifest_children (
	parent_key TEXT NOT NULL,
	parent_ordinal INTEGER NOT NULL,
	value BLOB NOT NULL,
	PRIMARY KEY (parent_key, parent_ordinal),
	FOREIGN KEY (parent_key, parent_ordinal)
		REFERENCES manifest_parents(key, ordinal) ON DELETE CASCADE
) WITHOUT ROWID`

const childIndexSQL = `CREATE INDEX IF NOT EXISTS idx_manifest_children_value
	ON manifest_children(value, parent_key)`

func TestValidateExactManifestAndTableCount(t *testing.T) {
	db := openManifestDB(t)
	createManifestSchema(t, db)
	manifest := validManifest()
	if err := Validate(db, manifest); err != nil {
		t.Fatal(err)
	}
	count, err := ExistingTableCount(db, []string{"manifest_parents", "manifest_children", "missing"})
	if err != nil || count != 2 {
		t.Fatalf("ExistingTableCount() = (%d, %v)", count, err)
	}
	if got := canonicalSQL(" CREATE TABLE IF NOT EXISTS x ( id INTEGER ); "); got != "createtablex(idinteger)" {
		t.Fatalf("canonicalSQL() = %q", got)
	}
	if got := foreignKeySignatures([]ForeignKey{{Columns: []string{"b"}}, {Columns: []string{"a"}}}); !strings.HasPrefix(got, "a|") {
		t.Fatalf("foreignKeySignatures() = %q", got)
	}
	if got := columnSetSignatures([][]string{{"b"}, {"a"}}); got != "a\x00b" {
		t.Fatalf("columnSetSignatures() = %q", got)
	}
}

func TestValidateRejectsManifestMetadataDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]Table)
	}{
		{name: "missing table", mutate: func(manifest []Table) { manifest[0].Name = "missing" }},
		{name: "table SQL", mutate: func(manifest []Table) { manifest[0].SQL += " CHECK (1)" }},
		{name: "column count", mutate: func(manifest []Table) { manifest[0].Columns = manifest[0].Columns[:2] }},
		{name: "column name", mutate: func(manifest []Table) { manifest[0].Columns[0].Name = "wrong" }},
		{name: "column type", mutate: func(manifest []Table) { manifest[0].Columns[0].Type = "BLOB" }},
		{name: "column nullability", mutate: func(manifest []Table) { manifest[0].Columns[0].NotNull = false }},
		{name: "primary key ordinal", mutate: func(manifest []Table) { manifest[0].Columns[0].PrimaryKeyOrdinal = 2 }},
		{name: "foreign key", mutate: func(manifest []Table) { manifest[1].ForeignKeys[0].OnDelete = "RESTRICT" }},
		{name: "named index set", mutate: func(manifest []Table) { manifest[1].Indexes = nil }},
		{name: "index uniqueness", mutate: func(manifest []Table) { manifest[1].Indexes[0].Unique = true }},
		{name: "index columns", mutate: func(manifest []Table) { manifest[1].Indexes[0].Columns = []string{"parent_key", "value"} }},
		{name: "index SQL", mutate: func(manifest []Table) { manifest[1].Indexes[0].SQL += " WHERE value IS NOT NULL" }},
		{name: "unique constraint", mutate: func(manifest []Table) { manifest[0].UniqueConstraints = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openManifestDB(t)
			createManifestSchema(t, db)
			manifest := validManifest()
			test.mutate(manifest)
			if err := Validate(db, manifest); err == nil {
				t.Fatal("Validate accepted manifest drift")
			}
		})
	}
}

func TestValidateRejectsUnexpectedAndPartialIndexes(t *testing.T) {
	t.Run("unexpected", func(t *testing.T) {
		db := openManifestDB(t)
		createManifestSchema(t, db)
		if err := db.Exec("CREATE INDEX idx_manifest_children_extra ON manifest_children(parent_key)").Error; err != nil {
			t.Fatal(err)
		}
		if err := Validate(db, validManifest()); err == nil {
			t.Fatal("Validate accepted an unexpected index")
		}
	})
	t.Run("partial", func(t *testing.T) {
		db := openManifestDB(t)
		if err := db.Exec(parentSQL).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(childSQL).Error; err != nil {
			t.Fatal(err)
		}
		partial := strings.Replace(childIndexSQL, ")", ") WHERE value IS NOT NULL", 1)
		if err := db.Exec(partial).Error; err != nil {
			t.Fatal(err)
		}
		manifest := validManifest()
		manifest[1].Indexes[0].SQL = partial
		if err := Validate(db, manifest); err == nil {
			t.Fatal("Validate accepted a partial index")
		}
	})
}

func TestManifestInspectionPropagatesClosedDatabaseErrors(t *testing.T) {
	db := openManifestDB(t)
	database, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExistingTableCount(db, []string{"manifest_parents"}); err == nil {
		t.Fatal("ExistingTableCount accepted a closed database")
	}
	if err := Validate(db, validManifest()); err == nil {
		t.Fatal("Validate accepted a closed database")
	}
}

func validManifest() []Table {
	return []Table{
		{
			Name: "manifest_parents", SQL: parentSQL,
			Columns: []Column{
				{Name: "key", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 1},
				{Name: "ordinal", Type: "INTEGER", NotNull: true, PrimaryKeyOrdinal: 2},
				{Name: "label", Type: "TEXT"},
			},
			UniqueConstraints: [][]string{{"ordinal", "key"}},
		},
		{
			Name: "manifest_children", SQL: childSQL,
			Columns: []Column{
				{Name: "parent_key", Type: "TEXT", NotNull: true, PrimaryKeyOrdinal: 1},
				{Name: "parent_ordinal", Type: "INTEGER", NotNull: true, PrimaryKeyOrdinal: 2},
				{Name: "value", Type: "BLOB", NotNull: true},
			},
			ForeignKeys: []ForeignKey{{
				Columns: []string{"parent_key", "parent_ordinal"}, ReferenceTable: "manifest_parents",
				ReferenceColumns: []string{"key", "ordinal"}, OnUpdate: "NO ACTION", OnDelete: "CASCADE",
			}},
			Indexes: []Index{{Name: "idx_manifest_children_value", Columns: []string{"value", "parent_key"}, SQL: childIndexSQL}},
		},
	}
}

func openManifestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database, err := db.DB()
		if err == nil {
			_ = database.Close()
		}
	})
	return db
}

func createManifestSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{parentSQL, childSQL, childIndexSQL} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}
