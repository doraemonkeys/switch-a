package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/store/migrationtest"
	"gorm.io/gorm"
)

type migrationSubjectSigner struct {
	version string
	err     error
}

type credentialMigrationClock struct{ now time.Time }

func (c *credentialMigrationClock) Now() time.Time { return c.now }

func (s migrationSubjectSigner) Sign(_ codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	if s.err != nil {
		return codexkeyring.Digest{}, s.err
	}
	sum := sha256.Sum256(input)
	return codexkeyring.Digest{Version: s.version, Sum: sum}, nil
}

func TestCredentialSessionMigrationBackfillsExplicitIndependentSessions(t *testing.T) {
	t.Parallel()
	db := openLegacyCredentialFixture(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)}

	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("migrateCredentialSessions() error = %v", err)
	}

	assertLegacyCredentialStorageRemoved(t, db)
	sessions := loadMigratedCredentialSessions(t, db)
	if len(sessions) != 9 {
		t.Fatalf("credential session count = %d, want 9", len(sessions))
	}
	bindings := loadMigratedBindings(t, db)
	if len(bindings) != 9 {
		t.Fatalf("route credential binding count = %d, want 9", len(bindings))
	}

	primary := sessionForRoute(t, sessions, bindings, migrationtest.StaticProviderID, "codex")
	if primary.Name != "Legacy static primary" || primary.Kind != credentialsession.KindAPIKey || primary.SecretData != "fixture-static-primary-not-secret" || primary.SubjectKind != credentialsession.SubjectPending {
		t.Fatalf("primary static session = %+v", primary)
	}
	legacyTimestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !primary.CreatedAt.Equal(legacyTimestamp) || !primary.UpdatedAt.Equal(legacyTimestamp) {
		t.Fatalf("primary static timestamps = (%v, %v), want legacy timestamp %v", primary.CreatedAt, primary.UpdatedAt, legacyTimestamp)
	}
	claude := sessionForRoute(t, sessions, bindings, migrationtest.APITypeOverrideProviderID, "claude")
	codex := sessionForRoute(t, sessions, bindings, migrationtest.APITypeOverrideProviderID, "codex")
	if claude.ID == codex.ID ||
		claude.Name != "Legacy API-type override" ||
		codex.Name != "Legacy API-type override · codex" ||
		claude.SecretData != "fixture-static-fallback-not-secret" ||
		codex.SecretData != "fixture-static-override-not-secret" {
		t.Fatalf("per-API sessions = claude %+v, codex %+v", claude, codex)
	}

	sameA := sessionForRoute(t, sessions, bindings, migrationtest.SameSecretStaticProviderAID, "codex")
	sameB := sessionForRoute(t, sessions, bindings, migrationtest.SameSecretStaticProviderBID, "codex")
	if sameA.ID == sameB.ID || sameA.SecretData != sameB.SecretData {
		t.Fatalf("equal legacy secrets must remain independent: A=%q B=%q", sameA.ID, sameB.ID)
	}

	owner := sessionForRoute(t, sessions, bindings, migrationtest.DuplicateAccountOwnerID, "codex")
	repair := sessionForRoute(t, sessions, bindings, migrationtest.DuplicateAccountRepairID, "codex")
	if owner.ID == repair.ID || string(owner.SubjectValue) != migrationtest.HistoricalDuplicateAccount || repair.SubjectKind != credentialsession.SubjectPending {
		t.Fatalf("login authority migration lost proof boundary: owner=%+v repair=%+v", owner, repair)
	}
	if owner.Version != 7 || repair.Version != 2 || owner.SecretData == repair.SecretData {
		t.Fatalf("login session secret/version isolation lost: owner=%+v repair=%+v", owner, repair)
	}
	if owner.AuthState.Status != credentialsession.AuthStatusActive || owner.AuthState.AccountID != migrationtest.HistoricalDuplicateAccount {
		t.Fatalf("matching binding and diagnostic authority did not migrate: %+v", owner.AuthState)
	}
	if repair.AuthState.Status != credentialsession.AuthStatusReauthRequired || repair.AuthState.StatusReason != "legacy_duplicate_account_binding" {
		t.Fatalf("reauthentication recovery state was made routable: %+v", repair.AuthState)
	}
}

