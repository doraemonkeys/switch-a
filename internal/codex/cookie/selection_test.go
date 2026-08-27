package providercookie

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"
)

func mustScope(t *testing.T, jar, authorityValue string) CookieScope {
	t.Helper()
	scope, err := NewCookieScope(mustJarID(t, jar), mustCookieAuthority(t, authorityValue))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func mustUpsert(t *testing.T, parser Parser, target, header string, at time.Time) StoredCookie {
	t.Helper()
	mutation, err := parser.ParseSetCookie(mustURL(t, target), header, at)
	if err != nil {
		t.Fatal(err)
	}
	cookie, ok := mutation.Cookie()
	if !ok {
		t.Fatal("expected upsert")
	}
	return cookie
}

func policyWithAuthorityLimit(max int) Policy {
	policy := DefaultPolicy()
	policy.MaxCookiesPerAuthority = max
	return policy
}

func TestSelectAndRenderUsesRFCMatchingAndStableOrder(t *testing.T) {
	parser := mustParser(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	scope := mustScope(t, "jar-a", "authority-a")
	cookies := []StoredCookie{
		mustUpsert(t, parser, "https://api.example.com/", "sid=root; Domain=example.com; Path=/", now.Add(-4*time.Minute)),
		mustUpsert(t, parser, "https://api.example.com/v1", "sid=v1; Domain=example.com; Path=/v1", now.Add(-3*time.Minute)),
		mustUpsert(t, parser, "https://api.example.com/v1", "alpha=first; Domain=example.com; Path=/v1", now.Add(-2*time.Minute)),
		mustUpsert(t, parser, "https://api.example.com/v1", "host=only; Path=/v1", now.Add(-time.Minute)),
		mustUpsert(t, parser, "https://api.example.com/v1", "secure=yes; Secure; Domain=example.com; Path=/v1", now),
	}
	snapshot, err := NewSnapshot(scope, cookies)
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := NewOverlay(scope, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "https exact host", target: "https://api.example.com/v1/responses", want: "sid=v1; alpha=first; host=only; secure=yes; sid=root"},
		{name: "wss exact host", target: "wss://api.example.com/v1/responses", want: "sid=v1; alpha=first; host=only; secure=yes; sid=root"},
		{name: "http omits secure", target: "http://api.example.com/v1/responses", want: "sid=v1; alpha=first; host=only; sid=root"},
		{name: "ws omits secure", target: "ws://api.example.com/v1/responses", want: "sid=v1; alpha=first; host=only; sid=root"},
		{name: "subdomain omits host only", target: "https://child.api.example.com/v1/responses", want: "sid=v1; alpha=first; secure=yes; sid=root"},
		{name: "path boundary", target: "https://api.example.com/v10", want: "sid=root"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, err := SelectAndRender(snapshot, overlay, mustURL(t, test.target), now, testHosts, DefaultPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if header != test.want {
				t.Fatalf("header = %q, want %q", header, test.want)
			}
		})
	}
}

func TestSelectionAppliesOverlayUpsertAndTombstone(t *testing.T) {
	parser := mustParser(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	scope := mustScope(t, "jar-a", "authority-a")
	oldSID := mustUpsert(t, parser, "https://example.com/", "sid=old; Path=/", now.Add(-time.Hour))
	keep := mustUpsert(t, parser, "https://example.com/", "keep=yes; Path=/", now.Add(-30*time.Minute))
	snapshot, err := NewSnapshot(scope, []StoredCookie{oldSID, keep})
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := NewOverlay(scope, policyWithAuthorityLimit(4))
	if err != nil {
		t.Fatal(err)
	}
	newSID := mustUpsert(t, parser, "https://example.com/", "sid=new; Path=/", now)
	if err := overlay.Apply(scope, Upsert(newSID)); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Apply(scope, Tombstone(keep.Key())); err != nil {
		t.Fatal(err)
	}
	header, err := SelectAndRender(snapshot, overlay, mustURL(t, "https://example.com/"), now, testHosts, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if header != "sid=new" {
		t.Fatalf("header = %q", header)
	}
	selected, err := Select(snapshot, overlay, mustURL(t, "https://example.com/"), now, testHosts)
	if err != nil {
		t.Fatal(err)
	}
	if !selected[0].CreatedAt().Equal(oldSID.CreatedAt()) {
		t.Fatal("replacement did not retain creation time")
	}
}

func TestSelectionDropsExpiredCookiesAndRejectsHeaderOverflow(t *testing.T) {
	parser := mustParser(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	scope := mustScope(t, "jar-a", "authority-a")
	expired := mustUpsert(t, parser, "https://example.com/", "gone=yes; Max-Age=1", now.Add(-time.Minute))
	quoted := mustUpsert(t, parser, "https://example.com/", `quoted="hello world"`, now)
	snapshot, err := NewSnapshot(scope, []StoredCookie{expired, quoted})
	if err != nil {
		t.Fatal(err)
	}
	overlay, _ := NewOverlay(scope, policyWithAuthorityLimit(2))
	header, err := SelectAndRender(snapshot, overlay, mustURL(t, "https://example.com/"), now, testHosts, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if header != `quoted="hello world"` {
		t.Fatalf("header = %q", header)
	}
	policy := DefaultPolicy()
	policy.MaxOutboundHeaderBytes = 5
	_, err = Render([]StoredCookie{quoted}, policy)
	var limitErr *LimitError
	if !errors.As(err, &limitErr) || limitErr.Limit != LimitOutboundHeaderBytes {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestStableSelectionIsIndependentOfSnapshotOrder(t *testing.T) {
	parser := mustParser(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	scope := mustScope(t, "jar", "authority")
	base := []StoredCookie{
		mustUpsert(t, parser, "https://example.com/", "z=1; Path=/", now),
		mustUpsert(t, parser, "https://example.com/a", "a=2; Path=/a", now),
		mustUpsert(t, parser, "https://example.com/", "m=3; Path=/", now),
	}
	property := func(seed uint64) bool {
		cookies := append([]StoredCookie(nil), base...)
		random := rand.New(rand.NewSource(int64(seed)))
		random.Shuffle(len(cookies), func(i, j int) { cookies[i], cookies[j] = cookies[j], cookies[i] })
		snapshot, err := NewSnapshot(scope, cookies)
		if err != nil {
			return false
		}
		overlay, err := NewOverlay(scope, policyWithAuthorityLimit(3))
		if err != nil {
			return false
		}
		header, err := SelectAndRender(snapshot, overlay, mustURL(t, "https://example.com/a/x"), now, testHosts, DefaultPolicy())
		return err == nil && header == "a=2; m=3; z=1"
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

func TestDomainPathAndDefaultPathBoundaries(t *testing.T) {
	domainTests := []struct {
		host, domain string
		hostOnly     bool
		want         bool
	}{
		{"example.com", "example.com", true, true},
		{"sub.example.com", "example.com", true, false},
		{"sub.example.com", "example.com", false, true},
		{"notexample.com", "example.com", false, false},
		{"127.0.0.1", "0.0.1", false, false},
		{"2001:db8::1", "db8::1", false, false},
	}
	for _, test := range domainTests {
		if got := DomainMatches(test.host, test.domain, test.hostOnly); got != test.want {
			t.Errorf("DomainMatches(%q, %q, %v) = %v", test.host, test.domain, test.hostOnly, got)
		}
	}
	pathTests := map[string]bool{
		"/foo|/foo":     true,
		"/foo/bar|/foo": true,
		"/foobar|/foo":  false,
		"/foo|/foo/":    false,
		"|/":            true,
		"/foo|relative": false,
	}
	for input, want := range pathTests {
		parts := splitPair(input)
		if got := PathMatches(parts[0], parts[1]); got != want {
			t.Errorf("PathMatches(%q, %q) = %v", parts[0], parts[1], got)
		}
	}
	defaults := map[string]string{"": "/", "relative": "/", "/": "/", "/a": "/", "/a/": "/a", "/a/b": "/a"}
	for input, want := range defaults {
		if got := DefaultPath(input); got != want {
			t.Errorf("DefaultPath(%q) = %q", input, got)
		}
	}
}

func splitPair(value string) []string {
	for index := range value {
		if value[index] == '|' {
			return []string{value[:index], value[index+1:]}
		}
	}
	return nil
}

func TestSnapshotCopiesItsInput(t *testing.T) {
	parser := mustParser(t)
	scope := mustScope(t, "jar", "authority")
	cookie := mustUpsert(t, parser, "https://example.com/", "a=1", time.Now())
	snapshot, err := NewSnapshot(scope, []StoredCookie{cookie})
	if err != nil {
		t.Fatal(err)
	}
	copyOne := snapshot.Cookies()
	copyOne[0] = StoredCookie{}
	if reflect.DeepEqual(snapshot.Cookies()[0], StoredCookie{}) || snapshot.Scope() != scope {
		t.Fatal("snapshot exposed mutable storage")
	}
	if _, err := NewSnapshot(scope, []StoredCookie{cookie, cookie}); err == nil {
		t.Fatal("duplicate CookieKey was accepted")
	}
	if _, err := NewSnapshot(CookieScope{}, nil); err == nil {
		t.Fatal("empty scope was accepted")
	}
	if _, err := NewSnapshot(scope, []StoredCookie{{}}); err == nil {
		t.Fatal("invalid cookie was accepted")
	}
}
