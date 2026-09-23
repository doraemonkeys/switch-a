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
	var missingContext context.Context
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	clock := &serviceClock{now: now}
	repository := newMemoryRepository()
	service := newTestService(t, repository, clock, nil, nil)
	owner := testClientScope(t, "coverage-owner")
	repository.createErr = fmt.Errorf("wrapped collision: %w", ErrIdentifierClash)
	request := beginTestRequest(t, service, "", owner)
	commitTestCookie(t, request)
	digest, _ := testDigester{version: "h1"}.Sign(codexkeyring.HMACJarHandle, []byte(request.handleValue))
	record := repository.bindings[digest]
	record.IdleExpiresAt = now.Add(time.Hour)
	repository.bindings[digest] = record
	reused := beginTestRequest(t, service, request.handleValue, owner)
	if !reused.publishHandle {
		t.Fatal("near-expiry handle did not refresh")
	}
	if _, err := reused.Commit(context.Background(), mustCookieAuthority(t, "seed")); err != nil {
		t.Fatal(err)
	}
	scheme, _ := NewResolvedExternalScheme("https")
	if header, err := reused.GatewaySetCookie(scheme); err != nil || header == "" {
		t.Fatalf("refresh header = %q, %v", header, err)
	}
	if _, err := reused.GatewaySetCookie(ResolvedExternalScheme{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid scheme = %v", err)
	}
	result, err := service.Cleanup(context.Background(), "cleanup", []codexidentity.CookieAuthority{mustCookieAuthority(t, "reachable")})
	if err != nil || result.ExpiredBindings != 1 || result.ExpiredCookies != 2 {
		t.Fatalf("cleanup = %#v, %v", result, err)
	}
	if _, err := service.Cleanup(missingContext, "cleanup", nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil context = %v", err)
	}
	if _, err := service.Cleanup(context.Background(), "", nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid operation = %v", err)
	}
	if _, err := service.BeginRequest(context.Background(), "request", "", []codexidentity.ClientScope{owner, owner}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("duplicate owner = %v", err)
	}
	if _, err := service.BeginRequest(context.Background(), "request", "", []codexidentity.ClientScope{{}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid owner = %v", err)
	}
}

func TestDeferredBindingFailureDoesNotPublishAHandle(t *testing.T) {
	for _, failure := range []string{"collision", "sign", "entropy", "zero jar", "capacity"} {
		t.Run(failure, func(t *testing.T) {
			repository := newMemoryRepository()
			trace := &traceRecorder{}
			service := newTestService(t, repository, &serviceClock{now: time.Now()}, nil, trace)
			request := beginTestRequest(t, service, "", testClientScope(t, "owner"))
			authority := mustCookieAuthority(t, "failure")
			if _, err := request.ApplyResponse(authority, mustURL(t, "https://example.com"), []string{"sid=value"}); err != nil {
				t.Fatal(err)
			}
			want := ErrCrypto
			switch failure {
			case "collision":
				repository.createAlways = ErrIdentifierClash
				want = ErrStorage
			case "sign":
				service.digester = testDigester{err: errors.New("sign unavailable")}
			case "entropy":
				service.random = bytes.NewReader(nil)
			case "zero jar":
				repository.createAlways = ErrIdentifierClash
				service.random = bytes.NewReader(make([]byte, GatewayHandleEntropyBytes+bindingGenerationAttempts*JarIDEntropyBytes))
			case "capacity":
				repository.createAlways = &LimitError{Limit: LimitHandleBindingsGlobal, Max: 1, Actual: 2}
				want = ErrLimitExceeded
			}
			if _, err := request.Commit(context.Background(), authority); !errors.Is(err, want) {
				t.Fatalf("commit = %v, want %v", err, want)
			}
			scheme, _ := NewResolvedExternalScheme("https")
			if header, err := request.GatewaySetCookie(scheme); err != nil || header != "" {
				t.Fatalf("failed commit published %q: %v", header, err)
			}
			if len(repository.bindings) != 0 {
				t.Fatal("failed commit leaked binding")
			}
			if failure == "capacity" {
				event := trace.events[len(trace.events)-1]
				if event.Limit != LimitHandleBindingsGlobal || event.Maximum != 1 || event.Actual != 2 {
					t.Fatalf("capacity trace = %+v", event)
				}
			}
		})
	}
}

func TestRequestBoundaryFailuresAndLifecycleValidation(t *testing.T) {
	var missingContext context.Context
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
	request, err := service.BeginRequest(context.Background(), operation, "", []codexidentity.ClientScope{testClientScope(t, "request")})
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := request.Select(missingContext, authority, mustURL(t, "https://example.com")); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil select context = %v", err)
	}
	if _, err := request.Commit(missingContext, authority); !errors.Is(err, ErrInvalidConfig) {
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
