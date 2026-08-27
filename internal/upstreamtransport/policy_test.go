package upstreamtransport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

func TestBuildRequestCookiePoliciesPreserveRequestIdentity(t *testing.T) {
	original := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	original.Header.Set("X-Client-Request-Id", "logical-request")
	original.Header.Add("Cookie", "client=a; "+providercookie.GatewayHandleName+"=opaque")
	original.Header.Add("Cookie", "second=b")

	preserved, err := BuildRequestWithPolicy(
		context.Background(), http.MethodPost, "https://provider.test/v1/responses", []byte("wire"), original,
		RequestPolicy{Cookies: PreserveClientCookies},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := preserved.Header.Get("X-Client-Request-Id"); got != "logical-request" {
		t.Fatalf("X-Client-Request-Id = %q", got)
	}
	if got := preserved.Header.Values("Cookie"); len(got) != 2 || got[0] != "client=a" || got[1] != "second=b" {
		t.Fatalf("preserved Cookie = %#v", got)
	}

	managed, err := BuildRequestWithPolicy(
		context.Background(), http.MethodPost, "https://provider.test/v1/responses", nil, original,
		RequestPolicy{Cookies: ServerManagedCookies},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := managed.Header.Values("Cookie"); len(got) != 0 {
		t.Fatalf("server-managed Cookie = %#v, want none", got)
	}
	if _, err := BuildRequestWithPolicy(context.Background(), http.MethodGet, "https://provider.test", nil, original,
		RequestPolicy{Cookies: CookiePolicy(99)}); err == nil {
		t.Fatal("invalid Cookie policy accepted")
	}
	if _, err := BuildRequestWithPolicy(context.Background(), http.MethodGet, "https://provider.test", nil, original,
		RequestPolicy{Headers: HeaderPolicy(99)}); err == nil {
		t.Fatal("invalid header policy accepted")
	}

	original.Header.Set("Chatgpt-Account-Id", "client-owned-when-rollout-disabled")
	unsanitized, err := BuildRequestWithPolicy(
		context.Background(), http.MethodGet, "https://provider.test", nil, original,
		RequestPolicy{Headers: PreserveClientHeaders},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := unsanitized.Header.Get("Chatgpt-Account-Id"); got != "client-owned-when-rollout-disabled" {
		t.Fatalf("rollout-disabled account header = %q", got)
	}
}

func TestFetchAlwaysExposesRedirectAsAttemptBoundary(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, targetKind := range []string{"cross-host", "same-host-different-port"} {
			t.Run(http.StatusText(status)+"/"+targetKind, func(t *testing.T) {
				var targetRequests atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					targetRequests.Add(1)
				}))
				t.Cleanup(target.Close)

				location := target.URL + "/credential-sink"
				if targetKind == "cross-host" {
					parsed, err := url.Parse(location)
					if err != nil {
						t.Fatal(err)
					}
					parsed.Host = "localhost:" + parsed.Port()
					location = parsed.String()
				}
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Add("Set-Cookie", "provider_session=one; Path=/")
					w.Header().Set("Location", location)
					w.WriteHeader(status)
				}))
				t.Cleanup(source.Close)

				original := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/messages", strings.NewReader("wire"))
				original.Header.Set("Authorization", "Bearer must-not-follow")
				original.Header.Set("Cookie", "provider_session=must-not-follow")
				request, err := BuildRequest(context.Background(), http.MethodPost, source.URL+"/start", []byte("wire"), original)
				if err != nil {
					t.Fatal(err)
				}
				transport := New(Config{})
				t.Cleanup(transport.CloseIdleConnections)
				response, err := transport.Fetch(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				head, body, err := response.Take()
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, body)
				_ = body.Close()
				if head.StatusCode != status || targetRequests.Load() != 0 {
					t.Fatalf("redirect status/target requests = %d/%d", head.StatusCode, targetRequests.Load())
				}
				if got := head.SourceHeader.Values("Set-Cookie"); len(got) != 1 {
					t.Fatalf("source Set-Cookie = %#v", got)
				}
			})
		}
	}
}
