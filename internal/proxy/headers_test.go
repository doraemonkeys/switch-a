package proxy

import (
	"net/http"
	"testing"
)

func TestEnsureExplicitUserAgentHeader(t *testing.T) {
	t.Run("suppresses default user agent when absent", func(t *testing.T) {
		headers := make(http.Header)
		EnsureExplicitUserAgentHeader(headers)

		values := headers.Values(headerUserAgent)
		if len(values) != 1 || values[0] != "" {
			t.Fatalf("User-Agent values = %#v, want explicit empty value", values)
		}
	})

	t.Run("preserves explicit user agent", func(t *testing.T) {
		headers := make(http.Header)
		headers.Set(headerUserAgent, "switch-a-test/1.0")
		EnsureExplicitUserAgentHeader(headers)

		if got := headers.Get(headerUserAgent); got != "switch-a-test/1.0" {
			t.Fatalf("User-Agent = %q, want %q", got, "switch-a-test/1.0")
		}
	})
}
