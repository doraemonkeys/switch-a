package providercookie

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func DefaultPath(requestPath string) string {
	if requestPath == "" || requestPath[0] != '/' {
		return "/"
	}
	lastSlash := strings.LastIndex(requestPath, "/")
	if lastSlash <= 0 {
		return "/"
	}
	return requestPath[:lastSlash]
}

func DomainMatches(host, cookieDomain string, hostOnly bool) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	cookieDomain = strings.TrimSuffix(strings.ToLower(cookieDomain), ".")
	if host == cookieDomain {
		return true
	}
	if hostOnly || net.ParseIP(host) != nil || net.ParseIP(cookieDomain) != nil {
		return false
	}
	return strings.HasSuffix(host, "."+cookieDomain)
}

func PathMatches(requestPath, cookiePath string) bool {
	if requestPath == "" || requestPath[0] != '/' {
		requestPath = "/"
	}
	if cookiePath == "" || cookiePath[0] != '/' {
		return false
	}
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || requestPath[len(cookiePath)] == '/'
}

func Select(snapshot Snapshot, overlay *Overlay, requestURL *url.URL, at time.Time, hosts HostCanonicalizer) ([]StoredCookie, error) {
	if _, err := snapshot.scope.MarshalBinary(); err != nil {
		return nil, &StateError{Reason: "snapshot scope is empty"}
	}
	changes, err := overlay.Changes(snapshot.scope)
	if err != nil {
		return nil, err
	}
	host, requestPath, secure, err := requestTarget(requestURL, hosts)
	if err != nil {
		return nil, err
	}
	effective := make(map[CookieKey]StoredCookie, len(snapshot.cookies)+len(changes))
	for _, cookie := range snapshot.cookies {
		if !cookie.Expired(at) {
			effective[cookie.key] = cookie
		}
	}
	for _, change := range changes {
		if change.kind == MutationTombstone || change.cookie.Expired(at) {
			delete(effective, change.key)
			continue
		}
		cookie := change.cookie
		if existing, exists := effective[change.key]; exists {
			// RFC replacement retains creation time so retries do not reorder same-path cookies.
			cookie.createdAt = existing.createdAt
		}
		effective[change.key] = cookie
	}

	selected := make([]StoredCookie, 0, len(effective))
	for _, cookie := range effective {
		if cookie.secure && !secure {
			continue
		}
		if !DomainMatches(host, cookie.key.domain, cookie.hostOnly) {
			continue
		}
		if !PathMatches(requestPath, cookie.key.path) {
			continue
		}
		selected = append(selected, cookie)
	}
	sort.Slice(selected, func(i, j int) bool {
		left, right := selected[i], selected[j]
		if len(left.key.path) != len(right.key.path) {
			return len(left.key.path) > len(right.key.path)
		}
		if !left.createdAt.Equal(right.createdAt) {
			return left.createdAt.Before(right.createdAt)
		}
		return keyLess(left.key, right.key)
	})
	return selected, nil
}

func Render(cookies []StoredCookie, policy Policy) (string, error) {
	if err := policy.Validate(); err != nil {
		return "", err
	}
	pairs := make([]string, 0, len(cookies))
	total := 0
	for _, cookie := range cookies {
		if cookie.key.name == "" {
			return "", &StateError{Reason: "cannot render an invalid cookie"}
		}
		pair := (&http.Cookie{Name: cookie.key.name, Value: cookie.value, Quoted: cookie.quoted}).String()
		if pair == "" {
			return "", &StateError{Reason: "cannot render cookie name/value"}
		}
		if len(pairs) > 0 {
			total += len("; ")
		}
		total += len(pair)
		if total > policy.MaxOutboundHeaderBytes {
			return "", &LimitError{Limit: LimitOutboundHeaderBytes, Max: policy.MaxOutboundHeaderBytes, Actual: total}
		}
		pairs = append(pairs, pair)
	}
	return strings.Join(pairs, "; "), nil
}

func SelectAndRender(snapshot Snapshot, overlay *Overlay, requestURL *url.URL, at time.Time, hosts HostCanonicalizer, policy Policy) (string, error) {
	selected, err := Select(snapshot, overlay, requestURL, at, hosts)
	if err != nil {
		return "", err
	}
	return Render(selected, policy)
}
