package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"gorm.io/gorm"
)

func jarCount(t *testing.T, db *gorm.DB) int {
	t.Helper()
	var count int64
	if err := db.Table(handlesTable).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return int(count)
}

func TestReclamationPrefersEmptyThenUnreturnedThenReusedJars(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "priority.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	policy.MaxHandleBindingsGlobal = 3
	owner, authority := testOwner(t, keyring, "owner"), testAuthority(t, "capacity")
	mutation := providercookie.Upsert(testCookie(t, "sid", "value", now, now.Add(time.Hour)))
	create := func(label string, at time.Time) providercookie.BindingRecord {
		t.Helper()
		record := testBinding(t, keyring, label, owner, at)
		created, err := repository.CreateJar(ctx, record, authority, []providercookie.Mutation{mutation}, policy)
		if err != nil {
			t.Fatal(databaseFailure(err))
		}
		created.Release()
		return record
	}
	empty := testBinding(t, keyring, "legacy-empty", owner, now)
	if err := repository.seedBinding(ctx, empty, policy); err != nil {
		t.Fatal(err)
	}
	reused := create("reused", now)
	use, err := repository.UseBinding(ctx, providercookie.BindingLookup{
		HandleDigests: []codexkeyring.Digest{reused.HandleDigest}, ClientScopes: []codexidentity.ClientScope{owner}, At: now, Policy: policy,
	})
	if err != nil || use.Record.LastReturnedAt.IsZero() {
		t.Fatalf("same-millisecond return = %+v, %v", use, err)
	}
	use.Release()
	unreturned := create("unreturned", now.Add(time.Second))
	fourth := create("fourth", now.Add(2*time.Second))
	assertExists := func(record providercookie.BindingRecord, want bool) {
		t.Helper()
		var count int64
		if err := db.Table(handlesTable).Where("jar_id = ?", record.JarID.Bytes()).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if (count == 1) != want {
			t.Fatalf("binding presence = %d, want %v", count, want)
		}
	}
	assertExists(empty, false)
	assertExists(reused, true)
	assertExists(unreturned, true)
	create("fifth", now.Add(3*time.Second))
	assertExists(unreturned, false)
	assertExists(reused, true)
	assertExists(fourth, true)
	if jarCount(t, db) != policy.MaxHandleBindingsGlobal {
		t.Fatal("capacity exceeded")
	}

	// Once every candidate has been returned, least recent real reuse wins.
	if err := db.Exec("UPDATE " + handlesTable + " SET last_returned_at_ms = last_access_at_ms").Error; err != nil {
		t.Fatal(err)
	}
	create("sixth", now.Add(4*time.Second))
	assertExists(reused, false)
}

