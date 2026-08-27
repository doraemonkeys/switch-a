package codexcontinuity_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var testNow = time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type testFixture struct {
	service  *codexcontinuity.Service
	repo     *continuitysqlite.Repository
	db       *gorm.DB
	clock    *fakeClock
	digester codexidentity.Digester
	close    func()
}

func TestLifecycleScopeAndUnknownClaimRules(t *testing.T) {
	fixture := newFixture(t, keyringDocument("h1", map[string]byte{"h1": 1}), policyWith(10, time.Hour))
	defer fixture.close()
	ctx := context.Background()
	clientA := clientScopes(t, fixture.digester, "client-a")
	clientB := clientScopes(t, fixture.digester, "client-b")
	scopeA := protocolScope(t, "vendor-a", "https://api.example.com", "account-a", "codex")
	scopeB := protocolScope(t, "vendor-a", "https://api.example.com", "account-b", "codex")

	metadata := evidence(codexcontinuity.KindTurnMetadata, "metadata-one")
	lease := requireClaim(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    metadata,
		Scope:       scopeFor(clientA, scopeA, "route-a"),
		OperationID: "claim-metadata",
	})
	if !lease.NewlyClaimed() || lease.Binding().Lifecycle != codexcontinuity.LifecyclePending {
		t.Fatalf("first claim = %#v, want newly claimed pending", lease)
	}
	resolved, err := fixture.service.ResolveOwner(ctx, codexcontinuity.ResolveRequest{
		Evidence: metadata, ClientScopeCandidates: clientA, OperationID: "resolve-metadata",
	})
	if err != nil || !resolved.Owner.ProtocolScope.Equal(scopeA) {
		t.Fatalf("resolve owner = %#v, %v", resolved, err)
	}
	validated, err := fixture.service.Validate(ctx, codexcontinuity.ValidateRequest{
		Evidence: metadata, ClientScopeCandidates: clientA, ProtocolScope: scopeA, OperationID: "validate-metadata",
	})
	if err != nil || validated.Lifecycle != codexcontinuity.LifecyclePending {
		t.Fatalf("validate pending = %#v, %v", validated, err)
	}

	retry := requireClaim(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    metadata,
		Scope:       scopeFor(clientA, scopeA, "route-b"),
		OperationID: "retry-metadata",
	})
	if retry.NewlyClaimed() || retry.Binding().Owner.RouteTargetHint != "route-a" {
		t.Fatalf("same-scope route replacement changed owner: %#v", retry)
	}
	committed, err := fixture.service.Commit(ctx, retry)
	if err != nil || committed.Lifecycle != codexcontinuity.LifecycleCommitted || committed.CommittedAt == nil {
		t.Fatalf("commit = %#v, %v", committed, err)
	}
	if _, err := fixture.service.Commit(ctx, retry); err != nil {
		t.Fatalf("idempotent commit: %v", err)
	}

	assertKind(t, resolveError(fixture.service, metadata, clientB), codexcontinuity.ErrorConflict)
	_, err = fixture.service.Validate(ctx, codexcontinuity.ValidateRequest{
		Evidence: metadata, ClientScopeCandidates: clientA, ProtocolScope: scopeB, OperationID: "cross-protocol",
	})
	assertKind(t, err, codexcontinuity.ErrorConflict)

	turnState := evidence(codexcontinuity.KindTurnState, "state-unknown")
	_, err = fixture.service.Claim(ctx, codexcontinuity.ClaimRequest{
		Evidence: turnState, Scope: scopeFor(clientA, scopeA, "route-a"), OperationID: "claim-state",
	})
	assertKind(t, err, codexcontinuity.ErrorInvalidTransition)
	_, err = fixture.service.Validate(ctx, codexcontinuity.ValidateRequest{
		Evidence: turnState, ClientScopeCandidates: clientA, ProtocolScope: scopeA, OperationID: "unknown-state",
	})
	assertKind(t, err, codexcontinuity.ErrorUnknown)

	response := evidence(codexcontinuity.KindResponseReference, "response-unknown")
	_, err = fixture.service.Claim(ctx, codexcontinuity.ClaimRequest{
		Evidence: response, Scope: scopeFor(clientA, scopeA, "route-a"), OperationID: "claim-response",
	})
	assertKind(t, err, codexcontinuity.ErrorInvalidTransition)
	_, err = fixture.service.PrepareVisible(ctx, codexcontinuity.ClaimRequest{
		Evidence: metadata, Scope: scopeFor(clientA, scopeA, "route-a"), OperationID: "prepare-metadata",
	})
	assertKind(t, err, codexcontinuity.ErrorInvalidTransition)
}

