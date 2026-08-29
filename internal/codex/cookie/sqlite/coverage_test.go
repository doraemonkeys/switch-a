package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"gorm.io/gorm"
)

type emptyCipher struct{}

func (emptyCipher) Seal(codexkeyring.AEADPurpose, []byte, []byte) (codexkeyring.SealedValue, error) {
	return codexkeyring.SealedValue{}, nil
}
func (emptyCipher) Open(codexkeyring.AEADPurpose, []byte, codexkeyring.SealedValue) ([]byte, error) {
	return nil, nil
}
func (emptyCipher) Capabilities() codexkeyring.Capabilities { return codexkeyring.Capabilities{} }

type invalidSealedCipher struct{ ValueCipher }

func (c invalidSealedCipher) Seal(codexkeyring.AEADPurpose, []byte, []byte) (codexkeyring.SealedValue, error) {
	return codexkeyring.SealedValue{Version: "a1", Nonce: []byte{1}, Ciphertext: make([]byte, 16)}, nil
}

type errorScanner struct{ err error }

func (s errorScanner) Scan(...any) error { return s.err }

type staticEntryScanner struct{ row entryRow }

func (s staticEntryScanner) Scan(destinations ...any) error {
	*destinations[0].(*string) = s.row.name
	*destinations[1].(*string) = s.row.domain
	*destinations[2].(*string) = s.row.path
	*destinations[3].(*string) = s.row.keyVersion
	*destinations[4].(*[]byte) = append([]byte(nil), s.row.nonce...)
	*destinations[5].(*[]byte) = append([]byte(nil), s.row.ciphertext...)
	*destinations[6].(*int) = s.row.hostOnly
	*destinations[7].(*int) = s.row.secure
	*destinations[8].(*int) = s.row.httpOnly
	*destinations[9].(*int) = s.row.quoted
	*destinations[10].(*int) = s.row.session
	*destinations[11].(*int) = s.row.sameSite
	*destinations[12].(*int64) = s.row.expiresAt
	*destinations[13].(*int64) = s.row.createdAt
	*destinations[14].(*int64) = s.row.accessedAt
	return nil
}

type fixedOpenCipher struct {
	ValueCipher
	plaintext []byte
}

func (c fixedOpenCipher) Open(codexkeyring.AEADPurpose, []byte, codexkeyring.SealedValue) ([]byte, error) {
	return append([]byte(nil), c.plaintext...), nil
}