func TestUnreturnedCookieTrafficStaysBoundedAndRollsBackWhenAllJarsAreActive(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "bounded.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	policy.MaxHandleBindingsGlobal = 4
	policy.MaxCookiesPerAuthority, policy.MaxCookiesPerJar, policy.MaxCookieEntriesGlobal = 1, 1, 2
	owner, authority := testOwner(t, keyring, "owner"), testAuthority(t, "bounded")
	mutation := providercookie.Upsert(testCookie(t, "sid", "value", now, now.Add(time.Hour)))
	for index := range 30 {
		record := testBinding(t, keyring, fmt.Sprintf("unreturned-%d", index), owner, now.Add(time.Duration(index)*time.Millisecond))
		created, err := repository.CreateJar(ctx, record, authority, []providercookie.Mutation{mutation}, policy)
		if err != nil {
			t.Fatal(databaseFailure(err))
		}
		if index >= 2 && created.Merge.ReclaimedBindings != 1 {
			t.Fatalf("reclamation = %+v", created.Merge)
		}
		created.Release()
		if jarCount(t, db) > 2 {
			t.Fatal("global cookie capacity grew")
		}
	}
	for _, limit := range []providercookie.LimitName{providercookie.LimitGlobalEntries, providercookie.LimitHandleBindingsGlobal} {
		t.Run(string(limit), func(t *testing.T) {
			// Pin both survivors, including a second request sharing one jar.
			var records []providercookie.BindingRecord
			err := withImmediateTransaction(ctx, repository.database, repository.busyTimeout, func(connection *sql.Conn) error {
				var err error
				records, err = queryBindings(ctx, connection, "1 = 1", nil)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			releases := make([]func(), 0, len(records))
			for _, record := range records {
				use, err := repository.UseBinding(ctx, providercookie.BindingLookup{HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{owner}, At: now.Add(time.Second), Policy: policy})
				if err != nil {
					t.Fatal(err)
				}
				releases = append(releases, use.Release)
			}
			defer func() {
				for _, release := range releases {
					release()
					release()
				}
			}()
			blockedPolicy := policy
			if limit == providercookie.LimitHandleBindingsGlobal {
				blockedPolicy.MaxHandleBindingsGlobal = 2
			}
			record := testBinding(t, keyring, "blocked-"+string(limit), owner, now.Add(time.Second))
			created, err := repository.CreateJar(ctx, record, authority, []providercookie.Mutation{mutation}, blockedPolicy)
			var capacity *providercookie.LimitError
			if !errors.As(err, &capacity) || capacity.Limit != limit || created.Release != nil {
				t.Fatalf("blocked create = %+v, %v", created, err)
			}
			if jarCount(t, db) != 2 {
				t.Fatal("failed creation leaked a binding")
			}
		})
	}
}

func TestLiveJarLeaseCoordinatesIndependentPoolsAndCleanup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "leases.db")
	firstDB := openTestDatabase(t, path)
	keyring := testKeyring(t, "a1")
	first := migrateAndOpen(t, firstDB, keyring, 0)
	secondDB := openTestDatabase(t, path)
	second, err := Open(ctx, Config{DB: secondDB, Cipher: keyring})
	if err != nil {
		t.Fatal(err)
	}
	if first.activity != second.activity {
		t.Fatal("independent pools did not share request leases")
	}
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	policy := providercookie.DefaultPolicy()
	policy.MaxHandleBindingsGlobal = 1
	owner, authority := testOwner(t, keyring, "owner"), testAuthority(t, "leases")
	record := testBinding(t, keyring, "active", owner, now)
	record.IdleExpiresAt, record.AbsoluteExpiresAt = now.Add(time.Hour), now.Add(2*time.Hour)
	cookie := testCookie(t, "sid", "value", now, now.Add(5*time.Hour))
	created, err := first.CreateJar(ctx, record, authority, []providercookie.Mutation{providercookie.Upsert(cookie)}, policy)
	if err != nil {
		t.Fatal(err)
	}
	use, err := second.UseBinding(ctx, providercookie.BindingLookup{HandleDigests: []codexkeyring.Digest{record.HandleDigest}, ClientScopes: []codexidentity.ClientScope{owner}, At: now, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	created.Release()
	at := now.Add(3 * time.Hour)
	result, err := first.Cleanup(ctx, providercookie.CleanupRequest{At: at, Policy: policy, ReachableAuthorities: []codexidentity.CookieAuthority{authority}})
	if err != nil || result.ExpiredBindings != 0 {
		t.Fatalf("active cleanup = %+v, %v", result, err)
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, authority)
	if _, err := second.Merge(ctx, scope, []providercookie.Mutation{providercookie.Upsert(cookie)}, at, policy); err != nil {
		t.Fatalf("in-flight merge lost binding: %v", err)
	}
	newRecord := testBinding(t, keyring, "blocked", owner, at)
	if _, err := first.CreateJar(ctx, newRecord, authority, []providercookie.Mutation{providercookie.Upsert(cookie)}, policy); !errors.Is(err, providercookie.ErrLimitExceeded) {
		t.Fatalf("active eviction = %v", err)
	}
	use.Release()
	use.Release()
	result, err = second.Cleanup(ctx, providercookie.CleanupRequest{At: at, Policy: policy})
	if err != nil || result.ExpiredBindings != 1 || jarCount(t, firstDB) != 0 {
		t.Fatalf("released cleanup = %+v, %v", result, err)
	}
}

func TestCreateJarRollsBackBindingCookiesAndReclamationTogether(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "rollback.db"))
	keyring := testKeyring(t, "a1")
	repository := migrateAndOpen(t, db, keyring, 0)
	now := time.Now().UTC()
	policy := providercookie.DefaultPolicy()
	policy.MaxHandleBindingsGlobal = 1
	owner, authority := testOwner(t, keyring, "owner"), testAuthority(t, "rollback")
	cookie := testCookie(t, "sid", "value", now, now.Add(time.Hour))
	oldRecord := testBinding(t, keyring, "old", owner, now)
	old, err := repository.CreateJar(ctx, oldRecord, authority, []providercookie.Mutation{providercookie.Upsert(cookie)}, policy)
	if err != nil {
		t.Fatal(err)
	}
	old.Release()
	// Reclamation happens after insertion. A failing delete must roll back both.
	if err := db.Exec("CREATE TRIGGER reject_reclamation BEFORE DELETE ON " + handlesTable + " BEGIN SELECT RAISE(ABORT, 'delete failed'); END").Error; err != nil {
		t.Fatal(err)
	}
	record := testBinding(t, keyring, "new", owner, now)
	if _, err := repository.CreateJar(ctx, record, authority, []providercookie.Mutation{providercookie.Upsert(cookie)}, policy); !errors.Is(err, providercookie.ErrStorage) {
		t.Fatalf("delete failure = %v", err)
	}
	if jarCount(t, db) != 1 {
		t.Fatal("transaction leaked new binding")
	}
	scope, _ := providercookie.NewCookieScope(oldRecord.JarID, authority)
	if snapshot, err := repository.Load(ctx, scope, now); err != nil || len(snapshot.Cookies()) != 1 {
		t.Fatalf("rollback lost original cookie: %+v, %v", snapshot, err)
	}
	if err := db.Exec("DROP TRIGGER reject_reclamation").Error; err != nil {
		t.Fatal(err)
	}
	repository.cipher = &failingCipher{ValueCipher: keyring, failAt: 1}
	if _, err := repository.CreateJar(ctx, record, authority, []providercookie.Mutation{providercookie.Upsert(cookie)}, policy); !errors.Is(err, providercookie.ErrCrypto) {
		t.Fatalf("seal failure = %v", err)
	}
	if jarCount(t, db) != 1 {
		t.Fatal("cipher failure leaked new binding")
	}
}

func TestVersionThreeMigrationPreservesCookiesAndReturnEvidence(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t, filepath.Join(t.TempDir(), "v3.db"))
	v3Definition := strings.Replace(v2HandlesDefinition, "\t\tUNIQUE (client_scope_key_version, client_scope_digest),\n", "", 1)
	setupLegacySchema(t, db, 3, v3Definition)
	for index := byte(1); index <= 2; index++ {
		jar, handle, owner := make([]byte, 32), make([]byte, 32), make([]byte, 32)
		jar[0], handle[0], owner[0] = index, index, index
		insertTestCookieData(t, db, jar, handle, owner)
		if index == 2 {
			if err := db.Exec("UPDATE "+handlesTable+" SET last_access_at_ms = created_at_ms + 1 WHERE jar_id = ?", jar).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	var returned, entries int64
	if err := db.Table(handlesTable).Where("last_returned_at_ms IS NOT NULL").Count(&returned).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table(entriesTable).Count(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if returned != 1 || entries != 2 || jarCount(t, db) != 2 {
		t.Fatalf("migration counts: returned=%d entries=%d", returned, entries)
	}
}
