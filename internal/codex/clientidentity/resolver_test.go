package clientidentity

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testDigester struct {
	versions []string
	err      error
}

func (d testDigester) ClientScopeCandidates(raw []byte) ([]codexidentity.ClientScope, error) {
	if d.err != nil {
		return nil, d.err
	}
	if len(raw) == 0 {
		return nil, errors.New("empty key")
	}
	var scopes []codexidentity.ClientScope
	for _, v := range d.versions {
		scope, err := codexidentity.ClientScopeFromDigest(v, sha256.Sum256(append([]byte(v), raw...)))
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}
func testDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sql.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestIdentityRotationBindingAndPortableRestart(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, filepath.Join(t.TempDir(), "identity.db"))
	resolver, _ := New(db, testDigester{versions: []string{"h1"}})
	first, err := resolver.Resolve(ctx, []byte("original-secret"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, _ := New(db, testDigester{versions: []string{"h2", "h1"}})
	again, err := rotated.Resolve(ctx, []byte("original-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != again.ID || !first.Primary.Equal(again.Primary) || len(again.Aliases) != 2 {
		t.Fatal("rotation changed canonical identity", again)
	}
	bound, err := rotated.BindKey(ctx, []byte("replacement-secret"), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.ID != first.ID || !bound.Primary.Equal(first.Primary) || len(bound.Aliases) != 4 {
		t.Fatal("binding lost identity scopes", bound)
	}
	other, err := rotated.Resolve(ctx, []byte("independent-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.BindKey(ctx, []byte("independent-secret"), first.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("expected established key conflict", err)
	}
	if other.ID == first.ID {
		t.Fatal("independent clients merged")
	}
	snapshot, err := Export(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Fatal("raw key leaked")
	}
	restoredDB := testDB(t, filepath.Join(t.TempDir(), "restored.db"))
	for range 2 {
		if err := restoredDB.Transaction(func(tx *gorm.DB) error { return Import(ctx, tx, snapshot) }); err != nil {
			t.Fatal(err)
		}
	}
	restored, _ := New(restoredDB, testDigester{versions: []string{"h2", "h1"}})
	resolved, err := restored.Resolve(ctx, []byte("replacement-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != first.ID || !resolved.Primary.Equal(first.Primary) {
		t.Fatal("portable identity did not survive")
	}
	clients, err := restored.ListClients(ctx)
	if err != nil || len(clients) != 2 {
		t.Fatal(clients, err)
	}
	versions, err := RequiredHMACVersions(ctx, restoredDB)
	if err != nil || len(versions) != 2 {
		t.Fatal(versions, err)
	}
	snapshot.Aliases[0].ClientID = "missing"
	if err := restoredDB.Transaction(func(tx *gorm.DB) error { return Import(ctx, tx, snapshot) }); err == nil {
		t.Fatal("missing client accepted")
	}
}
func TestConcurrentIndependentConnectionsAllocateOneIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first := testDB(t, path)
	second := testDB(t, path)
	resolvers := []*Resolver{}
	for _, db := range []*gorm.DB{first, second} {
		r, _ := New(db, testDigester{versions: []string{"h1"}})
		resolvers = append(resolvers, r)
	}
	const requests = 12
	results := make(chan Resolution, requests)
	failures := make(chan error, requests)
	var workers sync.WaitGroup
	for i := range requests {
		workers.Go(func() {
			r, err := resolvers[i%2].Resolve(context.Background(), []byte("same-key"))
			if err != nil {
				failures <- err
			} else {
				results <- r
			}
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	id := ""
	for result := range results {
		if id != "" && id != result.ID {
			t.Fatal("identity split")
		}
		id = result.ID
	}
}
func TestInvalidAndConflictingIdentityTransfers(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, filepath.Join(t.TempDir(), "errors.db"))
	if _, err := New(nil, testDigester{}); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := New(db, nil); err == nil {
		t.Fatal("missing digester accepted")
	}
	r, _ := New(db, testDigester{versions: []string{"h1"}})
	if _, err := r.BindKey(ctx, []byte("key"), ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.BindKey(ctx, []byte("key"), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, nil); err == nil {
		t.Fatal("empty key accepted")
	}
	empty, _ := New(db, testDigester{})
	if _, err := empty.Resolve(ctx, []byte("key")); err == nil {
		t.Fatal("empty scopes accepted")
	}
	if _, err := decodeScope("h1", nil); err == nil {
		t.Fatal("invalid digest accepted")
	}
	_, err := r.Resolve(ctx, []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Export(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Clients[0].PrimaryDigest[0] ^= 1
	if err := db.Transaction(func(tx *gorm.DB) error { return Import(ctx, tx, snapshot) }); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestInjectedAllocationAndDatabaseFailureRollBack(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "transaction.db"))
	now := time.Date(2026, time.September, 5, 0, 0, 0, 0, time.UTC)
	observed := []Trace{}
	resolver, err := NewWithConfig(Config{DB: db, Digester: testDigester{versions: []string{"h1"}}, Now: func() time.Time { return now }, NewID: func() string { return "deterministic-client" }})
	if err != nil {
		t.Fatal(err)
	}
	resolver.SetObserver(func(trace Trace) { observed = append(observed, trace) })
	failure := errors.New("alias persistence unavailable")
	if err := db.Callback().Create().Before("gorm:create").Register("reject-alias", func(tx *gorm.DB) {
		if tx.Statement.Table == "codex_client_key_aliases" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), []byte("key")); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	clients, err := resolver.ListClients(context.Background())
	if err != nil || len(clients) != 0 {
		t.Fatal("partial identity committed", clients, err)
	}
	if len(observed) != 1 || observed[0].Decision != "failed" {
		t.Fatal(observed)
	}
	if err := db.Callback().Create().Remove("reject-alias"); err != nil {
		t.Fatal(err)
	}
	identity, err := resolver.Resolve(context.Background(), []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID != "deterministic-client" {
		t.Fatal(identity.ID)
	}
	if _, err := resolver.BindKey(context.Background(), []byte("other-key"), identity.ID); err != nil {
		t.Fatal(err)
	}
	if observed[len(observed)-1].Decision != "key_bound" {
		t.Fatal(observed)
	}
	clients, err = resolver.ListClients(context.Background())
	if err != nil || !clients[0].CreatedAt.Equal(now) {
		t.Fatal(clients, err)
	}
	if err := db.Model(&Client{}).Where("id = ?", identity.ID).Update("primary_digest", []byte{1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), []byte("key")); err == nil {
		t.Fatal("corrupt canonical scope accepted")
	}
}
