package proxy

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	providercookie "github.com/doraemonkeys/switch-a/internal/codex/cookie"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

const proxyCodexTestKeyVersion = "proxy-test-v1"

// The proxy suite exercises protocol composition, not SQLite. Per-test in-memory
// repositories preserve ownership and cookie state without rerunning migrations
// hundreds of times under the race detector.
type proxyCodexTestHMAC struct{}

func (proxyCodexTestHMAC) Sign(
	purpose codexkeyring.HMACPurpose,
	input []byte,
) (codexkeyring.Digest, error) {
	payload := make([]byte, 0, len(purpose)+1+len(input))
	payload = append(payload, purpose...)
	payload = append(payload, 0)
	payload = append(payload, input...)
	sum := sha256.Sum256(payload)
	return codexkeyring.Digest{Version: proxyCodexTestKeyVersion, Sum: sum}, nil
}

func (h proxyCodexTestHMAC) LookupDigests(
	purpose codexkeyring.HMACPurpose,
	input []byte,
) ([]codexkeyring.Digest, error) {
	digest, err := h.Sign(purpose, input)
	if err != nil {
		return nil, err
	}
	return []codexkeyring.Digest{digest}, nil
}

type proxyCodexTestContinuityStore struct {
	mu       sync.Mutex
	bindings map[codexidentity.OpaqueDigest]codexcontinuity.Binding
}

func newProxyCodexTestContinuityStore() *proxyCodexTestContinuityStore {
	return &proxyCodexTestContinuityStore{
		bindings: make(map[codexidentity.OpaqueDigest]codexcontinuity.Binding),
	}
}

func (s *proxyCodexTestContinuityStore) Claim(
	_ context.Context,
	command codexcontinuity.StoreClaim,
) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if binding, ok := s.find(command.DigestCandidates); ok {
		return resolveProxyCodexTestBinding(
			binding,
			command.ClientScopeCandidates,
			&command.Owner.ProtocolScope,
		), nil
	}
	binding := codexcontinuity.Binding{
		Kind:             command.Kind,
		Digest:           command.CurrentDigest,
		Owner:            command.Owner,
		Lifecycle:        codexcontinuity.LifecyclePending,
		ClaimOperationID: command.OperationID,
		CreatedAt:        command.Now,
		UpdatedAt:        command.Now,
		ExpiresAt:        command.Now.Add(command.Limits.PendingTTL),
	}
	s.bindings[binding.Digest] = binding
	return codexcontinuity.StoreResult{
		Decision: codexcontinuity.StoreClaimed,
		Binding:  binding,
	}, nil
}

func (s *proxyCodexTestContinuityStore) Lookup(
	_ context.Context,
	command codexcontinuity.StoreLookup,
) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	binding, ok := s.find(command.DigestCandidates)
	if !ok {
		return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreUnknown}, nil
	}
	return resolveProxyCodexTestBinding(
		binding,
		command.ClientScopeCandidates,
		command.ProtocolScope,
	), nil
}

func (s *proxyCodexTestContinuityStore) Commit(
	_ context.Context,
	command codexcontinuity.StoreCommit,
) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	binding, ok := s.bindings[command.Binding.Digest]
	if !ok {
		return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreUnknown}, nil
	}
	if !sameProxyCodexTestClaim(binding, command.Binding) {
		return codexcontinuity.StoreResult{
			Decision: codexcontinuity.StoreConflict,
			Binding:  binding,
		}, nil
	}
	if binding.Lifecycle != codexcontinuity.LifecycleCommitted {
		committedAt := command.Now
		binding.Lifecycle = codexcontinuity.LifecycleCommitted
		binding.UpdatedAt = command.Now
		binding.CommittedAt = &committedAt
		binding.ExpiresAt = command.Now.Add(command.Limits.CommittedIdleTTL)
		s.bindings[binding.Digest] = binding
	}
	return codexcontinuity.StoreResult{
		Decision: codexcontinuity.StoreCommitted,
		Binding:  binding,
	}, nil
}

