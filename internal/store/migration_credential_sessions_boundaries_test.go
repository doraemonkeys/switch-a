package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/store/migrationtest"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCredentialSessionMigrationRejectsMissingDependencies(t *testing.T) {
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)}
	if err := migrateCredentialSessions(nil, clock); err == nil {
		t.Fatal("migrateCredentialSessions(nil database) succeeded")
	}
	db := openCredentialMigrationBoundaryDB(t)
	if err := migrateCredentialSessions(db, nil); err == nil {
		t.Fatal("migrateCredentialSessions(nil clock) succeeded")
	}
}

func TestCredentialSessionMigrationBootstrapsDatabaseWithoutLegacyProviderTables(t *testing.T) {
	db := openCredentialMigrationBoundaryDB(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 5, 5, 0, 0, time.UTC)}

	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("migrateCredentialSessions(empty database) error = %v", err)
	}
	applied, err := credentialMigrationApplied(db)
	if err != nil || !applied {
		t.Fatalf("credentialMigrationApplied() = (%t, %v)", applied, err)
	}
	if err := validateCredentialSessionSchema(db); err != nil {
		t.Fatalf("validateCredentialSessionSchema() error = %v", err)
	}
	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("idempotent empty migration error = %v", err)
	}
}

func TestCredentialSessionMigrationFailsClosedOnIncompleteLegacySchema(t *testing.T) {
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 5, 10, 0, 0, time.UTC)}
	testCases := []struct {
		name       string
		statements []string
		want       string
	}{
		{
			name: "one provider table",
			statements: []string{
				"CREATE TABLE providers (id TEXT PRIMARY KEY)",
			},
			want: "incomplete provider schema",
		},
		{
			name: "partially removed credential columns",
			statements: []string{
				"CREATE TABLE providers (id TEXT PRIMARY KEY, api_key TEXT)",
				"CREATE TABLE provider_api_types (provider_id TEXT, api_type TEXT, api_key TEXT, PRIMARY KEY(provider_id, api_type))",
			},
			want: "partially removed legacy credential columns",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := openCredentialMigrationBoundaryDB(t)
			for _, statement := range testCase.statements {
				if err := db.Exec(statement).Error; err != nil {
					t.Fatalf("prepare legacy schema: %v", err)
				}
			}
			err := migrateCredentialSessions(db, clock)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("migrateCredentialSessions() error = %v, want containing %q", err, testCase.want)
			}
			if db.Migrator().HasTable("credential_session_migrations") {
				t.Fatal("failed migration committed its schema or marker")
			}
		})
	}
}

func TestCredentialSessionMigrationRejectsUnrepresentableLegacyCredentials(t *testing.T) {
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 5, 15, 0, 0, time.UTC)}
	testCases := []struct {
		name   string
		mutate func(*gorm.DB) error
		want   string
	}{
		{
			name: "unsupported credential kind",
			mutate: func(db *gorm.DB) error {
				return db.Exec(
					"UPDATE providers SET credential_type = ? WHERE id = ?",
					"future-kind",
					migrationtest.StaticProviderID,
				).Error
			},
			want: "unsupported legacy credential type",
		},
		{
			name: "static route without secret",
			mutate: func(db *gorm.DB) error {
				if err := db.Exec(
					"UPDATE providers SET api_key = '' WHERE id = ?",
					migrationtest.StaticProviderID,
				).Error; err != nil {
					return err
				}
				return db.Exec(
					"UPDATE provider_api_types SET api_key = '' WHERE provider_id = ?",
					migrationtest.StaticProviderID,
				).Error
			},
			want: "has no credential to migrate",
		},
		{
			name: "login route without credential record",
			mutate: func(db *gorm.DB) error {
				return db.Exec(
					"DELETE FROM provider_credentials WHERE provider_id = ?",
					migrationtest.DuplicateAccountOwnerID,
				).Error
			},
			want: "has no credential session to migrate",
		},
		{
			name: "invalid usage snapshot",
			mutate: func(db *gorm.DB) error {
				return db.Exec(
					"UPDATE provider_auth_states SET usage_snapshot = ? WHERE provider_id = ?",
					"{",
					migrationtest.DuplicateAccountOwnerID,
				).Error
			},
			want: "decode usage snapshot",
		},
		{
			name: "binding and diagnostic account mismatch",
			mutate: func(db *gorm.DB) error {
				return db.Exec(
					"UPDATE provider_auth_states SET account_id = ? WHERE provider_id = ?",
					"different-diagnostic-account",
					migrationtest.DuplicateAccountOwnerID,
				).Error
			},
			want: "binding account proof conflicts with diagnostic auth account",
		},
		{
			name: "active login without binding proof",
			mutate: func(db *gorm.DB) error {
				return db.Exec(
					"UPDATE provider_auth_states SET status = ?, status_reason = '' WHERE provider_id = ?",
					credentialsession.AuthStatusActive,
					migrationtest.DuplicateAccountRepairID,
				).Error
			},
			want: "has no binding account proof",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := openLegacyCredentialFixture(t)
			if err := testCase.mutate(db); err != nil {
				t.Fatalf("mutate legacy fixture: %v", err)
			}
			err := migrateCredentialSessions(db, clock)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("migrateCredentialSessions() error = %v, want containing %q", err, testCase.want)
			}
			if !db.Migrator().HasColumn("providers", "api_key") {
				t.Fatal("failed migration did not roll back legacy storage")
			}
			for _, table := range []string{"provider_credentials", "provider_auth_states"} {
				if !db.Migrator().HasTable(table) {
					t.Fatalf("failed migration removed legacy table %q", table)
				}
			}
			for _, table := range []string{"credential_sessions", "route_target_credentials", "credential_session_migrations"} {
				if db.Migrator().HasTable(table) {
					t.Fatalf("failed migration committed new table %q", table)
				}
			}
		})
	}
}