func TestValidationBoundariesAndClosedStorageErrors(t *testing.T) {
	ctx := context.Background()
	keyring := testKeyring(t, "a1")
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "validation.db"))
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	openCases := []Config{
		{},
		{DB: db},
		{DB: db, Cipher: keyring, BusyTimeout: -time.Second},
		{DB: db, Cipher: emptyCipher{}},
	}
	for _, config := range openCases {
		if _, err := Open(ctx, config); err == nil {
			t.Fatalf("Open accepted invalid config: %#v", config)
		}
	}
	if _, err := Open(nil, Config{DB: db, Cipher: keyring}); err == nil {
		t.Fatal("Open accepted nil context")
	}
	if err := Migrate(nil, db); err == nil {
		t.Fatal("Migrate accepted nil context")
	}
	if err := ValidateSchema(nil, db); err == nil {
		t.Fatal("ValidateSchema accepted nil context")
	}

	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	owner := testOwner(t, keyring, "validation")
	record := testBinding(t, keyring, "validation", owner, now)
	policy := providercookie.DefaultPolicy()
	digest := record.HandleDigest
	lookup := providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{digest}, ClientScopes: []codexidentity.ClientScope{owner}, At: now, Policy: policy,
	}
	invalidLookups := []providercookie.BindingLookup{
		{},
		{HandleDigests: []codexkeyring.Digest{{}}, ClientScopes: []codexidentity.ClientScope{owner}, At: now, Policy: policy},
		{HandleDigests: []codexkeyring.Digest{digest, digest}, ClientScopes: []codexidentity.ClientScope{owner}, At: now, Policy: policy},
		{HandleDigests: []codexkeyring.Digest{digest}, ClientScopes: []codexidentity.ClientScope{{}}, At: now, Policy: policy},
	}
	for _, invalid := range invalidLookups {
		if _, err := repository.UseBinding(ctx, invalid); err == nil {
			t.Fatalf("UseBinding accepted invalid lookup: %#v", invalid)
		}
	}
	badPolicy := policy
	badPolicy.HandleIdleTTL = 0
	lookup.Policy = badPolicy
	if _, err := repository.UseBinding(ctx, lookup); err == nil {
		t.Fatal("UseBinding accepted invalid policy")
	}
	if _, err := repository.UseBinding(nil, lookup); err == nil {
		t.Fatal("UseBinding accepted nil context")
	}

	invalidRecords := []providercookie.BindingRecord{
		{},
		{HandleDigest: digest, ClientScope: owner, CreatedAt: now, LastAccessAt: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)},
		{HandleDigest: digest, JarID: record.JarID, CreatedAt: now, LastAccessAt: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)},
		{HandleDigest: digest, JarID: record.JarID, ClientScope: owner, CreatedAt: now, LastAccessAt: now.Add(-time.Second), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)},
	}
	for _, invalid := range invalidRecords {
		if err := repository.CreateBinding(ctx, invalid, policy); err == nil {
			t.Fatalf("CreateBinding accepted invalid record: %#v", invalid)
		}
	}
	if err := repository.CreateBinding(nil, record, policy); err == nil {
		t.Fatal("CreateBinding accepted nil context")
	}
	if err := repository.CreateBinding(ctx, record, badPolicy); err == nil {
		t.Fatal("CreateBinding accepted invalid policy")
	}
	otherOwner := testOwner(t, keyring, "validation-other")
	validClientBinding := providercookie.ClientJarBindingRequest{
		CurrentClientScope:    owner,
		ClientScopeCandidates: []codexidentity.ClientScope{owner},
		ProposedBinding:       record,
		At:                    now,
		Policy:                policy,
	}
	wrongOwnerBinding := record
	wrongOwnerBinding.ClientScope = otherOwner
	wrongTimeBinding := record
	wrongTimeBinding.LastAccessAt = now.Add(time.Second)
	invalidClientBindings := []providercookie.ClientJarBindingRequest{
		{},
		{CurrentClientScope: codexidentity.ClientScope{}, ClientScopeCandidates: []codexidentity.ClientScope{owner}, ProposedBinding: record, At: now, Policy: policy},
		{CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{{}}, ProposedBinding: record, At: now, Policy: policy},
		{CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{owner, owner}, ProposedBinding: record, At: now, Policy: policy},
		{CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{otherOwner}, ProposedBinding: record, At: now, Policy: policy},
		{CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{owner}, ProposedBinding: providercookie.BindingRecord{}, At: now, Policy: policy},
		{CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{owner}, ProposedBinding: wrongOwnerBinding, At: now, Policy: policy},
		{CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{owner}, ProposedBinding: wrongTimeBinding, At: now, Policy: policy},
	}
	for _, invalid := range invalidClientBindings {
		if _, err := repository.BindClientJar(ctx, invalid); err == nil {
			t.Fatalf("BindClientJar accepted invalid request: %#v", invalid)
		}
	}
	if _, err := repository.BindClientJar(nil, validClientBinding); err == nil {
		t.Fatal("BindClientJar accepted nil context")
	}
	invalidPolicyBinding := validClientBinding
	invalidPolicyBinding.Policy = badPolicy
	if _, err := repository.BindClientJar(ctx, invalidPolicyBinding); err == nil {
		t.Fatal("BindClientJar accepted invalid policy")
	}

	if _, err := repository.Load(nil, providercookie.CookieScope{}, now); err == nil {
		t.Fatal("Load accepted nil context")
	}
	if _, err := repository.Load(ctx, providercookie.CookieScope{}, now); err == nil {
		t.Fatal("Load accepted invalid scope")
	}
	if err := repository.Touch(nil, providercookie.CookieScope{}, nil, now); err == nil {
		t.Fatal("Touch accepted nil context")
	}
	if err := repository.Touch(ctx, providercookie.CookieScope{}, nil, now); err == nil {
		t.Fatal("Touch accepted invalid scope")
	}
	if _, err := repository.Merge(nil, providercookie.CookieScope{}, nil, now, policy); err == nil {
		t.Fatal("Merge accepted nil context")
	}
	if _, err := repository.Merge(ctx, providercookie.CookieScope{}, nil, now, policy); err == nil {
		t.Fatal("Merge accepted invalid scope")
	}
	if _, err := repository.Merge(ctx, providercookie.CookieScope{}, nil, now, badPolicy); err == nil {
		t.Fatal("Merge accepted invalid policy")
	}
	if _, err := repository.Cleanup(nil, providercookie.CleanupRequest{}); err == nil {
		t.Fatal("Cleanup accepted nil context")
	}
	if _, err := repository.Cleanup(ctx, providercookie.CleanupRequest{Policy: policy}); err == nil {
		t.Fatal("Cleanup accepted zero time")
	}
	if _, err := repository.Cleanup(ctx, providercookie.CleanupRequest{At: now, Policy: badPolicy}); err == nil {
		t.Fatal("Cleanup accepted invalid policy")
	}
	if _, err := repository.Cleanup(ctx, providercookie.CleanupRequest{At: now, Policy: policy, ReachableAuthorities: []codexidentity.CookieAuthority{{}}}); err == nil {
		t.Fatal("Cleanup accepted invalid authority")
	}

	if _, err := scanEntry(errorScanner{err: errors.New("scan failed")}); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("scan error = %v", err)
	}
	if got := uniqueKeys(nil); len(got) != 0 {
		t.Fatalf("unique empty = %v", got)
	}
	keyA, _ := providercookie.NewCookieKey("a", "example.com", "/z")
	keyB, _ := providercookie.NewCookieKey("a", "a.example.com", "/")
	keyC, _ := providercookie.NewCookieKey("b", "example.com", "/")
	keys := uniqueKeys([]providercookie.CookieKey{keyC, keyA, keyB, keyA})
	if len(keys) != 3 || keys[0] != keyB || keys[1] != keyA || keys[2] != keyC {
		t.Fatalf("unique key order = %#v", keys)
	}
	var typedNil *codexkeyring.Keyring
	if !isNil(typedNil) || isNil(struct{}{}) {
		t.Fatal("typed nil detection failed")
	}

	closedDB := openTestDatabase(t, filepath.Join(t.TempDir(), "closed.db"))
	closedRepository := migrateAndOpen(t, closedDB, keyring, 0)
	sqlDB, _ := closedDB.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	closedScope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "closed"))
	for name, operation := range map[string]func() error{
		"use":    func() error { _, err := closedRepository.UseBinding(ctx, lookup); return err },
		"create": func() error { return closedRepository.CreateBinding(ctx, record, policy) },
		"bind": func() error {
			_, err := closedRepository.BindClientJar(ctx, validClientBinding)
			return err
		},
		"load":     func() error { _, err := closedRepository.Load(ctx, closedScope, now); return err },
		"versions": func() error { _, err := closedRepository.RequiredAEADVersions(ctx); return err },
	} {
		if err := operation(); !errors.Is(err, providercookie.ErrStorage) && !errors.Is(err, providercookie.ErrInvalidConfig) {
			t.Fatalf("%s closed storage error = %v", name, err)
		}
	}
}