func (s *proxyCodexTestContinuityStore) Abandon(
	_ context.Context,
	command codexcontinuity.StoreAbandon,
) (codexcontinuity.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	binding, ok := s.bindings[command.Binding.Digest]
	if !ok {
		return codexcontinuity.StoreResult{Decision: codexcontinuity.StoreUnknown}, nil
	}
	if binding.Lifecycle != codexcontinuity.LifecyclePending ||
		!sameProxyCodexTestClaim(binding, command.Binding) {
		return codexcontinuity.StoreResult{
			Decision: codexcontinuity.StoreConflict,
			Binding:  binding,
		}, nil
	}
	delete(s.bindings, binding.Digest)
	return codexcontinuity.StoreResult{
		Decision: codexcontinuity.StoreAbandoned,
		Binding:  binding,
	}, nil
}

func (*proxyCodexTestContinuityStore) Cleanup(
	context.Context,
	codexcontinuity.StoreCleanup,
) (codexcontinuity.CleanupResult, error) {
	return codexcontinuity.CleanupResult{}, nil
}

func (*proxyCodexTestContinuityStore) RequiredHMACVersions(
	context.Context,
) ([]string, error) {
	return []string{proxyCodexTestKeyVersion}, nil
}

func (s *proxyCodexTestContinuityStore) find(
	digests []codexidentity.OpaqueDigest,
) (codexcontinuity.Binding, bool) {
	for _, digest := range digests {
		if binding, ok := s.bindings[digest]; ok {
			return binding, true
		}
	}
	return codexcontinuity.Binding{}, false
}

func resolveProxyCodexTestBinding(
	binding codexcontinuity.Binding,
	clientScopes []codexidentity.ClientScope,
	protocolScope *codexidentity.ProtocolScope,
) codexcontinuity.StoreResult {
	owned := false
	for _, clientScope := range clientScopes {
		owned = owned || binding.Owner.ClientScope.Equal(clientScope)
	}
	if !owned ||
		(protocolScope != nil && !binding.Owner.ProtocolScope.Equal(*protocolScope)) {
		return codexcontinuity.StoreResult{
			Decision: codexcontinuity.StoreConflict,
			Binding:  binding,
		}
	}
	return codexcontinuity.StoreResult{
		Decision: codexcontinuity.StoreOwned,
		Binding:  binding,
	}
}

func sameProxyCodexTestClaim(
	left codexcontinuity.Binding,
	right codexcontinuity.Binding,
) bool {
	return left.Kind == right.Kind &&
		left.Digest.Equal(right.Digest) &&
		left.ClaimOperationID == right.ClaimOperationID &&
		left.Owner.Equal(right.Owner)
}

type proxyCodexTestCookieRepository struct {
	mu       sync.Mutex
	bindings map[codexkeyring.Digest]providercookie.BindingRecord
	cookies  map[providercookie.CookieScope]map[providercookie.CookieKey]providercookie.StoredCookie
}

func newProxyCodexTestCookieRepository() *proxyCodexTestCookieRepository {
	return &proxyCodexTestCookieRepository{
		bindings: make(map[codexkeyring.Digest]providercookie.BindingRecord),
		cookies:  make(map[providercookie.CookieScope]map[providercookie.CookieKey]providercookie.StoredCookie),
	}
}

