package providercookie

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

type serviceClock struct{ now time.Time }

func (c *serviceClock) Now() time.Time { return c.now }

type testDigester struct {
	version string
	err     error
}

func (d testDigester) Sign(purpose codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	if d.err != nil {
		return codexkeyring.Digest{}, d.err
	}
	sum := sha256.Sum256(append([]byte(string(purpose)+":"+d.version+":"), input...))
	return codexkeyring.Digest{Version: d.version, Sum: sum}, nil
}

func (d testDigester) LookupDigests(purpose codexkeyring.HMACPurpose, input []byte) ([]codexkeyring.Digest, error) {
	digest, err := d.Sign(purpose, input)
	if err != nil {
		return nil, err
	}
	return []codexkeyring.Digest{digest}, nil
}

type memoryRepository struct {
	mu           sync.Mutex
	bindings     map[codexkeyring.Digest]BindingRecord
	cookies      map[CookieScope]map[CookieKey]StoredCookie
	useErr       error
	createErr    error
	loadErr      error
	touchErr     error
	mergeErr     error
	cleanupErr   error
	createAlways error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		bindings: make(map[codexkeyring.Digest]BindingRecord),
		cookies:  make(map[CookieScope]map[CookieKey]StoredCookie),
	}
}

func (r *memoryRepository) UseBinding(_ context.Context, lookup BindingLookup) (BindingUse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.useErr != nil {
		return BindingUse{}, r.useErr
	}
	for _, digest := range lookup.HandleDigests {
		record, exists := r.bindings[digest]
		if !exists {
			continue
		}
		owned := false
		for _, owner := range lookup.ClientScopes {
			owned = owned || owner == record.ClientScope
		}
		if !owned {
			return BindingUse{Disposition: BindingOwnerMismatch, Record: record}, nil
		}
		if !record.IdleExpiresAt.After(lookup.At) || !record.AbsoluteExpiresAt.After(lookup.At) {
			return BindingUse{Disposition: BindingExpired, Record: record}, nil
		}
		refresh := record.IdleExpiresAt.Sub(lookup.At) <= lookup.Policy.HandleRefreshWindow
		record.LastAccessAt = lookup.At
		record.IdleExpiresAt = lookup.At.Add(lookup.Policy.HandleIdleTTL)
		if record.AbsoluteExpiresAt.Before(record.IdleExpiresAt) {
			record.IdleExpiresAt = record.AbsoluteExpiresAt
		}
		r.bindings[digest] = record
		return BindingUse{Disposition: BindingValid, Record: record, Refresh: refresh}, nil
	}
	return BindingUse{Disposition: BindingUnknown}, nil
}

func (r *memoryRepository) CreateBinding(_ context.Context, record BindingRecord, _ Policy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createAlways != nil {
		return r.createAlways
	}
	if r.createErr != nil {
		err := r.createErr
		r.createErr = nil
		return err
	}
	if _, exists := r.bindings[record.HandleDigest]; exists {
		return ErrIdentifierClash
	}
	r.bindings[record.HandleDigest] = record
	return nil
}

func (r *memoryRepository) BindClientJar(_ context.Context, request ClientJarBindingRequest) (ClientJarBindingResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createAlways != nil {
		return ClientJarBindingResult{}, r.createAlways
	}
	if r.createErr != nil {
		err := r.createErr
		r.createErr = nil
		return ClientJarBindingResult{}, err
	}
	for digest, record := range r.bindings {
		owned := false
		for _, candidate := range request.ClientScopeCandidates {
			owned = owned || candidate.Equal(record.ClientScope)
		}
		if !owned {
			continue
		}
		if !record.IdleExpiresAt.After(request.At) || !record.AbsoluteExpiresAt.After(request.At) {
			delete(r.bindings, digest)
			continue
		}
		if _, exists := r.bindings[request.ProposedBinding.HandleDigest]; exists && digest != request.ProposedBinding.HandleDigest {
			return ClientJarBindingResult{}, ErrIdentifierClash
		}
		delete(r.bindings, digest)
		record.HandleDigest = request.ProposedBinding.HandleDigest
		record.ClientScope = request.CurrentClientScope
		record.LastAccessAt = request.At
		record.IdleExpiresAt = request.At.Add(request.Policy.HandleIdleTTL)
		if record.AbsoluteExpiresAt.Before(record.IdleExpiresAt) {
			record.IdleExpiresAt = record.AbsoluteExpiresAt
		}
		r.bindings[record.HandleDigest] = record
		return ClientJarBindingResult{Record: record}, nil
	}
	if _, exists := r.bindings[request.ProposedBinding.HandleDigest]; exists {
		return ClientJarBindingResult{}, ErrIdentifierClash
	}
	r.bindings[request.ProposedBinding.HandleDigest] = request.ProposedBinding
	return ClientJarBindingResult{Record: request.ProposedBinding, Created: true}, nil
}

