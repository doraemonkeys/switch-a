package providercookie

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestOverlayIsScopeBoundedDeterministicAndDisposable(t *testing.T) {
	parser := mustParser(t)
	now := time.Now()
	scopeA := mustScope(t, "jar-a", "authority-a")
	scopeB := mustScope(t, "jar-a", "authority-b")
	overlay, err := NewOverlay(scopeA, policyWithAuthorityLimit(2))
	if err != nil {
		t.Fatal(err)
	}
	b := mustUpsert(t, parser, "https://example.com/", "b=1", now)
	a := mustUpsert(t, parser, "https://example.com/", "a=1", now)
	if err := overlay.Apply(scopeA, Upsert(b)); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Apply(scopeA, Upsert(a)); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Apply(scopeA, Tombstone(a.Key())); err != nil {
		t.Fatal("replacement should not consume extra capacity:", err)
	}
	changes, err := overlay.Changes(scopeA)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Key().Name() != "a" || changes[1].Key().Name() != "b" {
		t.Fatalf("changes are not stable: %#v", changes)
	}
	c := mustUpsert(t, parser, "https://example.com/", "c=1", now)
	if err := overlay.Apply(scopeA, Upsert(c)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("capacity error = %v", err)
	}
	for _, operation := range []func() error{
		func() error { return overlay.Apply(scopeB, Upsert(c)) },
		func() error { _, err := overlay.Changes(scopeB); return err },
		func() error { return overlay.Discard(scopeB) },
	} {
		if err := operation(); !errors.Is(err, ErrScopeMismatch) {
			t.Fatalf("scope switch error = %v", err)
		}
	}
	if err := overlay.Discard(scopeA); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Discard(scopeA); err != nil {
		t.Fatal("discard must be idempotent:", err)
	}
	if err := overlay.Apply(scopeA, Upsert(c)); !errors.Is(err, ErrOverlayDiscarded) {
		t.Fatalf("apply after discard error = %v", err)
	}
	if _, err := overlay.Changes(scopeA); !errors.Is(err, ErrOverlayDiscarded) {
		t.Fatalf("read after discard error = %v", err)
	}
}

func TestOverlayRejectsInvalidConstructionAndMutation(t *testing.T) {
	scope := mustScope(t, "jar", "authority")
	if _, err := NewOverlay(CookieScope{}, DefaultPolicy()); err == nil {
		t.Fatal("empty scope accepted")
	}
	if _, err := NewOverlay(scope, Policy{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero capacity error = %v", err)
	}
	overlay, _ := NewOverlay(scope, policyWithAuthorityLimit(1))
	if err := overlay.Apply(scope, Mutation{}); err == nil {
		t.Fatal("zero mutation accepted")
	}
	var nilOverlay *Overlay
	if _, err := nilOverlay.Changes(scope); err == nil {
		t.Fatal("nil overlay accepted")
	}
}

func TestGatewayHandleCookiePolicy(t *testing.T) {
	handleValue := base64.RawURLEncoding.EncodeToString(make([]byte, GatewayHandleEntropyBytes))
	tests := []struct {
		name   string
		scheme string
		secure bool
	}{
		{name: "resolved https", scheme: "https", secure: true},
		{name: "resolved http", scheme: "http"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme, err := NewResolvedExternalScheme(test.scheme)
			if err != nil {
				t.Fatal(err)
			}
			handle, err := NewGatewayHandleCookie(handleValue, scheme)
			if err != nil {
				t.Fatal(err)
			}
			headerValue, err := handle.HeaderValue()
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := http.ParseSetCookie(headerValue)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Domain != "" || parsed.Path != GatewayHandlePath || !parsed.HttpOnly || parsed.SameSite != http.SameSiteLaxMode || parsed.Secure != test.secure {
				t.Fatalf("handle attributes = %#v", parsed)
			}
			if strings.Contains(strings.ToLower(headerValue), "domain=") {
				t.Fatal("gateway handle unexpectedly has Domain")
			}
			if handle.Name() != GatewayHandleName || handle.Path() != "/" || !handle.HTTPOnly() || handle.SameSite() != SameSiteLax || handle.Secure() != test.secure {
				t.Fatal("handle accessors disagree with header")
			}
			if scheme.HTTPS() != (test.scheme == "https") {
				t.Fatal("scheme accessor disagrees")
			}
		})
	}
	if _, err := NewResolvedExternalScheme("unknown"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid scheme error = %v", err)
	}
	scheme, _ := NewResolvedExternalScheme("https")
	if _, err := NewGatewayHandleCookie("bad;value", scheme); !errors.Is(err, ErrMalformedCookie) {
		t.Fatalf("invalid handle error = %v", err)
	}
	if _, err := NewGatewayHandleCookie(handleValue, ResolvedExternalScheme{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unresolved scheme error = %v", err)
	}
	if _, err := (GatewayHandleCookie{}).HeaderValue(); err == nil {
		t.Fatal("zero gateway handle rendered without an error")
	}
}

func TestLimitsValidateAndCheckProjectedCapacity(t *testing.T) {
	policy := DefaultPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := policy.CheckCapacity(CapacityUsage{AuthorityEntries: 1, AuthoritiesPerJar: 1, JarEntries: 2, HandleBindingsGlobal: 2, GlobalEntries: 3}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		usage CapacityUsage
		limit LimitName
	}{
		{name: "authority", usage: CapacityUsage{AuthorityEntries: policy.MaxCookiesPerAuthority + 1}, limit: LimitAuthorityEntries},
		{name: "authorities per jar", usage: CapacityUsage{AuthoritiesPerJar: policy.MaxAuthoritiesPerJar + 1}, limit: LimitAuthoritiesPerJar},
		{name: "jar", usage: CapacityUsage{JarEntries: policy.MaxCookiesPerJar + 1}, limit: LimitJarEntries},
		{name: "handles", usage: CapacityUsage{HandleBindingsGlobal: policy.MaxHandleBindingsGlobal + 1}, limit: LimitHandleBindingsGlobal},
		{name: "global", usage: CapacityUsage{GlobalEntries: policy.MaxCookieEntriesGlobal + 1}, limit: LimitGlobalEntries},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var limitErr *LimitError
			if err := policy.CheckCapacity(test.usage); !errors.As(err, &limitErr) || limitErr.Limit != test.limit {
				t.Fatalf("capacity error = %v", err)
			}
		})
	}
	if err := policy.CheckCapacity(CapacityUsage{AuthorityEntries: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative capacity error = %v", err)
	}
	bad := policy
	bad.MaxCookiesPerAuthority = bad.MaxCookiesPerJar + 1
	if err := bad.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("authority hierarchy error = %v", err)
	}
	bad = policy
	bad.MaxCookiesPerJar = bad.MaxCookieEntriesGlobal + 1
	if err := bad.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("jar hierarchy error = %v", err)
	}
}