func TestCorruptRowsAndInvalidCipherMetadataFailClosed(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "corrupt-rows.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC)
	record := testBinding(t, keyring, "corrupt", testOwner(t, keyring, "owner"), now)
	policy := providercookie.DefaultPolicy()
	if err := repository.CreateBinding(ctx, record, policy); err != nil {
		t.Fatal(err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "corrupt"))
	cookie := testCookie(t, "sid", "value", now, now.Add(time.Hour))
	if _, err := repository.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE " + entriesTable + " SET value_key_version = 'missing'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(ctx, scope, now); !errors.Is(err, providercookie.ErrCrypto) {
		t.Fatalf("missing key version = %v", err)
	}
	if _, err := repository.Merge(ctx, scope, nil, now, policy); !errors.Is(err, providercookie.ErrCrypto) {
		t.Fatalf("legacy rewrite with missing key = %v", err)
	}
	if err := db.Exec("UPDATE " + entriesTable + " SET value_key_version = 'a1', cookie_domain = 'Bad_Domain'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(ctx, scope, now); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("corrupt key = %v", err)
	}

	validDB := openTestDatabase(t, filepath.Join(t.TempDir(), "invalid-cipher.db"))
	if err := Migrate(ctx, validDB); err != nil {
		t.Fatal(err)
	}
	invalidRepository, err := Open(ctx, Config{DB: validDB, Cipher: invalidSealedCipher{ValueCipher: keyring}})
	if err != nil {
		t.Fatal(err)
	}
	record = testBinding(t, keyring, "invalid-cipher", testOwner(t, keyring, "owner"), now)
	if err := invalidRepository.CreateBinding(ctx, record, policy); err != nil {
		t.Fatal(err)
	}
	scope, _ = providercookie.NewCookieScope(record.JarID, testAuthority(t, "invalid-cipher"))
	if _, err := invalidRepository.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); !errors.Is(err, providercookie.ErrCrypto) {
		t.Fatalf("invalid sealed metadata = %v", err)
	}
	if _, err := invalidRepository.Merge(ctx, scope, []providercookie.Mutation{{}}, now, policy); err == nil {
		t.Fatal("invalid mutation was accepted")
	}
}

func TestDatabaseErrorClassification(t *testing.T) {
	for _, test := range []struct {
		message string
		want    error
	}{
		{message: "database disk image is malformed", want: providercookie.ErrStorageCorrupt},
		{message: "CHECK constraint failed", want: providercookie.ErrStorageCorrupt},
		{message: "database is locked", want: providercookie.ErrStorage},
	} {
		err := classifyDatabaseError("test", errors.New(test.message))
		if !errors.Is(err, test.want) {
			t.Fatalf("classify %q = %v", test.message, err)
		}
	}
	original := &providercookie.PersistenceError{Kind: providercookie.PersistenceDecrypt, Operation: "test"}
	if classifyDatabaseError("test", original) != original {
		t.Fatal("typed persistence error was rewrapped")
	}
	limit := &providercookie.LimitError{Limit: providercookie.LimitGlobalEntries, Max: 1, Actual: 2}
	if classifyDatabaseError("test", limit) != limit {
		t.Fatal("capacity error was rewrapped")
	}
	if classifyDatabaseError("test", nil) != nil {
		t.Fatal("nil error classification changed")
	}
	sum := sha256.Sum256([]byte("digest"))
	if err := validateLookup(providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{{Version: "h1", Sum: sum}},
		ClientScopes:  []codexidentity.ClientScope{{}},
		At:            time.Now(),
		Policy:        providercookie.DefaultPolicy(),
	}); err == nil {
		t.Fatal("invalid client scope accepted")
	}
}

