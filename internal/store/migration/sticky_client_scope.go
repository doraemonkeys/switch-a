package migration

import (
	"slices"

	"gorm.io/gorm"
)

const stickyCodexAPIType = "codex"

// MigrateStickyClientScope rebuilds the primary key explicitly because adding a
// GORM primaryKey field does not replace SQLite's existing composite key.
func MigrateStickyClientScope(db *gorm.DB) error {
	var columns []struct {
		Name string
		PK   int
	}
	if err := db.Raw("PRAGMA table_info(sticky_entries)").Scan(&columns).Error; err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}
	expected := []string{"ip", "user", "api_type", "model", "client_scope"}
	primaryKey := make([]string, len(expected))
	hasScope := false
	primaryKeyCount := 0
	for _, column := range columns {
		hasScope = hasScope || column.Name == "client_scope"
		if column.PK > 0 {
			primaryKeyCount++
			if column.PK <= len(primaryKey) {
				primaryKey[column.PK-1] = column.Name
			}
		}
	}
	if primaryKeyCount == len(expected) && slices.Equal(primaryKey, expected) {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE TABLE sticky_entries_scoped (
			ip text, user text, api_type text, model text,
			client_scope text NOT NULL DEFAULT '',
			provider_id text NOT NULL, expires_at datetime NOT NULL, updated_at datetime NOT NULL,
			PRIMARY KEY (ip, user, api_type, model, client_scope)
		)`).Error; err != nil {
			return err
		}
		// Old Codex rows cannot identify which credential established affinity.
		// Preserve other APIs and scoped rows if an interrupted older deployment
		// added the column without replacing its primary key.
		scopeExpression := "''"
		if hasScope {
			scopeExpression = "COALESCE(client_scope, '')"
		}
		copySQL := `INSERT INTO sticky_entries_scoped
			(ip, user, api_type, model, client_scope, provider_id, expires_at, updated_at)
			SELECT ip, user, api_type, model, ` + scopeExpression + `, provider_id, expires_at, updated_at
			FROM sticky_entries WHERE api_type <> ? OR ` + scopeExpression + ` <> ''`
		if err := tx.Exec(copySQL, stickyCodexAPIType).Error; err != nil {
			return err
		}
		if err := tx.Exec("DROP TABLE sticky_entries").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE sticky_entries_scoped RENAME TO sticky_entries").Error
	})
}
