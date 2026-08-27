package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	providercookiesqlite "github.com/doraemonkeys/switch-a/internal/codex/cookie/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
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
		inventory, err := persistence.InspectCodexPersistence(context.Background())
		if err != nil {
			t.Fatalf("InspectCodexPersistence() attempt %d error = %v", attempt, err)
		}
		if len(inventory.CredentialSubjects) != 0 ||
			len(inventory.CredentialHMACVersions) != 0 ||
			len(inventory.ContinuityHMACVersions) != 0 ||
			len(inventory.ProviderCookieHMACVersions) != 0 ||
			len(inventory.ProviderCookieAEADVersions) != 0 ||
			inventory.PendingStaticCredentialSubjectCount() != 0 ||
			inventory.PendingChatGPTReauthSubjectCount() != 0 {
			t.Fatalf("empty database inventory = %+v", inventory)
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

func TestSQLiteStoreCodexPersistenceInventoryKeepsHistoryFamiliesIndependent(t *testing.T) {
	persistence := setupTestStore(t)
	ctx := context.Background()

	if err := persistence.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{version: "credential-h2"}); err != nil {
		t.Fatal(err)
	}
	mustCreateStaticSession(t, persistence, "credential-b", "openai", "secret-b")
	if err := persistence.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{version: "credential-h1"}); err != nil {
		t.Fatal(err)
	}
	mustCreateStaticSession(t, persistence, "credential-a", "openai", "secret-a")
	seedContinuityHistory(t, persistence, "continuity-opaque", "continuity-client", "continuity-subject")
	seedProviderCookieHistory(t, persistence, "cookie-handle", "cookie-client", "cookie-value")

	inventory, err := persistence.InspectCodexPersistence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "credential HMAC", inventory.CredentialHMACVersions, []string{"credential-h1", "credential-h2"})
	assertStringSlice(t, "continuity HMAC", inventory.ContinuityHMACVersions, []string{"continuity-client", "continuity-opaque", "continuity-subject"})
	assertStringSlice(t, "provider-Cookie HMAC", inventory.ProviderCookieHMACVersions, []string{"cookie-client", "cookie-handle"})
	assertStringSlice(t, "provider-Cookie AEAD", inventory.ProviderCookieAEADVersions, []string{"cookie-value"})
	if got := []string{inventory.CredentialSubjects[0].SessionID, inventory.CredentialSubjects[1].SessionID}; !reflect.DeepEqual(got, []string{"credential-a", "credential-b"}) {
		t.Fatalf("credential subject order = %v", got)
	}

	// The report owns its byte slices; callers cannot mutate durable evidence by
	// retaining or changing one inventory snapshot.
	inventory.CredentialSubjects[0].Subject.Value[0] ^= 0xff
	reinspected, err := persistence.InspectCodexPersistence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(inventory.CredentialSubjects[0].Subject.Value, reinspected.CredentialSubjects[0].Subject.Value) {
		t.Fatal("credential subject report did not clone subject bytes")
	}
}

func TestSQLiteStoreCodexPersistenceInventoryRejectsMalformedCredentialSubjects(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*credentialsession.Session)
	}{
		{
			name: "pending carries resolved data",
			mutate: func(session *credentialsession.Session) {
				session.SubjectValue = []byte("unexpected")
			},
		},
		{
			name: "keyed digest has no generation",
			mutate: func(session *credentialsession.Session) {
				session.SubjectKind = credentialsession.SubjectKeyedDigest
				session.SubjectValue = make([]byte, 32)
			},
		},
		{
			name: "keyed digest has noncanonical generation",
			mutate: func(session *credentialsession.Session) {
				session.SubjectKind = credentialsession.SubjectKeyedDigest
				session.SubjectValue = make([]byte, 32)
				session.SubjectKeyVersion = " h1 "
			},
		},
		{
			name: "api key has account subject",
			mutate: func(session *credentialsession.Session) {
				session.SubjectKind = credentialsession.SubjectAccount
				session.SubjectValue = []byte("account")
			},
		},
		{
			name: "chatgpt pending outside reauth",
			mutate: func(session *credentialsession.Session) {
				session.Kind = credentialsession.KindChatGPT
				session.AuthState.Status = credentialsession.AuthStatusActive
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			persistence := setupTestStore(t)
			session := pendingStaticSession("malformed-" + strings.ReplaceAll(testCase.name, " ", "-"))
			testCase.mutate(session)
			if err := persistence.db.Create(session).Error; err != nil {
				t.Fatalf("insert malformed subject fixture: %v", err)
			}
			if err := persistence.db.Exec("DROP TABLE codex_continuity_bindings").Error; err != nil {
				t.Fatal(err)
			}
			_, err := persistence.InspectCodexPersistence(context.Background())
			if err == nil || !strings.Contains(err.Error(), session.ID) || strings.Contains(err.Error(), "continuity schema") {
				t.Fatalf("InspectCodexPersistence() error = %v", err)
			}
		})
	}
}

