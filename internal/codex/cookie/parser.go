package providercookie

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

const maxCookieUnixSecond int64 = 253402300799

// PublicSuffixList matches the narrow contract implemented by maintained public-suffix packages.
// Injection lets the Cookie domain reuse the exact dependency version chosen by codexidentity.
type PublicSuffixList interface {
	PublicSuffix(domain string) string
}

type PublicSuffixFunc func(domain string) string

func (f PublicSuffixFunc) PublicSuffix(domain string) string { return f(domain) }

type HostCanonicalizer interface {
	CanonicalizeCookieHost(host string) (string, error)
}

type HostCanonicalizerFunc func(host string) (string, error)

func (f HostCanonicalizerFunc) CanonicalizeCookieHost(host string) (string, error) { return f(host) }

type Parser struct {
	hosts    HostCanonicalizer
	suffixes PublicSuffixList
	policy   Policy
}

func NewParser(hosts HostCanonicalizer, suffixes PublicSuffixList, policy Policy) (Parser, error) {
	if isNilDependency(hosts) {
		return Parser{}, &ConfigurationError{Field: "host_canonicalizer", Reason: "must be provided"}
	}
	if isNilDependency(suffixes) {
		return Parser{}, &ConfigurationError{Field: "public_suffix_list", Reason: "must be provided"}
	}
	if err := policy.Validate(); err != nil {
		return Parser{}, err
	}
	return Parser{hosts: hosts, suffixes: suffixes, policy: policy}, nil
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type RejectedCookie struct {
	Index int
	Err   error
}

type ParseResult struct {
	Mutations []Mutation
	Rejected  []RejectedCookie
}

func (p Parser) ParseResponse(responseURL *url.URL, lines []string, observedAt time.Time) (ParseResult, error) {
	if len(lines) > p.policy.MaxSetCookieHeaders {
		return ParseResult{}, &LimitError{Limit: LimitSetCookieHeaders, Max: p.policy.MaxSetCookieHeaders, Actual: len(lines)}
	}
	total := 0
	for _, line := range lines {
		total += len(line)
		if total > p.policy.MaxSetCookieBytes {
			return ParseResult{}, &LimitError{Limit: LimitSetCookieBytes, Max: p.policy.MaxSetCookieBytes, Actual: total}
		}
	}
	result := ParseResult{Mutations: make([]Mutation, 0, len(lines))}
	for index, line := range lines {
		mutation, err := p.parse(responseURL, line, observedAt, index)
		if err != nil {
			if errors.Is(err, ErrLimitExceeded) || errors.Is(err, ErrInvalidRequest) {
				return ParseResult{}, err
			}
			result.Rejected = append(result.Rejected, RejectedCookie{Index: index, Err: err})
			continue
		}
		result.Mutations = append(result.Mutations, mutation)
	}
	return result, nil
}

func (p Parser) ParseSetCookie(responseURL *url.URL, line string, observedAt time.Time) (Mutation, error) {
	return p.parse(responseURL, line, observedAt, -1)
}

func (p Parser) parse(responseURL *url.URL, line string, observedAt time.Time, index int) (Mutation, error) {
	if len(line) > p.policy.MaxSetCookieLineBytes {
		return Mutation{}, &LimitError{Limit: LimitSetCookieLineBytes, Max: p.policy.MaxSetCookieLineBytes, Actual: len(line)}
	}
	host, requestPath, _, err := requestTarget(responseURL, p.hosts)
	if err != nil {
		return Mutation{}, err
	}
	parsed, err := http.ParseSetCookie(line)
	if err != nil {
		return Mutation{}, &ParseError{Index: index, Reason: "cannot parse", Cause: err}
	}
	if len(parsed.Name) > p.policy.MaxCookieNameBytes {
		return Mutation{}, &LimitError{Limit: LimitCookieNameBytes, Max: p.policy.MaxCookieNameBytes, Actual: len(parsed.Name)}
	}
	if len(parsed.Value) > p.policy.MaxCookieValueBytes {
		return Mutation{}, &LimitError{Limit: LimitCookieValueBytes, Max: p.policy.MaxCookieValueBytes, Actual: len(parsed.Value)}
	}

	domain, hostOnly, err := p.resolveDomain(host, line)
	if err != nil {
		return Mutation{}, err
	}
	if len(domain) > p.policy.MaxCookieDomainBytes {
		return Mutation{}, &LimitError{Limit: LimitCookieDomainBytes, Max: p.policy.MaxCookieDomainBytes, Actual: len(domain)}
	}
	path := parsed.Path
	if path == "" || path[0] != '/' {
		path = DefaultPath(requestPath)
	}
	if len(path) > p.policy.MaxCookiePathBytes {
		return Mutation{}, &LimitError{Limit: LimitCookiePathBytes, Max: p.policy.MaxCookiePathBytes, Actual: len(path)}
	}
	key, err := NewCookieKey(parsed.Name, domain, path)
	if err != nil {
		return Mutation{}, err
	}

	session := parsed.MaxAge == 0 && parsed.Expires.IsZero()
	expiresAt := canonicalTime(parsed.Expires)
	if parsed.MaxAge < 0 {
		return Tombstone(key), nil
	}
	if parsed.MaxAge > 0 {
		expiresAt = addMaxAge(observedAt, parsed.MaxAge)
	}
	if !expiresAt.IsZero() && !expiresAt.After(observedAt) {
		return Tombstone(key), nil
	}
	if session {
		expiresAt = addDurationClamped(observedAt, p.policy.SessionCookieTTL)
	} else {
		retentionCap := addDurationClamped(observedAt, p.policy.MaxPersistentCookieTTL)
		if expiresAt.IsZero() || retentionCap.Before(expiresAt) {
			expiresAt = retentionCap
		}
	}
	cookie, err := NewStoredCookie(key, parsed.Value, CookieOptions{
		HostOnly:  hostOnly,
		Secure:    parsed.Secure,
		HTTPOnly:  parsed.HttpOnly,
		Quoted:    parsed.Quoted,
		SameSite:  sameSiteFromHTTP(parsed.SameSite),
		Session:   session,
		ExpiresAt: expiresAt,
		CreatedAt: observedAt,
	})
	if err != nil {
		return Mutation{}, err
	}
	return Upsert(cookie), nil
}

func (p Parser) resolveDomain(host, line string) (string, bool, error) {
	rawAttribute, hasDomain, hasValue := lastAttributeValue(line, "domain")
	if !hasDomain {
		return host, true, nil
	}
	if !hasValue || strings.TrimSpace(rawAttribute) == "" {
		return "", false, &DomainError{Host: host, Reason: "domain attribute is empty or malformed"}
	}
	rawDomain := strings.TrimPrefix(strings.TrimSpace(rawAttribute), ".")
	if rawDomain == "" || strings.HasPrefix(rawDomain, ".") || strings.HasSuffix(rawDomain, ".") {
		return "", false, &DomainError{Host: host, Domain: rawDomain, Reason: "must not contain surrounding dots"}
	}
	domain, err := canonicalHost(p.hosts, rawDomain)
	if err != nil {
		return "", false, &DomainError{Host: host, Domain: rawDomain, Reason: "cannot be canonicalized"}
	}
	if len(domain) > DefaultMaxCookieDomainBytes {
		return "", false, &DomainError{Host: host, Domain: domain, Reason: "exceeds the DNS name length"}
	}

	hostIP := net.ParseIP(host)
	if hostIP != nil {
		return "", false, &DomainError{Host: host, Domain: domain, Reason: "IP hosts only accept host-only cookies"}
	}
	if !validDNSName(domain) {
		return "", false, &DomainError{Host: host, Domain: domain, Reason: "must be an ASCII DNS name"}
	}
	if !DomainMatches(host, domain, false) {
		return "", false, &DomainError{Host: host, Domain: domain, Reason: "does not domain-match the response host"}
	}
	publicSuffix := strings.ToLower(strings.TrimSuffix(p.suffixes.PublicSuffix(domain), "."))
	if publicSuffix == domain {
		return "", false, &DomainError{
			Host: host, Domain: domain, Reason: "would span a public suffix", Public: true,
		}
	}
	return domain, false, nil
}

func lastAttributeValue(line, target string) (value string, present, hasValue bool) {
	parts := strings.Split(line, ";")
	for _, attribute := range parts[1:] {
		name, candidate, found := strings.Cut(strings.TrimSpace(attribute), "=")
		if strings.EqualFold(strings.TrimSpace(name), target) {
			value, present, hasValue = candidate, true, found
		}
	}
	return value, present, hasValue
}

func requestTarget(target *url.URL, hosts HostCanonicalizer) (host, path string, secure bool, err error) {
	if target == nil {
		return "", "", false, &RequestTargetError{Reason: "URL is nil"}
	}
	if target.User != nil || target.Fragment != "" {
		return "", "", false, &RequestTargetError{Reason: "userinfo and fragment are forbidden"}
	}
	rawHost := target.Hostname()
	if rawHost == "" {
		return "", "", false, &RequestTargetError{Reason: "host is empty"}
	}
	host, err = canonicalHost(hosts, rawHost)
	if err != nil {
		return "", "", false, &RequestTargetError{Reason: "host cannot be canonicalized"}
	}
	switch strings.ToLower(target.Scheme) {
	case "https", "wss":
		secure = true
	case "http", "ws":
		secure = false
	default:
		return "", "", false, &RequestTargetError{Reason: fmt.Sprintf("scheme %q is unsupported", target.Scheme)}
	}
	path = target.Path
	if path == "" || path[0] != '/' {
		path = "/"
	}
	return host, path, secure, nil
}

func canonicalHost(hosts HostCanonicalizer, raw string) (string, error) {
	if isNilDependency(hosts) {
		return "", &ConfigurationError{Field: "host_canonicalizer", Reason: "must be provided"}
	}
	host, err := hosts.CanonicalizeCookieHost(raw)
	if err != nil {
		return "", err
	}
	if host == "" || host != strings.ToLower(host) || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", &DomainError{Domain: host, Reason: "canonicalizer returned a non-canonical host"}
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.String() != host {
			return "", &DomainError{Domain: host, Reason: "canonicalizer returned a non-canonical IP"}
		}
		return host, nil
	}
	if !validDNSName(host) {
		return "", &DomainError{Domain: host, Reason: "canonicalizer returned an invalid ASCII DNS name"}
	}
	return host, nil
}

