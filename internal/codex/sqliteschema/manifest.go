// Package sqliteschema validates versioned SQLite schemas without mutating
// damaged databases.
package sqliteschema

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

type Column struct {
	Name              string
	Type              string
	NotNull           bool
	PrimaryKeyOrdinal int
}

type ForeignKey struct {
	Columns          []string
	ReferenceTable   string
	ReferenceColumns []string
	OnUpdate         string
	OnDelete         string
}

type Index struct {
	Name    string
	Unique  bool
	Columns []string
	SQL     string
}

type Table struct {
	Name              string
	SQL               string
	Columns           []Column
	ForeignKeys       []ForeignKey
	Indexes           []Index
	UniqueConstraints [][]string
}

func ExistingTableCount(db *gorm.DB, names []string) (int, error) {
	count := 0
	for _, name := range names {
		var exists int
		result := db.Raw(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			name,
		).Scan(&exists)
		if result.Error != nil {
			return 0, fmt.Errorf("inspect SQLite table %q: %w", name, result.Error)
		}
		if exists != 0 {
			count++
		}
	}
	return count, nil
}

func Validate(db *gorm.DB, manifest []Table) error {
	for _, expected := range manifest {
		if err := validateTable(db, expected); err != nil {
			return err
		}
	}
	return nil
}

type columnRow struct {
	CID     int
	Name    string
	Type    string
	NotNull int `gorm:"column:not_null"`
	PK      int
	Hidden  int
}

func validateTable(db *gorm.DB, expected Table) error {
	var actualSQL sql.NullString
	result := db.Raw(
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
		expected.Name,
	).Scan(&actualSQL)
	if result.Error != nil {
		return fmt.Errorf("inspect SQLite table %q: %w", expected.Name, result.Error)
	}
	if result.RowsAffected != 1 || !actualSQL.Valid {
		return fmt.Errorf("SQLite table %q is missing", expected.Name)
	}
	if canonicalSQL(actualSQL.String) != canonicalSQL(expected.SQL) {
		return fmt.Errorf("SQLite table %q definition does not match its versioned manifest", expected.Name)
	}

	var columns []columnRow
	if err := db.Raw(`SELECT cid, name, type, "notnull" AS not_null, pk, hidden
		FROM pragma_table_xinfo(?) ORDER BY cid`, expected.Name).Scan(&columns).Error; err != nil {
		return fmt.Errorf("inspect SQLite columns for %q: %w", expected.Name, err)
	}
	if len(columns) != len(expected.Columns) {
		return fmt.Errorf("SQLite table %q has %d columns; want %d", expected.Name, len(columns), len(expected.Columns))
	}
	for position, want := range expected.Columns {
		got := columns[position]
		if got.Hidden != 0 || got.Name != want.Name || !strings.EqualFold(got.Type, want.Type) ||
			(got.NotNull != 0) != want.NotNull || got.PK != want.PrimaryKeyOrdinal {
			return fmt.Errorf("SQLite table %q column %q does not match its versioned manifest", expected.Name, want.Name)
		}
	}
	if err := validateForeignKeys(db, expected); err != nil {
		return err
	}
	return validateIndexes(db, expected)
}

type foreignKeyRow struct {
	ID              int
	Seq             int
	ReferenceTable  string `gorm:"column:reference_table"`
	SourceColumn    string `gorm:"column:source_column"`
	ReferenceColumn string `gorm:"column:reference_column"`
	OnUpdate        string
	OnDelete        string
}

func validateForeignKeys(db *gorm.DB, table Table) error {
	var rows []foreignKeyRow
	if err := db.Raw(`SELECT id, seq, "table" AS reference_table, "from" AS source_column,
		"to" AS reference_column, on_update, on_delete
		FROM pragma_foreign_key_list(?) ORDER BY id, seq`, table.Name).Scan(&rows).Error; err != nil {
		return fmt.Errorf("inspect SQLite foreign keys for %q: %w", table.Name, err)
	}
	actual := make([]ForeignKey, 0)
	for _, row := range rows {
		if len(actual) == 0 || row.Seq == 0 {
			actual = append(actual, ForeignKey{
				ReferenceTable: row.ReferenceTable,
				OnUpdate:       strings.ToUpper(row.OnUpdate),
				OnDelete:       strings.ToUpper(row.OnDelete),
			})
		}
		last := &actual[len(actual)-1]
		last.Columns = append(last.Columns, row.SourceColumn)
		last.ReferenceColumns = append(last.ReferenceColumns, row.ReferenceColumn)
	}
	if foreignKeySignatures(actual) != foreignKeySignatures(table.ForeignKeys) {
		return fmt.Errorf("SQLite table %q foreign keys do not match its versioned manifest", table.Name)
	}
	return nil
}