func TestUncertainPendingAbandonAndTombstoneLifecycle(t *testing.T) {
	policy := policyWith(10, time.Hour)
	fixture := newFixture(t, keyringDocument("h1", map[string]byte{"h1": 1}), policy)
	defer fixture.close()
	ctx := context.Background()
	clients := clientScopes(t, fixture.digester, "client")
	otherClients := clientScopes(t, fixture.digester, "other-client")
	scope := protocolScope(t, "vendor", "https://api.example.com", "account", "codex")
	otherScope := protocolScope(t, "vendor", "https://other.example.com", "account", "codex")

	uncertain := requirePrepare(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindResponseReference, "uncertain-response"),
		Scope:       scopeFor(clients, scope, "route-a"),
		OperationID: "uncertain-write",
	})
	if uncertain.Binding().Lifecycle != codexcontinuity.LifecyclePending {
		t.Fatalf("uncertain binding lifecycle = %q", uncertain.Binding().Lifecycle)
	}
	_, err := fixture.service.PrepareVisible(ctx, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindResponseReference, "uncertain-response"),
		Scope:       scopeFor(otherClients, otherScope, "route-b"),
		OperationID: "steal-uncertain",
	})
	assertKind(t, err, codexcontinuity.ErrorConflict)

	localOnly := requirePrepare(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindTurnState, "local-only-state"),
		Scope:       scopeFor(clients, scope, "route-a"),
		OperationID: "local-only",
	})
	if err := fixture.service.AbandonBeforeDisclosure(ctx, localOnly); err != nil {
		t.Fatalf("abandon local-only pending: %v", err)
	}
	_, err = fixture.service.Validate(ctx, codexcontinuity.ValidateRequest{
		Evidence:              evidence(codexcontinuity.KindTurnState, "local-only-state"),
		ClientScopeCandidates: clients,
		ProtocolScope:         scope,
		OperationID:           "validate-abandoned",
	})
	assertKind(t, err, codexcontinuity.ErrorUnknown)

	retry := requirePrepare(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindResponseReference, "uncertain-response"),
		Scope:       scopeFor(clients, scope, "route-c"),
		OperationID: "uncertain-retry",
	})
	if retry.NewlyClaimed() {
		t.Fatal("retry must not own abandonment rights for an older pending row")
	}
	assertKind(t, fixture.service.AbandonBeforeDisclosure(ctx, retry), codexcontinuity.ErrorInvalidTransition)

	fixture.clock.Advance(time.Hour)
	_, err = fixture.service.Validate(ctx, codexcontinuity.ValidateRequest{
		Evidence:              evidence(codexcontinuity.KindResponseReference, "uncertain-response"),
		ClientScopeCandidates: clients,
		ProtocolScope:         scope,
		OperationID:           "expired-response",
	})
	assertKind(t, err, codexcontinuity.ErrorExpired)
	cleanup, err := fixture.service.Cleanup(ctx)
	if err != nil {
		t.Fatalf("cleanup tombstone: %v", err)
	}
	if cleanup.Deleted != 0 {
		t.Fatalf("cleanup before tombstone expiry deleted %d", cleanup.Deleted)
	}

	fixture.clock.Advance(time.Hour)
	cleanup, err = fixture.service.Cleanup(ctx)
	if err != nil || cleanup.Deleted == 0 {
		t.Fatalf("cleanup after tombstone expiry = %#v, %v", cleanup, err)
	}
	_, err = fixture.service.Validate(ctx, codexcontinuity.ValidateRequest{
		Evidence:              evidence(codexcontinuity.KindResponseReference, "uncertain-response"),
		ClientScopeCandidates: clients,
		ProtocolScope:         scope,
		OperationID:           "unknown-after-retention",
	})
	assertKind(t, err, codexcontinuity.ErrorUnknown)
}

