package sqlite

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	glebarezsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openTestDatabase(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(glebarezsqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(16)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func testKeyring(t *testing.T, currentAEAD string) *codexkeyring.Keyring {
	t.Helper()
	material := func(value byte) string {
		return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
	}
	document := map[string]any{
		"schema_version": 1,
		"hmac": map[string]any{
			"current": "h2",
			"keys":    map[string]string{"h1": material(1), "h2": material(2)},
		},
		"aead": map[string]any{
			"current": currentAEAD,
			"keys":    map[string]string{"a1": material(3), "a2": material(4)},
		},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := codexkeyring.Parse(encoded, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func testOwner(t *testing.T, keyring *codexkeyring.Keyring, label string) codexidentity.ClientScope {
	t.Helper()
	digester, err := codexidentity.NewDigester(keyring)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := digester.ClientScope([]byte("client-api-key-" + label))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testJarID(t *testing.T, label string) providercookie.JarID {
	t.Helper()
	sum := sha256.Sum256([]byte("jar:" + label))
	id, err := providercookie.JarIDFromBytes(sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testAuthority(t *testing.T, label string) codexidentity.CookieAuthority {
	t.Helper()
	origin, err := codexidentity.ParseOrigin("https://" + label + ".example")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := codexidentity.NewAccountCredentialSubject("account-" + label)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := codexidentity.NewUpstreamAuthority("openai", origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	return authority.CookieAuthority()
}

func testBinding(t *testing.T, keyring *codexkeyring.Keyring, label string, owner codexidentity.ClientScope, now time.Time) providercookie.BindingRecord {
	t.Helper()
	digest, err := keyring.Sign(codexkeyring.HMACJarHandle, []byte("handle-"+label))
	if err != nil {
		t.Fatal(err)
	}
	return providercookie.BindingRecord{
		HandleDigest:      digest,
		JarID:             testJarID(t, label),
		ClientScope:       owner,
		CreatedAt:         now,
		LastAccessAt:      now,
		IdleExpiresAt:     now.Add(5 * 24 * time.Hour),
		AbsoluteExpiresAt: now.Add(180 * 24 * time.Hour),
	}
}

func testCookie(t *testing.T, name, value string, created, expires time.Time) providercookie.StoredCookie {
	t.Helper()
	key, err := providercookie.NewCookieKey(name, "example.com", "/")
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := providercookie.NewStoredCookie(key, value, providercookie.CookieOptions{
		HostOnly: true, HTTPOnly: true, SameSite: providercookie.SameSiteLax,
		CreatedAt: created, ExpiresAt: expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cookie
}

func migrateAndOpen(t *testing.T, db *gorm.DB, keyring *codexkeyring.Keyring, busy time.Duration) *Repository {
	t.Helper()
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	repository, err := Open(context.Background(), Config{DB: db, Cipher: keyring, BusyTimeout: busy})
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestSchemaBindingOwnershipAndCapacity(t *testing.T) {
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "bindings.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal("idempotent migration:", err)
	}
	if err := ValidateSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	owner := testOwner(t, keyring, "a")
	record := testBinding(t, keyring, "a", owner, now)
	policy := providercookie.DefaultPolicy()
	if err := repository.CreateBinding(context.Background(), record, policy); err != nil {
		t.Fatal(err)
	}

	unknownDigest, _ := keyring.Sign(codexkeyring.HMACJarHandle, []byte("unknown"))
	use, err := repository.UseBinding(context.Background(), providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{unknownDigest}, ClientScopes: []codexidentity.ClientScope{owner}, At: now, Policy: policy,
	})
	if err != nil || use.Disposition != providercookie.BindingUnknown {
		t.Fatalf("unknown = %#v, %v", use, err)
	}
	use, err = repository.UseBinding(context.Background(), providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{testOwner(t, keyring, "b")}, At: now, Policy: policy,
	})
	if err != nil || use.Disposition != providercookie.BindingOwnerMismatch {
		t.Fatalf("mismatch = %#v, %v", use, err)
	}
	use, err = repository.UseBinding(context.Background(), providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{owner}, At: now, Policy: policy,
	})
	if err != nil || use.Disposition != providercookie.BindingValid || !use.Refresh {
		t.Fatalf("valid = %#v, %v", use, err)
	}
	use, err = repository.UseBinding(context.Background(), providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{owner}, At: record.AbsoluteExpiresAt, Policy: policy,
	})
	if err != nil || use.Disposition != providercookie.BindingExpired {
		t.Fatalf("expired = %#v, %v", use, err)
	}
	if err := repository.CreateBinding(context.Background(), record, policy); !errors.Is(err, providercookie.ErrIdentifierClash) {
		t.Fatalf("collision = %v", err)
	}
	hmacVersions, err := repository.RequiredHMACVersions(context.Background())
	if err != nil || len(hmacVersions) != 1 || hmacVersions[0] != "h2" {
		t.Fatalf("HMAC versions = %v, %v", hmacVersions, err)
	}
	aeadVersions, err := repository.RequiredAEADVersions(context.Background())
	if err != nil || len(aeadVersions) != 0 {
		t.Fatalf("AEAD versions = %v, %v", aeadVersions, err)
	}
	policy.MaxHandleBindingsGlobal = 1
	if err := repository.CreateBinding(context.Background(), testBinding(t, keyring, "b", owner, now), policy); !errors.Is(err, providercookie.ErrLimitExceeded) {
		t.Fatalf("handle capacity = %v", err)
	}
}

func TestEncryptedMergeAADTamperRollbackAndRotation(t *testing.T) {
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "cookies.db"))
	oldKeyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, oldKeyring, 0)
	now := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	owner := testOwner(t, oldKeyring, "owner")
	record := testBinding(t, oldKeyring, "jar", owner, now)
	policy := providercookie.DefaultPolicy()
	if err := repository.CreateBinding(context.Background(), record, policy); err != nil {
		t.Fatal(err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "authority"))
	cookie := testCookie(t, "session", "super-secret-cookie-value", now, now.Add(time.Hour))
	result, err := repository.Merge(context.Background(), scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy)
	if err != nil || result.Upserted != 1 {
		t.Fatalf("merge = %#v, %v", result, err)
	}
	snapshot, err := repository.Load(context.Background(), scope, now)
	if err != nil || len(snapshot.Cookies()) != 1 || snapshot.Cookies()[0].Value() != cookie.Value() {
		t.Fatalf("load = %#v, %v", snapshot.Cookies(), err)
	}
	var ciphertext []byte
	if err := db.Raw("SELECT value_ciphertext FROM " + entriesTable).Row().Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(cookie.Value())) {
		t.Fatal("plaintext Cookie value appeared in ciphertext storage")
	}
	var rawRows []byte
	if err := db.Raw("SELECT handle_digest || client_scope_digest || value_nonce || value_ciphertext FROM " + handlesTable + " CROSS JOIN " + entriesTable).Row().Scan(&rawRows); err != nil {
		t.Fatal(err)
	}
	for _, rawSecret := range [][]byte{
		[]byte("handle-jar"),
		[]byte("client-api-key-owner"),
		[]byte(cookie.Value()),
	} {
		if bytes.Contains(rawRows, rawSecret) {
			t.Fatalf("raw secret %q appeared in persisted columns", rawSecret)
		}
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := scanEntry(sqlDB.QueryRowContext(context.Background(), "SELECT "+entryColumns+" FROM "+entriesTable))
	if err != nil {
		t.Fatal(err)
	}
	otherJarScope, _ := providercookie.NewCookieScope(testJarID(t, "other-jar"), scope.Authority())
	otherAuthorityScope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "other-authority"))
	for name, tamper := range map[string]func(entryRow) (providercookie.CookieScope, entryRow){
		"jar": func(row entryRow) (providercookie.CookieScope, entryRow) {
			return otherJarScope, row
		},
		"authority": func(row entryRow) (providercookie.CookieScope, entryRow) {
			return otherAuthorityScope, row
		},
		"name": func(row entryRow) (providercookie.CookieScope, entryRow) {
			row.name = "other"
			return scope, row
		},
		"domain": func(row entryRow) (providercookie.CookieScope, entryRow) {
			row.domain = "other.example.com"
			return scope, row
		},
		"path": func(row entryRow) (providercookie.CookieScope, entryRow) {
			row.path = "/other"
			return scope, row
		},
	} {
		t.Run("AAD "+name, func(t *testing.T) {
			tamperedScope, tamperedRow := tamper(persisted)
			if _, err := repository.decryptEntry(tamperedScope, tamperedRow); !errors.Is(err, providercookie.ErrDecrypt) {
				t.Fatalf("AAD tamper %s = %v", name, err)
			}
		})
	}

	if err := db.Exec("UPDATE " + entriesTable + " SET cookie_path = '/tampered'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(context.Background(), scope, now); !errors.Is(err, providercookie.ErrDecrypt) {
		t.Fatalf("AAD tamper = %v", err)
	}
	if err := db.Exec("DELETE FROM " + entriesTable).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Merge(context.Background(), scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); err != nil {
		t.Fatal(err)
	}

	rotated := testKeyring(t, "a2")
	rotatedRepository, err := Open(context.Background(), Config{DB: db, Cipher: rotated})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotatedRepository.Load(context.Background(), scope, now); err != nil {
		t.Fatal("legacy read:", err)
	}
	var version string
	if err := db.Raw("SELECT value_key_version FROM " + entriesTable).Scan(&version).Error; err != nil || version != "a1" {
		t.Fatalf("read-only rotation changed version: %q, %v", version, err)
	}
	rotationResult, err := rotatedRepository.Merge(context.Background(), scope, nil, now, policy)
	if err != nil || rotationResult.Reencrypted != 1 {
		t.Fatalf("rotation merge = %#v, %v", rotationResult, err)
	}
	if err := db.Raw("SELECT value_key_version FROM " + entriesTable).Scan(&version).Error; err != nil || version != "a2" {
		t.Fatalf("rotated version = %q, %v", version, err)
	}
}

func TestPersistenceSurvivesRepositoryRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	keyring := testKeyring(t, "a1")
	now := time.Date(2026, 8, 27, 5, 30, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()

	db := openTestDatabase(t, path)
	repository := migrateAndOpen(t, db, keyring, 0)
	record := testBinding(t, keyring, "restart", testOwner(t, keyring, "restart"), now)
	if err := repository.CreateBinding(ctx, record, policy); err != nil {
		t.Fatal(err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "restart"))
	cookie := testCookie(t, "sid", "persisted-across-restart", now, now.Add(time.Hour))
	if _, err := repository.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	restartedDB := openTestDatabase(t, path)
	if err := Migrate(ctx, restartedDB); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(ctx, Config{DB: restartedDB, Cipher: keyring})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.Load(ctx, scope, now)
	if err != nil || len(snapshot.Cookies()) != 1 || snapshot.Cookies()[0].Value() != cookie.Value() {
		t.Fatalf("restart snapshot = %#v, %v", snapshot.Cookies(), err)
	}
	use, err := restarted.UseBinding(ctx, providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{record.ClientScope},
		At: now.Add(time.Minute), Policy: policy,
	})
	if err != nil || use.Disposition != providercookie.BindingValid || use.Record.JarID != record.JarID {
		t.Fatalf("restart binding = %#v, %v", use, err)
	}
	replacement := testBinding(t, keyring, "restart-replacement", record.ClientScope, now.Add(2*time.Minute))
	if err := restarted.CreateBinding(ctx, replacement, policy); err != nil {
		t.Fatalf("create independent same-scope binding: %v", err)
	}
	var bindingCount int64
	if err := restartedDB.Table(handlesTable).Count(&bindingCount).Error; err != nil || bindingCount != 2 {
		t.Fatalf("restart binding count = %d, %v", bindingCount, err)
	}
}

func TestCreateBindingKeepsConcurrentMissingHandlesIndependent(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "concurrent-bind.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 5*time.Second)
	now := time.Date(2026, 8, 27, 5, 45, 0, 0, time.UTC)
	owner := testOwner(t, keyring, "concurrent-bind")
	policy := providercookie.DefaultPolicy()

	const workers = 24
	policy.MaxHandleBindingsGlobal = workers
	proposals := make([]providercookie.BindingRecord, workers)
	for index := range proposals {
		proposals[index] = testBinding(t, keyring, fmt.Sprintf("concurrent-bind-%02d", index), owner, now)
	}
	errorsFound := make([]error, workers)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range proposals {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			errorsFound[index] = repository.CreateBinding(ctx, proposals[index], policy)
		}(index)
	}
	close(start)
	group.Wait()

	for index, err := range errorsFound {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
	}
	var count int64
	if err := db.Table(handlesTable).Count(&count).Error; err != nil || count != workers {
		t.Fatalf("binding count = %d, %v", count, err)
	}

	otherOwner := testOwner(t, keyring, "concurrent-bind-other")
	otherProposal := testBinding(t, keyring, "concurrent-bind-capacity", otherOwner, now)
	if err := repository.CreateBinding(ctx, otherProposal, policy); !errors.Is(err, providercookie.ErrLimitExceeded) {
		t.Fatalf("additional handle capacity = %v", err)
	}
}