type indexRow struct {
	Name     string
	IsUnique int `gorm:"column:is_unique"`
	Origin   string
	Partial  int
}

func validateIndexes(db *gorm.DB, table Table) error {
	var rows []indexRow
	if err := db.Raw(`SELECT name, "unique" AS is_unique, origin, partial
		FROM pragma_index_list(?) ORDER BY name`, table.Name).Scan(&rows).Error; err != nil {
		return fmt.Errorf("inspect SQLite indexes for %q: %w", table.Name, err)
	}
	named := make(map[string]indexRow, len(rows))
	uniqueConstraints := make([][]string, 0)
	for _, row := range rows {
		columns, err := readIndexColumns(db, row.Name)
		if err != nil {
			return err
		}
		switch row.Origin {
		case "c":
			named[row.Name] = row
		case "u":
			uniqueConstraints = append(uniqueConstraints, columns)
		}
	}
	if len(named) != len(table.Indexes) {
		return fmt.Errorf("SQLite table %q has an unexpected named-index set", table.Name)
	}
	for _, expected := range table.Indexes {
		row, exists := named[expected.Name]
		if !exists || (row.IsUnique != 0) != expected.Unique || row.Partial != 0 {
			return fmt.Errorf("SQLite index %q does not match its versioned manifest", expected.Name)
		}
		columns, err := readIndexColumns(db, row.Name)
		if err != nil {
			return err
		}
		if strings.Join(columns, "\x00") != strings.Join(expected.Columns, "\x00") {
			return fmt.Errorf("SQLite index %q columns do not match their versioned order", expected.Name)
		}
		var actualSQL sql.NullString
		result := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", row.Name).Scan(&actualSQL)
		if result.Error != nil {
			return fmt.Errorf("inspect SQLite index %q: %w", row.Name, result.Error)
		}
		if result.RowsAffected != 1 || !actualSQL.Valid || canonicalSQL(actualSQL.String) != canonicalSQL(expected.SQL) {
			return fmt.Errorf("SQLite index %q definition does not match its versioned manifest", row.Name)
		}
	}
	if columnSetSignatures(uniqueConstraints) != columnSetSignatures(table.UniqueConstraints) {
		return fmt.Errorf("SQLite table %q unique constraints do not match its versioned manifest", table.Name)
	}
	return nil
}

func readIndexColumns(db *gorm.DB, indexName string) ([]string, error) {
	var rows []struct {
		SeqNo int
		Name  string
	}
	if err := db.Raw("SELECT seqno, name FROM pragma_index_info(?) ORDER BY seqno", indexName).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("inspect SQLite index %q columns: %w", indexName, err)
	}
	columns := make([]string, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, row.Name)
	}
	return columns, nil
}

func foreignKeySignatures(keys []ForeignKey) string {
	signatures := make([]string, 0, len(keys))
	for _, key := range keys {
		signatures = append(signatures, strings.Join([]string{
			strings.Join(key.Columns, ","), key.ReferenceTable,
			strings.Join(key.ReferenceColumns, ","), strings.ToUpper(key.OnUpdate), strings.ToUpper(key.OnDelete),
		}, "|"))
	}
	sort.Strings(signatures)
	return strings.Join(signatures, "\x00")
}

func columnSetSignatures(columnSets [][]string) string {
	signatures := make([]string, 0, len(columnSets))
	for _, columns := range columnSets {
		signatures = append(signatures, strings.Join(columns, ","))
	}
	sort.Strings(signatures)
	return strings.Join(signatures, "\x00")
}

func canonicalSQL(statement string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(statement) {
		if !unicode.IsSpace(character) && character != ';' && character != '"' && character != '`' && character != '[' && character != ']' {
			builder.WriteRune(character)
		}
	}
	return strings.ReplaceAll(builder.String(), "ifnotexists", "")
}
