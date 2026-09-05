package upstreamtransport

import (
	"context"
	"errors"
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
		context.Background(), http.MethodPost, "https://provider.test/v1/responses", testBodySource([]byte("wire")), original,
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

func TestFetchFollowRedirectsPreservesNetHTTPMethodAndBodySemantics(t *testing.T) {
	tests := []struct {
		status     int
		wantMethod string
		wantBody   string
	}{
		{http.StatusMovedPermanently, http.MethodGet, ""},
		{http.StatusFound, http.MethodGet, ""},
		{http.StatusSeeOther, http.MethodGet, ""},
		{http.StatusTemporaryRedirect, http.MethodPost, "wire"},
		{http.StatusPermanentRedirect, http.MethodPost, "wire"},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			var targetRequests atomic.Int32
			var targetMethod, targetBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/start" {
					w.Header().Set("Location", "/target")
					w.WriteHeader(test.status)
					return
				}
				targetRequests.Add(1)
				targetMethod = request.Method
				payload, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				targetBody = string(payload)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)

			original := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/messages", strings.NewReader("wire"))
			request, err := BuildRequest(context.Background(), http.MethodPost, server.URL+"/start", testBodySource([]byte("wire")), original)
			if err != nil {
				t.Fatal(err)
			}
			transport := New(Config{})
			t.Cleanup(transport.CloseIdleConnections)
			response, _, err := transport.Fetch(context.Background(), request, ExecutionOptions{Redirects: FollowRedirects})
			if err != nil {
				t.Fatal(err)
			}
			head, body, err := response.Take()
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, body)
			_ = body.Close()
			if head.StatusCode != http.StatusNoContent || targetRequests.Load() != 1 {
				t.Fatalf("final status/target requests = %d/%d", head.StatusCode, targetRequests.Load())
			}
			if targetMethod != test.wantMethod || targetBody != test.wantBody {
				t.Fatalf("redirected method/body = %q/%q, want %q/%q", targetMethod, targetBody, test.wantMethod, test.wantBody)
			}
		})
	}
}

func TestFetchExposeRedirectsReturnsRawAttemptBoundary(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var targetRequests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				targetRequests.Add(1)
			}))
			t.Cleanup(target.Close)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Add("Set-Cookie", "provider_session=one; Path=/")
				w.Header().Set("Location", target.URL+"/target")
				w.WriteHeader(status)
			}))
			t.Cleanup(source.Close)

			original := httptest.NewRequest(http.MethodPost, "http://gateway.test/responses", strings.NewReader("wire"))
			request, err := BuildRequest(context.Background(), http.MethodPost, source.URL+"/start", testBodySource([]byte("wire")), original)
			if err != nil {
				t.Fatal(err)
			}
			transport := New(Config{})
			t.Cleanup(transport.CloseIdleConnections)
			response, _, err := transport.Fetch(context.Background(), request, ExecutionOptions{Redirects: ExposeRedirects})
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

func TestFetchFollowRedirectsReturnsLimitAndPolicyErrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", "/again")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	original := httptest.NewRequest(http.MethodGet, "http://gateway.test/v1/messages", nil)
	request, err := BuildRequest(context.Background(), http.MethodGet, server.URL+"/again", nil, original)
	if err != nil {
		t.Fatal(err)
	}
	transport := New(Config{})
	t.Cleanup(transport.CloseIdleConnections)
	if _, _, err := transport.Fetch(context.Background(), request, ExecutionOptions{}); err == nil {
		t.Fatal("redirect limit did not return an error")
	} else {
		var urlError *url.Error
		if !errors.As(err, &urlError) || !strings.Contains(err.Error(), "stopped after 10 redirects") {
			t.Fatalf("redirect limit error = %T %v", err, err)
		}
	}
	if got := requests.Load(); got != 10 {
		t.Fatalf("redirect requests = %d, want 10", got)
	}

	request, err = BuildRequest(context.Background(), http.MethodGet, server.URL+"/again", nil, original)
	if err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	if _, _, err := transport.Fetch(context.Background(), request, ExecutionOptions{Redirects: RedirectPolicy(99)}); err == nil {
		t.Fatal("invalid redirect policy accepted")
	}
	if got := requests.Load(); got != before {
		t.Fatalf("invalid policy contacted upstream: requests = %d, want %d", got, before)
	}
}