type failingCipher struct {
	ValueCipher
	mu        sync.Mutex
	sealCount int
	failAt    int
}

func (c *failingCipher) Seal(purpose codexkeyring.AEADPurpose, aad, plaintext []byte) (codexkeyring.SealedValue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sealCount++
	if c.sealCount == c.failAt {
		return codexkeyring.SealedValue{}, errors.New("injected seal failure")
	}
	return c.ValueCipher.Seal(purpose, aad, plaintext)
}

func TestMergeRollsBackOnCryptoFailure(t *testing.T) {
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "rollback.db"))
	keyring := testKeyring(t, "a1")
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	cipher := &failingCipher{ValueCipher: keyring, failAt: 2}
	repository, err := Open(context.Background(), Config{DB: db, Cipher: cipher})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 6, 0, 0, 0, time.UTC)
	record := testBinding(t, keyring, "rollback", testOwner(t, keyring, "owner"), now)
	policy := providercookie.DefaultPolicy()
	if err := repository.CreateBinding(context.Background(), record, policy); err != nil {
		t.Fatal(err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "rollback"))
	mutations := []providercookie.Mutation{
		providercookie.Upsert(testCookie(t, "a", "one", now, now.Add(time.Hour))),
		providercookie.Upsert(testCookie(t, "b", "two", now, now.Add(time.Hour))),
	}
	if _, err := repository.Merge(context.Background(), scope, mutations, now, policy); !errors.Is(err, providercookie.ErrCrypto) {
		t.Fatalf("merge failure = %v", err)
	}
	var count int
	if err := db.Raw("SELECT COUNT(*) FROM " + entriesTable).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("rollback entry count = %d, %v", count, err)
	}
	if err := db.Raw("SELECT COUNT(*) FROM " + authoritiesTable).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("rollback authority count = %d, %v", count, err)
	}
}