func TestCredentialSessionMigrationBackfillsNamesForIntermediateSchema(t *testing.T) {
	t.Parallel()
	db := openLegacyCredentialFixture(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)}
	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE credential_sessions DROP COLUMN name`).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("migrate intermediate schema: %v", err)
	}
	var unnamed int64
	if err := db.Model(&credentialsession.Session{}).Where("TRIM(name) = ''").Count(&unnamed).Error; err != nil {
		t.Fatal(err)
	}
	if unnamed != 0 {
		t.Fatalf("unnamed credential sessions = %d", unnamed)
	}
	bindings := loadMigratedBindings(t, db)
	sessions := loadMigratedCredentialSessions(t, db)
	primary := sessionForRoute(t, sessions, bindings, migrationtest.StaticProviderID, "codex")
	if primary.Name != "Legacy static primary" {
		t.Fatalf("backfilled name = %q", primary.Name)
	}
}

func TestCredentialSessionMigrationPreservesOptionalRouteVendorScope(t *testing.T) {
	t.Parallel()
	db := openLegacyCredentialFixture(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)}
	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatal(err)
	}
	if err := finalizePendingStaticSubjects(db, migrationSubjectSigner{version: "fixture-h1"}); err != nil {
		t.Fatal(err)
	}
	repository, err := credentialsession.NewRepository(db, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	route, err := repository.Resolve(context.Background(), migrationtest.StaticProviderID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if route.VendorScope != "" {
		t.Fatalf("migrated vendor scope = %q, want optional empty scope", route.VendorScope)
	}
	target, err := url.Parse("https://static-primary.example.test")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(route, "codex", target)
	if err != nil || candidate.Authority().Vendor() != "" {
		t.Fatalf("resolved migrated candidate = (%#v, %v)", candidate, err)
	}
}

func TestCredentialSessionMigrationIsIdempotentAndLeavesFinalizationToBootstrap(t *testing.T) {
	t.Parallel()
	db := openLegacyCredentialFixture(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)}

	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("pending migration error = %v", err)
	}
	beforeIDs := migratedSessionIDs(t, db)
	var pending int64
	if err := db.Model(&credentialsession.Session{}).Where("subject_kind = ?", credentialsession.SubjectPending).Count(&pending).Error; err != nil {
		t.Fatalf("count pending subjects: %v", err)
	}
	if pending != 7 {
		t.Fatalf("pending subjects = %d, want 6 static plus 1 login recovery", pending)
	}

	clock.now = clock.now.Add(time.Minute)
	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("rerun migration error = %v", err)
	}
	afterIDs := migratedSessionIDs(t, db)
	if !reflect.DeepEqual(beforeIDs, afterIDs) {
		t.Fatalf("session IDs changed on rerun: before=%v after=%v", beforeIDs, afterIDs)
	}
	if err := db.Model(&credentialsession.Session{}).Where("subject_kind = ?", credentialsession.SubjectPending).Count(&pending).Error; err != nil {
		t.Fatalf("recount pending subjects: %v", err)
	}
	if pending != 7 {
		t.Fatalf("pending subjects after migration rerun = %d, want all subjects unchanged", pending)
	}
	beforeFinalization := loadMigratedCredentialSessions(t, db)
	if err := finalizePendingStaticSubjects(db, migrationSubjectSigner{version: "fixture-h2"}); err != nil {
		t.Fatalf("finalize pending static subjects: %v", err)
	}
	if err := db.Model(&credentialsession.Session{}).Where("subject_kind = ?", credentialsession.SubjectPending).Count(&pending).Error; err != nil {
		t.Fatalf("recount finalized subjects: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending subjects after finalization = %d, want login recovery only", pending)
	}
	var versions []string
	if err := db.Model(&credentialsession.Session{}).Where("kind = ?", credentialsession.KindAPIKey).Distinct("subject_key_version").Pluck("subject_key_version", &versions).Error; err != nil {
		t.Fatalf("read static subject versions: %v", err)
	}
	if !reflect.DeepEqual(versions, []string{"fixture-h2"}) {
		t.Fatalf("static subject versions = %v", versions)
	}
	afterFinalization := loadMigratedCredentialSessions(t, db)
	for id, before := range beforeFinalization {
		after := afterFinalization[id]
		if !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("subject finalization changed session %q updated time: before=%v after=%v", id, before.UpdatedAt, after.UpdatedAt)
		}
	}
}

func TestCredentialSessionFinalizationFailureDoesNotUndoSignerFreeMigration(t *testing.T) {
	t.Parallel()
	db := openLegacyCredentialFixture(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)}
	wantErr := errors.New("fixture signer unavailable")

	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("signer-free migration: %v", err)
	}
	err := finalizePendingStaticSubjects(db, migrationSubjectSigner{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("migration error = %v, want signer error", err)
	}
	for _, table := range []string{"credential_sessions", "route_target_credentials", "credential_session_migrations"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("failed finalization removed migrated table %q", table)
		}
	}
	var staticPending int64
	if err := db.Model(&credentialsession.Session{}).
		Where("kind = ? AND subject_kind = ?", credentialsession.KindAPIKey, credentialsession.SubjectPending).
		Count(&staticPending).Error; err != nil {
		t.Fatal(err)
	}
	if staticPending != 6 {
		t.Fatalf("pending static subjects after signer failure = %d, want 6", staticPending)
	}
}

func TestCredentialSessionRepositoryPreservesSharedReferencesOnRouteDeletion(t *testing.T) {
	t.Parallel()
	db := openLegacyCredentialFixture(t)
	clock := &credentialMigrationClock{now: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)}
	if err := migrateCredentialSessions(db, clock); err != nil {
		t.Fatalf("migration error = %v", err)
	}
	if err := finalizePendingStaticSubjects(db, migrationSubjectSigner{version: "fixture-h1"}); err != nil {
		t.Fatalf("finalize static subjects: %v", err)
	}
	repository, err := credentialsession.NewRepository(db, clock, nil)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	snapshotA, err := repository.Resolve(context.Background(), migrationtest.SameSecretStaticProviderAID, "codex")
	if err != nil {
		t.Fatalf("resolve shared source: %v", err)
	}
	if err := repository.Bind(context.Background(), credentialsession.RouteBinding{
		RouteTargetID: migrationtest.SameSecretStaticProviderBID,
		APIType:       "codex",
		SessionID:     snapshotA.Credential.SessionID,
	}); err != nil {
		t.Fatalf("explicitly share session: %v", err)
	}
	if err := repository.DeleteRouteBindings(context.Background(), migrationtest.SameSecretStaticProviderAID); err != nil {
		t.Fatalf("delete route references: %v", err)
	}
	if err := repository.DeleteIfUnreferenced(context.Background(), snapshotA.Credential.SessionID); !errors.Is(err, credentialsession.ErrSessionReferenced) {
		t.Fatalf("DeleteIfUnreferenced(shared) error = %v", err)
	}
	resolvedB, err := repository.Resolve(context.Background(), migrationtest.SameSecretStaticProviderBID, "codex")
	if err != nil || resolvedB.Credential.SessionID != snapshotA.Credential.SessionID {
		t.Fatalf("remaining target lost shared session: snapshot=%+v err=%v", resolvedB, err)
	}
}

func assertLegacyCredentialStorageRemoved(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasColumn("providers", "api_key") || db.Migrator().HasColumn("providers", "credential_type") || db.Migrator().HasColumn("provider_api_types", "api_key") {
		t.Fatal("provider/API type secret columns remain after M1")
	}
	if db.Migrator().HasTable("provider_credentials") || db.Migrator().HasTable("provider_auth_states") {
		t.Fatal("provider-owned credential tables remain after M1")
	}
}

func loadMigratedCredentialSessions(t *testing.T, db *gorm.DB) map[string]credentialsession.Session {
	t.Helper()
	var rows []credentialsession.Session
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load credential sessions: %v", err)
	}
	result := make(map[string]credentialsession.Session, len(rows))
	for _, row := range rows {
		result[row.ID] = row
	}
	return result
}

func loadMigratedBindings(t *testing.T, db *gorm.DB) []credentialsession.RouteBinding {
	t.Helper()
	var rows []credentialsession.RouteBinding
	if err := db.Order("route_target_id ASC, api_type ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load route bindings: %v", err)
	}
	return rows
}

func sessionForRoute(
	t *testing.T,
	sessions map[string]credentialsession.Session,
	bindings []credentialsession.RouteBinding,
	routeTargetID string,
	apiType string,
) credentialsession.Session {
	t.Helper()
	for _, binding := range bindings {
		if binding.RouteTargetID == routeTargetID && binding.APIType == apiType {
			session, ok := sessions[binding.SessionID]
			if !ok {
				t.Fatalf("binding %+v references missing session", binding)
			}
			return session
		}
	}
	t.Fatalf("route binding %q/%q is missing", routeTargetID, apiType)
	return credentialsession.Session{}
}

func migratedSessionIDs(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var ids []string
	if err := db.Model(&credentialsession.Session{}).Pluck("id", &ids).Error; err != nil {
		t.Fatalf("load migrated session IDs: %v", err)
	}
	sort.Strings(ids)
	return ids
}

func openLegacyCredentialFixture(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := migrationtest.OpenLegacyCredentialDatabase(filepath.Join(t.TempDir(), migrationtest.LegacyCredentialSchemaVersion+".db"))
	if err != nil {
		t.Fatalf("open legacy credential fixture: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
