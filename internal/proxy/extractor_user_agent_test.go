package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractUserAgent_TruncatesLongValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	longUserAgent := strings.Repeat("a", MaxUserAgentLength+32)
	req.Header.Set("User-Agent", longUserAgent)

	got := ExtractUserAgent(req)
	if len(got) != MaxUserAgentLength {
		t.Fatalf("ExtractUserAgent() length = %d, want %d", len(got), MaxUserAgentLength)
	}
	if got != longUserAgent[:MaxUserAgentLength] {
		t.Fatalf("ExtractUserAgent() = %q, want truncated prefix", got)
	}
}