func TestCredentialSessionMigrationFinalizationRollsBackSignerFailure(t *testing.T) {
	db := openLegacyCredentialFixture(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 5, 20, 0, 0, time.UTC)}
	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("pending migration error = %v", err)
	}

	var before int64
	if err := db.Model(&credentialsession.Session{}).
		Where("subject_kind = ?", credentialsession.SubjectPending).
		Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	signErr := errors.New("keyring unavailable")
	err := finalizePendingStaticSubjects(db, migrationSubjectSigner{err: signErr})
	if !errors.Is(err, signErr) {
		t.Fatalf("finalizePendingStaticSubjects() error = %v, want %v", err, signErr)
	}
	var after int64
	if err := db.Model(&credentialsession.Session{}).
		Where("subject_kind = ?", credentialsession.SubjectPending).
		Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("failed finalization changed pending count: before=%d after=%d", before, after)
	}
}

func TestCredentialSessionMigrationValidationDetectsMissingAndLegacyStorage(t *testing.T) {
	empty := openCredentialMigrationBoundaryDB(t)
	if err := validateCredentialSessionSchema(empty); err == nil ||
		!strings.Contains(err.Error(), "missing table") {
		t.Fatalf("validateCredentialSessionSchema(empty) error = %v", err)
	}

	legacy := openLegacyCredentialFixture(t)
	if err := createCredentialSessionSchema(legacy); err != nil {
		t.Fatalf("createCredentialSessionSchema() error = %v", err)
	}
	if err := validateCredentialSessionSchema(legacy); err == nil ||
		!strings.Contains(err.Error(), "left provider-owned credential storage") {
		t.Fatalf("validateCredentialSessionSchema(legacy) error = %v", err)
	}

	if _, err := staticSubject("", migrationSubjectSigner{version: "h-current"}); err == nil {
		t.Fatal("staticSubject(blank secret) succeeded")
	}
}

func TestBackfillLoginProviderSessionUsesBindingSubjectAndTimestampFallbacks(t *testing.T) {
	db := openCredentialMigrationBoundaryDB(t)
	if err := createCredentialSessionSchema(db); err != nil {
		t.Fatal(err)
	}
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 5, 25, 0, 0, time.UTC)}
	repository, err := credentialsession.NewRepository(db, clock, nil)
	if err != nil {
		t.Fatal(err)
	}

	accountID := "account-from-binding"
	providerCreatedAt := clock.now.Add(-time.Hour)
	providerUpdatedAt := clock.now.Add(-time.Minute)
	provider := legacyCredentialProvider{
		ID:        "orphan-login",
		Vendor:    "openai",
		CreatedAt: providerCreatedAt,
		UpdatedAt: providerUpdatedAt,
	}
	credential := &legacyCredentialRecord{
		ProviderID:       provider.ID,
		SecretData:       "{\"access_token\":\"token\"}",
		BindingAccountID: &accountID,
	}
	if err := backfillLoginProviderSession(
		db,
		repository,
		provider,
		nil,
		credential,
		nil,
		clock,
	); err != nil {
		t.Fatalf("backfillLoginProviderSession() error = %v", err)
	}
	sessions, err := repository.List(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("migrated sessions = (%#v, %v)", sessions, err)
	}
	session := sessions[0]
	if session.Version != 1 ||
		session.SubjectKind != credentialsession.SubjectAccount ||
		string(session.SubjectValue) != accountID ||
		session.AuthState.AccountID != "" ||
		!session.CreatedAt.Equal(providerCreatedAt) ||
		!session.UpdatedAt.Equal(providerUpdatedAt) {
		t.Fatalf("fallback login session = %#v", session)
	}

	if err := backfillLoginProviderSession(
		db,
		repository,
		legacyCredentialProvider{ID: "unused-login"},
		nil,
		nil,
		nil,
		clock,
	); err != nil {
		t.Fatalf("unrouted login provider without credentials should be ignored: %v", err)
	}
	if err := backfillLoginProviderSession(
		db,
		repository,
		legacyCredentialProvider{ID: "anonymous-login", Vendor: "openai"},
		nil,
		&legacyCredentialRecord{SecretData: "{\"access_token\":\"token\"}"},
		nil,
		clock,
	); err == nil || !strings.Contains(err.Error(), "has no binding account proof") {
		t.Fatalf("anonymous login migration error = %v", err)
	}
}

func TestCredentialSessionMigrationHelpersPropagateCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	empty := openCredentialMigrationBoundaryDB(t).WithContext(ctx)
	if err := createCredentialSessionSchema(empty); !errors.Is(err, context.Canceled) {
		t.Fatalf("createCredentialSessionSchema(canceled) error = %v", err)
	}
	legacy := openLegacyCredentialFixture(t).WithContext(ctx)
	if _, err := credentialMigrationApplied(legacy); !errors.Is(err, context.Canceled) {
		t.Fatalf("credentialMigrationApplied(canceled) error = %v", err)
	}
	if err := dropLegacyCredentialStorage(legacy); !errors.Is(err, context.Canceled) {
		t.Fatalf("dropLegacyCredentialStorage(canceled) error = %v", err)
	}
	if err := finalizePendingStaticSubjects(
		legacy,
		migrationSubjectSigner{version: "h-current"},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("finalizePendingStaticSubjects(canceled) error = %v", err)
	}
}

func openCredentialMigrationBoundaryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "migration-boundary.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open migration boundary database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open sql database: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Logf("close migration boundary database: %v", closeErr)
		}
	})
	return db
}