func TestMissingJarAmbiguousHandleAndVersionCorruptionFailClosed(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "missing-jar.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	owner := testOwner(t, keyring, "owner")
	missingScope, _ := providercookie.NewCookieScope(testJarID(t, "missing"), testAuthority(t, "missing"))
	if _, err := repository.Load(ctx, missingScope, now); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("missing Jar load = %v", err)
	}
	if err := repository.Touch(ctx, missingScope, nil, now); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("missing Jar touch = %v", err)
	}
	if _, err := repository.Merge(ctx, missingScope, nil, now, policy); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("missing Jar merge = %v", err)
	}

	first := testBinding(t, keyring, "first", owner, now)
	otherOwner := testOwner(t, keyring, "other-owner")
	second := testBinding(t, keyring, "second", otherOwner, now)
	if err := repository.CreateBinding(ctx, first, policy); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateBinding(ctx, second, policy); err != nil {
		t.Fatal(err)
	}
	proposed := testBinding(t, keyring, "proposed", owner, now)
	if _, err := repository.BindClientJar(ctx, providercookie.ClientJarBindingRequest{
		CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{owner, otherOwner},
		ProposedBinding: proposed, At: now, Policy: policy,
	}); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("ambiguous ClientScope = %v", err)
	}
	handleCollision := proposed
	handleCollision.HandleDigest = second.HandleDigest
	if _, err := repository.BindClientJar(ctx, providercookie.ClientJarBindingRequest{
		CurrentClientScope: owner, ClientScopeCandidates: []codexidentity.ClientScope{owner},
		ProposedBinding: handleCollision, At: now, Policy: policy,
	}); !errors.Is(err, providercookie.ErrIdentifierClash) {
		t.Fatalf("rebound handle collision = %v", err)
	}
	if _, err := repository.UseBinding(ctx, providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{first.HandleDigest, second.HandleDigest},
		ClientScopes:  []codexidentity.ClientScope{owner, otherOwner}, At: now, Policy: policy,
	}); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("ambiguous handle = %v", err)
	}

	sqlDB, _ := db.DB()
	rows, err := sqlDB.QueryContext(ctx, "SELECT ''")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanVersions(rows); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("empty key version = %v", err)
	}
	if versions, err := RequiredHMACVersions(ctx, db); err != nil || len(versions) != 1 || versions[0] != "h2" {
		t.Fatalf("standalone HMAC versions = %v, %v", versions, err)
	}
}

func TestSchemaMigrationRollsBackDDLFailureAndRejectsWrongColumns(t *testing.T) {
	ctx := context.Background()
	conflict := openTestDatabase(t, filepath.Join(t.TempDir(), "ddl-conflict.db"))
	if err := conflict.Exec("CREATE TABLE idx_codex_provider_cookie_handles_expiry (id INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, conflict); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("DDL failure = %v", err)
	}
	var count int
	if err := conflict.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", handlesTable).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed migration was not rolled back: %d, %v", count, err)
	}

	wrong := openTestDatabase(t, filepath.Join(t.TempDir(), "wrong-columns.db"))
	if err := wrong.Exec("CREATE TABLE " + schemaTable + " (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := wrong.Exec("INSERT INTO "+schemaTable+" (id, version) VALUES (1, ?)", CurrentSchemaVersion).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{handlesTable, authoritiesTable, entriesTable} {
		if err := wrong.Exec("CREATE TABLE " + table + " (wrong_column INTEGER)").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateSchema(ctx, wrong); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("wrong columns = %v", err)
	}
	if err := Migrate(ctx, wrong); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("wrong columns through migrate = %v", err)
	}

	zero := openTestDatabase(t, filepath.Join(t.TempDir(), "zero-version.db"))
	if err := zero.Exec("CREATE TABLE " + schemaTable + " (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := zero.Exec("INSERT INTO " + schemaTable + " (id, version) VALUES (1, 0)").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, zero); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("zero version = %v", err)
	}

	publish := openTestDatabase(t, filepath.Join(t.TempDir(), "publish-failure.db"))
	if err := publish.Exec("CREATE TABLE " + schemaTable + " (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL CHECK (version >= 1))").Error; err != nil {
		t.Fatal(err)
	}
	if err := publish.Exec("CREATE TRIGGER reject_cookie_schema_version BEFORE INSERT ON " + schemaTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, publish); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("unpublished schema metadata = %v", err)
	}
	if err := publish.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", entriesTable).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("publish failure did not roll back DDL: %d, %v", count, err)
	}
}

func TestTransactionCommitFailureAndHelperStorageErrors(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "transaction-errors.db"))
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	err := withImmediateTransaction(ctx, sqlDB, time.Second, func(connection *sql.Conn) error {
		_, err := connection.ExecContext(ctx, "ROLLBACK")
		return err
	})
	if !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("commit without transaction = %v", err)
	}
	tx, err := beginImmediate(ctx, sqlDB, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.commit(ctx); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("double commit = %v", err)
	}

	connection, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, "DROP TABLE "+entriesTable); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanupStale(ctx, connection, time.Now(), time.Hour); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("cleanup missing entries = %v", err)
	}
	if _, err := deleteEmptyAuthorities(ctx, connection, nil); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("empty-authority cleanup missing entries = %v", err)
	}
	if _, err := deleteAuthority(ctx, connection, make([]byte, 32), []byte("authority")); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("delete authority missing entries = %v", err)
	}
	if err := deleteJar(ctx, connection, make([]byte, 32)); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("delete Jar missing entries = %v", err)
	}
	_ = connection.Close()
}

