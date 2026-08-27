package providercookie

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testSuffixes = PublicSuffixFunc(func(domain string) string {
	if domain == "github.io" || strings.HasSuffix(domain, ".github.io") {
		return "github.io"
	}
	if domain == "co.uk" || strings.HasSuffix(domain, ".co.uk") {
		return "co.uk"
	}
	if index := strings.LastIndexByte(domain, '.'); index >= 0 {
		return domain[index+1:]
	}
	return domain
})

var testHosts = HostCanonicalizerFunc(func(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	host = strings.ReplaceAll(host, "éxample.com", "xn--xample-9ua.com")
	if strings.Contains(host, "%") {
		return "", fmt.Errorf("zone identifiers are forbidden")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	if !validDNSName(host) {
		return "", fmt.Errorf("invalid host")
	}
	return host, nil
})

func mustParser(t *testing.T) Parser {
	t.Helper()
	parser, err := NewParser(testHosts, testSuffixes, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return parser
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestParserDomainAndPathPolicy(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		requestURL string
		header     string
		domain     string
		path       string
		hostOnly   bool
		wantErr    error
	}{
		{name: "host only canonicalizes case", requestURL: "https://API.Example.COM/v1/responses", header: "sid=one", domain: "api.example.com", path: "/v1", hostOnly: true},
		{name: "host only canonicalizes idna", requestURL: "https://éxample.com/v1/responses", header: "sid=one", domain: "xn--xample-9ua.com", path: "/v1", hostOnly: true},
		{name: "domain canonicalizes idna", requestURL: "https://sub.éxample.com/v1", header: "sid=one; Domain=éxample.com", domain: "xn--xample-9ua.com", path: "/"},
		{name: "host root dot is canonicalized", requestURL: "https://example.com./v1", header: "sid=one", domain: "example.com", path: "/", hostOnly: true},
		{name: "leading dot domain", requestURL: "https://api.example.com/v1/responses", header: "sid=one; Domain=.Example.COM; Path=/v1", domain: "example.com", path: "/v1"},
		{name: "invalid path uses default", requestURL: "https://api.example.com/v1/responses", header: "sid=one; Path=relative", domain: "api.example.com", path: "/v1", hostOnly: true},
		{name: "root default path", requestURL: "https://api.example.com/single", header: "sid=one", domain: "api.example.com", path: "/", hostOnly: true},
		{name: "cross domain rejected", requestURL: "https://api.example.com/v1", header: "sid=one; Domain=evil.example", wantErr: ErrInvalidDomain},
		{name: "public suffix rejected", requestURL: "https://api.example.com/v1", header: "sid=one; Domain=com", wantErr: ErrPublicSuffix},
		{name: "exact public suffix rejected", requestURL: "https://com/v1", header: "sid=one; Domain=com", wantErr: ErrPublicSuffix},
		{name: "ipv4 host only", requestURL: "http://127.0.0.1/v1/a", header: "sid=one", domain: "127.0.0.1", path: "/v1", hostOnly: true},
		{name: "exact ipv4 domain rejected", requestURL: "http://127.0.0.1/v1", header: "sid=one; Domain=.127.0.0.1", wantErr: ErrInvalidDomain},
		{name: "different ipv4 rejected", requestURL: "http://127.0.0.1/v1", header: "sid=one; Domain=127.0.0.2", wantErr: ErrInvalidDomain},
		{name: "ipv6 host only", requestURL: "ws://[2001:db8::1]/v1/a", header: "sid=one", domain: "2001:db8::1", path: "/v1", hostOnly: true},
		{name: "malformed empty domain rejected", requestURL: "https://api.example.com/v1", header: "sid=one; Domain=", wantErr: ErrInvalidDomain},
		{name: "trailing dot domain rejected", requestURL: "https://api.example.com/v1", header: "sid=one; Domain=example.com.", wantErr: ErrInvalidDomain},
		{name: "multi-level public suffix rejected", requestURL: "https://a.co.uk/v1", header: "sid=one; Domain=co.uk", wantErr: ErrPublicSuffix},
		{name: "private public suffix rejected", requestURL: "https://tenant.github.io/v1", header: "sid=one; Domain=github.io", wantErr: ErrPublicSuffix},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutation, err := mustParser(t).ParseSetCookie(mustURL(t, test.requestURL), test.header, now)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cookie, ok := mutation.Cookie()
			if !ok {
				t.Fatal("expected upsert")
			}
			if cookie.Key().Domain() != test.domain || cookie.Key().Path() != test.path || cookie.HostOnly() != test.hostOnly {
				t.Fatalf("cookie = domain %q path %q hostOnly %v", cookie.Key().Domain(), cookie.Key().Path(), cookie.HostOnly())
			}
		})
	}
}

