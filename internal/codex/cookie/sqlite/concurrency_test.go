package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

type pausedMergeCipher struct {
	ValueCipher
	sealCount int
	pauseAt   int
	entered   chan struct{}
	release   <-chan struct{}
}

func (c *pausedMergeCipher) Seal(purpose codexkeyring.AEADPurpose, aad, plaintext []byte) (codexkeyring.SealedValue, error) {
	c.sealCount++
	if c.sealCount == c.pauseAt {
		close(c.entered)
		<-c.release
	}
	return c.ValueCipher.Seal(purpose, aad, plaintext)
}

func TestMultiConnectionMergesIsolateUncommittedChangesAndPreserveCommittedKeys(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "multi-connection.db")
	firstDB := openSingleWriterTestDatabase(t, path)
	keyring := testKeyring(t, "a1")
	first := migrateAndOpen(t, firstDB, keyring, 0)
	secondDB := openSingleWriterTestDatabase(t, path)
	// Each repository owns a separate pool, so the held writer cannot turn
	// SQLite contention into a database/sql connection-queue wait.
	second, err := Open(ctx, Config{DB: secondDB, Cipher: keyring, BusyTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(databaseFailure(err))
	}
	now := time.Date(2026, 8, 27, 7, 0, 0, 0, time.UTC)
	record := testBinding(t, keyring, "multi-connection", testOwner(t, keyring, "owner"), now)
	policy := providercookie.DefaultPolicy()
	if err := first.CreateBinding(ctx, record, policy); err != nil {
		t.Fatal(databaseFailure(err))
	}
	scope, _ := providercookie.NewCookieScope(record.JarID, testAuthority(t, "multi-connection"))
	mutation := func(name, value string) providercookie.Mutation {
		return providercookie.Upsert(testCookie(t, name, value, now, now.Add(time.Hour)))
	}
	if _, err := first.Merge(ctx, scope, []providercookie.Mutation{mutation("base", "original"), mutation("shared", "original")}, now, policy); err != nil {
		t.Fatal(databaseFailure(err))
	}

	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	// Pause after one row was written, while the transaction still has another
	// mutation to apply. A separate reader must see neither partial change.
	cipher := &pausedMergeCipher{ValueCipher: keyring, pauseAt: 2, entered: make(chan struct{}), release: release}
	first, err = Open(ctx, Config{DB: firstDB, Cipher: cipher})
	if err != nil {
		t.Fatal(databaseFailure(err))
	}
	done := make(chan struct{})
	var firstErr error
	firstMutations := []providercookie.Mutation{mutation("first", "one"), mutation("shared", "first")}
	go func() {
		defer close(done)
		_, firstErr = first.Merge(ctx, scope, firstMutations, now, policy)
	}()
	defer func() {
		unblock()
		<-done
	}()
	select {
	case <-cipher.entered:
	case <-done:
		t.Fatalf("first merge did not reach the transaction barrier: %s", databaseFailure(firstErr))
	}

	var visibleNames []string
	if err := secondDB.Table(entriesTable).Order("cookie_name").Pluck("cookie_name", &visibleNames).Error; err != nil {
		t.Fatal(databaseFailure(err))
	}
	if !reflect.DeepEqual(visibleNames, []string{"base", "shared"}) {
		t.Fatalf("uncommitted rows visible on another connection: %v", visibleNames)
	}
	// The writer is held until this attempt returns. This checks admission's
	// decision, not how quickly a machine can schedule a burst of writers.
	_, err = second.Merge(ctx, scope, []providercookie.Mutation{mutation("rejected", "must-not-persist")}, now, policy)
	assertBusyAdmission(t, err)
	unblock()
	<-done
	if firstErr != nil {
		t.Fatal(databaseFailure(firstErr))
	}
	if _, err := second.Merge(ctx, scope, []providercookie.Mutation{mutation("second", "two"), mutation("shared", "second")}, now, policy); err != nil {
		t.Fatal(databaseFailure(err))
	}
	snapshot, err := first.Load(ctx, scope, now)
	if err != nil {
		t.Fatal(databaseFailure(err))
	}
	values := make(map[string]string)
	for _, cookie := range snapshot.Cookies() {
		values[cookie.Key().Name()] = cookie.Value()
	}
	want := map[string]string{"base": "original", "first": "one", "second": "two", "shared": "second"}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("committed merge values = %v, want %v", values, want)
	}
}

func assertBusyAdmission(t *testing.T, err error) {
	t.Helper()
	const sqliteBusy = 5
	var persistence *providercookie.PersistenceError
	var coded interface{ Code() int }
	if !errors.Is(err, providercookie.ErrStorage) ||
		!errors.As(err, &persistence) || persistence.Operation != "begin_transaction" ||
		!errors.As(persistence.Cause, &coded) || coded.Code() != sqliteBusy {
		t.Fatalf("busy admission = %s", databaseFailure(err))
	}
}