func TestSQLHelperFailuresRemainTypedAndRollback(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 13, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	keyring := testKeyring(t, "a1")

	type fixture struct {
		db         *gorm.DB
		repository *Repository
		connection *sql.Conn
		record     providercookie.BindingRecord
		scope      providercookie.CookieScope
		authority  []byte
		cookie     providercookie.StoredCookie
		sealed     codexkeyring.SealedValue
	}
	newFixture := func(t *testing.T) fixture {
		t.Helper()
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "helper.db"))
		repository := migrateAndOpen(t, db, keyring, 0)
		record := testBinding(t, keyring, t.Name(), testOwner(t, keyring, t.Name()), now)
		if err := repository.CreateBinding(ctx, record, policy); err != nil {
			t.Fatal(err)
		}
		scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "helper"))
		authority, _ := encodedAuthority(scope)
		cookie := testCookie(t, "sid", "value", now, now.Add(time.Hour))
		sealed, err := repository.sealCookie(scope, cookie)
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		connection, err := sqlDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connection.Close() })
		return fixture{db: db, repository: repository, connection: connection, record: record, scope: scope, authority: authority, cookie: cookie, sealed: sealed}
	}

	t.Run("authority helpers", func(t *testing.T) {
		item := newFixture(t)
		if err := item.db.Exec("DROP TABLE " + authoritiesTable).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := authorityExists(ctx, item.connection, item.scope, item.authority); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("authorityExists = %v", err)
		}
		if err := upsertAuthority(ctx, item.connection, item.scope, item.authority, now); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("upsertAuthority = %v", err)
		}
		if _, err := evictAuthorities(ctx, item.connection, item.record.JarID, 1); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("evictAuthorities = %v", err)
		}
	})

	t.Run("entry helpers", func(t *testing.T) {
		item := newFixture(t)
		if err := item.db.Exec("DROP TABLE " + entriesTable).Error; err != nil {
			t.Fatal(err)
		}
		if err := upsertCookie(ctx, item.connection, item.scope, item.authority, item.cookie, item.sealed, now); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("upsertCookie = %v", err)
		}
		if _, err := deleteCookie(ctx, item.connection, item.scope, item.authority, item.cookie.Key()); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("deleteCookie = %v", err)
		}
		if _, err := item.repository.reencryptLegacy(ctx, item.connection, item.scope, item.authority); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("reencryptLegacy = %v", err)
		}
		if _, err := evictEntries(ctx, item.connection, "jar_id = ?", []any{item.record.JarID.Bytes()}, 1); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("evictEntries = %v", err)
		}
		if _, err := enforceJarCapacity(ctx, item.connection, item.record.JarID, item.authority, policy); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("enforceJarCapacity = %v", err)
		}
		if _, err := item.repository.Cleanup(ctx, providercookie.CleanupRequest{At: now, Policy: policy}); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("Cleanup = %v", err)
		}
		if err := item.repository.CreateBinding(ctx, testBinding(t, keyring, "second", testOwner(t, keyring, "second"), now), policy); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("CreateBinding = %v", err)
		}
	})

	t.Run("repository queries", func(t *testing.T) {
		item := newFixture(t)
		if err := item.db.Exec("DROP TABLE " + handlesTable).Error; err != nil {
			t.Fatal(err)
		}
		lookup := providercookie.BindingLookup{
			HandleDigests: []codexkeyring.Digest{item.record.HandleDigest},
			ClientScopes:  []codexidentity.ClientScope{item.record.ClientScope}, At: now, Policy: policy,
		}
		if _, err := item.repository.UseBinding(ctx, lookup); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("UseBinding = %v", err)
		}
		if _, err := item.repository.Load(ctx, item.scope, now); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("Load = %v", err)
		}
		if _, err := item.repository.RequiredHMACVersions(ctx); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("RequiredHMACVersions = %v", err)
		}
	})

	t.Run("touch update", func(t *testing.T) {
		item := newFixture(t)
		if _, err := item.repository.Merge(ctx, item.scope, []providercookie.Mutation{providercookie.Upsert(item.cookie)}, now, policy); err != nil {
			t.Fatal(err)
		}
		if err := item.db.Exec("DROP TABLE " + entriesTable).Error; err != nil {
			t.Fatal(err)
		}
		if err := item.repository.Touch(ctx, item.scope, []providercookie.CookieKey{item.cookie.Key()}, now); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("Touch = %v", err)
		}
	})

	t.Run("eviction delete triggers", func(t *testing.T) {
		item := newFixture(t)
		if _, err := item.repository.Merge(ctx, item.scope, []providercookie.Mutation{providercookie.Upsert(item.cookie)}, now, policy); err != nil {
			t.Fatal(err)
		}
		if err := item.db.Exec("CREATE TRIGGER block_cookie_delete BEFORE DELETE ON " + entriesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := evictEntries(ctx, item.connection, "jar_id = ?", []any{item.record.JarID.Bytes()}, 0); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("evict entry delete = %v", err)
		}
		if err := item.db.Exec("DROP TRIGGER block_cookie_delete").Error; err != nil {
			t.Fatal(err)
		}
		if err := item.db.Exec("CREATE TRIGGER block_authority_delete BEFORE DELETE ON " + authoritiesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := evictAuthorities(ctx, item.connection, item.record.JarID, 0); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("evict authority delete = %v", err)
		}
	})

	t.Run("cleanup intermediate tables", func(t *testing.T) {
		item := newFixture(t)
		if err := item.db.Exec("DROP TABLE " + handlesTable).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := cleanupStale(ctx, item.connection, now, time.Hour); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("cleanup missing handles = %v", err)
		}
	})

	t.Run("delete authority row", func(t *testing.T) {
		item := newFixture(t)
		if err := item.db.Exec("DROP TABLE " + authoritiesTable).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := deleteAuthority(ctx, item.connection, item.record.JarID.Bytes(), item.authority); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("delete missing authority = %v", err)
		}
	})
}

func TestCleanupRejectsCorruptReachabilityRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "reachability-corrupt.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 8, 27, 14, 0, 0, 0, time.UTC)
	connection, err := func() (*sql.Conn, error) {
		sqlDB, openErr := db.DB()
		if openErr != nil {
			return nil, openErr
		}
		return sqlDB.Conn(ctx)
	}()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, "INSERT INTO "+authoritiesTable+" (jar_id, authority, created_at_ms, last_access_at_ms) VALUES (?, ?, ?, ?)", []byte{1}, []byte("authority"), toMillis(now), toMillis(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, "INSERT INTO "+entriesTable+` (
		jar_id, authority, cookie_name, cookie_domain, cookie_path, value_key_version, value_nonce, value_ciphertext,
		host_only, secure, http_only, quoted, session, same_site, expires_at_ms, created_at_ms, last_access_at_ms
	) VALUES (?, ?, 'sid', 'example.com', '/', 'a1', ?, ?, 1, 0, 1, 0, 0, 0, ?, ?, ?)`,
		[]byte{1}, []byte("authority"), make([]byte, 12), make([]byte, 16), toMillis(now.Add(time.Hour)), toMillis(now), toMillis(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Cleanup(ctx, providercookie.CleanupRequest{At: now, Policy: providercookie.DefaultPolicy()}); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("corrupt reachability = %v", err)
	}
}

func TestAdditionalSchemaVersionAndCodecFailureSurfaces(t *testing.T) {
	ctx := context.Background()
	keyring := testKeyring(t, "a1")

	uninitialized := openTestDatabase(t, filepath.Join(t.TempDir(), "uninitialized.db"))
	if err := ValidateSchema(ctx, uninitialized); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("uninitialized validation = %v", err)
	}
	if _, err := RequiredHMACVersions(nil, uninitialized); !errors.Is(err, providercookie.ErrInvalidConfig) {
		t.Fatalf("nil HMAC version context = %v", err)
	}
	if _, err := RequiredAEADVersions(nil, uninitialized); !errors.Is(err, providercookie.ErrInvalidConfig) {
		t.Fatalf("nil AEAD version context = %v", err)
	}

	closed := openTestDatabase(t, filepath.Join(t.TempDir(), "closed-schema.db"))
	closedSQL, _ := closed.DB()
	if err := closedSQL.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, closed); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("closed migration = %v", err)
	}
	if err := ValidateSchema(ctx, closed); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("closed validation = %v", err)
	}

	wrong := openTestDatabase(t, filepath.Join(t.TempDir(), "wrong-column-name.db"))
	if err := Migrate(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	if err := wrong.Exec("ALTER TABLE " + authoritiesTable + " RENAME COLUMN last_access_at_ms TO wrong_access_at_ms").Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, wrong); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("same-count wrong columns = %v", err)
	}

	invalidMetadata := entryRow{
		name: "sid", domain: "example.com", path: "/", nonce: make([]byte, 12), ciphertext: make([]byte, 16),
		hostOnly: 1, expiresAt: 2, createdAt: 1, accessedAt: 1,
	}
	if _, err := scanEntry(staticEntryScanner{row: invalidMetadata}); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("invalid metadata = %v", err)
	}

	db := openTestDatabase(t, filepath.Join(t.TempDir(), "codec.db"))
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 8, 27, 15, 0, 0, 0, time.UTC)
	record := testBinding(t, keyring, "codec", testOwner(t, keyring, "codec"), now)
	if err := repository.CreateBinding(ctx, record, providercookie.DefaultPolicy()); err != nil {
		t.Fatal(err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "codec"))
	row := invalidMetadata
	row.keyVersion = "a1"
	corruptValueRepository := *repository
	corruptValueRepository.cipher = fixedOpenCipher{ValueCipher: keyring, plaintext: []byte{'\n'}}
	if _, err := corruptValueRepository.decryptEntry(scope, row); !errors.Is(err, providercookie.ErrStorageCorrupt) {
		t.Fatalf("invalid plaintext Cookie = %v", err)
	}
	if _, err := repository.decryptEntry(providercookie.CookieScope{}, row); err == nil {
		t.Fatal("uninitialized decrypt scope accepted")
	}
	if _, err := repository.RequiredAEADVersions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP TABLE " + entriesTable).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RequiredAEADVersions(ctx); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("missing AEAD version table = %v", err)
	}
}

func TestMergeSQLMutationFailuresRollbackAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	keyring := testKeyring(t, "a1")

	type fixture struct {
		db         *gorm.DB
		repository *Repository
		scope      providercookie.CookieScope
	}
	newFixture := func(t *testing.T) fixture {
		t.Helper()
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "merge-trigger.db"))
		repository := migrateAndOpen(t, db, keyring, 0)
		record := testBinding(t, keyring, t.Name(), testOwner(t, keyring, t.Name()), now)
		if err := repository.CreateBinding(ctx, record, policy); err != nil {
			t.Fatal(err)
		}
		scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "merge-trigger"))
		return fixture{db: db, repository: repository, scope: scope}
	}

	t.Run("authority insert", func(t *testing.T) {
		item := newFixture(t)
		if err := item.db.Exec("CREATE TRIGGER block_authority_insert BEFORE INSERT ON " + authoritiesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		cookie := testCookie(t, "sid", "value", now, now.Add(time.Hour))
		if _, err := item.repository.Merge(ctx, item.scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("authority insert = %v", err)
		}
		assertEntryCount(t, item.db, 0)
	})

	t.Run("cookie upsert", func(t *testing.T) {
		item := newFixture(t)
		first := testCookie(t, "first", "value", now, now.Add(time.Hour))
		if _, err := item.repository.Merge(ctx, item.scope, []providercookie.Mutation{providercookie.Upsert(first)}, now, policy); err != nil {
			t.Fatal(err)
		}
		if err := item.db.Exec("CREATE TRIGGER block_cookie_insert BEFORE INSERT ON " + entriesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		second := testCookie(t, "second", "value", now, now.Add(time.Hour))
		if _, err := item.repository.Merge(ctx, item.scope, []providercookie.Mutation{providercookie.Upsert(second)}, now, policy); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("cookie upsert = %v", err)
		}
		assertEntryCount(t, item.db, 1)
	})

	t.Run("cookie delete", func(t *testing.T) {
		item := newFixture(t)
		cookie := testCookie(t, "sid", "value", now, now.Add(time.Hour))
		if _, err := item.repository.Merge(ctx, item.scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); err != nil {
			t.Fatal(err)
		}
		if err := item.db.Exec("CREATE TRIGGER block_cookie_delete_merge BEFORE DELETE ON " + entriesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := item.repository.Merge(ctx, item.scope, []providercookie.Mutation{providercookie.Tombstone(cookie.Key())}, now, policy); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("cookie delete = %v", err)
		}
		assertEntryCount(t, item.db, 1)
	})
}

func assertEntryCount(t *testing.T, db *gorm.DB, want int) {
	t.Helper()
	var count int
	if err := db.Raw("SELECT COUNT(*) FROM " + entriesTable).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("entry count = %d, want %d", count, want)
	}
}

func TestLegacyReencryptionFailuresRollbackAndPreserveCiphertext(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 17, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()

	type fixture struct {
		db     *gorm.DB
		scope  providercookie.CookieScope
		before []byte
	}
	newLegacyFixture := func(t *testing.T) fixture {
		t.Helper()
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "legacy.db"))
		legacyKeys := testKeyring(t, "a1")
		legacyRepository := migrateAndOpen(t, db, legacyKeys, 0)
		record := testBinding(t, legacyKeys, t.Name(), testOwner(t, legacyKeys, t.Name()), now)
		if err := legacyRepository.CreateBinding(ctx, record, policy); err != nil {
			t.Fatal(err)
		}
		scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "legacy-failure"))
		cookie := testCookie(t, "sid", "secret", now, now.Add(time.Hour))
		if _, err := legacyRepository.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); err != nil {
			t.Fatal(err)
		}
		var before []byte
		if err := db.Raw("SELECT value_ciphertext FROM " + entriesTable).Row().Scan(&before); err != nil {
			t.Fatal(err)
		}
		return fixture{db: db, scope: scope, before: before}
	}

	assertLegacyUnchanged := func(t *testing.T, item fixture) {
		t.Helper()
		var version string
		var ciphertext []byte
		if err := item.db.Raw("SELECT value_key_version, value_ciphertext FROM "+entriesTable).Row().Scan(&version, &ciphertext); err != nil {
			t.Fatal(err)
		}
		if version != "a1" || !bytes.Equal(ciphertext, item.before) {
			t.Fatalf("legacy row changed after rollback: version=%q", version)
		}
	}

	t.Run("seal", func(t *testing.T) {
		item := newLegacyFixture(t)
		rotatedKeys := testKeyring(t, "a2")
		repository, err := Open(ctx, Config{DB: item.db, Cipher: &failingCipher{ValueCipher: rotatedKeys, failAt: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.Merge(ctx, item.scope, nil, now, policy); !errors.Is(err, providercookie.ErrCrypto) {
			t.Fatalf("legacy seal failure = %v", err)
		}
		assertLegacyUnchanged(t, item)
	})

	t.Run("update", func(t *testing.T) {
		item := newLegacyFixture(t)
		rotatedKeys := testKeyring(t, "a2")
		repository, err := Open(ctx, Config{DB: item.db, Cipher: rotatedKeys})
		if err != nil {
			t.Fatal(err)
		}
		if err := item.db.Exec("CREATE TRIGGER block_legacy_update BEFORE UPDATE ON " + entriesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repository.Merge(ctx, item.scope, nil, now, policy); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("legacy update failure = %v", err)
		}
		assertLegacyUnchanged(t, item)
	})
}

func TestCorruptExpiredBindingAndAbsoluteRefreshBoundaryFailClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	keyring := testKeyring(t, "a1")

	t.Run("corrupt expired JarID", func(t *testing.T) {
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "corrupt-expired.db"))
		repository := migrateAndOpen(t, db, keyring, 0)
		record := testBinding(t, keyring, "corrupt-expired", testOwner(t, keyring, "corrupt-expired"), now)
		if err := repository.CreateBinding(ctx, record, policy); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("UPDATE "+handlesTable+" SET jar_id = ?, idle_expires_at_ms = ?", []byte{1}, toMillis(now.Add(-time.Second))).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repository.Cleanup(ctx, providercookie.CleanupRequest{At: now, Policy: policy}); !errors.Is(err, providercookie.ErrStorageCorrupt) {
			t.Fatalf("corrupt expired binding = %v", err)
		}
	})

	t.Run("absolute expiry bounds refresh", func(t *testing.T) {
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "absolute-refresh.db"))
		repository := migrateAndOpen(t, db, keyring, 0)
		record := testBinding(t, keyring, "absolute-refresh", testOwner(t, keyring, "absolute-refresh"), now)
		record.AbsoluteExpiresAt = now.Add(time.Hour)
		record.IdleExpiresAt = record.AbsoluteExpiresAt
		if err := repository.CreateBinding(ctx, record, policy); err != nil {
			t.Fatal(err)
		}
		use, err := repository.UseBinding(ctx, providercookie.BindingLookup{
			HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{record.ClientScope},
			At: now.Add(time.Minute), Policy: policy,
		})
		if err != nil || use.Disposition != providercookie.BindingValid || !use.Refresh || !use.Record.IdleExpiresAt.Equal(record.AbsoluteExpiresAt) {
			t.Fatalf("absolute-bounded refresh = %#v, %v", use, err)
		}
	})
}

func TestBindingRowCorruptionVariantsFailClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 19, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	keyring := testKeyring(t, "a1")

	for _, test := range []struct {
		name       string
		assignment string
		argument   any
	}{
		{name: "owner digest", assignment: "client_scope_digest = ?", argument: []byte{1}},
		{name: "JarID", assignment: "jar_id = ?", argument: []byte{1}},
		{name: "owner version", assignment: "client_scope_key_version = ?", argument: ""},
		{name: "timestamps", assignment: "last_access_at_ms = ?", argument: toMillis(now.Add(-time.Hour))},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openTestDatabase(t, filepath.Join(t.TempDir(), "corrupt-binding.db"))
			repository := migrateAndOpen(t, db, keyring, 0)
			record := testBinding(t, keyring, "corrupt-binding", testOwner(t, keyring, "corrupt-binding"), now)
			if err := repository.CreateBinding(ctx, record, policy); err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("UPDATE "+handlesTable+" SET "+test.assignment, test.argument).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := repository.UseBinding(ctx, providercookie.BindingLookup{
				HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{record.ClientScope},
				At: now, Policy: policy,
			}); !errors.Is(err, providercookie.ErrStorageCorrupt) {
				t.Fatalf("corrupt binding = %v", err)
			}
		})
	}
}

func TestCleanupDeleteFailuresRollbackWholeSweep(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 20, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	keyring := testKeyring(t, "a1")

	newFixture := func(t *testing.T) (*gorm.DB, *Repository, providercookie.BindingRecord, providercookie.CookieScope) {
		t.Helper()
		db := openTestDatabase(t, filepath.Join(t.TempDir(), "cleanup-delete.db"))
		repository := migrateAndOpen(t, db, keyring, 0)
		record := testBinding(t, keyring, t.Name(), testOwner(t, keyring, t.Name()), now)
		if err := repository.CreateBinding(ctx, record, policy); err != nil {
			t.Fatal(err)
		}
		scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "cleanup-delete"))
		cookie := testCookie(t, "sid", "value", now, now.Add(60*24*time.Hour))
		if _, err := repository.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, now, policy); err != nil {
			t.Fatal(err)
		}
		return db, repository, record, scope
	}

	t.Run("expired Jar", func(t *testing.T) {
		db, repository, _, _ := newFixture(t)
		if err := db.Exec("CREATE TRIGGER block_expired_jar_cookie_delete BEFORE DELETE ON " + entriesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		at := now.Add(policy.HandleIdleTTL + time.Second)
		if _, err := repository.Cleanup(ctx, providercookie.CleanupRequest{At: at, Policy: policy}); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("expired Jar delete = %v", err)
		}
		assertEntryCount(t, db, 1)
	})

	t.Run("orphan authority", func(t *testing.T) {
		db, repository, record, scope := newFixture(t)
		authority, _ := encodedAuthority(scope)
		if err := db.Exec("UPDATE "+authoritiesTable+" SET unreachable_since_ms = ? WHERE jar_id = ? AND authority = ?", toMillis(now.Add(-policy.OrphanAuthorityGrace-time.Second)), record.JarID.Bytes(), authority).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("CREATE TRIGGER block_orphan_cookie_delete BEFORE DELETE ON " + entriesTable + " BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repository.Cleanup(ctx, providercookie.CleanupRequest{At: now, Policy: policy}); !errors.Is(err, providercookie.ErrStorage) {
			t.Fatalf("orphan delete = %v", err)
		}
		assertEntryCount(t, db, 1)
	})
}