func TestAcquireExistingFinalizesPendingWithoutReclaiming(t *testing.T) {
	fixture := newFixture(t, keyringDocument("h1", map[string]byte{"h1": 1}), lifecyclePolicy(100))
	defer fixture.close()
	ctx := context.Background()
	clients := clientScopes(t, fixture.digester, "client")
	otherClients := clientScopes(t, fixture.digester, "other-client")
	scope := protocolScope(t, "vendor", "https://api.example.com", "account", "codex")
	otherScope := protocolScope(t, "vendor", "https://other.example.com", "account", "codex")

	for _, kind := range []codexcontinuity.Kind{
		codexcontinuity.KindSessionID,
		codexcontinuity.KindTurnMetadata,
		codexcontinuity.KindTurnState,
		codexcontinuity.KindResponseReference,
	} {
		t.Run(string(kind), func(t *testing.T) {
			item := evidence(kind, "pending-"+string(kind))
			claim := codexcontinuity.ClaimRequest{
				Evidence: item, Scope: scopeFor(clients, scope, "route-a"), OperationID: "origin-" + string(kind),
			}
			var original codexcontinuity.Lease
			if kind.ClientClaimable() {
				original = requireClaim(t, fixture.service, claim)
			} else {
				original = requirePrepare(t, fixture.service, claim)
			}

			acquired, err := fixture.service.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
				Evidence: item, ClientScopeCandidates: clients, ProtocolScope: scope, OperationID: "retry-" + string(kind),
			})
			if err != nil {
				t.Fatal(err)
			}
			if acquired.NewlyClaimed() || acquired.Binding().Lifecycle != codexcontinuity.LifecyclePending {
				t.Fatalf("acquired lease = %#v, want existing pending", acquired)
			}
			if acquired.Binding().ClaimOperationID != original.Binding().ClaimOperationID {
				t.Fatalf("claim operation changed from %q to %q", original.Binding().ClaimOperationID, acquired.Binding().ClaimOperationID)
			}
			assertKind(t, fixture.service.AbandonBeforeDisclosure(ctx, acquired), codexcontinuity.ErrorInvalidTransition)

			const finalizers = 8
			start := make(chan struct{})
			errors := make(chan error, finalizers)
			var group sync.WaitGroup
			group.Add(finalizers)
			for range finalizers {
				go func() {
					defer group.Done()
					<-start
					_, commitErr := fixture.service.Commit(ctx, acquired)
					errors <- commitErr
				}()
			}
			close(start)
			group.Wait()
			close(errors)
			for commitErr := range errors {
				if commitErr != nil {
					t.Fatal("concurrent finalize:", commitErr)
				}
			}

			committed, err := fixture.service.Validate(ctx, codexcontinuity.ValidateRequest{
				Evidence: item, ClientScopeCandidates: clients, ProtocolScope: scope, OperationID: "validate-" + string(kind),
			})
			if err != nil || committed.Lifecycle != codexcontinuity.LifecycleCommitted || committed.CommittedAt == nil {
				t.Fatalf("committed binding = %#v, %v", committed, err)
			}
			if got, want := committed.ExpiresAt, testNow.Add(2*time.Hour); !got.Equal(want) {
				t.Fatalf("committed expiry = %v, want %v", got, want)
			}
			idempotent, err := fixture.service.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
				Evidence: item, ClientScopeCandidates: clients, ProtocolScope: scope, OperationID: "committed-retry-" + string(kind),
			})
			if err != nil {
				t.Fatal(err)
			}
			again, err := fixture.service.Commit(ctx, idempotent)
			if err != nil || again.ClaimOperationID != original.Binding().ClaimOperationID || !again.CommittedAt.Equal(*committed.CommittedAt) {
				t.Fatalf("idempotent finalize = %#v, %v", again, err)
			}
			_, err = fixture.service.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
				Evidence: item, ClientScopeCandidates: otherClients, ProtocolScope: otherScope, OperationID: "conflict-" + string(kind),
			})
			assertKind(t, err, codexcontinuity.ErrorConflict)
		})
	}

	unknown := evidence(codexcontinuity.KindResponseReference, "never-issued")
	_, err := fixture.service.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
		Evidence: unknown, ClientScopeCandidates: clients, ProtocolScope: scope, OperationID: "unknown-reference",
	})
	assertKind(t, err, codexcontinuity.ErrorUnknown)
	issued := requirePrepare(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence: unknown, Scope: scopeFor(otherClients, otherScope, "route-other"), OperationID: "issue-after-unknown",
	})
	if !issued.NewlyClaimed() {
		t.Fatal("unknown-only acquisition created a durable owner")
	}
}