func TestParserAppliesLocalRetentionCaps(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	for _, header := range []string{
		"sid=value; Max-Age=999999999",
		"sid=value; Expires=Wed, 26 Aug 2037 12:00:00 GMT",
	} {
		mutation, err := mustParser(t).ParseSetCookie(mustURL(t, "https://example.com/"), header, now)
		if err != nil {
			t.Fatal(err)
		}
		cookie, ok := mutation.Cookie()
		if !ok || !cookie.ExpiresAt().Equal(now.Add(DefaultMaxPersistentCookieTTL)) || cookie.Session() {
			t.Fatalf("retention cap not applied: %#v", cookie)
		}
	}
}

func TestParserExpiryAndAttributes(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	future := "Wed, 26 Aug 2026 14:00:00 GMT"
	past := "Wed, 26 Aug 2026 10:00:00 GMT"
	tests := []struct {
		name      string
		header    string
		kind      MutationKind
		expiresAt time.Time
		session   bool
		secure    bool
		httpOnly  bool
		sameSite  SameSite
		quoted    bool
	}{
		{name: "session", header: "sid=value", kind: MutationUpsert, expiresAt: now.Add(DefaultSessionCookieTTL), session: true},
		{name: "expires future", header: "sid=value; Expires=" + future, kind: MutationUpsert, expiresAt: now.Add(2 * time.Hour)},
		{name: "expires past deletes", header: "sid=value; Expires=" + past, kind: MutationTombstone},
		{name: "max age zero deletes", header: "sid=value; Max-Age=0", kind: MutationTombstone},
		{name: "negative max age deletes", header: "sid=value; Max-Age=-10", kind: MutationTombstone},
		{name: "max age overrides past expires", header: "sid=value; Max-Age=60; Expires=" + past, kind: MutationUpsert, expiresAt: now.Add(time.Minute)},
		{name: "invalid max age leaves expires", header: "sid=value; Max-Age=nope; Expires=" + future, kind: MutationUpsert, expiresAt: now.Add(2 * time.Hour)},
		{name: "metadata", header: `sid="hello world"; Secure; HttpOnly; SameSite=Strict`, kind: MutationUpsert, expiresAt: now.Add(DefaultSessionCookieTTL), session: true, secure: true, httpOnly: true, sameSite: SameSiteStrict, quoted: true},
		{name: "same site lax", header: "sid=value; SameSite=Lax", kind: MutationUpsert, expiresAt: now.Add(DefaultSessionCookieTTL), session: true, sameSite: SameSiteLax},
		{name: "same site none", header: "sid=value; SameSite=None", kind: MutationUpsert, expiresAt: now.Add(DefaultSessionCookieTTL), session: true, sameSite: SameSiteNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutation, err := mustParser(t).ParseSetCookie(mustURL(t, "https://api.example.com/v1"), test.header, now)
			if err != nil {
				t.Fatal(err)
			}
			if mutation.Kind() != test.kind {
				t.Fatalf("kind = %v, want %v", mutation.Kind(), test.kind)
			}
			cookie, upsert := mutation.Cookie()
			if test.kind == MutationTombstone {
				if upsert {
					t.Fatal("tombstone exposed a cookie")
				}
				return
			}
			if !upsert || !cookie.ExpiresAt().Equal(test.expiresAt) || cookie.Session() != test.session {
				t.Fatalf("expiry = %v session=%v", cookie.ExpiresAt(), cookie.Session())
			}
			if cookie.Secure() != test.secure || cookie.HTTPOnly() != test.httpOnly || cookie.SameSite() != test.sameSite || cookie.Quoted() != test.quoted {
				t.Fatalf("metadata = secure %v httpOnly %v sameSite %v quoted %v", cookie.Secure(), cookie.HTTPOnly(), cookie.SameSite(), cookie.Quoted())
			}
			if !cookie.CreatedAt().Equal(now) || cookie.Expired(now) {
				t.Fatal("unexpected creation or expiration boundary")
			}
		})
	}
}