func TestConcurrentPerKeyMergesPreserveDifferentKeysAndSerializeSameKey(t *testing.T) {
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "concurrent.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 5*time.Second)
	now := time.Date(2026, 8, 27, 7, 0, 0, 0, time.UTC)
	record := testBinding(t, keyring, "concurrent", testOwner(t, keyring, "owner"), now)
	policy := providercookie.DefaultPolicy()
	if err := repository.CreateBinding(context.Background(), record, policy); err != nil {
		t.Fatal(err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "concurrent"))

	const workers = 24
	start := make(chan struct{})
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			cookie := testCookie(t, fmt.Sprintf("key-%02d", index), fmt.Sprintf("value-%02d", index), now, now.Add(time.Hour))
			_, err := repository.Merge(context.Background(), scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy)
			errorsFound <- err
		}(index)
	}
	close(start)
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := repository.Load(context.Background(), scope, now)
	if err != nil || len(snapshot.Cookies()) != workers {
		t.Fatalf("different-key count = %d, %v", len(snapshot.Cookies()), err)
	}

	start = make(chan struct{})
	errorsFound = make(chan error, workers)
	for index := range workers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			cookie := testCookie(t, "shared", fmt.Sprintf("winner-%02d", index), now, now.Add(time.Hour))
			_, err := repository.Merge(context.Background(), scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy)
			errorsFound <- err
		}(index)
	}
	close(start)
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err = repository.Load(context.Background(), scope, now)
	if err != nil {
		t.Fatal(err)
	}
	shared := 0
	for _, cookie := range snapshot.Cookies() {
		if cookie.Key().Name() == "shared" {
			shared++
			if !strings.HasPrefix(cookie.Value(), "winner-") {
				t.Fatalf("shared value = %q", cookie.Value())
			}
		}
	}
	if shared != 1 {
		t.Fatalf("shared rows = %d", shared)
	}
}

