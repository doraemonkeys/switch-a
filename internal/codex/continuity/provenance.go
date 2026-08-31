package codexcontinuity

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

// provenanceStore is a process-local continuity proof cache. Entries enter it
// only after the durable store proved ownership or an upstream response reserved
// a value. Client-supplied existing state can query it but can never create it.
type provenanceStore struct {
	mu      sync.Mutex
	entries map[provenanceKey]Binding
}

type provenanceKey struct {
	kind   Kind
	digest codexidentity.OpaqueDigest
}

func newProvenanceStore() *provenanceStore {
	return &provenanceStore{entries: make(map[provenanceKey]Binding)}
}

func (s *provenanceStore) Claim(_ context.Context, command StoreClaim) (StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.resolveLocked(command.Kind, command.DigestCandidates, command.Now, command.Limits)
	if result.Decision != StoreUnknown {
		if result.Decision == StoreOwned && !bindingMatchesClaimScope(result.Binding, command) {
			result.Decision = StoreConflict
		}
		return result, nil
	}
	s.purgeElapsedLocked(command.Kind, command.Now, command.Limits)
	if s.countKindLocked(command.Kind) >= command.Limits.MaxBindings {
		return StoreResult{Decision: StoreCapacity}, nil
	}
	binding := Binding{
		Kind:             command.Kind,
		Digest:           command.CurrentDigest,
		Owner:            command.Owner,
		Lifecycle:        LifecyclePending,
		ClaimOperationID: command.OperationID,
		CreatedAt:        command.Now,
		UpdatedAt:        command.Now,
		ExpiresAt:        command.Now.Add(command.Limits.PendingTTL),
	}
	s.entries[provenanceKey{kind: binding.Kind, digest: binding.Digest}] = binding
	return StoreResult{Decision: StoreClaimed, Binding: binding}, nil
}

func (s *provenanceStore) Lookup(_ context.Context, command StoreLookup) (StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.resolveLocked(command.Kind, command.DigestCandidates, command.Now, command.Limits)
	if result.Decision != StoreOwned {
		return result, nil
	}
	if !clientScopeMatches(result.Binding.Owner.ClientScope, command.ClientScopeCandidates) {
		result.Decision = StoreConflict
		return result, nil
	}
	if command.ProtocolScope != nil && !result.Binding.Owner.ProtocolScope.Equal(*command.ProtocolScope) {
		result.Decision = StoreConflict
	}
	return result, nil
}

func (s *provenanceStore) Commit(_ context.Context, command StoreCommit) (StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := provenanceKey{kind: command.Binding.Kind, digest: command.Binding.Digest}
	binding, exists := s.entries[key]
	if !exists {
		return StoreResult{Decision: StoreUnknown}, nil
	}
	binding, decision := s.reconcileLocked(key, binding, command.Now, command.Limits)
	if decision != StoreOwned {
		return StoreResult{Decision: decision, Binding: binding}, nil
	}
	if !sameProvenanceClaim(binding, command.Binding) {
		return StoreResult{Decision: StoreConflict, Binding: binding}, nil
	}
	if binding.Lifecycle == LifecycleCommitted {
		refreshed, changed := RefreshCommittedIdleDeadline(binding, command.Now, command.Limits.CommittedIdleTTL)
		if changed {
			s.entries[key] = refreshed
			binding = refreshed
		}
		return StoreResult{Decision: StoreCommitted, Binding: binding}, nil
	}
	committedAt := command.Now
	binding.Lifecycle = LifecycleCommitted
	binding.UpdatedAt = command.Now
	binding.CommittedAt = &committedAt
	binding.ExpiresAt = command.Now.Add(command.Limits.CommittedIdleTTL)
	s.entries[key] = binding
	return StoreResult{Decision: StoreCommitted, Binding: binding}, nil
}

func (s *provenanceStore) Abandon(_ context.Context, command StoreAbandon) (StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := provenanceKey{kind: command.Binding.Kind, digest: command.Binding.Digest}
	binding, exists := s.entries[key]
	if !exists {
		return StoreResult{Decision: StoreUnknown}, nil
	}
	binding, decision := s.reconcileLocked(key, binding, command.Now, command.Limits)
	if decision != StoreOwned {
		return StoreResult{Decision: decision, Binding: binding}, nil
	}
	if binding.Lifecycle != LifecyclePending || !sameProvenanceClaim(binding, command.Binding) {
		return StoreResult{Decision: StoreConflict, Binding: binding}, nil
	}
	delete(s.entries, key)
	return StoreResult{Decision: StoreAbandoned}, nil
}