func TestCapacityCleanupAndImmediateReclaimAfterFullRetention(t *testing.T) {
	policy := policyWith(1, 10*time.Minute)
	fixture := newFixture(t, keyringDocument("h1", map[string]byte{"h1": 1}), policy)
	defer fixture.close()
	clients := clientScopes(t, fixture.digester, "client")
	scope := protocolScope(t, "vendor", "https://api.example.com", "account", "codex")

	requireClaim(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindSessionID, "session-one"),
		Scope:       scopeFor(clients, scope, "route"),
		OperationID: "capacity-one",
	})
	_, err := fixture.service.Claim(context.Background(), codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindSessionID, "session-two"),
		Scope:       scopeFor(clients, scope, "route"),
		OperationID: "capacity-two",
	})
	assertKind(t, err, codexcontinuity.ErrorCapacity)

	fixture.clock.Advance(20 * time.Minute)
	lease := requireClaim(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindSessionID, "session-two"),
		Scope:       scopeFor(clients, scope, "route"),
		OperationID: "capacity-recovered",
	})
	if !lease.NewlyClaimed() {
		t.Fatal("capacity cleanup did not admit a new owner")
	}

	// The exact same digest can also be atomically reclaimed after its active and
	// tombstone retention are both over.
	fixture.clock.Advance(20 * time.Minute)
	reclaimed := requireClaim(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindSessionID, "session-two"),
		Scope:       scopeFor(clients, scope, "route"),
		OperationID: "same-key-reclaim",
	})
	if !reclaimed.NewlyClaimed() || reclaimed.Binding().ClaimOperationID != "same-key-reclaim" {
		t.Fatalf("same key reclaim = %#v", reclaimed)
	}
}

func TestConcurrentClaimHasOneDurableOwner(t *testing.T) {
	fixture := newFixture(t, keyringDocument("h1", map[string]byte{"h1": 1}), policyWith(100, time.Hour))
	defer fixture.close()
	client := clientScopes(t, fixture.digester, "client")
	scopes := []codexidentity.ProtocolScope{
		protocolScope(t, "vendor", "https://a.example.com", "account", "codex"),
		protocolScope(t, "vendor", "https://b.example.com", "account", "codex"),
	}
	const workers = 32
	start := make(chan struct{})
	type outcome struct {
		scope int
		err   error
	}
	outcomes := make(chan outcome, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			scopeIndex := index % len(scopes)
			_, err := fixture.service.Claim(context.Background(), codexcontinuity.ClaimRequest{
				Evidence:    evidence(codexcontinuity.KindThreadID, "shared-thread"),
				Scope:       scopeFor(client, scopes[scopeIndex], fmt.Sprintf("route-%d", index)),
				OperationID: fmt.Sprintf("claim-%d", index),
			})
			outcomes <- outcome{scope: scopeIndex, err: err}
		}(worker)
	}
	close(start)
	wait.Wait()
	close(outcomes)
	winner := -1
	successes := 0
	conflicts := 0
	for result := range outcomes {
		if result.err == nil {
			successes++
			if winner == -1 {
				winner = result.scope
			} else if winner != result.scope {
				t.Fatalf("both scopes claimed the same opaque owner")
			}
			continue
		}
		if codexcontinuity.IsError(result.err, codexcontinuity.ErrorConflict) {
			conflicts++
			continue
		}
		t.Fatalf("concurrent claim returned %v", result.err)
	}
	if successes == 0 || conflicts == 0 || successes+conflicts != workers {
		t.Fatalf("successes=%d conflicts=%d winner=%d", successes, conflicts, winner)
	}
}

