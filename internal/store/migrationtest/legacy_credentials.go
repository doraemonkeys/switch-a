// Package migrationtest provides immutable database inputs for migration tests.
package migrationtest

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	// LegacyCredentialSchemaVersion names the provider-owned credential schema
	// immediately before CredentialSession is introduced.
	LegacyCredentialSchemaVersion = "provider-owned-credentials-v1"

	StaticProviderID          = "legacy-static-primary"
	APITypeOverrideProviderID = "legacy-static-api-type-override"
	// SameSecretStaticProviderAID and SameSecretStaticProviderBID intentionally
	// have equal old fields. M1 must first create independent sessions; later
	// tests can explicitly repoint B to A's session to exercise shared deletion.
	SameSecretStaticProviderAID = "legacy-static-same-secret-a"
	SameSecretStaticProviderBID = "legacy-static-same-secret-b"
	ProviderDeletionTargetID    = "legacy-static-delete-target"
	ChatGPTProviderID           = "legacy-chatgpt-primary"
	DuplicateAccountOwnerID     = "legacy-chatgpt-duplicate-owner"
	DuplicateAccountRepairID    = "legacy-chatgpt-duplicate-repair"
	HistoricalDuplicateAccount  = "fixture-account-duplicate"
)

//go:embed testdata/provider_owned_credentials_v1.sql
var legacyCredentialFixtureSQL string

// CreateLegacyCredentialDatabase writes a closed, deterministic legacy SQLite
// database at path. A closed handle lets callers exercise the real store
// startup path without sharing a connection with the fixture loader.
func CreateLegacyCredentialDatabase(path string) error {
	db, err := OpenLegacyCredentialDatabase(path)
	if err != nil {
		return err
	}
	if err := closeGORMDatabase(db); err != nil {
		return fmt.Errorf("close legacy credential fixture: %w", err)
	}
	return nil
}

// OpenLegacyCredentialDatabase creates and returns a ready-to-query fixture
// database at path. The caller owns the returned database handle.
func OpenLegacyCredentialDatabase(path string) (*gorm.DB, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("open legacy credential fixture: %w", err)
	}
	if err := ApplyLegacyCredentialFixture(db); err != nil {
		closeErr := closeGORMDatabase(db)
		return nil, errors.Join(
			fmt.Errorf("apply legacy credential fixture: %w", err),
			closeErr,
		)
	}
	return db, nil
}

// ApplyLegacyCredentialFixture resets only the tables owned by the frozen
// fixture. It intentionally executes committed SQL instead of AutoMigrate so a
// future model change cannot silently mutate the migration's input contract.
func ApplyLegacyCredentialFixture(db *gorm.DB) error {
	if db == nil {
		return errors.New("legacy credential fixture database is nil")
	}
	if db.Config == nil {
		return errors.New("legacy credential fixture database is invalid")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get legacy credential fixture database: %w", err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("reserve legacy credential fixture connection: %w", err)
	}
	defer conn.Close()

	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable fixture foreign keys: %w", err)
	}
	_, applyErr := conn.ExecContext(ctx, legacyCredentialFixtureSQL)
	if applyErr != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
	}
	_, restoreErr := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	if applyErr != nil {
		return fmt.Errorf("execute %s fixture: %w", LegacyCredentialSchemaVersion, applyErr)
	}
	if restoreErr != nil {
		return fmt.Errorf("restore fixture foreign keys: %w", restoreErr)
	}
	return nil
}

func openSQLite(path string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

func closeGORMDatabase(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if db.Config == nil {
		return errors.New("legacy credential fixture database is invalid")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