func TestAccessTimesRemainMonotonicAtSQLCommitBoundaries(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "monotonic.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	policy := providercookie.DefaultPolicy()
	created := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	owner := testOwner(t, keyring, "monotonic")
	binding := testBinding(t, keyring, "monotonic", owner, created)
	if err := repository.CreateBinding(ctx, binding, policy); err != nil {
		t.Fatal(err)
	}

	later := created.Add(2 * time.Hour)
	earlier := created.Add(time.Hour)
	for _, at := range []time.Time{later, earlier} {
		used, err := repository.UseBinding(ctx, providercookie.BindingLookup{
			HandleDigests: []codexkeyring.Digest{binding.HandleDigest},
			ClientScopes:  []codexidentity.ClientScope{owner},
			At:            at,
			Policy:        policy,
		})
		if err != nil || used.Disposition != providercookie.BindingValid {
			t.Fatalf("UseBinding(%s) = (%#v, %v)", at, used, err)
		}
		if !used.Record.LastAccessAt.Equal(later) {
			t.Fatalf("UseBinding(%s) last access = %s, want %s", at, used.Record.LastAccessAt, later)
		}
	}
	concurrentBindingLater := later.Add(2 * time.Hour)
	concurrentBindingEarlier := later.Add(time.Hour)
	bindingStart := make(chan struct{})
	bindingErrors := make(chan error, 2)
	var bindingWorkers sync.WaitGroup
	for _, at := range []time.Time{concurrentBindingLater, concurrentBindingEarlier} {
		bindingWorkers.Add(1)
		go func(at time.Time) {
			defer bindingWorkers.Done()
			<-bindingStart
			_, err := repository.UseBinding(ctx, providercookie.BindingLookup{
				HandleDigests: []codexkeyring.Digest{binding.HandleDigest},
				ClientScopes:  []codexidentity.ClientScope{owner},
				At:            at,
				Policy:        policy,
			})
			bindingErrors <- err
		}(at)
	}
	close(bindingStart)
	bindingWorkers.Wait()
	close(bindingErrors)
	for err := range bindingErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var bindingTimes struct {
		LastAccessAtMS  int64
		IdleExpiresAtMS int64
	}
	if err := db.Raw("SELECT last_access_at_ms, idle_expires_at_ms FROM "+handlesTable+" WHERE handle_key_version = ? AND handle_digest = ?",
		binding.HandleDigest.Version, binding.HandleDigest.Sum[:]).Scan(&bindingTimes).Error; err != nil {
		t.Fatal(err)
	}
	wantIdle := concurrentBindingLater.Add(policy.HandleIdleTTL)
	if bindingTimes.LastAccessAtMS != toMillis(concurrentBindingLater) || bindingTimes.IdleExpiresAtMS != toMillis(wantIdle) {
		t.Fatalf("concurrent binding times = access:%s idle:%s, want access:%s idle:%s",
			fromMillis(bindingTimes.LastAccessAtMS), fromMillis(bindingTimes.IdleExpiresAtMS), concurrentBindingLater, wantIdle)
	}

	capOwner := testOwner(t, keyring, "absolute-cap-owner")
	capBinding := testBinding(t, keyring, "absolute-cap", capOwner, created)
	capBinding.AbsoluteExpiresAt = created.Add(6 * 24 * time.Hour)
	if err := repository.CreateBinding(ctx, capBinding, policy); err != nil {
		t.Fatal(err)
	}
	nearAbsolute := created.Add(4 * 24 * time.Hour)
	used, err := repository.UseBinding(ctx, providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{capBinding.HandleDigest},
		ClientScopes:  []codexidentity.ClientScope{capOwner},
		At:            nearAbsolute,
		Policy:        policy,
	})
	if err != nil || !used.Record.IdleExpiresAt.Equal(capBinding.AbsoluteExpiresAt) {
		t.Fatalf("absolute-capped binding use = (%#v, %v)", used, err)
	}

	scope, err := providercookie.NewCookieScope(binding.JarID, testAuthority(t, "monotonic"))
	if err != nil {
		t.Fatal(err)
	}
	stored := testCookie(t, "sid", "value", created, binding.AbsoluteExpiresAt)
	if _, err := repository.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(stored)}, later, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(stored)}, earlier, policy); err != nil {
		t.Fatal(err)
	}
	assertCookieAccessTimes(t, db, scope, stored.Key(), later)

	concurrentLater := later.Add(2 * time.Hour)
	concurrentEarlier := later.Add(time.Hour)
	start := make(chan struct{})
	errorsByTouch := make(chan error, 2)
	var workers sync.WaitGroup
	for _, at := range []time.Time{concurrentLater, concurrentEarlier} {
		workers.Add(1)
		go func(at time.Time) {
			defer workers.Done()
			<-start
			errorsByTouch <- repository.Touch(ctx, scope, []providercookie.CookieKey{stored.Key()}, at)
		}(at)
	}
	close(start)
	workers.Wait()
	close(errorsByTouch)
	for err := range errorsByTouch {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertCookieAccessTimes(t, db, scope, stored.Key(), concurrentLater)
}