func (s *provenanceStore) Cleanup(_ context.Context, command StoreCleanup) (CleanupResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result CleanupResult
	for key, binding := range s.entries {
		limits, exists := command.Policy[binding.Kind]
		if !exists {
			continue
		}
		before := binding.Lifecycle
		_, decision := s.reconcileLocked(key, binding, command.Now, limits)
		switch {
		case decision == StoreUnknown:
			result.Deleted++
		case before != LifecycleTombstone && decision == StoreExpired:
			result.Expired++
			result.Tombstoned++
		}
	}
	return result, nil
}

func (s *provenanceStore) RequiredHMACVersions(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := make(map[string]struct{})
	for _, binding := range s.entries {
		versions[binding.Digest.KeyVersion()] = struct{}{}
		versions[binding.Owner.ClientScope.KeyVersion()] = struct{}{}
	}
	result := make([]string, 0, len(versions))
	for version := range versions {
		result = append(result, version)
	}
	sort.Strings(result)
	return result, nil
}

func (s *provenanceStore) remember(command StoreClaim, binding Binding) StoreResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.resolveLocked(command.Kind, command.DigestCandidates, command.Now, command.Limits)
	if result.Decision == StoreExpired || result.Decision == StoreConflict {
		return result
	}
	if result.Decision == StoreOwned {
		if !bindingMatchesClaimScope(result.Binding, command) {
			return StoreResult{Decision: StoreConflict, Binding: result.Binding}
		}
		if result.Binding.Lifecycle == LifecycleCommitted && binding.Lifecycle != LifecycleCommitted {
			return result
		}
		delete(s.entries, provenanceKey{kind: result.Binding.Kind, digest: result.Binding.Digest})
	} else {
		s.purgeElapsedLocked(command.Kind, command.Now, command.Limits)
		if s.countKindLocked(command.Kind) >= command.Limits.MaxBindings {
			return StoreResult{Decision: StoreCapacity}
		}
	}
	s.entries[provenanceKey{kind: binding.Kind, digest: binding.Digest}] = binding
	return StoreResult{Decision: StoreOwned, Binding: binding}
}

func (s *provenanceStore) resolveLocked(
	kind Kind,
	digests []codexidentity.OpaqueDigest,
	now time.Time,
	limits Limits,
) StoreResult {
	var result StoreResult
	for _, digest := range digests {
		key := provenanceKey{kind: kind, digest: digest}
		binding, exists := s.entries[key]
		if !exists {
			continue
		}
		binding, decision := s.reconcileLocked(key, binding, now, limits)
		if decision == StoreUnknown {
			continue
		}
		if result.Decision != "" {
			return StoreResult{Decision: StoreConflict, Binding: result.Binding}
		}
		result = StoreResult{Decision: decision, Binding: binding}
	}
	if result.Decision == "" {
		result.Decision = StoreUnknown
	}
	return result
}

func (s *provenanceStore) reconcileLocked(
	key provenanceKey,
	binding Binding,
	now time.Time,
	limits Limits,
) (Binding, StoreDecision) {
	if binding.Lifecycle == LifecycleTombstone {
		if binding.TombstoneUntil == nil || !now.Before(*binding.TombstoneUntil) {
			delete(s.entries, key)
			return Binding{}, StoreUnknown
		}
		return binding, StoreExpired
	}
	if now.Before(binding.ExpiresAt) {
		return binding, StoreOwned
	}
	tombstoneUntil := binding.ExpiresAt.Add(limits.TombstoneTTL)
	if !now.Before(tombstoneUntil) {
		delete(s.entries, key)
		return Binding{}, StoreUnknown
	}
	binding.Lifecycle = LifecycleTombstone
	binding.UpdatedAt = now
	binding.TombstoneUntil = &tombstoneUntil
	s.entries[key] = binding
	return binding, StoreExpired
}

func (s *provenanceStore) purgeElapsedLocked(kind Kind, now time.Time, limits Limits) {
	for key, binding := range s.entries {
		if key.kind == kind {
			_, _ = s.reconcileLocked(key, binding, now, limits)
		}
	}
}

func (s *provenanceStore) countKindLocked(kind Kind) int64 {
	var count int64
	for key := range s.entries {
		if key.kind == kind {
			count++
		}
	}
	return count
}

func bindingMatchesClaimScope(binding Binding, command StoreClaim) bool {
	return clientScopeMatches(binding.Owner.ClientScope, command.ClientScopeCandidates) &&
		binding.Owner.ProtocolScope.Equal(command.Owner.ProtocolScope)
}

func clientScopeMatches(owner codexidentity.ClientScope, candidates []codexidentity.ClientScope) bool {
	for _, candidate := range candidates {
		if owner.Equal(candidate) {
			return true
		}
	}
	return false
}

func sameProvenanceClaim(current, requested Binding) bool {
	return current.Kind == requested.Kind && current.Digest.Equal(requested.Digest) &&
		current.Owner.Equal(requested.Owner) && current.ClaimOperationID == requested.ClaimOperationID
}
