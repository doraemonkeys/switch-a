package codexidentity

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestOriginFromRequestURLCanonicalMatrix(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "https default", raw: "HTTPS://EXAMPLE.COM:443/v1/responses?stream=true", want: "https://example.com"},
		{name: "wss equivalent", raw: "wss://example.com/socket", want: "https://example.com"},
		{name: "http default leading zeros", raw: "http://Example.Com:00080/a", want: "http://example.com"},
		{name: "ws equivalent", raw: "ws://example.com/a?b=c", want: "http://example.com"},
		{name: "non-default normalized", raw: "https://example.com:00444/a", want: "https://example.com:444"},
		{name: "unicode IDNA", raw: "https://BÜCHER.example./a", want: "https://xn--bcher-kva.example"},
		{name: "punycode IDNA", raw: "https://XN--BCHER-KVA.EXAMPLE", want: "https://xn--bcher-kva.example"},
		{name: "IPv4", raw: "http://192.0.2.1:80", want: "http://192.0.2.1"},
		{name: "IPv6", raw: "wss://[2001:0DB8:0:0:0:0:0:1]:443/x", want: "https://[2001:db8::1]"},
		{name: "IPv6 non-default", raw: "ws://[2001:db8::1]:8080/x", want: "http://[2001:db8::1]:8080"},
		{name: "localhost", raw: "https://LOCALHOST/", want: "https://localhost"},
		{name: "public suffix remains origin", raw: "https://GITHUB.IO/a", want: "https://github.io"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := url.Parse(test.raw)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			origin, err := OriginFromRequestURL(target)
			if err != nil {
				t.Fatalf("OriginFromRequestURL() error = %v", err)
			}
			if got := origin.String(); got != test.want {
				t.Fatalf("origin = %q, want %q", got, test.want)
			}
			if origin.Scheme() == "" || origin.Host() == "" {
				t.Fatalf("origin accessors returned empty values: %#v", origin)
			}
			encoded, err := origin.MarshalBinary()
			if err != nil || !strings.Contains(string(encoded), originCodec) {
				t.Fatalf("MarshalBinary() = %x, %v", encoded, err)
			}
			text, err := origin.MarshalText()
			if err != nil || string(text) != test.want {
				t.Fatalf("MarshalText() = %q, %v", text, err)
			}
			jsonValue, err := json.Marshal(origin)
			if err != nil || string(jsonValue) != `"`+test.want+`"` {
				t.Fatalf("json.Marshal() = %s, %v", jsonValue, err)
			}
		})
	}
}

func TestOriginEquivalenceAndPortAccessors(t *testing.T) {
	https := mustRequestOrigin(t, "https://example.com:443/path")
	wss := mustRequestOrigin(t, "wss://EXAMPLE.COM/socket")
	if !https.Equal(wss) || https != wss {
		t.Fatalf("https = %#v, wss = %#v", https, wss)
	}
	if port, ok := https.Port(); ok || port != 0 {
		t.Fatalf("default Port() = (%d, %t)", port, ok)
	}
	nonDefault := mustRequestOrigin(t, "https://example.com:8443")
	if port, ok := nonDefault.Port(); !ok || port != 8443 {
		t.Fatalf("non-default Port() = (%d, %t)", port, ok)
	}
	if mustRequestOrigin(t, "http://example.com") != mustRequestOrigin(t, "ws://EXAMPLE.COM:80/x") {
		t.Fatal("http and ws origins differ")
	}
}

func TestParseOriginRejectsRequestTargetBytes(t *testing.T) {
	for _, accepted := range []string{
		"https://example.com",
		"https://example.com/",
		"wss://BÜCHER.example.:443/",
	} {
		origin, err := ParseOrigin(accepted)
		if err != nil || origin.String() == "" {
			t.Fatalf("ParseOrigin(%q) = %#v, %v", accepted, origin, err)
		}
	}
	for _, rejected := range []string{
		"https://example.com/v1",
		"https://example.com/?q=1",
		"https://example.com?",
		"https://example.com/#fragment",
	} {
		if _, err := ParseOrigin(rejected); !IsError(err, ErrorInvalidOrigin) {
			t.Fatalf("ParseOrigin(%q) error = %v", rejected, err)
		}
	}
	// Runtime extraction deliberately accepts path/query because the caller must
	// pass the final physical request URL, not an origin-only configuration.
	if origin := mustRequestOrigin(t, "https://example.com/v1?q=1"); origin.String() != "https://example.com" {
		t.Fatalf("runtime origin = %q", origin)
	}
}

func TestOriginRejectsUnsafeAndMalformedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  *url.URL
	}{
		{name: "nil", url: nil},
		{name: "unsupported scheme", url: &url.URL{Scheme: "ftp", Host: "example.com"}},
		{name: "empty host", url: &url.URL{Scheme: "https"}},
		{name: "userinfo", url: &url.URL{Scheme: "https", Host: "example.com", User: url.User("user")}},
		{name: "userinfo embedded", url: &url.URL{Scheme: "https", Host: "user@example.com"}},
		{name: "fragment", url: &url.URL{Scheme: "https", Host: "example.com", Fragment: "secret"}},
		{name: "opaque", url: &url.URL{Scheme: "https", Opaque: "example.com"}},
		{name: "empty port", url: &url.URL{Scheme: "https", Host: "example.com:"}},
		{name: "zero port", url: &url.URL{Scheme: "https", Host: "example.com:0"}},
		{name: "large port", url: &url.URL{Scheme: "https", Host: "example.com:65536"}},
		{name: "non-decimal port", url: &url.URL{Scheme: "https", Host: "example.com:abc"}},
		{name: "negative port", url: &url.URL{Scheme: "https", Host: "example.com:-1"}},
		{name: "unbracketed IPv6", url: &url.URL{Scheme: "https", Host: "2001:db8::1"}},
		{name: "missing IPv6 bracket", url: &url.URL{Scheme: "https", Host: "[2001:db8::1"}},
		{name: "bracketed DNS", url: &url.URL{Scheme: "https", Host: "[example.com]"}},
		{name: "IPv6 zone", url: &url.URL{Scheme: "https", Host: "[fe80::1%25eth0]"}},
		{name: "empty DNS label", url: &url.URL{Scheme: "https", Host: "example..com"}},
		{name: "two root dots", url: &url.URL{Scheme: "https", Host: "example.com.."}},
		{name: "leading hyphen", url: &url.URL{Scheme: "https", Host: "-example.com"}},
		{name: "invalid IDNA", url: &url.URL{Scheme: "https", Host: "\u200d.example"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := OriginFromRequestURL(test.url); !IsError(err, ErrorInvalidOrigin) {
				t.Fatalf("OriginFromRequestURL() error = %v", err)
			}
		})
	}
	if _, err := (NormalizedOrigin{}).MarshalText(); !IsError(err, ErrorInvalidOrigin) {
		t.Fatalf("zero MarshalText() error = %v", err)
	}
	if _, err := (NormalizedOrigin{}).MarshalBinary(); !IsError(err, ErrorInvalidOrigin) {
		t.Fatalf("zero MarshalBinary() error = %v", err)
	}
	if _, err := json.Marshal(NormalizedOrigin{}); !IsError(err, ErrorInvalidOrigin) {
		t.Fatalf("zero MarshalJSON() error = %v", err)
	}
}

func TestCookieHostAndPublicSuffixSemantics(t *testing.T) {
	tests := map[string]string{
		"BÜCHER.example.":       "xn--bcher-kva.example",
		"192.0.2.1":             "192.0.2.1",
		"2001:0DB8:0:0:0:0:0:1": "2001:db8::1",
	}
	for raw, want := range tests {
		got, err := CanonicalizeCookieHost(raw)
		if err != nil || got != want {
			t.Fatalf("CanonicalizeCookieHost(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", " example.com", "example..com", "[2001:db8::1]", "fe80::1%eth0"} {
		if _, err := CanonicalizeCookieHost(raw); err == nil {
			t.Fatalf("CanonicalizeCookieHost(%q) succeeded", raw)
		}
	}
	suffixes := PublicSuffixList{}
	if got := suffixes.PublicSuffix("tenant.github.io"); got != "github.io" {
		t.Fatalf("private PublicSuffix() = %q, want github.io", got)
	}
	a := mustRequestOrigin(t, "https://a.github.io")
	b := mustRequestOrigin(t, "https://b.github.io")
	if a.Equal(b) {
		t.Fatal("public suffix semantics collapsed distinct origins")
	}
}

func mustRequestOrigin(t *testing.T, raw string) NormalizedOrigin {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	origin, err := OriginFromRequestURL(target)
	if err != nil {
		t.Fatalf("OriginFromRequestURL(%q) error = %v", raw, err)
	}
	return origin
}
