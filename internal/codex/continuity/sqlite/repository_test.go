package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var repositoryNow = time.Date(2026, time.August, 27, 2, 0, 0, 0, time.UTC)

func TestRepositoryLifecycleOwnershipCapacityAndVersions(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	repository, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: time.Minute, CommittedIdleTTL: 2 * time.Minute, TombstoneTTL: time.Minute, MaxBindings: 2,
	}
	ownerA := testOwner(t, 1, "h2", accountScope(t, "account-a", "codex"), "route-a")
	ownerRouteB := ownerA
	ownerRouteB.RouteTargetHint = "route-b"
	ownerClientB := testOwner(t, 2, "h2", ownerA.ProtocolScope, "route-b")
	ownerScopeB := testOwner(t, 1, "h2", accountScope(t, "account-b", "codex"), "route-b")
	digest := testDigest(t, codexcontinuity.KindTurnMetadata, 10, "h1")
	claim := storeClaim(codexcontinuity.KindTurnMetadata, digest, ownerA, "claim-a", repositoryNow, limits)

	result, err := repository.Claim(context.Background(), claim)
	if err != nil || result.Decision != codexcontinuity.StoreClaimed {
		t.Fatalf("first claim = %#v, %v", result, err)
	}
	binding := result.Binding
	result, err = repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindTurnMetadata, digest, ownerRouteB, "claim-retry", repositoryNow, limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreOwned || result.Binding.Owner.RouteTargetHint != "route-a" {
		t.Fatalf("same owner retry = %#v, %v", result, err)
	}
	result, err = repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindTurnMetadata, digest, ownerClientB, "claim-client-b", repositoryNow, limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreConflict {
		t.Fatalf("client conflict = %#v, %v", result, err)
	}
	result, err = repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindTurnMetadata, digest, ownerScopeB, "claim-scope-b", repositoryNow, limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreConflict {
		t.Fatalf("scope conflict = %#v, %v", result, err)
	}

	result, err = repository.Lookup(context.Background(), storeLookup(
		codexcontinuity.KindTurnMetadata, digest, []codexidentity.ClientScope{ownerA.ClientScope}, nil, repositoryNow, limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreOwned {
		t.Fatalf("owner resolve = %#v, %v", result, err)
	}
	expected := ownerA.ProtocolScope
	lookup := storeLookup(
		codexcontinuity.KindTurnMetadata,
		digest,
		[]codexidentity.ClientScope{ownerA.ClientScope},
		&expected,
		repositoryNow,
		limits,
	)
	result, err = repository.Lookup(context.Background(), lookup)
	if err != nil || result.Decision != codexcontinuity.StoreOwned {
		t.Fatalf("owner validate = %#v, %v", result, err)
	}
	lookup.ProtocolScope = &ownerScopeB.ProtocolScope
	result, err = repository.Lookup(context.Background(), lookup)
	if err != nil || result.Decision != codexcontinuity.StoreConflict {
		t.Fatalf("lookup scope conflict = %#v, %v", result, err)
	}

	result, err = repository.Commit(context.Background(), codexcontinuity.StoreCommit{
		Binding: binding, Now: repositoryNow.Add(10 * time.Second), Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreCommitted || result.Binding.Lifecycle != codexcontinuity.LifecycleCommitted {
		t.Fatalf("commit = %#v, %v", result, err)
	}
	committed := result.Binding
	result, err = repository.Commit(context.Background(), codexcontinuity.StoreCommit{
		Binding: binding, Now: repositoryNow.Add(20 * time.Second), Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreCommitted {
		t.Fatalf("idempotent commit = %#v, %v", result, err)
	}
	if want := repositoryNow.Add(20*time.Second + limits.CommittedIdleTTL); !result.Binding.ExpiresAt.Equal(want) {
		t.Fatalf("renewed expiry = %v, want %v", result.Binding.ExpiresAt, want)
	}
	committed = result.Binding
	wrongClaim := binding
	wrongClaim.ClaimOperationID = "other-operation"
	result, err = repository.Commit(context.Background(), codexcontinuity.StoreCommit{
		Binding: wrongClaim, Now: repositoryNow.Add(20 * time.Second), Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreConflict {
		t.Fatalf("stale commit = %#v, %v", result, err)
	}
	result, err = repository.Abandon(context.Background(), codexcontinuity.StoreAbandon{
		Binding: committed, Now: repositoryNow.Add(20 * time.Second), Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreConflict {
		t.Fatalf("abandon committed = %#v, %v", result, err)
	}

	secondDigest := testDigest(t, codexcontinuity.KindTurnMetadata, 11, "h2")
	second, err := repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindTurnMetadata, secondDigest, ownerA, "claim-second", repositoryNow, limits,
	))
	if err != nil || second.Decision != codexcontinuity.StoreClaimed {
		t.Fatalf("second claim = %#v, %v", second, err)
	}
	thirdDigest := testDigest(t, codexcontinuity.KindTurnMetadata, 12, "h2")
	third, err := repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindTurnMetadata, thirdDigest, ownerA, "claim-third", repositoryNow, limits,
	))
	if err != nil || third.Decision != codexcontinuity.StoreCapacity {
		t.Fatalf("capacity = %#v, %v", third, err)
	}
	result, err = repository.Abandon(context.Background(), codexcontinuity.StoreAbandon{
		Binding: second.Binding, Now: repositoryNow, Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreAbandoned {
		t.Fatalf("abandon pending = %#v, %v", result, err)
	}
	result, err = repository.Lookup(context.Background(), storeLookup(
		codexcontinuity.KindTurnMetadata, secondDigest, []codexidentity.ClientScope{ownerA.ClientScope}, nil, repositoryNow, limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreUnknown {
		t.Fatalf("lookup abandoned = %#v, %v", result, err)
	}

	expiredAt := committed.ExpiresAt
	result, err = repository.Lookup(context.Background(), storeLookup(
		codexcontinuity.KindTurnMetadata,
		digest,
		[]codexidentity.ClientScope{ownerA.ClientScope},
		nil,
		expiredAt,
		limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreExpired || result.Binding.Lifecycle != codexcontinuity.LifecycleTombstone {
		t.Fatalf("lazy expiry = %#v, %v", result, err)
	}
	result, err = repository.Lookup(context.Background(), storeLookup(
		codexcontinuity.KindTurnMetadata,
		digest,
		[]codexidentity.ClientScope{ownerA.ClientScope},
		nil,
		expiredAt.Add(time.Minute),
		limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreUnknown {
		t.Fatalf("tombstone deletion = %#v, %v", result, err)
	}

	keyedOwner := testOwner(t, 3, "h2", keyedScope(t, 30, "h3"), "route-keyed")
	keyedDigest := testDigest(t, codexcontinuity.KindResponseReference, 31, "h1")
	keyed, err := repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindResponseReference, keyedDigest, keyedOwner, "keyed", repositoryNow, limits,
	))
	if err != nil || keyed.Decision != codexcontinuity.StoreClaimed {
		t.Fatalf("keyed owner = %#v, %v", keyed, err)
	}
	versions, err := repository.RequiredHMACVersions(context.Background())
	if err != nil || !reflect.DeepEqual(versions, []string{"h1", "h2", "h3"}) {
		t.Fatalf("required versions = %v, %v", versions, err)
	}
}

func TestCleanupTransitionsAndDeletes(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	defer closeDB()
	repository, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: time.Minute, CommittedIdleTTL: time.Minute, TombstoneTTL: time.Minute, MaxBindings: 20,
	}
	owner := testOwner(t, 1, "h1", accountScope(t, "account", "codex"), "route")
	for index, kind := range []codexcontinuity.Kind{codexcontinuity.KindThreadID, codexcontinuity.KindSessionID} {
		result, err := repository.Claim(context.Background(), storeClaim(
			kind, testDigest(t, kind, byte(40+index), "h1"), owner, string(kind), repositoryNow, limits,
		))
		if err != nil || result.Decision != codexcontinuity.StoreClaimed {
			t.Fatalf("seed cleanup binding: %#v, %v", result, err)
		}
	}
	policy := map[codexcontinuity.Kind]codexcontinuity.Limits{
		codexcontinuity.KindThreadID:  limits,
		codexcontinuity.KindSessionID: limits,
	}
	cleanup, err := repository.Cleanup(context.Background(), codexcontinuity.StoreCleanup{
		Now: repositoryNow.Add(time.Minute), Policy: policy,
	})
	if err != nil || cleanup.Expired != 2 || cleanup.Tombstoned != 2 || cleanup.Deleted != 0 {
		t.Fatalf("tombstone cleanup = %#v, %v", cleanup, err)
	}
	cleanup, err = repository.Cleanup(context.Background(), codexcontinuity.StoreCleanup{
		Now: repositoryNow.Add(2 * time.Minute), Policy: policy,
	})
	if err != nil || cleanup.Deleted != 2 {
		t.Fatalf("delete cleanup = %#v, %v", cleanup, err)
	}

	result, err := repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindThreadID,
		testDigest(t, codexcontinuity.KindThreadID, 50, "h1"),
		owner,
		"missing-policy",
		repositoryNow,
		limits,
	))
	if err != nil || result.Decision != codexcontinuity.StoreClaimed {
		t.Fatal(result, err)
	}
	if _, err := repository.Cleanup(context.Background(), codexcontinuity.StoreCleanup{
		Now: repositoryNow.Add(time.Minute), Policy: map[codexcontinuity.Kind]codexcontinuity.Limits{},
	}); err == nil {
		t.Fatal("cleanup without policy succeeded")
	}
}

func TestMultipleDigestGenerationsFailClosed(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	defer closeDB()
	repository, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: time.Hour, CommittedIdleTTL: time.Hour, TombstoneTTL: time.Hour, MaxBindings: 10,
	}
	owner := testOwner(t, 1, "h1", accountScope(t, "account", "codex"), "route")
	digestOne := testDigest(t, codexcontinuity.KindTurnState, 60, "h1")
	digestTwo := testDigest(t, codexcontinuity.KindTurnState, 61, "h2")
	for index, digest := range []codexidentity.OpaqueDigest{digestOne, digestTwo} {
		binding := codexcontinuity.Binding{
			Kind:             codexcontinuity.KindTurnState,
			Digest:           digest,
			Owner:            owner,
			Lifecycle:        codexcontinuity.LifecyclePending,
			ClaimOperationID: []string{"one", "two"}[index],
			CreatedAt:        repositoryNow,
			UpdatedAt:        repositoryNow,
			ExpiresAt:        repositoryNow.Add(time.Hour),
		}
		row, err := encodeBinding(binding)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	lookup := codexcontinuity.StoreLookup{
		Kind:                  codexcontinuity.KindTurnState,
		DigestCandidates:      []codexidentity.OpaqueDigest{digestOne, digestTwo},
		ClientScopeCandidates: []codexidentity.ClientScope{owner.ClientScope},
		OperationID:           "ambiguous",
		Now:                   repositoryNow,
		Limits:                limits,
	}
	if _, err := repository.Lookup(context.Background(), lookup); err == nil || !strings.Contains(err.Error(), "multiple HMAC generations") {
		t.Fatalf("ambiguous lookup error = %v", err)
	}
	claim := storeClaim(codexcontinuity.KindTurnState, digestOne, owner, "ambiguous-claim", repositoryNow, limits)
	claim.DigestCandidates = []codexidentity.OpaqueDigest{digestOne, digestTwo}
	if _, err := repository.Claim(context.Background(), claim); err == nil || !strings.Contains(err.Error(), "multiple HMAC generations") {
		t.Fatalf("ambiguous claim error = %v", err)
	}
}

func TestRepositoryUnavailableAfterClose(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	repository, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	closeDB()
	owner := testOwner(t, 1, "h1", accountScope(t, "account", "codex"), "route")
	digest := testDigest(t, codexcontinuity.KindTurnMetadata, 70, "h1")
	limits := codexcontinuity.Limits{PendingTTL: time.Hour, CommittedIdleTTL: time.Hour, TombstoneTTL: time.Hour, MaxBindings: 1}
	if _, err := repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindTurnMetadata, digest, owner, "closed", repositoryNow, limits,
	)); err == nil {
		t.Fatal("claim on closed database succeeded")
	}
	if _, err := repository.RequiredHMACVersions(context.Background()); err == nil {
		t.Fatal("version query on closed database succeeded")
	}
}

func TestMissingExpiredAndFullyElapsedMutationDecisions(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	defer closeDB()
	repository, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: time.Minute, CommittedIdleTTL: time.Minute, TombstoneTTL: time.Minute, MaxBindings: 10,
	}
	owner := testOwner(t, 1, "h1", accountScope(t, "account", "codex"), "route")
	digest := testDigest(t, codexcontinuity.KindWindowID, 75, "h1")
	missing := codexcontinuity.Binding{
		Kind: codexcontinuity.KindWindowID, Digest: digest, Owner: owner, ClaimOperationID: "missing",
	}
	result, err := repository.Commit(context.Background(), codexcontinuity.StoreCommit{
		Binding: missing, Now: repositoryNow, Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreUnknown {
		t.Fatalf("commit missing = %#v, %v", result, err)
	}
	result, err = repository.Abandon(context.Background(), codexcontinuity.StoreAbandon{
		Binding: missing, Now: repositoryNow, Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreUnknown {
		t.Fatalf("abandon missing = %#v, %v", result, err)
	}

	claimed, err := repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindWindowID, digest, owner, "expires", repositoryNow, limits,
	))
	if err != nil {
		t.Fatal(err)
	}
	result, err = repository.Commit(context.Background(), codexcontinuity.StoreCommit{
		Binding: claimed.Binding, Now: repositoryNow.Add(time.Minute), Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreExpired {
		t.Fatalf("commit expired = %#v, %v", result, err)
	}
	result, err = repository.Abandon(context.Background(), codexcontinuity.StoreAbandon{
		Binding: claimed.Binding, Now: repositoryNow.Add(time.Minute), Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreExpired {
		t.Fatalf("abandon expired = %#v, %v", result, err)
	}

	fullyElapsedDigest := testDigest(t, codexcontinuity.KindWindowID, 76, "h1")
	fullyElapsed, err := repository.Claim(context.Background(), storeClaim(
		codexcontinuity.KindWindowID, fullyElapsedDigest, owner, "fully-elapsed", repositoryNow, limits,
	))
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := repository.Cleanup(context.Background(), codexcontinuity.StoreCleanup{
		Now:    repositoryNow.Add(2 * time.Minute),
		Policy: map[codexcontinuity.Kind]codexcontinuity.Limits{codexcontinuity.KindWindowID: limits},
	})
	if err != nil || cleanup.Expired == 0 || cleanup.Deleted == 0 {
		t.Fatalf("fully elapsed cleanup = %#v, %v", cleanup, err)
	}
	result, err = repository.Commit(context.Background(), codexcontinuity.StoreCommit{
		Binding: fullyElapsed.Binding, Now: repositoryNow.Add(2 * time.Minute), Limits: limits,
	})
	if err != nil || result.Decision != codexcontinuity.StoreUnknown {
		t.Fatalf("commit deleted = %#v, %v", result, err)
	}
}

func TestRepositoryRejectsMalformedDirectCommands(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	defer closeDB()
	repository, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: time.Hour, CommittedIdleTTL: time.Hour, TombstoneTTL: time.Hour, MaxBindings: 10,
	}
	owner := testOwner(t, 1, "h1", accountScope(t, "account", "codex"), "route")
	digest := testDigest(t, codexcontinuity.KindTurnMetadata, 77, "h1")
	claim := storeClaim(codexcontinuity.KindTurnMetadata, digest, owner, "operation", repositoryNow, limits)
	claim.DigestCandidates = nil
	if _, err := repository.Claim(context.Background(), claim); err == nil {
		t.Fatal("claim accepted no lookup candidates")
	}
	lookup := storeLookup(
		codexcontinuity.KindTurnMetadata,
		digest,
		[]codexidentity.ClientScope{owner.ClientScope},
		nil,
		repositoryNow,
		limits,
	)
	lookup.DigestCandidates = nil
	if _, err := repository.Lookup(context.Background(), lookup); err == nil {
		t.Fatal("lookup accepted no lookup candidates")
	}
	invalidOwner := claim
	invalidOwner.DigestCandidates = []codexidentity.OpaqueDigest{digest}
	invalidOwner.Owner = codexcontinuity.Owner{}
	if _, err := repository.Claim(context.Background(), invalidOwner); err == nil {
		t.Fatal("claim accepted invalid owner")
	}
	invalidOperation := storeClaim(
		codexcontinuity.KindTurnMetadata,
		testDigest(t, codexcontinuity.KindTurnMetadata, 78, "h1"),
		owner,
		strings.Repeat("x", codexcontinuity.MaxOperationIDBytes+1),
		repositoryNow,
		limits,
	)
	if _, err := repository.Claim(context.Background(), invalidOperation); err == nil {
		t.Fatal("claim bypassed operation-id storage constraint")
	}
}

func storeClaim(
	kind codexcontinuity.Kind,
	digest codexidentity.OpaqueDigest,
	owner codexcontinuity.Owner,
	operation string,
	now time.Time,
	limits codexcontinuity.Limits,
) codexcontinuity.StoreClaim {
	return codexcontinuity.StoreClaim{
		Kind:                  kind,
		CurrentDigest:         digest,
		DigestCandidates:      []codexidentity.OpaqueDigest{digest},
		Owner:                 owner,
		ClientScopeCandidates: []codexidentity.ClientScope{owner.ClientScope},
		OperationID:           operation,
		Now:                   now,
		Limits:                limits,
	}
}

func storeLookup(
	kind codexcontinuity.Kind,
	digest codexidentity.OpaqueDigest,
	clients []codexidentity.ClientScope,
	protocol *codexidentity.ProtocolScope,
	now time.Time,
	limits codexcontinuity.Limits,
) codexcontinuity.StoreLookup {
	return codexcontinuity.StoreLookup{
		Kind:                  kind,
		DigestCandidates:      []codexidentity.OpaqueDigest{digest},
		ClientScopeCandidates: clients,
		ProtocolScope:         protocol,
		OperationID:           "lookup",
		Now:                   now,
		Limits:                limits,
	}
}

func openMigratedDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, closeDB := openTestDB(t)
	if err := Migrate(context.Background(), db); err != nil {
		closeDB()
		t.Fatal(err)
	}
	return db, closeDB
}

func openTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "continuity.db")
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
		t.Fatal(err)
	}
	closed := false
	return db, func() {
		if closed {
			return
		}
		closed = true
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}
}

func testOwner(
	t *testing.T,
	clientSeed byte,
	clientVersion string,
	scope codexidentity.ProtocolScope,
	route string,
) codexcontinuity.Owner {
	t.Helper()
	return codexcontinuity.Owner{
		ClientScope:     testClient(t, clientSeed, clientVersion),
		ProtocolScope:   scope,
		RouteTargetHint: route,
	}
}

func testClient(t *testing.T, seed byte, version string) codexidentity.ClientScope {
	t.Helper()
	var sum [codexidentity.DigestSize]byte
	fillDigest(sum[:], seed)
	client, err := codexidentity.ClientScopeFromDigest(version, sum)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testDigest(
	t *testing.T,
	kind codexcontinuity.Kind,
	seed byte,
	version string,
) codexidentity.OpaqueDigest {
	t.Helper()
	var sum [codexidentity.DigestSize]byte
	fillDigest(sum[:], seed)
	namespace, err := kind.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := codexidentity.OpaqueDigestFromParts(namespace, version, sum)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func fillDigest(target []byte, seed byte) {
	for index := range target {
		target[index] = seed + byte(index)
	}
}

func accountScope(t *testing.T, account, apiType string) codexidentity.ProtocolScope {
	t.Helper()
	subject, err := codexidentity.NewAccountCredentialSubject(account)
	if err != nil {
		t.Fatal(err)
	}
	return scopeFromSubject(t, subject, apiType)
}

func keyedScope(t *testing.T, seed byte, version string) codexidentity.ProtocolScope {
	t.Helper()
	var sum [codexidentity.DigestSize]byte
	fillDigest(sum[:], seed)
	subject, err := codexidentity.NewKeyedCredentialSubject(version, sum)
	if err != nil {
		t.Fatal(err)
	}
	return scopeFromSubject(t, subject, "codex")
}

func scopeFromSubject(
	t *testing.T,
	subject codexidentity.CredentialSubject,
	apiType string,
) codexidentity.ProtocolScope {
	t.Helper()
	origin, err := codexidentity.ParseOrigin("https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	authority, err := codexidentity.NewUpstreamAuthority("vendor", origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := codexidentity.NewProtocolScope(authority, apiType)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestOpenRejectsMissingSchema(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()
	if _, err := Open(context.Background(), db); err == nil {
		t.Fatal("Open accepted a missing schema")
	}
	if _, err := Open(context.Background(), nil); err == nil {
		t.Fatal("Open accepted a nil database")
	}
}

func TestFindCandidateRowsRequiresDigests(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	defer closeDB()
	if _, err := findCandidateRows(db, codexcontinuity.KindTurnState, nil); err == nil {
		t.Fatal("findCandidateRows accepted no candidates")
	}
	missing := codexcontinuity.Binding{
		Kind:   codexcontinuity.KindTurnState,
		Digest: testDigest(t, codexcontinuity.KindTurnState, 90, "h1"),
	}
	if _, found, err := findExactRow(db, missing); err != nil || found {
		t.Fatalf("findExactRow missing = %t, %v", found, err)
	}
	if err := deleteBinding(db, codexcontinuity.Binding{
		Kind:             codexcontinuity.KindTurnState,
		Digest:           missing.Digest,
		ClaimOperationID: "missing",
	}); err == nil {
		t.Fatal("deleteBinding accepted a missing row")
	}
}

func TestErrorsIsUsedByRepositoryCallers(t *testing.T) {
	if !errors.Is(gorm.ErrRecordNotFound, gorm.ErrRecordNotFound) {
		t.Fatal("unexpected errors.Is behavior")
	}
}
