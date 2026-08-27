package providercookie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

func TestOpaqueIdentifiersAndAssociatedDataStayCanonicalAndRedacted(t *testing.T) {
	if _, err := JarIDFromBytes(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("short JarID = %v", err)
	}
	if _, err := JarIDFromBytes(make([]byte, JarIDEntropyBytes)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero JarID = %v", err)
	}
	jar := mustJarID(t, "opaque")
	copyBytes := jar.Bytes()
	copyBytes[0] ^= 0xff
	if jar.Bytes()[0] == copyBytes[0] {
		t.Fatal("JarID.Bytes exposed mutable storage")
	}
	if jar.String() != "provider-cookie-jar(redacted)" || jar.GoString() != jar.String() {
		t.Fatalf("JarID formatting leaked: %s / %#v", jar.String(), jar)
	}
	encodedJar, err := json.Marshal(jar)
	if err != nil || string(encodedJar) != `"redacted"` {
		t.Fatalf("JarID JSON = %s, %v", encodedJar, err)
	}

	authority := mustCookieAuthority(t, "aad")
	scope, err := NewCookieScope(jar, authority)
	if err != nil {
		t.Fatal(err)
	}
	if scope.JarID() != jar || scope.Authority() != authority {
		t.Fatal("scope accessors changed identity")
	}
	if !strings.Contains(scope.String(), "jar=redacted") || scope.GoString() != scope.String() {
		t.Fatalf("scope formatting = %s", scope)
	}
	encodedScope, err := json.Marshal(scope)
	if err != nil || !bytes.Contains(encodedScope, []byte(`"jar":"redacted"`)) {
		t.Fatalf("scope JSON = %s, %v", encodedScope, err)
	}
	key, _ := NewCookieKey("sid", "example.com", "/one")
	aad, err := EncodeValueAssociatedData(scope, key)
	if err != nil || !bytes.Equal(aad, mustAAD(t, scope, key)) {
		t.Fatalf("AAD = %x, %v", aad, err)
	}
	otherKey, _ := NewCookieKey("sid", "example.com", "/two")
	otherAAD, _ := EncodeValueAssociatedData(scope, otherKey)
	if bytes.Equal(aad, otherAAD) {
		t.Fatal("CookieKey path was omitted from AAD")
	}
	if _, err := EncodeValueAssociatedData(CookieScope{}, key); err == nil {
		t.Fatal("uninitialized scope accepted")
	}
	if _, err := EncodeValueAssociatedData(scope, CookieKey{}); err == nil {
		t.Fatal("uninitialized key accepted")
	}
	if _, err := NewCookieScope(JarID{}, authority); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero scope JarID = %v", err)
	}
	if _, err := NewCookieScope(jar, codexidentity.CookieAuthority{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero authority = %v", err)
	}
}

func mustAAD(t *testing.T, scope CookieScope, key CookieKey) []byte {
	t.Helper()
	value, err := cookieAssociatedContext(scope, key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestServiceCoversRefreshCleanupCollisionAndCryptoFailureBranches(t *testing.T) {
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	clock := &serviceClock{now: now}
	repository := newMemoryRepository()
	trace := &traceRecorder{}
	service := newTestService(t, repository, clock, deterministicRandom(), trace)
	operation, _ := NewOperationID("coverage-service")
	owner := testClientScope(t, "coverage-owner")

	repository.createErr = fmt.Errorf("wrapped collision: %w", ErrIdentifierClash)
	access, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner})
	if err != nil || !access.Issued() {
		t.Fatalf("collision retry = %#v, %v", access, err)
	}
	if access.String() != access.GoString() || !strings.Contains(access.String(), "redacted") {
		t.Fatalf("JarAccess formatting leaked: %#v", access)
	}

	digest, _ := testDigester{version: "h1"}.Sign(codexkeyring.HMACJarHandle, []byte(access.HandleValue()))
	record := repository.bindings[digest]
	record.IdleExpiresAt = now.Add(time.Hour)
	repository.bindings[digest] = record
	reused, err := service.ResolveJar(context.Background(), operation, access.HandleValue(), []codexidentity.ClientScope{owner})
	if err != nil || !reused.Refresh() || reused.Issued() {
		t.Fatalf("refresh resolve = %#v, %v", reused, err)
	}

	result, err := service.Cleanup(context.Background(), operation, []codexidentity.CookieAuthority{mustCookieAuthority(t, "reachable")})
	if err != nil || result.ExpiredBindings != 1 || result.ExpiredCookies != 2 {
		t.Fatalf("cleanup = %#v, %v", result, err)
	}
	if _, err := service.Cleanup(nil, operation, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil cleanup context = %v", err)
	}
	if _, err := service.Cleanup(context.Background(), "", nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid cleanup operation = %v", err)
	}

	collisionRepository := newMemoryRepository()
	collisionRepository.createAlways = fmt.Errorf("wrapped: %w", ErrIdentifierClash)
	collisionService := newTestService(t, collisionRepository, clock, deterministicRandom(), nil)
	if _, err := collisionService.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrStorage) {
		t.Fatalf("collision exhaustion = %v", err)
	}

	signService, err := NewService(ServiceConfig{
		Repository: collisionRepository, HandleDigester: testDigester{version: "h1", err: errors.New("sign unavailable")},
		Random: deterministicRandom(), Clock: clock, HostCanonicalizer: testHosts,
		PublicSuffixList: testSuffixes, Policy: DefaultPolicy(), Trace: TraceSinkFunc(func(TraceEvent) {}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signService.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrCrypto) {
		t.Fatalf("sign failure = %v", err)
	}
	shortRandomService := newTestService(t, newMemoryRepository(), clock, bytes.NewReader(make([]byte, GatewayHandleEntropyBytes)), discardTrace{})
	if _, err := shortRandomService.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrCrypto) {
		t.Fatalf("Jar entropy failure = %v", err)
	}
	zeroJarService := newTestService(t, newMemoryRepository(), clock, bytes.NewReader(make([]byte, GatewayHandleEntropyBytes+JarIDEntropyBytes)), nil)
	if _, err := zeroJarService.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner}); !errors.Is(err, ErrCrypto) {
		t.Fatalf("zero JarID retry = %v", err)
	}
	if _, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{owner, owner}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("duplicate owner scope = %v", err)
	}
}