func (r *memoryRepository) Load(_ context.Context, scope CookieScope, _ time.Time) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadErr != nil {
		return Snapshot{}, r.loadErr
	}
	values := r.cookies[scope]
	cookies := make([]StoredCookie, 0, len(values))
	for _, cookie := range values {
		cookies = append(cookies, cookie)
	}
	return NewSnapshot(scope, cookies)
}

func (r *memoryRepository) Touch(_ context.Context, _ CookieScope, _ []CookieKey, _ time.Time) error {
	return r.touchErr
}

func (r *memoryRepository) Merge(_ context.Context, scope CookieScope, mutations []Mutation, _ time.Time, _ Policy) (MergeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mergeErr != nil {
		return MergeResult{}, r.mergeErr
	}
	if r.cookies[scope] == nil {
		r.cookies[scope] = make(map[CookieKey]StoredCookie)
	}
	result := MergeResult{}
	for _, mutation := range mutations {
		if cookie, ok := mutation.Cookie(); ok {
			r.cookies[scope][mutation.Key()] = cookie
			result.Upserted++
		} else {
			delete(r.cookies[scope], mutation.Key())
			result.Deleted++
		}
	}
	return result, nil
}

func (r *memoryRepository) Cleanup(_ context.Context, _ CleanupRequest) (CleanupResult, error) {
	if r.cleanupErr != nil {
		return CleanupResult{}, r.cleanupErr
	}
	return CleanupResult{ExpiredBindings: 1, ExpiredCookies: 2}, nil
}

type traceRecorder struct {
	mu     sync.Mutex
	events []TraceEvent
}

func (r *traceRecorder) RecordProviderCookieTrace(event TraceEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func testClientScope(t *testing.T, label string) codexidentity.ClientScope {
	t.Helper()
	sum := sha256.Sum256([]byte(label))
	scope, err := codexidentity.ClientScopeFromDigest("h1", sum)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func newTestService(t *testing.T, repository Repository, clock Clock, random io.Reader, trace TraceSink) *Service {
	t.Helper()
	service, err := NewService(ServiceConfig{
		Repository:        repository,
		HandleDigester:    testDigester{version: "h1"},
		Random:            random,
		Clock:             clock,
		HostCanonicalizer: testHosts,
		PublicSuffixList:  testSuffixes,
		Policy:            DefaultPolicy(),
		Trace:             trace,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func deterministicRandom() io.Reader {
	data := make([]byte, 4096)
	for index := range data {
		data[index] = byte(index%251 + 1)
	}
	return bytes.NewReader(data)
}

func TestServiceUsesClientScopeAsAuthoritativeJarIdentity(t *testing.T) {
	repository := newMemoryRepository()
	clock := &serviceClock{now: time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)}
	trace := &traceRecorder{}
	service := newTestService(t, repository, clock, deterministicRandom(), trace)
	operation, _ := NewOperationID("operation-handle")
	owner := testClientScope(t, "owner-a")
	other := testClientScope(t, "owner-b")

	first, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner})
	if err != nil || !first.Issued() || first.HandleValue() == "" {
		t.Fatalf("first resolve = %#v, %v", first, err)
	}
	reused, err := service.ResolveJar(context.Background(), operation, first.HandleValue(), []codexidentity.ClientScope{owner})
	if err != nil || reused.Issued() || reused.JarID() != first.JarID() {
		t.Fatalf("reused resolve = %#v, %v", reused, err)
	}
	missing, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner})
	if err != nil || !missing.Issued() || missing.JarID() != first.JarID() || missing.HandleValue() == first.HandleValue() {
		t.Fatalf("missing-handle resolve = %#v, %v", missing, err)
	}
	malformed, err := service.ResolveJar(context.Background(), operation, "not-a-handle", []codexidentity.ClientScope{owner})
	if err != nil || !malformed.Issued() || malformed.JarID() != first.JarID() {
		t.Fatalf("malformed resolve = %#v, %v", malformed, err)
	}
	unknownValue := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xfe}, GatewayHandleEntropyBytes))
	unknown, err := service.ResolveJar(context.Background(), operation, unknownValue, []codexidentity.ClientScope{owner})
	if err != nil || !unknown.Issued() || unknown.JarID() != first.JarID() {
		t.Fatalf("unknown resolve = %#v, %v", unknown, err)
	}
	if len(repository.bindings) != 1 {
		t.Fatalf("same ClientScope created %d bindings", len(repository.bindings))
	}
	mismatch, err := service.ResolveJar(context.Background(), operation, unknown.HandleValue(), []codexidentity.ClientScope{other})
	if err != nil || !mismatch.Issued() || mismatch.JarID() == first.JarID() {
		t.Fatalf("mismatch resolve = %#v, %v", mismatch, err)
	}
	clock.now = clock.now.Add(DefaultHandleAbsoluteTTL + time.Second)
	expired, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner})
	if err != nil || !expired.Issued() || expired.JarID() == first.JarID() {
		t.Fatalf("expired resolve = %#v, %v", expired, err)
	}
	if len(trace.events) != 7 {
		t.Fatalf("trace events = %d", len(trace.events))
	}
	for _, event := range trace.events {
		if strings.Contains(event.Reason, first.HandleValue()) {
			t.Fatal("trace leaked raw handle")
		}
	}
}

