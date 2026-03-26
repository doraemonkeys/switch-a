// Package store provides data storage implementations.
package store

import (
	"context"
	"fmt"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Compile-time interface check.
var _ internal.Store = (*SQLiteStore)(nil)

// SQLiteStore implements the Store interface using GORM and SQLite.
type SQLiteStore struct {
	db    *gorm.DB
	clock internal.Clock
}

// NewSQLiteStore creates a new SQLite store with the given database path and clock.
func NewSQLiteStore(dbPath string, clock internal.Clock) (*SQLiteStore, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
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

	// Configure connection pool for SQLite
	// SQLite handles concurrency best with a single writer, so we limit connections
	sqlDB, err := db.DB()
	if err != nil { // coverage-ignore -- DB() rarely fails on valid gorm.DB
		return nil, err
	}
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
		&model.HealthState{},
		&model.RuntimeConfig{},
		&model.RequestLog{},
		&model.RequestAttempt{},
	); err != nil { // coverage-ignore -- AutoMigrate rarely fails on valid schema
		return nil, err
	}

	if err := migrateBaseURLToAPIType(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate base_url: %w", err)
	}
	if err := migrateStickyConfig(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate sticky config: %w", err)
	}
	if err := migrateGlobalMaxAttemptsConfig(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate global max attempts config: %w", err)
	}
	if err := migrateWebSocketColumn(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate websocket column: %w", err)
	}
	if err := migrateRequestLogLifecycleFields(db); err != nil { // coverage-ignore -- one-time migration
		return nil, fmt.Errorf("migrate request log lifecycle fields: %w", err)
	}

	return &SQLiteStore{db: db, clock: clock}, nil
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