func TestSQLiteStoreFinalizesEveryStaticSubjectAndInstallsSignerAfterCommit(t *testing.T) {
	persistence := setupTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"unbound", "disabled", "enabled"} {
		mustCreateStaticSession(t, persistence, id, "openai", "secret-"+id)
	}
	if err := persistence.CreateProvider(ctx, providerWithSessionRefs("disabled-route", "openai", map[string]string{"codex": "disabled"})); err != nil {
		t.Fatal(err)
	}
	disabledRoute, err := persistence.GetProvider(ctx, "disabled-route")
	if err != nil {
		t.Fatal(err)
	}
	disabledRoute.Enabled = false
	if err := persistence.UpdateProvider(ctx, disabledRoute); err != nil {
		t.Fatal(err)
	}
	if err := persistence.CreateProvider(ctx, providerWithSessionRefs("enabled-route", "openai", map[string]string{"codex": "enabled"})); err != nil {
		t.Fatal(err)
	}
	insertPendingChatGPTReauth(t, persistence, "chatgpt-reauth")

	before, err := persistence.InspectCodexPersistence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "pending static", before.PendingStaticCredentialSessionIDs, []string{"disabled", "enabled", "unbound"})
	assertStringSlice(t, "pending ChatGPT", before.PendingChatGPTReauthSessionIDs, []string{"chatgpt-reauth"})
	if err := persistence.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{version: "bootstrap-h1"}); err != nil {
		t.Fatal(err)
	}
	after, err := persistence.InspectCodexPersistence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.PendingStaticCredentialSubjectCount() != 0 {
		t.Fatalf("pending static subjects after finalization = %v", after.PendingStaticCredentialSessionIDs)
	}
	assertStringSlice(t, "credential HMAC", after.CredentialHMACVersions, []string{"bootstrap-h1"})
	assertStringSlice(t, "pending ChatGPT", after.PendingChatGPTReauthSessionIDs, []string{"chatgpt-reauth"})

	created := mustCreateStaticSession(t, persistence, "after-install", "openai", "secret-after")
	if created.SubjectKeyVersion != "bootstrap-h1" {
		t.Fatalf("post-install subject = %#v", created.Subject())
	}
	if err := persistence.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{version: "bootstrap-h2"}); err != nil {
		t.Fatalf("idempotent finalization rerun: %v", err)
	}
	created = mustCreateStaticSession(t, persistence, "after-rerun", "openai", "secret-after-rerun")
	if created.SubjectKeyVersion != "bootstrap-h2" {
		t.Fatalf("post-rerun subject = %#v", created.Subject())
	}
}

func TestSQLiteStoreStaticSubjectFinalizationRollsBackAndDoesNotPublishSigner(t *testing.T) {
	testCases := []struct {
		name    string
		prepare func(*testing.T, *SQLiteStore) StaticCredentialSubjectSigner
		cleanup func(*SQLiteStore)
	}{
		{
			name: "signer",
			prepare: func(_ *testing.T, _ *SQLiteStore) StaticCredentialSubjectSigner {
				return &failingStaticSubjectSigner{version: "failure-h1", failAt: 2}
			},
		},
		{
			name: "update",
			prepare: func(t *testing.T, persistence *SQLiteStore) StaticCredentialSubjectSigner {
				t.Helper()
				if err := persistence.db.Exec(`CREATE TRIGGER fail_static_subject_update
					BEFORE UPDATE OF subject_kind ON credential_sessions
					WHEN OLD.id = 'pending-b'
					BEGIN SELECT RAISE(ABORT, 'forced subject update failure'); END`).Error; err != nil {
					t.Fatal(err)
				}
				return migrationSubjectSigner{version: "failure-h1"}
			},
		},
		{
			name: "commit",
			prepare: func(t *testing.T, persistence *SQLiteStore) StaticCredentialSubjectSigner {
				t.Helper()
				const callbackName = "test:defer-invalid-static-subject-binding"
				if err := persistence.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
					if err := tx.Exec("PRAGMA defer_foreign_keys = ON").Error; err != nil {
						tx.AddError(err)
						return
					}
					now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
					if err := tx.Exec(`INSERT OR IGNORE INTO route_target_credentials
						(route_target_id, api_type, session_id, created_at, updated_at)
						VALUES (?, ?, ?, ?, ?)`, "missing-route", "codex", "pending-a", now, now).Error; err != nil {
						tx.AddError(err)
					}
				}); err != nil {
					t.Fatal(err)
				}
				return migrationSubjectSigner{version: "failure-h1"}
			},
			cleanup: func(persistence *SQLiteStore) {
				persistence.db.Callback().Update().Remove("test:defer-invalid-static-subject-binding")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			persistence := setupTestStore(t)
			mustCreateStaticSession(t, persistence, "pending-a", "openai", "secret-a")
			mustCreateStaticSession(t, persistence, "pending-b", "openai", "secret-b")
			signer := testCase.prepare(t, persistence)
			err := persistence.FinalizeStaticCredentialSubjects(context.Background(), signer)
			if testCase.cleanup != nil {
				testCase.cleanup(persistence)
			}
			if err == nil {
				t.Fatal("FinalizeStaticCredentialSubjects() succeeded")
			}
			inventory, inspectErr := persistence.InspectCodexPersistence(context.Background())
			if inspectErr != nil {
				t.Fatal(inspectErr)
			}
			assertStringSlice(t, "rolled-back pending static", inventory.PendingStaticCredentialSessionIDs, []string{"pending-a", "pending-b"})
			created := mustCreateStaticSession(t, persistence, "after-failure", "openai", "secret-after")
			if created.SubjectKind != credentialsession.SubjectPending {
				t.Fatalf("failed finalization published signer: %#v", created.Subject())
			}
		})
	}
}

