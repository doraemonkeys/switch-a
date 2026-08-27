package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	providercookiesqlite "github.com/doraemonkeys/switch-a/internal/codex/cookie/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteStoreRegistersIndependentCodexSchemasAndRestarts(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "codex.db")
	for attempt := 0; attempt < 2; attempt++ {
		persistence, err := NewSQLiteStore(databasePath, internal.RealClock{}, nil)
		if err != nil {
			t.Fatalf("NewSQLiteStore() attempt %d error = %v", attempt, err)
		}
		for _, table := range []string{
			"codex_continuity_schema_meta",
			"codex_continuity_bindings",
			"codex_provider_cookie_schema_meta",
			"codex_provider_cookie_handles",
			"codex_provider_cookie_authorities",
			"codex_provider_cookie_entries",
		} {
			if !persistence.db.Migrator().HasTable(table) {
				t.Errorf("attempt %d did not register %s", attempt, table)
			}
		}
		versions, err := persistence.InspectCodexPersistence(context.Background())
		if err != nil {
			t.Fatalf("InspectCodexPersistence() attempt %d error = %v", attempt, err)
		}
		if len(versions.HMAC) != 0 || len(versions.AEAD) != 0 {
			t.Fatalf("empty database key versions = %+v", versions)
		}
		if err := persistence.Close(); err != nil {
			t.Fatalf("Close() attempt %d error = %v", attempt, err)
		}
	}
}

func TestSQLiteStoreUpgradesPreCodexDatabaseWithoutLosingExistingConfig(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "pre-codex.db")
	database := openRawCodexTestDatabase(t, databasePath)
	if err := database.Exec(`CREATE TABLE runtime_configs (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(
		"INSERT INTO runtime_configs (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)",
		"log_retention_days", "9",
	).Error; err != nil {
		t.Fatal(err)
	}
	closeRawCodexTestDatabase(t, database)

	persistence, err := NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("upgrade pre-Codex database: %v", err)
	}
	defer persistence.Close()
	value, err := persistence.GetConfig(context.Background(), "log_retention_days")
	if err != nil || value != "9" {
		t.Fatalf("preserved config = %q, %v", value, err)
	}
	for _, table := range []string{"codex_continuity_bindings", "codex_provider_cookie_entries"} {
		if !persistence.db.Migrator().HasTable(table) {
			t.Errorf("upgrade did not create %s", table)
		}
	}
}

func TestSQLiteStoreRejectsFutureProviderCookieSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "future.db")
	persistence, err := NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.db.Exec(
		"UPDATE codex_provider_cookie_schema_meta SET version = ? WHERE id = 1",
		providercookiesqlite.CurrentSchemaVersion+1,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := NewSQLiteStore(databasePath, internal.RealClock{}, nil); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("future provider-Cookie schema error = %v", err)
	}
}

func TestSQLiteStoreProviderCookieMigrationRollsBackMetadataOnPartialSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "partial.db")
	database := openRawCodexTestDatabase(t, databasePath)
	if err := database.Exec("CREATE TABLE codex_provider_cookie_handles (partial INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	closeRawCodexTestDatabase(t, database)

	if _, err := NewSQLiteStore(databasePath, internal.RealClock{}, nil); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("partial provider-Cookie schema error = %v", err)
	}
	database = openRawCodexTestDatabase(t, databasePath)
	defer closeRawCodexTestDatabase(t, database)
	if database.Migrator().HasTable("codex_provider_cookie_schema_meta") {
		t.Fatal("failed M3 transaction published schema metadata")
	}
}

func TestSQLiteStoreOpensCodexRepositoriesWithoutRemigrating(t *testing.T) {
	persistence := setupTestStore(t)
	repositories, err := persistence.OpenCodexRepositories(context.Background(), codexTestCipher{})
	if err != nil {
		t.Fatalf("OpenCodexRepositories() error = %v", err)
	}
	if repositories.Continuity == nil || repositories.ProviderCookies == nil {
		t.Fatalf("repositories = %+v", repositories)
	}
	if _, err := (*SQLiteStore)(nil).OpenCodexRepositories(context.Background(), codexTestCipher{}); err == nil {
		t.Fatal("nil store opened repositories")
	}
}

func TestSQLiteStoreCodexCompositionFailsClosedOnUnavailableSchemasAndCipher(t *testing.T) {
	if _, err := (*SQLiteStore)(nil).InspectCodexPersistence(context.Background()); err == nil {
		t.Fatal("nil store inspected Codex persistence")
	}

	t.Run("continuity schema", func(t *testing.T) {
		persistence := setupTestStore(t)
		if err := persistence.db.Exec("DROP TABLE codex_continuity_bindings").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := persistence.InspectCodexPersistence(context.Background()); err == nil {
			t.Fatal("inspection accepted missing continuity schema")
		}
		if _, err := persistence.OpenCodexRepositories(context.Background(), codexTestCipher{}); err == nil {
			t.Fatal("repository composition accepted missing continuity schema")
		}
	})

	t.Run("provider cookie schema", func(t *testing.T) {
		persistence := setupTestStore(t)
		if err := persistence.db.Exec("DROP TABLE codex_provider_cookie_entries").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := persistence.InspectCodexPersistence(context.Background()); err == nil {
			t.Fatal("inspection accepted missing provider-Cookie schema")
		}
	})

	t.Run("provider cookie cipher", func(t *testing.T) {
		persistence := setupTestStore(t)
		if _, err := persistence.OpenCodexRepositories(context.Background(), emptyCodexTestCipher{}); err == nil {
			t.Fatal("repository composition accepted cipher without a current AEAD generation")
		}
	})
}

func TestMergeKeyVersionsDeduplicatesSortsAndPreservesCorruptEmptyVersion(t *testing.T) {
	got := mergeKeyVersions([]string{"h2", "", "h1"}, []string{"h3", "h2"})
	want := []string{"", "h1", "h2", "h3"}
	if len(got) != len(want) {
		t.Fatalf("mergeKeyVersions() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("mergeKeyVersions() = %v, want %v", got, want)
		}
	}
}

type codexTestCipher struct{}

type emptyCodexTestCipher struct{ codexTestCipher }

func (codexTestCipher) Seal(codexkeyring.AEADPurpose, []byte, []byte) (codexkeyring.SealedValue, error) {
	return codexkeyring.SealedValue{}, nil
}

func (codexTestCipher) Open(codexkeyring.AEADPurpose, []byte, codexkeyring.SealedValue) ([]byte, error) {
	return nil, nil
}

func (codexTestCipher) Capabilities() codexkeyring.Capabilities {
	return codexkeyring.Capabilities{AEADCurrent: "a1", AEADVersions: []string{"a1"}}
}

func (emptyCodexTestCipher) Capabilities() codexkeyring.Capabilities {
	return codexkeyring.Capabilities{}
}

func openRawCodexTestDatabase(t *testing.T, path string) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func closeRawCodexTestDatabase(t *testing.T, database *gorm.DB) {
	t.Helper()
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
}
