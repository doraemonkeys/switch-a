package upstreamheaders

import (
	"net/http"
	"reflect"
	"testing"
)

func TestForHTTPAttemptBuildsCleanIndependentSnapshot(t *testing.T) {
	t.Parallel()

	source := http.Header{
		"authorization":          {"Bearer client-one", "Bearer client-two"},
		"X-API-KEY":              {"client-api-key"},
		"chatgpt-account-id":     {"client-account"},
		"cOnNeCtIoN":             {"keep-alive, X-Nominated", "x-second"},
		"kEeP-aLiVe":             {"timeout=5"},
		"x-nominated":            {"private"},
		"X-Second":               {"also-private"},
		"X-Client-Request-Id":    {"request-42"},
		"X-Ordinary":             {"first", "second"},
		"Originator":             {"codex_cli_rs"},
		"Sec-WebSocket-Protocol": {"realtime"},
	}

	clean := ForHTTPAttempt(source)

	for _, removed := range []string{
		"Authorization",
		"X-Api-Key",
		"ChatGPT-Account-Id",
		"Connection",
		"Keep-Alive",
		"X-Nominated",
		"X-Second",
	} {
		if values := valuesEqualFold(clean, removed); len(values) != 0 {
			t.Errorf("%s survived with values %#v", removed, values)
		}
	}
	for name, want := range map[string][]string{
		"X-Client-Request-Id":    {"request-42"},
		"X-Ordinary":             {"first", "second"},
		"Originator":             {"codex_cli_rs"},
		"Sec-WebSocket-Protocol": {"realtime"},
	} {
		if got := clean.Values(name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s values = %#v, want %#v", name, got, want)
		}
	}

	clean.Set("X-Ordinary", "changed")
	if got := source["X-Ordinary"]; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("source header was aliased: %#v", got)
	}
}

func TestForHTTPAttemptCombinesOrdinaryHeaderCasingDeterministically(t *testing.T) {
	t.Parallel()

	source := http.Header{
		"x-trace": {"lower"},
		"X-Trace": {"canonical"},
	}

	clean := ForHTTPAttempt(source)

	if got, want := clean.Values("X-Trace"), []string{"canonical", "lower"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("X-Trace values = %#v, want %#v", got, want)
	}
}

func TestForWebSocketAttemptExcludesHandshakeHeaders(t *testing.T) {
	t.Parallel()

	source := http.Header{
		"Sec-WebSocket-Protocol":   {"realtime", "chat"},
		"sec-websocket-extensions": {"permessage-deflate"},
		"Sec-WebSocket-Version":    {"13"},
		"Origin":                   {"https://client.example"},
		"X-Client-Request-Id":      {"request-42"},
	}

	clean := ForWebSocketAttempt(source)

	for _, name := range []string{"Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions", "Sec-WebSocket-Version"} {
		if values := valuesEqualFold(clean, name); len(values) != 0 {
			t.Errorf("%s survived WebSocket policy with values %#v", name, values)
		}
	}
	if got := clean.Get("Origin"); got != "https://client.example" {
		t.Fatalf("Origin = %q, want preserved", got)
	}
	if got := clean.Get("X-Client-Request-Id"); got != "request-42" {
		t.Fatalf("X-Client-Request-Id = %q, want preserved", got)
	}
}

func TestForWebSocketTransportAttemptPreservesProviderIdentityOnly(t *testing.T) {
	t.Parallel()
	source := http.Header{
		"Authorization":          {"Bearer legacy"},
		"X-Api-Key":              {"legacy-key"},
		"Chatgpt-Account-Id":     {"legacy-account"},
		"Connection":             {"Upgrade, X-Private"},
		"X-Private":              {"transport-private"},
		"Sec-WebSocket-Protocol": {"realtime"},
		"X-Client-Request-Id":    {"request-42"},
	}

	projected := ForWebSocketTransportAttempt(source)
	for _, name := range []string{"Authorization", "X-Api-Key", "ChatGPT-Account-Id", "X-Client-Request-Id"} {
		if got := projected.Get(name); got == "" {
			t.Fatalf("%s was not preserved", name)
		}
	}
	for _, name := range []string{"Connection", "X-Private", "Sec-WebSocket-Protocol"} {
		if got := projected.Values(name); len(got) != 0 {
			t.Fatalf("%s survived transport projection: %#v", name, got)
		}
	}
}

func TestForHTTPTransportAttemptPreservesProviderIdentityButNotConnectionState(t *testing.T) {
	t.Parallel()
	projected := ForHTTPTransportAttempt(http.Header{
		"Authorization":       {"Bearer legacy"},
		"X-Api-Key":           {"legacy-key"},
		"Chatgpt-Account-Id":  {"legacy-account"},
		"Connection":          {"close, X-Private"},
		"X-Private":           {"transport-private"},
		"X-Client-Request-Id": {"request-42"},
	})
	for _, name := range []string{"Authorization", "X-Api-Key", "ChatGPT-Account-Id", "X-Client-Request-Id"} {
		if got := projected.Get(name); got == "" {
			t.Fatalf("%s was not preserved", name)
		}
	}
	for _, name := range []string{"Connection", "X-Private"} {
		if got := projected.Values(name); len(got) != 0 {
			t.Fatalf("%s survived transport projection: %#v", name, got)
		}
	}
}

func TestAttemptBuilderPreservesEmptyOrdinaryHeaderEntries(t *testing.T) {
	t.Parallel()

	clean := ForHTTPAttempt(http.Header{
		"X-Empty": nil,
		"X-Mixed": {"value"},
		"x-mixed": nil,
	})

	if values, present := clean["X-Empty"]; !present || values != nil {
		t.Fatalf("X-Empty = %#v, present=%t; want present nil entry", values, present)
	}
	if got := clean.Values("X-Mixed"); !reflect.DeepEqual(got, []string{"value"}) {
		t.Fatalf("X-Mixed values = %#v, want value preserved", got)
	}
}

func TestAttemptBuildersReturnWritableHeaderForNilSource(t *testing.T) {
	t.Parallel()

	for _, build := range []func(http.Header) http.Header{
		ForHTTPAttempt, ForHTTPTransportAttempt, ForWebSocketAttempt, ForWebSocketTransportAttempt,
	} {
		header := build(nil)
		header.Set("X-Test", "value")
		if got := header.Get("X-Test"); got != "value" {
			t.Fatalf("writable header value = %q", got)
		}
	}
}

func valuesEqualFold(header http.Header, target string) []string {
	for name, values := range header {
		if http.CanonicalHeaderKey(name) == http.CanonicalHeaderKey(target) {
			return values
		}
	}
	return nil
}