func TestSQLiteStoreStaticSubjectFinalizationOrdersConcurrentCreatesAfterSignerPublication(t *testing.T) {
	persistence := setupTestStore(t)
	mustCreateStaticSession(t, persistence, "pending", "openai", "secret-pending")
	signer := &blockingStaticSubjectSigner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	finalizeResult := make(chan error, 1)
	go func() {
		finalizeResult <- persistence.FinalizeStaticCredentialSubjects(context.Background(), signer)
	}()
	<-signer.started

	createResult := make(chan struct {
		session *credentialsession.Session
		err     error
	}, 1)
	createStarted := make(chan struct{})
	go func() {
		close(createStarted)
		session := pendingStaticSession("created-during-finalization")
		created, err := persistence.CreateCredentialSession(context.Background(), session)
		createResult <- struct {
			session *credentialsession.Session
			err     error
		}{session: created, err: err}
	}()
	<-createStarted
	select {
	case result := <-createResult:
		t.Fatalf("concurrent create crossed bootstrap boundary: (%#v, %v)", result.session, result.err)
	default:
	}

	close(signer.release)
	if err := <-finalizeResult; err != nil {
		t.Fatal(err)
	}
	result := <-createResult
	if result.err != nil || result.session.SubjectKind != credentialsession.SubjectKeyedDigest || result.session.SubjectKeyVersion != "blocking-h1" {
		t.Fatalf("ordered create result = (%#v, %v)", result.session, result.err)
	}
	inventory, err := persistence.InspectCodexPersistence(context.Background())
	if err != nil || inventory.PendingStaticCredentialSubjectCount() != 0 {
		t.Fatalf("post-concurrency inventory = (%+v, %v)", inventory, err)
	}
}

func TestSQLiteStoreStaticSubjectFinalizationRejectsUnavailableInputsAndCancellation(t *testing.T) {
	if err := (*SQLiteStore)(nil).FinalizeStaticCredentialSubjects(context.Background(), migrationSubjectSigner{version: "h1"}); err == nil {
		t.Fatal("nil store finalized subjects")
	}
	persistence := setupTestStore(t)
	if err := persistence.FinalizeStaticCredentialSubjects(context.Background(), nil); err == nil {
		t.Fatal("nil signer finalized subjects")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := persistence.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{version: "h1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled finalization error = %v", err)
	}

	persistence = setupTestStore(t)
	mustCreateStaticSession(t, persistence, "cancel-during-finalization", "openai", "secret")
	ctx, cancel = context.WithCancel(context.Background())
	signErr := errors.New("signer canceled operation")
	err := persistence.FinalizeStaticCredentialSubjects(ctx, staticSubjectSignerFunc(func(codexkeyring.HMACPurpose, []byte) (codexkeyring.Digest, error) {
		cancel()
		return codexkeyring.Digest{}, signErr
	}))
	if !errors.Is(err, signErr) {
		t.Fatalf("mid-finalization cancellation error = %v", err)
	}
	created := mustCreateStaticSession(t, persistence, "after-mid-finalization-cancel", "openai", "secret")
	if created.SubjectKind != credentialsession.SubjectPending {
		t.Fatalf("canceled finalization published signer: %#v", created.Subject())
	}
}

