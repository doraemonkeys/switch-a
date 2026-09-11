package clientaccess

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestUsageIdentityFollowsOriginalKeyAcrossProtocols(t *testing.T) {
	const key = "unregistered-client-api-key"
	want := IdentifyKey([]byte(key))
	for _, test := range []struct {
		name, apiType, query string
		headers              http.Header
		attributed           bool
	}{
		{"bearer", "codex", "", http.Header{"Authorization": {"Bearer " + key}}, true},
		{"api key", "claude", "", http.Header{"X-Api-Key": {key}}, true},
		{"google header", "gemini", "", http.Header{"X-Goog-Api-Key": {key}}, true},
		{"google query", "gemini", "?key=" + key, nil, true},
		{"same duplicate locations", "gemini", "?key=" + key, http.Header{"Authorization": {"Bearer " + key}}, true},
		{"distinct credentials", "gemini", "?key=another", http.Header{"Authorization": {"Bearer " + key}}, false},
		{"absent", "codex", "", nil, false},
		{"invalid", "codex", "", http.Header{"Authorization": {"Basic other"}}, false},
		{"google location is protocol scoped", "codex", "?key=" + key, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://gateway/request"+test.query, nil)
			request.Header = test.headers
			before := request.Header.Clone()
			query := request.URL.RawQuery
			got := ObserveUsageIdentity(request, test.apiType)
			if test.attributed && got != want || !test.attributed && got != (UsageIdentity{}) {
				t.Fatalf("identity = %+v, attributed = %v", got, test.attributed)
			}
			if !reflect.DeepEqual(before, request.Header) || query != request.URL.RawQuery {
				t.Fatal("usage observation changed request credentials")
			}
		})
	}
	if got := ObserveUsageIdentity(nil, "codex"); got != (UsageIdentity{}) {
		t.Fatalf("nil request identity = %+v", got)
	}
}

func TestUsageIdentityMaskDoesNotDetermineGrouping(t *testing.T) {
	first := IdentifyKey([]byte("prefix-first-middle-same"))
	second := IdentifyKey([]byte("prefix-other-middle-same"))
	if first.MaskedKey != second.MaskedKey || first.Fingerprint == second.Fingerprint {
		t.Fatalf("identities = %+v / %+v", first, second)
	}
	if first.MaskedKey != "prefix…same" || len(first.Fingerprint) != 64 {
		t.Fatalf("identity = %+v", first)
	}
	short := IdentifyKey([]byte("short"))
	if strings.Contains(short.MaskedKey, "short") || short.Fingerprint == "" {
		t.Fatalf("short identity = %+v", short)
	}
	if got := IdentifyKey([]byte("abc")); got.Fingerprint != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("fingerprint = %q", got.Fingerprint)
	}
}
