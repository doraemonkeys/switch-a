package codexhttp

import (
	"crypto/tls"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustedProxySchemeResolverUsesOnlyTrustedEvidence(t *testing.T) {
	resolver := NewTrustedProxySchemeResolver([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	tests := []struct {
		name       string
		remote     string
		forwarded  string
		xForwarded string
		tls        bool
		wantHTTPS  bool
		wantError  bool
	}{
		{name: "direct TLS wins", remote: "192.0.2.1:1", forwarded: "for=1;proto=http", tls: true, wantHTTPS: true},
		{name: "untrusted headers ignored", remote: "192.0.2.1:1", forwarded: "for=1;proto=https"},
		{name: "trusted Forwarded", remote: "10.1.2.3:9", forwarded: `for=1;proto="https"`, wantHTTPS: true},
		{name: "trusted XFP", remote: "10.1.2.3:9", xForwarded: "https", wantHTTPS: true},
		{name: "matching trusted headers", remote: "10.1.2.3:9", forwarded: "proto=https", xForwarded: "HTTPS", wantHTTPS: true},
		{name: "conflicting trusted headers", remote: "10.1.2.3:9", forwarded: "proto=https", xForwarded: "http", wantError: true},
		{name: "missing trusted evidence", remote: "10.1.2.3:9", wantError: true},
		{name: "forwarded chain rejected", remote: "10.1.2.3:9", forwarded: "proto=https, proto=http", wantError: true},
		{name: "xfp chain rejected", remote: "10.1.2.3:9", xForwarded: "https,http", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "http://gateway.test/", nil)
			request.RemoteAddr = test.remote
			if test.forwarded != "" {
				request.Header.Set("Forwarded", test.forwarded)
			}
			if test.xForwarded != "" {
				request.Header.Set("X-Forwarded-Proto", test.xForwarded)
			}
			if test.tls {
				request.TLS = &tls.ConnectionState{}
			}
			scheme, err := resolver.ResolveExternalScheme(request)
			if (err != nil) != test.wantError {
				t.Fatalf("ResolveExternalScheme error = %v", err)
			}
			if err == nil && scheme.HTTPS() != test.wantHTTPS {
				t.Fatalf("HTTPS = %t, want %t", scheme.HTTPS(), test.wantHTTPS)
			}
		})
	}
	if _, err := resolver.ResolveExternalScheme(nil); err == nil {
		t.Fatal("nil request accepted")
	}
}

func TestForwardedProtoRejectsAmbiguousParameters(t *testing.T) {
	for _, value := range []string{"for=1", "proto=ftp", "proto=https;proto=https", `proto="unterminated`} {
		if _, _, err := forwardedProto([]string{value}); err == nil {
			t.Fatalf("forwardedProto(%q) unexpectedly succeeded", value)
		}
	}
}
