package migration

import (
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/health/state"
	"gorm.io/gorm"
)

// MigrateProviderTransports preserves the old implicit Codex HTTP/WS capability
// as two explicit routes. Both retain the original credential session identity.
func MigrateProviderTransports(db *gorm.DB) error {
	if db.Migrator().HasColumn("provider_api_types", "transport") {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		statements := []string{
			// Include GORM's named provider constraint before publishing bindings.
			// Adding it later rebuilds this parent table and cascades away every binding.
			`CREATE TABLE provider_api_types_transport (provider_id TEXT NOT NULL, api_type TEXT NOT NULL, transport TEXT NOT NULL DEFAULT 'http', base_url TEXT NOT NULL DEFAULT '', PRIMARY KEY(provider_id, api_type, transport), CONSTRAINT fk_providers_api_types FOREIGN KEY(provider_id) REFERENCES providers(id))`,
			`CREATE TABLE route_target_credentials_transport (route_target_id TEXT NOT NULL, api_type TEXT NOT NULL, transport TEXT NOT NULL DEFAULT 'http', session_id TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, PRIMARY KEY(route_target_id, api_type, transport), FOREIGN KEY(route_target_id, api_type, transport) REFERENCES provider_api_types_transport(provider_id, api_type, transport) ON DELETE CASCADE, FOREIGN KEY(session_id) REFERENCES credential_sessions(id) ON DELETE RESTRICT)`,
		}
		if tx.Migrator().HasTable("provider_api_types") {
			base := "base_url"
			if !tx.Migrator().HasColumn("provider_api_types", "base_url") {
				base = "''"
			}
			statements = append(statements,
				`INSERT INTO provider_api_types_transport SELECT provider_id, api_type, 'http', `+base+` FROM provider_api_types`,
				`INSERT INTO provider_api_types_transport SELECT provider_id, api_type, 'websocket', `+base+` FROM provider_api_types WHERE api_type = 'codex'`,
				`INSERT INTO route_target_credentials_transport SELECT route_target_id, api_type, 'http', session_id, created_at, updated_at FROM route_target_credentials`,
				`INSERT INTO route_target_credentials_transport SELECT route_target_id, api_type, 'websocket', session_id, created_at, updated_at FROM route_target_credentials WHERE api_type = 'codex'`,
			)
		}
		statements = append(statements,
			`DROP TABLE route_target_credentials`,
			`DROP TABLE IF EXISTS provider_api_types`,
			`ALTER TABLE provider_api_types_transport RENAME TO provider_api_types`,
			`ALTER TABLE route_target_credentials_transport RENAME TO route_target_credentials`,
			`CREATE INDEX idx_route_target_credentials_session_id ON route_target_credentials(session_id)`,
		)
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("migrate provider transports: %w", err)
			}
		}
		return nil
	})
}

// MigrateStickyTransports keeps protocol affinity independent while preserving
// each legacy Codex winner for both transports during the upgrade.
func MigrateStickyTransports(db *gorm.DB) error {
	if !db.Migrator().HasTable("sticky_entries") || db.Migrator().HasColumn("sticky_entries", "transport") {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		statements := []string{
			`CREATE TABLE sticky_entries_transport (
    transport TEXT NOT NULL DEFAULT 'http', ip TEXT, user TEXT, api_type TEXT, model TEXT,
    client_scope TEXT NOT NULL DEFAULT '', provider_id TEXT NOT NULL,
    expires_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
    PRIMARY KEY(transport,ip,user,api_type,model,client_scope))`,
			`INSERT INTO sticky_entries_transport SELECT 'http',ip,user,api_type,model,client_scope,provider_id,expires_at,updated_at FROM sticky_entries`,
			`INSERT INTO sticky_entries_transport SELECT 'websocket',ip,user,api_type,model,client_scope,provider_id,expires_at,updated_at FROM sticky_entries WHERE api_type='codex'`,
			`DROP TABLE sticky_entries`,
			`ALTER TABLE sticky_entries_transport RENAME TO sticky_entries`,
		}
		for _, sql := range statements {
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// MigrateProviderRouteStorage applies the route key change before GORM observes
// the new models. Keeping affinity and capability upgrades in one transaction
// prevents a failed upgrade from publishing only half of the transport boundary.
func MigrateProviderRouteStorage(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, migrate := range []func(*gorm.DB) error{
			MigrateProviderTransports, MigrateStickyClientScope, MigrateStickyTransports, healthstate.Migrate,
		} {
			if err := migrate(tx); err != nil {
				return err
			}
		}
		return nil
	})
}