func TestRequestOverlayLifecycleIsScopeLocalAndCommitOnly(t *testing.T) {
	repository := newMemoryRepository()
	clock := &serviceClock{now: time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)}
	service := newTestService(t, repository, clock, deterministicRandom(), nil)
	operation, _ := NewOperationID("operation-overlay")
	access, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{testClientScope(t, "owner")})
	if err != nil {
		t.Fatal(err)
	}
	authorityA := mustCookieAuthority(t, "authority-a")
	authorityB := mustCookieAuthority(t, "authority-b")
	request, err := service.BeginRequest(operation, access)
	if err != nil {
		t.Fatal(err)
	}
	if rejected, err := request.ApplyResponse(authorityA, mustURL(t, "https://api.example.com/login"), []string{"sid=overlay; Path=/"}); err != nil || len(rejected) != 0 {
		t.Fatalf("apply = %v, rejected=%v", err, rejected)
	}
	header, err := request.Select(context.Background(), authorityA, mustURL(t, "https://api.example.com/v1"))
	if err != nil || header != "sid=overlay" {
		t.Fatalf("same-scope selection = %q, %v", header, err)
	}
	header, err = request.Select(context.Background(), authorityB, mustURL(t, "https://api.example.com/v1"))
	if err != nil || header != "" {
		t.Fatalf("cross-scope selection = %q, %v", header, err)
	}
	if err := request.Discard(authorityA); err != nil {
		t.Fatal(err)
	}
	header, err = request.Select(context.Background(), authorityA, mustURL(t, "https://api.example.com/v1"))
	if err != nil || header != "" {
		t.Fatalf("discarded selection = %q, %v", header, err)
	}
	if _, err := request.ApplyResponse(authorityA, mustURL(t, "https://api.example.com/login"), []string{"sid=persisted; Path=/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Commit(context.Background(), authorityA); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Select(context.Background(), authorityA, mustURL(t, "https://api.example.com/v1")); !errors.Is(err, ErrOverlayDiscarded) {
		t.Fatalf("closed request error = %v", err)
	}

	next, _ := service.BeginRequest(operation, access)
	header, err = next.Select(context.Background(), authorityA, mustURL(t, "https://api.example.com/v1"))
	if err != nil || header != "sid=persisted" {
		t.Fatalf("persisted selection = %q, %v", header, err)
	}
	next.DiscardAll()
	if err := next.Discard(authorityA); !errors.Is(err, ErrOverlayDiscarded) {
		t.Fatalf("discard after close = %v", err)
	}
}