func validDNSName(domain string) bool {
	if domain == "" || len(domain) > DefaultMaxCookieDomainBytes {
		return false
	}
	for _, char := range domain {
		if char > 127 {
			return false
		}
	}
	for label := range strings.SplitSeq(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			char := label[index]
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func sameSiteFromHTTP(value http.SameSite) SameSite {
	switch value {
	case http.SameSiteLaxMode:
		return SameSiteLax
	case http.SameSiteStrictMode:
		return SameSiteStrict
	case http.SameSiteNoneMode:
		return SameSiteNone
	default:
		return SameSiteDefault
	}
}

func addMaxAge(observedAt time.Time, seconds int) time.Time {
	base := observedAt.UTC()
	seconds64 := int64(seconds)
	if base.Unix() >= maxCookieUnixSecond ||
		seconds64 >= maxCookieUnixSecond-base.Unix() ||
		seconds64 > math.MaxInt64/int64(time.Second) {
		return time.Unix(maxCookieUnixSecond, 0).UTC()
	}
	return base.Add(time.Duration(seconds64) * time.Second)
}

func addDurationClamped(observedAt time.Time, duration time.Duration) time.Time {
	base := observedAt.UTC()
	maximum := time.Unix(maxCookieUnixSecond, 0).UTC()
	if duration > 0 && base.After(maximum.Add(-duration)) {
		return maximum
	}
	return base.Add(duration)
}