func TestRequestBoundaryFailuresAndLifecycleValidation(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxSetCookieHeaders = 1
	policy.MaxCookiesPerAuthority = 1
	repository := newMemoryRepository()
	clock := &serviceClock{now: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)}
	service, err := NewService(ServiceConfig{
		Repository: repository, HandleDigester: testDigester{version: "h1"}, Random: deterministicRandom(), Clock: clock,
		HostCanonicalizer: testHosts, PublicSuffixList: testSuffixes, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, _ := NewOperationID("coverage-request")
	access, err := service.ResolveJar(context.Background(), operation, "", []codexidentity.ClientScope{testClientScope(t, "request")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginRequest("", access); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid request operation = %v", err)
	}
	if _, err := service.BeginRequest(operation, JarAccess{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty access = %v", err)
	}
	request, _ := service.BeginRequest(operation, access)
	authority := mustCookieAuthority(t, "boundary")
	if _, err := request.ApplyResponse(authority, mustURL(t, "https://example.com"), []string{"a=1", "b=2"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("response boundary = %v", err)
	}
	if _, err := request.ApplyResponse(authority, mustURL(t, "https://example.com"), []string{"a=1", "b=2"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("response boundary repeat = %v", err)
	}
	if _, err := request.ApplyResponse(authority, mustURL(t, "https://example.com"), []string{"a=1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := request.ApplyResponse(authority, mustURL(t, "https://example.com"), []string{"b=2"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("overlay boundary = %v", err)
	}
	if _, err := request.Select(nil, authority, mustURL(t, "https://example.com")); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil select context = %v", err)
	}
	if _, err := request.Commit(nil, authority); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil commit context = %v", err)
	}
	if err := request.Discard(mustCookieAuthority(t, "absent")); err != nil {
		t.Fatal(err)
	}
	request.DiscardAll()
	request.DiscardAll()
	if _, _, err := (*Request)(nil).overlay(authority); err == nil {
		t.Fatal("nil request accepted")
	}
}

func TestOperationTraceAndPersistenceErrorCategories(t *testing.T) {
	for _, value := range []string{"", " padded", "padded ", strings.Repeat("x", MaxOperationIDBytes+1), "bad\nvalue"} {
		if _, err := NewOperationID(value); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("operation %q = %v", value, err)
		}
	}
	if got, err := NewOperationID("good-operation"); err != nil || got != "good-operation" {
		t.Fatalf("valid operation = %q, %v", got, err)
	}
	called := false
	TraceSinkFunc(func(TraceEvent) { called = true }).RecordProviderCookieTrace(TraceEvent{})
	if !called {
		t.Fatal("trace adapter did not invoke function")
	}
	discardTrace{}.RecordProviderCookieTrace(TraceEvent{})

	if (*PersistenceError)(nil).Error() != "<nil>" {
		t.Fatal("nil persistence error formatting changed")
	}
	for _, test := range []struct {
		kind PersistenceErrorKind
		want error
	}{
		{PersistenceUnavailable, ErrStorage},
		{PersistenceCorrupt, ErrStorageCorrupt},
		{PersistenceCrypto, ErrCrypto},
		{PersistenceDecrypt, ErrDecrypt},
	} {
		withoutCause := &PersistenceError{Kind: test.kind, Operation: "test"}
		if !errors.Is(withoutCause, test.want) || !strings.Contains(withoutCause.Error(), string(test.kind)) {
			t.Fatalf("category %s = %v", test.kind, withoutCause)
		}
		cause := errors.New("hidden cause")
		withCause := &PersistenceError{Kind: test.kind, Operation: "test", Cause: cause}
		if !errors.Is(withCause, test.want) || !errors.Is(withCause, cause) {
			t.Fatalf("category with cause %s = %v", test.kind, withCause)
		}
	}
}