func TestServiceAndRequestFailuresStayExplicit(t *testing.T) {
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	operation, _ := NewOperationID("operation-failures")
	owner := testClientScope(t, "owner")
	repository := newMemoryRepository()
	service := newTestService(t, repository, &serviceClock{now: now}, deterministicRandom(), nil)

	repository.createAlways = errors.New("database down")
	if _, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrStorage) {
		t.Fatalf("create failure = %v", err)
	}
	repository.createAlways = nil
	access, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner})
	if err != nil {
		t.Fatal(err)
	}
	repository.useErr = errors.New("busy")
	if _, err := service.ResolveJar(context.Background(), operation, access.HandleValue(), []codexidentity.ClientScope{owner}); !errors.Is(err, ErrStorage) {
		t.Fatalf("lookup failure = %v", err)
	}
	repository.useErr = nil

	request, _ := service.BeginRequest(operation, access)
	authority := mustCookieAuthority(t, "failure")
	repository.loadErr = &PersistenceError{Kind: PersistenceCorrupt, Operation: "load", Cause: errors.New("corrupt")}
	if _, err := request.Select(context.Background(), authority, mustURL(t, "https://example.com")); !errors.Is(err, ErrStorageCorrupt) {
		t.Fatalf("load failure = %v", err)
	}
	repository.loadErr = nil
	repository.touchErr = errors.New("touch")
	if _, err := request.Select(context.Background(), authority, mustURL(t, "https://example.com")); !errors.Is(err, ErrStorage) {
		t.Fatalf("touch failure = %v", err)
	}
	repository.touchErr = nil
	if _, err := request.ApplyResponse(authority, mustURL(t, "https://example.com"), []string{"sid=value"}); err != nil {
		t.Fatal(err)
	}
	repository.mergeErr = errors.New("merge")
	if _, err := request.Commit(context.Background(), authority); !errors.Is(err, ErrStorage) {
		t.Fatalf("merge failure = %v", err)
	}
	repository.mergeErr = nil
	if _, err := request.Commit(context.Background(), authority); err != nil {
		t.Fatal("overlay should remain retryable after rollback:", err)
	}
	repository.cleanupErr = errors.New("cleanup")
	if _, err := service.Cleanup(context.Background(), operation, nil); !errors.Is(err, ErrStorage) {
		t.Fatalf("cleanup failure = %v", err)
	}
}

func TestServiceRejectsInvalidDependenciesAndCryptoFailures(t *testing.T) {
	policy := DefaultPolicy()
	base := ServiceConfig{Repository: newMemoryRepository(), HandleDigester: testDigester{version: "h1"}, HostCanonicalizer: testHosts, PublicSuffixList: testSuffixes, Policy: policy}
	invalid := []ServiceConfig{
		{HandleDigester: base.HandleDigester, HostCanonicalizer: testHosts, PublicSuffixList: testSuffixes, Policy: policy},
		{Repository: base.Repository, HostCanonicalizer: testHosts, PublicSuffixList: testSuffixes, Policy: policy},
	}
	for _, config := range invalid {
		if _, err := NewService(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("invalid config = %v", err)
		}
	}
	service, err := NewService(ServiceConfig{
		Repository: base.Repository, HandleDigester: testDigester{version: "h1", err: errors.New("key unavailable")},
		Random: deterministicRandom(), Clock: &serviceClock{now: time.Now()}, HostCanonicalizer: testHosts,
		PublicSuffixList: testSuffixes, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	op, _ := NewOperationID("operation-crypto")
	owner := testClientScope(t, "owner")
	validHandle := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, GatewayHandleEntropyBytes))
	if _, err := service.ResolveJar(context.Background(), op, validHandle, []codexidentity.ClientScope{owner}); !errors.Is(err, ErrCrypto) {
		t.Fatalf("lookup crypto failure = %v", err)
	}
	service = newTestService(t, base.Repository, &serviceClock{now: time.Now()}, bytes.NewReader(nil), nil)
	if _, err := service.ResolveJar(context.Background(), op, "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrCrypto) {
		t.Fatalf("random failure = %v", err)
	}
	if _, err := service.ResolveJar(nil, op, "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil context = %v", err)
	}
	if _, err := service.ResolveJar(context.Background(), "", "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty operation = %v", err)
	}
	if _, err := service.ResolveJar(context.Background(), op, "", nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty scopes = %v", err)
	}
}