type failingStaticSubjectSigner struct {
	version string
	failAt  int
	calls   int
}

type blockingStaticSubjectSigner struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (signer *blockingStaticSubjectSigner) Sign(_ codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	signer.mu.Lock()
	signer.calls++
	call := signer.calls
	signer.mu.Unlock()
	if call == 1 {
		close(signer.started)
		<-signer.release
	}
	return migrationSubjectSigner{version: "blocking-h1"}.Sign(codexkeyring.HMACCredentialSubject, input)
}

type staticSubjectSignerFunc func(codexkeyring.HMACPurpose, []byte) (codexkeyring.Digest, error)

func (signer staticSubjectSignerFunc) Sign(purpose codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	return signer(purpose, input)
}

func (signer *failingStaticSubjectSigner) Sign(_ codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	signer.calls++
	if signer.calls == signer.failAt {
		return codexkeyring.Digest{}, errors.New("forced signer failure")
	}
	return migrationSubjectSigner{version: signer.version}.Sign(codexkeyring.HMACCredentialSubject, input)
}

func pendingStaticSession(id string) *credentialsession.Session {
	session := &credentialsession.Session{
		ID: id, Vendor: "openai", Kind: credentialsession.KindAPIKey,
		SecretData: "secret", Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	_ = session.SetSubject(credentialsession.PendingSubject())
	return session
}

func insertPendingChatGPTReauth(t *testing.T, persistence *SQLiteStore, id string) {
	t.Helper()
	session := &credentialsession.Session{
		ID: id, Vendor: "openai", Kind: credentialsession.KindChatGPT,
		SecretData: `{"access_token":"expired"}`, Version: 1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusReauthRequired, StatusReason: "reauthentication_required",
		},
	}
	if err := session.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.CreateCredentialSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
}

func seedContinuityHistory(t *testing.T, persistence *SQLiteStore, opaqueVersion, clientVersion, subjectVersion string) {
	t.Helper()
	if err := persistence.db.Exec(`INSERT INTO codex_continuity_bindings (
		kind, opaque_key_version, opaque_digest, client_key_version, client_digest,
		protocol_vendor, protocol_origin, protocol_subject_kind, protocol_subject_account,
		protocol_subject_key_version, protocol_subject_digest, protocol_api_type,
		route_target_hint, claim_operation_id, lifecycle, created_at_ns, updated_at_ns,
		committed_at_ns, expires_at_ns, tombstone_until_ns
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, NULL)`,
		"thread_id", opaqueVersion, make([]byte, 32), clientVersion, make([]byte, 32),
		"openai", "https://api.openai.com", "keyed-digest", subjectVersion, make([]byte, 32),
		"codex", "", "inventory-operation", "pending", int64(1), int64(1), int64(2),
	).Error; err != nil {
		t.Fatal(err)
	}
}

func seedProviderCookieHistory(t *testing.T, persistence *SQLiteStore, handleVersion, clientVersion, valueVersion string) {
	t.Helper()
	if err := persistence.db.Exec(`INSERT INTO codex_provider_cookie_handles
		(handle_key_version, handle_digest, jar_id, client_scope_key_version, client_scope_digest,
		created_at_ms, last_access_at_ms, idle_expires_at_ms, absolute_expires_at_ms)
		VALUES (?, zeroblob(32), zeroblob(32), ?, zeroblob(32), ?, ?, ?, ?)`,
		handleVersion, clientVersion, int64(1), int64(1), int64(3), int64(4),
	).Error; err != nil {
		t.Fatal(err)
	}
	authority := "https://api.openai.com"
	if err := persistence.db.Exec(`INSERT INTO codex_provider_cookie_authorities
		(jar_id, authority, created_at_ms, last_access_at_ms, unreachable_since_ms)
		VALUES (zeroblob(32), ?, ?, ?, NULL)`, authority, int64(1), int64(1)).Error; err != nil {
		t.Fatal(err)
	}
	if err := persistence.db.Exec(`INSERT INTO codex_provider_cookie_entries
		(jar_id, authority, cookie_name, cookie_domain, cookie_path, value_key_version,
		value_nonce, value_ciphertext, host_only, secure, http_only, quoted, session,
		same_site, expires_at_ms, created_at_ms, last_access_at_ms)
		VALUES (zeroblob(32), ?, ?, ?, ?, ?, zeroblob(12), zeroblob(16), ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		authority, "session", "api.openai.com", "/", valueVersion,
		1, 1, 1, 0, 1, 0, int64(2), int64(1), int64(1),
	).Error; err != nil {
		t.Fatal(err)
	}
}

func assertStringSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
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
