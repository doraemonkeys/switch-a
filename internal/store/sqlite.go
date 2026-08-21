// Package store provides data storage implementations.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
	storemigration "github.com/doraemonkeys/switch-a/internal/store/migration"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Compile-time interface check.
var _ internal.Store = (*SQLiteStore)(nil)

// SQLiteStore implements the Store interface using GORM and SQLite.
type SQLiteStore struct {
	db                  *gorm.DB
	clock               internal.Clock
	credentialMutations *providerCredentialMutationCoordinator
	ruleRepository      *errorrulesqlite.Repository
}

// RequestLogTimestampMigrationObserver keeps storage independent of a logging
// implementation while delivering diagnostics before any later startup stage
// can fail and make them unrecoverable.
type RequestLogTimestampMigrationReport = storemigration.RequestLogTimestampMigrationReport

type RequestLogTimestampMigrationObserver func(RequestLogTimestampMigrationReport)

// NewSQLiteStore creates a new SQLite store with the given database path and clock.
func NewSQLiteStore(dbPath string, clock internal.Clock, observeTimestampMigration RequestLogTimestampMigrationObserver) (*SQLiteStore, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil { // coverage-ignore -- DB() rarely fails on valid gorm.DB
		return nil, err
	}
	initialized := false
	defer func() {
		// A failed migration or rule-set compile returns no store for the caller to
		// close, so constructor failure must release the opened database itself.
		if !initialized {
			_ = sqlDB.Close()
		}
	}()

	// Enable WAL mode for better concurrency
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil { // coverage-ignore -- PRAGMA rarely fails on valid connection
		return nil, err
	}

	// Set busy_timeout to wait up to 5 seconds when database is locked
	// This prevents "database is locked" errors under high concurrency
	if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil { // coverage-ignore -- PRAGMA rarely fails on valid connection
		return nil, err
	}

	// Configure connection pool for SQLite
	// SQLite handles concurrency best with a single writer, so we limit connections
	// MaxOpenConns=1 ensures serialized writes (SQLite recommendation for WAL mode)
	// This prevents "database is locked" errors from concurrent writers
	sqlDB.SetMaxOpenConns(1)
	// Allow idle connections to be reused
	sqlDB.SetMaxIdleConns(1)
	// Close idle connections after 5 minutes to prevent stale connections
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.AutoMigrate(
		&model.Group{},
		&model.Provider{},
		&model.ProviderAPIType{},
		&model.ProviderCredential{},
		&model.ProviderAuthState{},
		&model.HealthState{},
		&model.RoutingPolicy{},
		&model.RoutingPolicyGroup{},
		&model.RoutingPolicyVendor{},
		&model.RuntimeConfig{},
		&model.RequestLog{},
		&model.RequestAttempt{},
		&ProviderImportReceipt{},
		&stickyEntryRecord{},
	); err != nil { // coverage-ignore -- AutoMigrate rarely fails on valid schema
		return nil, err
	}

	if err := migrateProviderStateTables(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate provider credential/auth state tables: %w", err)
	}
	if err := storemigration.MigrateProviderUsageLimitPolicyStorage(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate provider usage-limit policy storage: %w", err)
	}
	if err := storemigration.MigrateRoutingPolicyLifecycleStorage(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate routing policy lifecycle storage: %w", err)
	}

	if err := storemigration.MigrateBaseURLToAPIType(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate base_url: %w", err)
	}
	if err := storemigration.MigrateStickyConfig(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate sticky config: %w", err)
	}
	if err := storemigration.MigrateGlobalMaxAttemptsConfig(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate global max attempts config: %w", err)
	}
	if err := storemigration.MigrateWebSocketColumn(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate websocket column: %w", err)
	}
	if err := storemigration.MigrateRequestLogLifecycleFields(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate request log lifecycle fields: %w", err)
	}
	timestampMigrationReport, err := storemigration.MigrateRequestLogCreatedAtInstants(db)
	if err != nil { // coverage-ignore -- one-time timestamp backfill
		return nil, fmt.Errorf("migrate request-log timestamp instants: %w", err)
	}
	if observeTimestampMigration != nil {
		timestampMigrationReport.InvalidIDs = append([]uint(nil), timestampMigrationReport.InvalidIDs...)
		observeTimestampMigration(timestampMigrationReport)
	}
	if err := storemigration.MigrateRequestLogAnalyticsIndexes(db); err != nil { // coverage-ignore -- idempotent schema migration
		return nil, fmt.Errorf("migrate request-log analytics indexes: %w", err)
	}

	ruleRepository, err := errorrulesqlite.Open(context.Background(), errorrulesqlite.Config{
		DB:    db,
		Clock: clock,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize internal-error rules: %w", err)
	}

	result := &SQLiteStore{
		db:                  db,
		clock:               clock,
		credentialMutations: newProviderCredentialMutationCoordinator(),
		ruleRepository:      ruleRepository,
	}
	initialized = true
	return result, nil
}

func (s *SQLiteStore) InternalErrorRuleRepository() *errorrulesqlite.Repository {
	return s.ruleRepository
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil { // coverage-ignore -- DB() rarely fails on valid gorm.DB
		return err
	}
	return sqlDB.Close()
}

// InitDefaultConfig initializes default runtime configuration values.
func (s *SQLiteStore) InitDefaultConfig(ctx context.Context) error {
	for key, value := range GetDefaultConfigs() {
		err := s.db.WithContext(ctx).Exec(
			"INSERT OR IGNORE INTO runtime_configs (key, value, updated_at) VALUES (?, ?, ?)",
			key, value, s.clock.Now(),
		).Error
		if err != nil { // coverage-ignore -- INSERT OR IGNORE rarely fails on valid schema
			return fmt.Errorf("init default config %q: %w", key, err)
		}
	}
	return nil
}