func TestProtocolScopeDimensionsAreIndependentOwnerBoundaries(t *testing.T) {
	fixture := newFixture(t, keyringDocument("h1", map[string]byte{"h1": 1}), policyWith(20, time.Hour))
	defer fixture.close()
	clients := clientScopes(t, fixture.digester, "client")
	base := protocolScope(t, "vendor", "https://api.example.com", "account", "codex")
	evidenceValue := evidence(codexcontinuity.KindSessionID, "scope-matrix")
	requireClaim(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidenceValue,
		Scope:       scopeFor(clients, base, "route-a"),
		OperationID: "scope-base",
	})
	for name, scope := range map[string]codexidentity.ProtocolScope{
		"vendor":   protocolScope(t, "other-vendor", "https://api.example.com", "account", "codex"),
		"origin":   protocolScope(t, "vendor", "https://other.example.com", "account", "codex"),
		"subject":  protocolScope(t, "vendor", "https://api.example.com", "other-account", "codex"),
		"api type": protocolScope(t, "vendor", "https://api.example.com", "account", "chat_completions"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fixture.service.Validate(context.Background(), codexcontinuity.ValidateRequest{
				Evidence:              evidenceValue,
				ClientScopeCandidates: clients,
				ProtocolScope:         scope,
				OperationID:           "scope-" + name,
			})
			assertKind(t, err, codexcontinuity.ErrorConflict)
		})
	}
}

func TestRestartAndHMACRotationUseLegacyCandidates(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "continuity.db")
	oldDocument := keyringDocument("h1", map[string]byte{"h1": 1})
	oldFixture := newFixtureAt(t, databasePath, oldDocument, policyWith(20, time.Hour), true)
	clientsOld := clientScopes(t, oldFixture.digester, "client")
	scope := protocolScope(t, "vendor", "https://api.example.com", "account", "codex")
	lease := requirePrepare(t, oldFixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindResponseReference, "response-old"),
		Scope:       scopeFor(clientsOld, scope, "route-a"),
		OperationID: "old-response",
	})
	if _, err := oldFixture.service.Commit(context.Background(), lease); err != nil {
		t.Fatalf("commit old response: %v", err)
	}
	oldFixture.close()

	rotatedDocument := keyringDocument("h2", map[string]byte{"h1": 1, "h2": 2})
	rotated := newFixtureAt(t, databasePath, rotatedDocument, policyWith(20, time.Hour), false)
	defer rotated.close()
	clientsRotated := clientScopes(t, rotated.digester, "client")
	binding, err := rotated.service.Validate(context.Background(), codexcontinuity.ValidateRequest{
		Evidence:              evidence(codexcontinuity.KindResponseReference, "response-old"),
		ClientScopeCandidates: clientsRotated,
		ProtocolScope:         scope,
		OperationID:           "validate-legacy",
	})
	if err != nil || binding.Digest.KeyVersion() != "h1" || binding.Owner.ClientScope.KeyVersion() != "h1" {
		t.Fatalf("legacy validation = %#v, %v", binding, err)
	}
	newLease := requireClaim(t, rotated.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindWindowID, "window-new"),
		Scope:       scopeFor(clientsRotated, scope, "route-b"),
		OperationID: "new-window",
	})
	if newLease.Binding().Digest.KeyVersion() != "h2" || newLease.Binding().Owner.ClientScope.KeyVersion() != "h2" {
		t.Fatalf("current issuance used legacy key: %#v", newLease.Binding())
	}
	versions, err := rotated.service.RequiredHMACVersions(context.Background())
	if err != nil {
		t.Fatalf("required versions: %v", err)
	}
	if !equalStrings(versions, []string{"h1", "h2"}) {
		t.Fatalf("required versions = %v", versions)
	}
}

