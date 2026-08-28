package codexcontinuity

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestProvenanceStoreEnforcesOwnershipAndCapacity(t *testing.T) {
	store := newProvenanceStore()
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	digest := testOpaqueDigest(t, KindTurnState, 1, "h1")
	client := testClientScope(t, 2, "h1")
	scope := testScope(t, "account-a", "codex")
	command := provenanceClaimCommand(digest, client, scope, "response-a", now)
	command.Limits.MaxBindings = 1

	claimed, err := store.Claim(context.Background(), command)
	if err != nil || claimed.Decision != StoreClaimed || claimed.Binding.Lifecycle != LifecyclePending {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	owned, _ := store.Claim(context.Background(), command)
	if owned.Decision != StoreOwned {
		t.Fatalf("idempotent claim = %#v", owned)
	}

	conflicting := command
	conflicting.Owner.ProtocolScope = testScope(t, "account-b", "codex")
	conflicting.OperationID = "response-b"
	if result, _ := store.Claim(context.Background(), conflicting); result.Decision != StoreConflict {
		t.Fatalf("cross-authority claim = %#v", result)
	}
	second := command
	second.CurrentDigest = testOpaqueDigest(t, KindTurnState, 3, "h1")
	second.DigestCandidates = []codexidentity.OpaqueDigest{second.CurrentDigest}
	if result, _ := store.Claim(context.Background(), second); result.Decision != StoreCapacity {
		t.Fatalf("capacity claim = %#v", result)
	}

	lookup := provenanceLookupCommand(command, &scope)
	if result, _ := store.Lookup(context.Background(), lookup); result.Decision != StoreOwned {
		t.Fatalf("owned lookup = %#v", result)
	}
	lookup.ClientScopeCandidates = []codexidentity.ClientScope{testClientScope(t, 4, "h1")}
	if result, _ := store.Lookup(context.Background(), lookup); result.Decision != StoreConflict {
		t.Fatalf("cross-client lookup = %#v", result)
	}
	wrongScope := testScope(t, "account-b", "codex")
	lookup.ClientScopeCandidates = []codexidentity.ClientScope{client}
	lookup.ProtocolScope = &wrongScope
	if result, _ := store.Lookup(context.Background(), lookup); result.Decision != StoreConflict {
		t.Fatalf("cross-authority lookup = %#v", result)
	}

	versions, err := store.RequiredHMACVersions(context.Background())
	if err != nil || len(versions) != 1 || versions[0] != "h1" {
		t.Fatalf("required versions = %v, %v", versions, err)
	}
}

func TestProvenanceStoreCommitAndAbandonRequireExactLease(t *testing.T) {
	store := newProvenanceStore()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	digest := testOpaqueDigest(t, KindResponseReference, 5, "h1")
	client := testClientScope(t, 6, "h1")
	command := provenanceClaimCommand(digest, client, testScope(t, "account", "codex"), "response", now)
	command.Kind = KindResponseReference
	claimed, _ := store.Claim(context.Background(), command)

	unknown := StoreCommit{Binding: claimed.Binding, Now: now, Limits: command.Limits}
	unknown.Binding.Digest = testOpaqueDigest(t, KindResponseReference, 7, "h1")
	if result, _ := store.Commit(context.Background(), unknown); result.Decision != StoreUnknown {
		t.Fatalf("unknown commit = %#v", result)
	}
	mismatched := StoreCommit{Binding: claimed.Binding, Now: now, Limits: command.Limits}
	mismatched.Binding.ClaimOperationID = "other-operation"
	if result, _ := store.Commit(context.Background(), mismatched); result.Decision != StoreConflict {
		t.Fatalf("mismatched commit = %#v", result)
	}
	commit := StoreCommit{Binding: claimed.Binding, Now: now.Add(time.Second), Limits: command.Limits}
	committed, _ := store.Commit(context.Background(), commit)
	if committed.Decision != StoreCommitted || committed.Binding.Lifecycle != LifecycleCommitted || committed.Binding.CommittedAt == nil {
		t.Fatalf("commit = %#v", committed)
	}
	if result, _ := store.Commit(context.Background(), commit); result.Decision != StoreCommitted {
		t.Fatalf("idempotent commit = %#v", result)
	}
	if result, _ := store.Abandon(context.Background(), StoreAbandon{Binding: claimed.Binding, Now: now, Limits: command.Limits}); result.Decision != StoreConflict {
		t.Fatalf("committed abandon = %#v", result)
	}

	abandonStore := newProvenanceStore()
	abandonClaim, _ := abandonStore.Claim(context.Background(), command)
	mismatchedAbandon := StoreAbandon{Binding: abandonClaim.Binding, Now: now, Limits: command.Limits}
	mismatchedAbandon.Binding.Owner.ProtocolScope = testScope(t, "other", "codex")
	if result, _ := abandonStore.Abandon(context.Background(), mismatchedAbandon); result.Decision != StoreConflict {
		t.Fatalf("mismatched abandon = %#v", result)
	}
	if result, _ := abandonStore.Abandon(context.Background(), StoreAbandon{Binding: abandonClaim.Binding, Now: now, Limits: command.Limits}); result.Decision != StoreAbandoned {
		t.Fatalf("abandon = %#v", result)
	}
	if result, _ := abandonStore.Lookup(context.Background(), provenanceLookupCommand(command, nil)); result.Decision != StoreUnknown {
		t.Fatalf("lookup after abandon = %#v", result)
	}
}

func TestProvenanceStoreExpiresAndCleansTombstones(t *testing.T) {
	store := newProvenanceStore()
	now := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	command := provenanceClaimCommand(
		testOpaqueDigest(t, KindTurnState, 8, "h2"),
		testClientScope(t, 9, "h3"),
		testScope(t, "account", "codex"),
		"expiring-response",
		now,
	)
	if result, _ := store.Claim(context.Background(), command); result.Decision != StoreClaimed {
		t.Fatalf("claim = %#v", result)
	}
	expiry := now.Add(command.Limits.PendingTTL)
	cleanup, err := store.Cleanup(context.Background(), StoreCleanup{
		Now: expiry, Policy: map[Kind]Limits{KindTurnState: command.Limits},
	})
	if err != nil || cleanup.Expired != 1 || cleanup.Tombstoned != 1 {
		t.Fatalf("expiry cleanup = %#v, %v", cleanup, err)
	}
	lookup := provenanceLookupCommand(command, nil)
	lookup.Now = expiry
	if result, _ := store.Lookup(context.Background(), lookup); result.Decision != StoreExpired {
		t.Fatalf("tombstone lookup = %#v", result)
	}
	cleanup, _ = store.Cleanup(context.Background(), StoreCleanup{
		Now: expiry.Add(command.Limits.TombstoneTTL), Policy: map[Kind]Limits{KindTurnState: command.Limits},
	})
	if cleanup.Deleted != 1 {
		t.Fatalf("deletion cleanup = %#v", cleanup)
	}
	lookup.Now = expiry.Add(command.Limits.TombstoneTTL)
	if result, _ := store.Lookup(context.Background(), lookup); result.Decision != StoreUnknown {
		t.Fatalf("expired tombstone lookup = %#v", result)
	}

	versions, _ := store.RequiredHMACVersions(context.Background())
	if len(versions) != 0 {
		t.Fatalf("versions after cleanup = %v", versions)
	}
}

func TestProvenanceRememberDoesNotDowngradeCommittedProof(t *testing.T) {
	store := newProvenanceStore()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	command := provenanceClaimCommand(
		testOpaqueDigest(t, KindTurnState, 10, "h1"),
		testClientScope(t, 11, "h1"),
		testScope(t, "account", "codex"),
		"response",
		now,
	)
	binding := bindingFromClaim(command)
	committedAt := now
	binding.Lifecycle = LifecycleCommitted
	binding.CommittedAt = &committedAt
	binding.ExpiresAt = now.Add(command.Limits.CommittedTTL)
	if result := store.remember(command, binding); result.Decision != StoreOwned {
		t.Fatalf("remember committed = %#v", result)
	}
	if result := store.remember(command, bindingFromClaim(command)); result.Binding.Lifecycle != LifecycleCommitted {
		t.Fatalf("committed proof downgraded = %#v", result)
	}
	conflicting := command
	conflicting.Owner.ProtocolScope = testScope(t, "other", "codex")
	if result := store.remember(conflicting, bindingFromClaim(conflicting)); result.Decision != StoreConflict {
		t.Fatalf("conflicting remember = %#v", result)
	}
}

func provenanceClaimCommand(
	digest codexidentity.OpaqueDigest,
	client codexidentity.ClientScope,
	scope codexidentity.ProtocolScope,
	operationID string,
	now time.Time,
) StoreClaim {
	return StoreClaim{
		Kind: KindTurnState, CurrentDigest: digest,
		DigestCandidates:      []codexidentity.OpaqueDigest{digest},
		Owner:                 Owner{ClientScope: client, ProtocolScope: scope},
		ClientScopeCandidates: []codexidentity.ClientScope{client},
		OperationID:           operationID, Now: now, Limits: testLimits(),
	}
}

func provenanceLookupCommand(command StoreClaim, scope *codexidentity.ProtocolScope) StoreLookup {
	return StoreLookup{
		Kind: command.Kind, DigestCandidates: command.DigestCandidates,
		ClientScopeCandidates: command.ClientScopeCandidates,
		ProtocolScope:         scope, OperationID: "lookup", Now: command.Now, Limits: command.Limits,
	}
}