func TestParserRejectsMalformedAndOversizedInput(t *testing.T) {
	base := DefaultPolicy()
	now := time.Now()
	target := mustURL(t, "https://api.example.com/v1")

	tests := []struct {
		name    string
		policy  func(Policy) Policy
		lines   []string
		wantErr error
		limit   LimitName
	}{
		{name: "line", policy: func(p Policy) Policy { p.MaxSetCookieLineBytes = 4; return p }, lines: []string{"a=value"}, wantErr: ErrLimitExceeded, limit: LimitSetCookieLineBytes},
		{name: "count", policy: func(p Policy) Policy { p.MaxSetCookieHeaders = 1; return p }, lines: []string{"a=1", "b=2"}, wantErr: ErrLimitExceeded, limit: LimitSetCookieHeaders},
		{name: "total", policy: func(p Policy) Policy { p.MaxSetCookieLineBytes = 6; p.MaxSetCookieBytes = 6; return p }, lines: []string{"a=12", "b=34"}, wantErr: ErrLimitExceeded, limit: LimitSetCookieBytes},
		{name: "name", policy: func(p Policy) Policy { p.MaxCookieNameBytes = 1; return p }, lines: []string{"ab=1"}, wantErr: ErrLimitExceeded, limit: LimitCookieNameBytes},
		{name: "value", policy: func(p Policy) Policy { p.MaxCookieValueBytes = 1; return p }, lines: []string{"a=12"}, wantErr: ErrLimitExceeded, limit: LimitCookieValueBytes},
		{name: "domain", policy: func(p Policy) Policy { p.MaxCookieDomainBytes = 3; return p }, lines: []string{"a=1"}, wantErr: ErrLimitExceeded, limit: LimitCookieDomainBytes},
		{name: "path", policy: func(p Policy) Policy { p.MaxCookiePathBytes = 1; return p }, lines: []string{"a=1; Path=/aa"}, wantErr: ErrLimitExceeded, limit: LimitCookiePathBytes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser, err := NewParser(testHosts, testSuffixes, test.policy(base))
			if err != nil {
				t.Fatal(err)
			}
			_, err = parser.ParseResponse(target, test.lines, now)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if test.limit != "" {
				var limitErr *LimitError
				if !errors.As(err, &limitErr) || limitErr.Limit != test.limit {
					t.Fatalf("limit error = %#v, want %s", limitErr, test.limit)
				}
			}
		})
	}
}

func TestParserConfigurationAndRequestTargetErrors(t *testing.T) {
	if _, err := NewParser(testHosts, nil, DefaultPolicy()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil suffix list error = %v", err)
	}
	if _, err := NewParser(nil, testSuffixes, DefaultPolicy()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil canonicalizer error = %v", err)
	}
	badPolicy := DefaultPolicy()
	badPolicy.MaxOutboundHeaderBytes = 0
	if _, err := NewParser(testHosts, testSuffixes, badPolicy); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bad limits error = %v", err)
	}

	parser := mustParser(t)
	for _, target := range []*url.URL{nil, mustURL(t, "ftp://example.com/a"), mustURL(t, "https:///a"), mustURL(t, "https://user@example.com/a"), mustURL(t, "https://example.com/a#fragment"), mustURL(t, "https://[fe80::1%25eth0]/a")} {
		if _, err := parser.ParseSetCookie(target, "a=1", time.Now()); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("target %v error = %v", target, err)
		}
	}
}

func TestMaxAgeIsClampedWithoutDurationOverflow(t *testing.T) {
	got := addMaxAge(time.Unix(0, 0), int(^uint(0)>>1))
	if got.Year() != 9999 {
		t.Fatalf("clamped year = %d", got.Year())
	}
}

func TestParseResponsePreservesWireOrder(t *testing.T) {
	result, err := mustParser(t).ParseResponse(
		mustURL(t, "https://example.com/path"),
		[]string{"b=2", "a=1"},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mutations) != 2 || result.Mutations[0].Key().Name() != "b" || result.Mutations[1].Key().Name() != "a" {
		t.Fatalf("mutations = %#v", result.Mutations)
	}
}

func TestParseResponseRejectsMalformedEntriesWithoutLosingValidWireOrder(t *testing.T) {
	result, err := mustParser(t).ParseResponse(
		mustURL(t, "https://example.com/path"),
		[]string{"b=2", "not a cookie", "a=1"},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mutations) != 2 || result.Mutations[0].Key().Name() != "b" || result.Mutations[1].Key().Name() != "a" {
		t.Fatalf("mutations = %#v", result.Mutations)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Index != 1 || !errors.Is(result.Rejected[0].Err, ErrMalformedCookie) {
		t.Fatalf("rejected = %#v", result.Rejected)
	}
}