func TestUnavailableStoreAndSecretFreeObservability(t *testing.T) {
	var events []codexcontinuity.Event
	fixture := newFixtureConfigured(
		t,
		filepath.Join(t.TempDir(), "closed.db"),
		keyringDocument("h1", map[string]byte{"h1": 1}),
		policyWith(10, time.Hour),
		codexcontinuity.ObserverFunc(func(event codexcontinuity.Event) { events = append(events, event) }),
		true,
	)
	clients := clientScopes(t, fixture.digester, "raw-client-secret")
	scope := protocolScope(t, "vendor", "https://api.example.com", "account-secret", "codex")
	lease := requireClaim(t, fixture.service, codexcontinuity.ClaimRequest{
		Evidence:    evidence(codexcontinuity.KindConversationID, "opaque-secret"),
		Scope:       scopeFor(clients, scope, "route"),
		OperationID: "observable-operation",
	})
	if _, err := fixture.service.Commit(context.Background(), lease); err != nil {
		t.Fatalf("commit observable binding: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events = %v", events)
	}
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"raw-client-secret", "opaque-secret", "account-secret"} {
			if stringsContains(string(encoded), secret) {
				t.Fatalf("event leaked %q: %s", secret, encoded)
			}
		}
	}

	sqlDB, err := fixture.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.ResolveOwner(context.Background(), codexcontinuity.ResolveRequest{
		Evidence:              evidence(codexcontinuity.KindConversationID, "opaque-secret"),
		ClientScopeCandidates: clients,
		OperationID:           "store-closed",
	})
	assertKind(t, err, codexcontinuity.ErrorUnavailable)
	_, err = fixture.service.Cleanup(context.Background())
	assertKind(t, err, codexcontinuity.ErrorUnavailable)
	_, err = fixture.service.RequiredHMACVersions(context.Background())
	assertKind(t, err, codexcontinuity.ErrorUnavailable)
	fixture.close = func() {}
}

func newFixture(t *testing.T, document []byte, policy codexcontinuity.Policy) testFixture {
	t.Helper()
	return newFixtureAt(t, filepath.Join(t.TempDir(), "continuity.db"), document, policy, true)
}

func newFixtureAt(
	t *testing.T,
	databasePath string,
	document []byte,
	policy codexcontinuity.Policy,
	migrate bool,
) testFixture {
	t.Helper()
	return newFixtureConfigured(t, databasePath, document, policy, nil, migrate)
}