func assertCookieAccessTimes(
	t *testing.T,
	db *gorm.DB,
	scope providercookie.CookieScope,
	key providercookie.CookieKey,
	want time.Time,
) {
	t.Helper()
	authority, err := encodedAuthority(scope)
	if err != nil {
		t.Fatal(err)
	}
	var entryAccess int64
	if err := db.Raw("SELECT last_access_at_ms FROM "+entriesTable+`
		WHERE jar_id = ? AND authority = ? AND cookie_name = ? AND cookie_domain = ? AND cookie_path = ?`,
		scope.JarID().Bytes(), authority, key.Name(), key.Domain(), key.Path()).Scan(&entryAccess).Error; err != nil {
		t.Fatal(err)
	}
	var authorityAccess int64
	if err := db.Raw("SELECT last_access_at_ms FROM "+authoritiesTable+" WHERE jar_id = ? AND authority = ?",
		scope.JarID().Bytes(), authority).Scan(&authorityAccess).Error; err != nil {
		t.Fatal(err)
	}
	if entryAccess != toMillis(want) || authorityAccess != toMillis(want) {
		t.Fatalf("access times = entry:%s authority:%s, want %s", fromMillis(entryAccess), fromMillis(authorityAccess), want)
	}
}

func TestCleanupReachabilityTouchAndDeterministicCapacity(t *testing.T) {
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "cleanup.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	owner := testOwner(t, keyring, "owner")
	record := testBinding(t, keyring, "cleanup", owner, now)
	policy := providercookie.DefaultPolicy()
	if err := repository.CreateBinding(context.Background(), record, policy); err != nil {
		t.Fatal(err)
	}
	authorityA := testAuthority(t, "cleanup-a")
	scopeA, _ := providercookie.NewCookieScope(record.JarID, authorityA)
	cookieA := testCookie(t, "a", "one", now, now.Add(48*time.Hour))
	if _, err := repository.Merge(context.Background(), scopeA, []providercookie.Mutation{providercookie.Upsert(cookieA)}, now, policy); err != nil {
		t.Fatal(err)
	}
	if err := repository.Touch(context.Background(), scopeA, []providercookie.CookieKey{cookieA.Key(), cookieA.Key()}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if result, err := repository.Cleanup(context.Background(), providercookie.CleanupRequest{
		At: now.Add(30 * time.Minute), Policy: policy, ReachableAuthorities: []codexidentity.CookieAuthority{authorityA},
	}); err != nil || result.OrphanAuthorities != 0 {
		t.Fatalf("reachable cleanup = %#v, %v", result, err)
	}
	if _, err := repository.Cleanup(context.Background(), providercookie.CleanupRequest{At: now.Add(time.Hour), Policy: policy}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Cleanup(context.Background(), providercookie.CleanupRequest{
		At: now.Add(2 * time.Hour), Policy: policy, ReachableAuthorities: []codexidentity.CookieAuthority{authorityA},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Cleanup(context.Background(), providercookie.CleanupRequest{At: now.Add(3 * time.Hour), Policy: policy}); err != nil {
		t.Fatal(err)
	}
	result, err := repository.Cleanup(context.Background(), providercookie.CleanupRequest{At: now.Add(3*time.Hour + policy.OrphanAuthorityGrace), Policy: policy})
	if err != nil || result.OrphanAuthorities != 1 {
		t.Fatalf("orphan cleanup = %#v, %v", result, err)
	}

	evictionPolicy := policy
	evictionPolicy.MaxCookiesPerAuthority = 1
	evictionPolicy.MaxCookiesPerJar = 2
	evictionPolicy.MaxAuthoritiesPerJar = 1
	cookieA = testCookie(t, "a", "one", now.Add(4*time.Hour), now.Add(8*time.Hour))
	cookieB := testCookie(t, "b", "two", now.Add(4*time.Hour), now.Add(8*time.Hour))
	if _, err := repository.Merge(context.Background(), scopeA, []providercookie.Mutation{providercookie.Upsert(cookieA)}, now.Add(4*time.Hour), evictionPolicy); err != nil {
		t.Fatal(err)
	}
	mergeResult, err := repository.Merge(context.Background(), scopeA, []providercookie.Mutation{providercookie.Upsert(cookieB)}, now.Add(4*time.Hour), evictionPolicy)
	if err != nil || mergeResult.Evicted != 1 {
		t.Fatalf("entry eviction = %#v, %v", mergeResult, err)
	}
	snapshot, err := repository.Load(context.Background(), scopeA, now.Add(4*time.Hour))
	if err != nil || len(snapshot.Cookies()) != 1 || snapshot.Cookies()[0].Key().Name() != "b" {
		t.Fatalf("entry eviction snapshot = %#v, %v", snapshot.Cookies(), err)
	}
	authorityB := testAuthority(t, "cleanup-b")
	scopeB, _ := providercookie.NewCookieScope(record.JarID, authorityB)
	cookieC := testCookie(t, "c", "three", now.Add(5*time.Hour), now.Add(8*time.Hour))
	mergeResult, err = repository.Merge(context.Background(), scopeB, []providercookie.Mutation{providercookie.Upsert(cookieC)}, now.Add(5*time.Hour), evictionPolicy)
	if err != nil || mergeResult.Evicted == 0 {
		t.Fatalf("authority eviction = %#v, %v", mergeResult, err)
	}
	snapshot, err = repository.Load(context.Background(), scopeA, now.Add(5*time.Hour))
	if err != nil || len(snapshot.Cookies()) != 0 {
		t.Fatalf("evicted authority still readable = %#v, %v", snapshot.Cookies(), err)
	}

	globalPolicy := evictionPolicy
	globalPolicy.MaxCookiesPerJar = 1
	globalPolicy.MaxCookieEntriesGlobal = 1
	otherRecord := testBinding(t, keyring, "other", testOwner(t, keyring, "other-owner"), now.Add(5*time.Hour))
	if err := repository.CreateBinding(context.Background(), otherRecord, globalPolicy); err != nil {
		t.Fatal(err)
	}
	otherScope, _ := providercookie.NewCookieScope(otherRecord.JarID, testAuthority(t, "other"))
	if _, err := repository.Merge(context.Background(), otherScope, []providercookie.Mutation{
		providercookie.Upsert(testCookie(t, "d", "four", now.Add(5*time.Hour), now.Add(8*time.Hour))),
	}, now.Add(5*time.Hour), globalPolicy); !errors.Is(err, providercookie.ErrLimitExceeded) {
		t.Fatalf("global capacity = %v", err)
	}
	if snapshot, err := repository.Load(context.Background(), otherScope, now.Add(5*time.Hour)); err != nil || len(snapshot.Cookies()) != 0 {
		t.Fatalf("capacity rollback = %#v, %v", snapshot.Cookies(), err)
	}

	expiryResult, err := repository.Cleanup(context.Background(), providercookie.CleanupRequest{
		At: now.Add(181 * 24 * time.Hour), Policy: policy,
	})
	if err != nil || expiryResult.ExpiredBindings < 2 {
		t.Fatalf("binding cleanup = %#v, %v", expiryResult, err)
	}
}

func TestBusyDatabaseFailsClosedAndTombstoneDeletesPerKey(t *testing.T) {
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "busy.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, time.Millisecond)
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	record := testBinding(t, keyring, "busy", testOwner(t, keyring, "owner"), now)
	policy := providercookie.DefaultPolicy()
	if err := repository.CreateBinding(context.Background(), record, policy); err != nil {
		t.Fatal(err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "busy"))
	cookie := testCookie(t, "sid", "value", now, now.Add(time.Hour))
	if _, err := repository.Merge(context.Background(), scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); err != nil {
		t.Fatal(err)
	}
	deleted, err := repository.Merge(context.Background(), scope, []providercookie.Mutation{providercookie.Tombstone(cookie.Key())}, now, policy)
	if err != nil || deleted.Deleted != 1 {
		t.Fatalf("tombstone = %#v, %v", deleted, err)
	}

	sqlDB, _ := db.DB()
	connection, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	if _, err := repository.Merge(context.Background(), scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("busy error = %v", err)
	}
}

func TestStandaloneSchemaAPIsRejectMissingPartialAndFutureSchemas(t *testing.T) {
	ctx := context.Background()
	missing := openTestDatabase(t, filepath.Join(t.TempDir(), "missing.db"))
	if err := ValidateSchema(ctx, missing); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("missing schema = %v", err)
	}
	if _, err := Open(ctx, Config{DB: missing, Cipher: testKeyring(t, "a1")}); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("open missing schema = %v", err)
	}

	partial := openTestDatabase(t, filepath.Join(t.TempDir(), "partial.db"))
	if err := partial.Exec("CREATE TABLE " + handlesTable + " (jar_id BLOB)").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, partial); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("partial schema = %v", err)
	}

	future := openTestDatabase(t, filepath.Join(t.TempDir(), "future.db"))
	if err := future.Exec("CREATE TABLE " + schemaTable + " (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := future.Exec("INSERT INTO "+schemaTable+" (id, version) VALUES (1, ?)", CurrentSchemaVersion+1).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, future); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("future schema = %v", err)
	}
	var count int
	if err := future.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", entriesTable).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("future migration changed domain schema: %d, %v", count, err)
	}

	valid := openTestDatabase(t, filepath.Join(t.TempDir(), "versions.db"))
	repository := migrateAndOpen(t, valid, testKeyring(t, "a1"), 0)
	hmacVersions, err := RequiredHMACVersions(ctx, valid)
	if err != nil || len(hmacVersions) != 0 {
		t.Fatalf("standalone HMAC versions = %v, %v", hmacVersions, err)
	}
	aeadVersions, err := RequiredAEADVersions(ctx, valid)
	if err != nil || len(aeadVersions) != 0 {
		t.Fatalf("standalone AEAD versions = %v, %v", aeadVersions, err)
	}
	if repository == nil {
		t.Fatal("repository is nil")
	}
}
