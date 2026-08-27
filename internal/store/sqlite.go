// Package store provides data storage implementations.
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
	providercookiesqlite "github.com/doraemonkeys/switch-a/internal/codex/cookie/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
	storemigration "github.com/doraemonkeys/switch-a/internal/store/migration"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const sqliteForeignKeysPragma = "_pragma=foreign_keys(1)"

// Compile-time interface check.
var _ internal.Store = (*SQLiteStore)(nil)

// SQLiteStore implements the Store interface using GORM and SQLite.
type SQLiteStore struct {
	db                  *gorm.DB
	clock               internal.Clock
	credentialMutations *credentialsession.MutationCoordinator
	credentialSessions  *credentialsession.Repository
	credentialSigner    StaticCredentialSubjectSigner
	ruleRepository      *errorrulesqlite.Repository
}

// RequestLogTimestampMigrationObserver keeps storage independent of a logging
// implementation while delivering diagnostics before any later startup stage
// can fail and make them unrecoverable.
type RequestLogTimestampMigrationReport = storemigration.RequestLogTimestampMigrationReport

type RequestLogTimestampMigrationObserver func(RequestLogTimestampMigrationReport)

// NewSQLiteStore creates a new SQLite store with the given database path and clock.
func NewSQLiteStore(
	dbPath string,
	clock internal.Clock,
	observeTimestampMigration RequestLogTimestampMigrationObserver,
	signers ...StaticCredentialSubjectSigner,
) (*SQLiteStore, error) {
	var signer StaticCredentialSubjectSigner
	if len(signers) > 1 {
		return nil, fmt.Errorf("initialize SQLite store: at most one credential subject signer is allowed")
	}
	if len(signers) == 1 {
		signer = signers[0]
	}
	// foreign_keys is connection-local in SQLite. Encoding it in the DSN is the
	// only way to preserve the invariant when database/sql replaces a pooled
	// connection after startup.
	db, err := gorm.Open(sqlite.Open(sqliteDSN(dbPath)), &gorm.Config{
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

	// Configure the pool before the first application query. The DSN initializes
	// every future connection; this read proves the connection used for migrations
	// has the required enforcement enabled.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	if err := assertSQLiteForeignKeys(db); err != nil {
		return nil, err
	}

	// Enable WAL mode for better concurrency
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil { // coverage-ignore -- PRAGMA rarely fails on valid connection
		return nil, err
	}

	// Set busy_timeout to wait up to 5 seconds when database is locked
	// This prevents "database is locked" errors under high concurrency
	if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil { // coverage-ignore -- PRAGMA rarely fails on valid connection
		return nil, err
	}

	// M1 owns the destructive boundary. Running it before GORM sees the new
	// models prevents AutoMigrate from silently retaining provider-owned secret
	// columns or inventing an implicit dual-read period.
	if err := storemigration.MigrateProviderUsageLimitPolicyStorage(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate provider usage-limit policy storage: %w", err)
	}
	if err := migrateCredentialSessions(db, clock, signer); err != nil {
		return nil, fmt.Errorf("migrate credential sessions: %w", err)
	}
	if err := continuitysqlite.Migrate(context.Background(), db); err != nil {
		return nil, fmt.Errorf("migrate Codex continuity storage: %w", err)
	}
	if err := providercookiesqlite.Migrate(context.Background(), db); err != nil {
		return nil, fmt.Errorf("migrate Codex provider-Cookie storage: %w", err)
	}

	if err := db.AutoMigrate(
		&model.Group{},
		&model.Provider{},
		&model.ProviderAPIType{},
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
	credentialSessions, err := credentialsession.NewRepository(db, clock, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize credential sessions: %w", err)
	}

	result := &SQLiteStore{
		db:                  db,
		clock:               clock,
		credentialMutations: credentialsession.NewMutationCoordinator(),
		credentialSessions:  credentialSessions,
		credentialSigner:    signer,
		ruleRepository:      ruleRepository,
	}
	initialized = true
	return result, nil
}

func sqliteDSN(dbPath string) string {
	separator := "?"
	if strings.Contains(dbPath, "?") {
		separator = "&"
	}
	return dbPath + separator + sqliteForeignKeysPragma
}

func assertSQLiteForeignKeys(db *gorm.DB) error {
	var enabled int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil {
		return fmt.Errorf("verify SQLite foreign-key enforcement: %w", err)
	}
	if enabled != 1 {
		return fmt.Errorf("verify SQLite foreign-key enforcement: PRAGMA foreign_keys=%d", enabled)
	}
	return nil
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