func TestValueObjectsAndStoredCookieValidation(t *testing.T) {
	authority := mustCookieAuthority(t, "opaque")
	if _, err := NewCookieScope(JarID{}, authority); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty jar error = %v", err)
	}
	if _, err := NewCookieScope(mustJarID(t, "jar"), codexidentity.CookieAuthority{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty authority scope error = %v", err)
	}
	jarID := mustJarID(t, "jar")
	scope, _ := NewCookieScope(jarID, authority)
	if scope.JarID() != jarID || scope.Authority() != authority {
		t.Fatal("scope accessors mismatch")
	}
	for _, input := range []struct{ name, domain, path string }{
		{"bad name", "example.com", "/"},
		{"a", "Example.com", "/"},
		{"a", ".example.com", "/"},
		{"a", "example.com.", "/"},
		{"a", "bad_domain", "/"},
		{"a", "example.com", "relative"},
	} {
		if _, err := NewCookieKey(input.name, input.domain, input.path); err == nil {
			t.Fatalf("invalid key accepted: %#v", input)
		}
	}
	key, err := NewCookieKey("a", "example.com", "/")
	if err != nil {
		t.Fatal(err)
	}
	if key.Name() != "a" || key.Domain() != "example.com" || key.Path() != "/" {
		t.Fatal("key accessors mismatch")
	}
	if _, err := NewStoredCookie(CookieKey{}, "a", CookieOptions{}); err == nil {
		t.Fatal("empty cookie key accepted")
	}
	if _, err := NewStoredCookie(key, "bad;value", CookieOptions{}); !errors.Is(err, ErrMalformedCookie) {
		t.Fatalf("invalid value error = %v", err)
	}
	if _, err := NewStoredCookie(key, "value", CookieOptions{SameSite: SameSiteNone + 1}); !errors.Is(err, ErrMalformedCookie) {
		t.Fatalf("invalid same-site error = %v", err)
	}
}

func TestTypedErrorsExposeCategoriesWithoutCookieValues(t *testing.T) {
	scopeA := mustScope(t, "jar-a", "authority-a")
	scopeB := mustScope(t, "jar-b", "authority-b")
	errorsToCheck := []struct {
		err      error
		category error
	}{
		{&ParseError{Index: 2, Field: "value", Reason: "invalid", Cause: fmt.Errorf("detail")}, ErrMalformedCookie},
		{&ParseError{Index: -1, Reason: "invalid"}, ErrMalformedCookie},
		{&DomainError{Host: "api.example.com", Domain: "com", Reason: "public", Public: true}, ErrPublicSuffix},
		{&LimitError{Limit: LimitOutboundHeaderBytes, Max: 1, Actual: 2}, ErrLimitExceeded},
		{&ScopeError{Expected: scopeA, Actual: scopeB}, ErrScopeMismatch},
		{&StateError{Reason: "bad"}, ErrMalformedCookie},
		{&StateError{Reason: "discarded", Cause: ErrOverlayDiscarded}, ErrOverlayDiscarded},
		{&RequestTargetError{Reason: "bad"}, ErrInvalidRequest},
		{&ConfigurationError{Field: "field", Reason: "bad"}, ErrInvalidConfig},
	}
	for _, test := range errorsToCheck {
		message := test.err.Error()
		if message == "" || strings.Contains(message, "secret-cookie-value") {
			t.Fatalf("unsafe error message %q", message)
		}
		if !errors.Is(test.err, test.category) {
			t.Fatalf("%T does not unwrap to %v", test.err, test.category)
		}
	}
}

func TestDNSNameValidationBoundaries(t *testing.T) {
	longLabel := strings.Repeat("a", 64) + ".com"
	for value, want := range map[string]bool{
		"example.com": true,
		"localhost":   true,
		"":            false,
		"a..com":      false,
		"-a.com":      false,
		"a-.com":      false,
		"a_b.com":     false,
		"é.com":       false,
		longLabel:     false,
	} {
		if got := validDNSName(value); got != want {
			t.Errorf("validDNSName(%q) = %v, want %v", value, got, want)
		}
	}
}
