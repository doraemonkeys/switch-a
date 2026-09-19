package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model/providerroute"
	"gorm.io/gorm"
)

func TestProviderTransportUpgradePreservesBindingsThroughFullStartup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	storage, err := NewSQLiteStore(path, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	createMaintenanceSession(t, storage, "codex-session", "account")
	mustCreateStaticSession(t, storage, "claude-session", "", "claude-secret")
	provider := providerWithSessionRefs("legacy", "openai", map[string]string{
		"codex": "codex-session", "claude": "claude-session",
	})
	if err := storage.CreateProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	restorePreTransportRouteSchema(t, storage.db)
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}

	// The standalone migration can preserve every row while a subsequent ORM
	// constraint rebuild cascades them away. Exercise the complete startup twice.
	for startup := range 2 {
		reopened, err := NewSQLiteStore(path, internal.RealClock{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer reopened.Close()
			current, err := reopened.GetProvider(ctx, provider.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(current.APITypes) != 3 || len(current.CredentialSessions) != 3 {
				t.Fatalf("startup %d: routes=%d bindings=%d; want 3 of each", startup, len(current.APITypes), len(current.CredentialSessions))
			}
			for _, route := range current.APITypes {
				binding, err := reopened.ResolveCredentialSession(ctx, current.ID, route.APIType, route.Transport)
				if err != nil || binding.Credential.SessionID != route.APIType+"-session" {
					t.Fatalf("startup %d route %s/%s lost its credential: %v", startup, route.APIType, route.Transport, err)
				}
			}
			if _, err := reopened.LoadCodexMaintenanceCatalog(ctx); err != nil {
				t.Fatalf("startup %d: %v", startup, err)
			}
			if err := assertSQLiteForeignKeys(reopened.db); err != nil {
				t.Fatal(err)
			}
		}()
	}
}

func restorePreTransportRouteSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		statements := []string{
			`CREATE TABLE provider_api_types_legacy (provider_id TEXT, api_type TEXT, base_url TEXT NOT NULL DEFAULT '', PRIMARY KEY(provider_id,api_type), CONSTRAINT fk_providers_api_types FOREIGN KEY(provider_id) REFERENCES providers(id))`,
			`CREATE TABLE route_target_credentials_legacy (route_target_id TEXT NOT NULL, api_type TEXT NOT NULL, session_id TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, PRIMARY KEY(route_target_id,api_type), FOREIGN KEY(route_target_id,api_type) REFERENCES provider_api_types_legacy(provider_id,api_type) ON DELETE CASCADE, FOREIGN KEY(session_id) REFERENCES credential_sessions(id) ON DELETE RESTRICT)`,
			`INSERT INTO provider_api_types_legacy SELECT provider_id,api_type,base_url FROM provider_api_types WHERE transport=?`,
			`INSERT INTO route_target_credentials_legacy SELECT route_target_id,api_type,session_id,created_at,updated_at FROM route_target_credentials WHERE transport=?`,
			`DROP TABLE route_target_credentials`,
			`DROP TABLE provider_api_types`,
			`ALTER TABLE provider_api_types_legacy RENAME TO provider_api_types`,
			`ALTER TABLE route_target_credentials_legacy RENAME TO route_target_credentials`,
			`CREATE INDEX idx_route_target_credentials_session_id ON route_target_credentials(session_id)`,
		}
		for index, statement := range statements {
			var result *gorm.DB
			if index == 2 || index == 3 {
				result = tx.Exec(statement, providerroute.HTTP)
			} else {
				result = tx.Exec(statement)
			}
			if result.Error != nil {
				return result.Error
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