func (r *proxyCodexTestCookieRepository) UseBinding(
	_ context.Context,
	lookup providercookie.BindingLookup,
) (providercookie.BindingUse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, digest := range lookup.HandleDigests {
		record, exists := r.bindings[digest]
		if !exists {
			continue
		}
		owned := false
		for _, owner := range lookup.ClientScopes {
			owned = owned || owner.Equal(record.ClientScope)
		}
		if !owned {
			return providercookie.BindingUse{
				Disposition: providercookie.BindingOwnerMismatch,
				Record:      record,
			}, nil
		}
		if !record.IdleExpiresAt.After(lookup.At) ||
			!record.AbsoluteExpiresAt.After(lookup.At) {
			return providercookie.BindingUse{
				Disposition: providercookie.BindingExpired,
				Record:      record,
			}, nil
		}
		refresh := record.IdleExpiresAt.Sub(lookup.At) <= lookup.Policy.HandleRefreshWindow
		record.LastAccessAt = lookup.At
		record.IdleExpiresAt = lookup.At.Add(lookup.Policy.HandleIdleTTL)
		if record.AbsoluteExpiresAt.Before(record.IdleExpiresAt) {
			record.IdleExpiresAt = record.AbsoluteExpiresAt
		}
		r.bindings[digest] = record
		return providercookie.BindingUse{
			Disposition: providercookie.BindingValid,
			Record:      record,
			Refresh:     refresh,
		}, nil
	}
	return providercookie.BindingUse{Disposition: providercookie.BindingUnknown}, nil
}

func (r *proxyCodexTestCookieRepository) CreateBinding(
	_ context.Context,
	record providercookie.BindingRecord,
	_ providercookie.Policy,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.bindings[record.HandleDigest]; exists {
		return providercookie.ErrIdentifierClash
	}
	r.bindings[record.HandleDigest] = record
	return nil
}

func (r *proxyCodexTestCookieRepository) BindClientJar(
	_ context.Context,
	request providercookie.ClientJarBindingRequest,
) (providercookie.ClientJarBindingResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for digest, record := range r.bindings {
		for _, candidate := range request.ClientScopeCandidates {
			if !candidate.Equal(record.ClientScope) {
				continue
			}
			delete(r.bindings, digest)
			record.HandleDigest = request.ProposedBinding.HandleDigest
			record.ClientScope = request.CurrentClientScope
			record.LastAccessAt = request.At
			r.bindings[record.HandleDigest] = record
			return providercookie.ClientJarBindingResult{Record: record}, nil
		}
	}
	r.bindings[request.ProposedBinding.HandleDigest] = request.ProposedBinding
	return providercookie.ClientJarBindingResult{Record: request.ProposedBinding, Created: true}, nil
}

func (r *proxyCodexTestCookieRepository) Load(
	_ context.Context,
	scope providercookie.CookieScope,
	at time.Time,
) (providercookie.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	values := r.cookies[scope]
	cookies := make([]providercookie.StoredCookie, 0, len(values))
	for key, cookie := range values {
		if cookie.Expired(at) {
			delete(values, key)
			continue
		}
		cookies = append(cookies, cookie)
	}
	return providercookie.NewSnapshot(scope, cookies)
}

func (*proxyCodexTestCookieRepository) Touch(
	context.Context,
	providercookie.CookieScope,
	[]providercookie.CookieKey,
	time.Time,
) error {
	return nil
}

func (r *proxyCodexTestCookieRepository) Merge(
	_ context.Context,
	scope providercookie.CookieScope,
	mutations []providercookie.Mutation,
	_ time.Time,
	_ providercookie.Policy,
) (providercookie.MergeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cookies[scope] == nil {
		r.cookies[scope] = make(map[providercookie.CookieKey]providercookie.StoredCookie)
	}
	result := providercookie.MergeResult{}
	for _, mutation := range mutations {
		if cookie, ok := mutation.Cookie(); ok {
			r.cookies[scope][mutation.Key()] = cookie
			result.Upserted++
			continue
		}
		delete(r.cookies[scope], mutation.Key())
		result.Deleted++
	}
	return result, nil
}

func (*proxyCodexTestCookieRepository) Cleanup(
	context.Context,
	providercookie.CleanupRequest,
) (providercookie.CleanupResult, error) {
	return providercookie.CleanupResult{}, nil
}