func newFixtureConfigured(
	t *testing.T,
	databasePath string,
	document []byte,
	policy codexcontinuity.Policy,
	observer codexcontinuity.Observer,
	migrate bool,
) testFixture {
	t.Helper()
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
	if migrate {
		if err := continuitysqlite.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := continuitysqlite.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := codexkeyring.Parse(document, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digester, err := codexidentity.NewDigester(keyring)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: testNow}
	service, err := codexcontinuity.NewService(codexcontinuity.Config{
		Store: repo, Digester: digester, Policy: policy, Clock: clock, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return testFixture{
		service:  service,
		repo:     repo,
		db:       db,
		clock:    clock,
		digester: digester,
		close: func() {
			if err := sqlDB.Close(); err != nil {
				t.Errorf("close database: %v", err)
			}
		},
	}
}

func keyringDocument(current string, hmacSeeds map[string]byte) []byte {
	keys := make(map[string]string, len(hmacSeeds))
	for version, seed := range hmacSeeds {
		material := make([]byte, 32)
		for index := range material {
			material[index] = seed + byte(index)
		}
		keys[version] = base64.RawURLEncoding.EncodeToString(material)
	}
	aeadMaterial := make([]byte, 32)
	for index := range aeadMaterial {
		aeadMaterial[index] = 200 + byte(index)
	}
	document := map[string]any{
		"schema_version": 1,
		"hmac":           map[string]any{"current": current, "keys": keys},
		"aead": map[string]any{"current": "a1", "keys": map[string]string{
			"a1": base64.RawURLEncoding.EncodeToString(aeadMaterial),
		}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return encoded
}

func policyWith(capacity int64, ttl time.Duration) codexcontinuity.Policy {
	configured := make(map[codexcontinuity.Kind]codexcontinuity.Limits)
	for _, kind := range []codexcontinuity.Kind{
		codexcontinuity.KindThreadID,
		codexcontinuity.KindSessionID,
		codexcontinuity.KindConversationID,
		codexcontinuity.KindWindowID,
		codexcontinuity.KindTurnState,
		codexcontinuity.KindTurnMetadata,
		codexcontinuity.KindResponseReference,
	} {
		configured[kind] = codexcontinuity.Limits{
			PendingTTL: ttl, CommittedTTL: ttl, TombstoneTTL: ttl, MaxBindings: capacity,
		}
	}
	policy, err := codexcontinuity.NewPolicy(configured)
	if err != nil {
		panic(err)
	}
	return policy
}

func lifecyclePolicy(capacity int64) codexcontinuity.Policy {
	configured := make(map[codexcontinuity.Kind]codexcontinuity.Limits)
	for _, kind := range []codexcontinuity.Kind{
		codexcontinuity.KindThreadID,
		codexcontinuity.KindSessionID,
		codexcontinuity.KindConversationID,
		codexcontinuity.KindWindowID,
		codexcontinuity.KindTurnState,
		codexcontinuity.KindTurnMetadata,
		codexcontinuity.KindResponseReference,
	} {
		configured[kind] = codexcontinuity.Limits{
			PendingTTL: 5 * time.Minute, CommittedTTL: 2 * time.Hour,
			TombstoneTTL: time.Hour, MaxBindings: capacity,
		}
	}
	policy, err := codexcontinuity.NewPolicy(configured)
	if err != nil {
		panic(err)
	}
	return policy
}

func clientScopes(t *testing.T, digester codexidentity.Digester, raw string) []codexidentity.ClientScope {
	t.Helper()
	result, err := digester.ClientScopeCandidates([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func protocolScope(t *testing.T, vendor, originValue, account, apiType string) codexidentity.ProtocolScope {
	t.Helper()
	origin, err := codexidentity.ParseOrigin(originValue)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := codexidentity.NewAccountCredentialSubject(account)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := codexidentity.NewUpstreamAuthority(vendor, origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := codexidentity.NewProtocolScope(authority, apiType)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func scopeFor(
	clients []codexidentity.ClientScope,
	protocol codexidentity.ProtocolScope,
	route string,
) codexcontinuity.Scope {
	return codexcontinuity.Scope{
		CurrentClientScope: clients[0], ClientScopeCandidates: clients, ProtocolScope: protocol, RouteTargetHint: route,
	}
}

func evidence(kind codexcontinuity.Kind, value string) codexcontinuity.Evidence {
	return codexcontinuity.Evidence{Kind: kind, DigestInput: []byte("codexheaders-binding/v1:" + string(kind) + ":" + value)}
}

func requireClaim(
	t *testing.T,
	service *codexcontinuity.Service,
	request codexcontinuity.ClaimRequest,
) codexcontinuity.Lease {
	t.Helper()
	lease, err := service.Claim(context.Background(), request)
	if err != nil {
		t.Fatalf("claim %s: %v", request.OperationID, err)
	}
	return lease
}

func requirePrepare(
	t *testing.T,
	service *codexcontinuity.Service,
	request codexcontinuity.ClaimRequest,
) codexcontinuity.Lease {
	t.Helper()
	lease, err := service.PrepareVisible(context.Background(), request)
	if err != nil {
		t.Fatalf("prepare %s: %v", request.OperationID, err)
	}
	return lease
}

func resolveError(
	service *codexcontinuity.Service,
	evidence codexcontinuity.Evidence,
	clients []codexidentity.ClientScope,
) error {
	_, err := service.ResolveOwner(context.Background(), codexcontinuity.ResolveRequest{
		Evidence: evidence, ClientScopeCandidates: clients, OperationID: "resolve-error",
	})
	return err
}

func assertKind(t *testing.T, err error, kind codexcontinuity.ErrorKind) {
	t.Helper()
	if !codexcontinuity.IsError(err, kind) {
		t.Fatalf("error = %v, want kind %q", err, kind)
	}
}

func equalStrings(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func stringsContains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
