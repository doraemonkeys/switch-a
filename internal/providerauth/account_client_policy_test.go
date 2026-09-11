package providerauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"github.com/doraemonkeys/switch-a/internal/providerauth/accountclient"
)

type accountPolicyStoreFunc func(context.Context) (accountclient.Policy, error)

func (f accountPolicyStoreFunc) ResolveAccountClientPolicy(ctx context.Context) (accountclient.Policy, error) {
	return f(ctx)
}

func TestGlobalOfficialPolicyFreezesNewLoginUntilCallbackCompletes(t *testing.T) {
	now := time.Now().UTC()
	policy := accountclient.Policy{FallbackClient: accountclient.FallbackOfficialStable, OfficialVersion: officialversion.Release{Version: "0.151.0"}}
	wantUA := clientdisguise.BuiltinAccountProfile().UserAgent("0.151.0")
	policyCalls, networkCalls := 0, 0
	service := newCallbackLifecycleTestService(Config{
		Clock: fixedClock{now: now},
		RuntimeConfig: oauthRuntimeConfigFunc(func(context.Context, string) (string, error) {
			return "oauth-only-client", nil
		}),
		AccountClientPolicy: accountPolicyStoreFunc(func(context.Context) (accountclient.Policy, error) { policyCalls++; return policy, nil }),
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			networkCalls++
			if request.Header.Get("User-Agent") != wantUA || request.Header.Get("Originator") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Thread-Id") != "" {
				t.Fatalf("account headers = %#v", request.Header)
			}
			if request.URL.Path == "/oauth/token" {
				if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "authorization_code" {
					t.Fatal(request.Form, err)
				}
				idToken := chatgptAuthJWT(t, "acct", "", "pro", now.Add(time.Hour))
				return accountResponse(`{"access_token":"access","refresh_token":"refresh","id_token":"` + idToken + `"}`), nil
			}
			return accountResponse(`{"plan_type":"pro"}`), nil
		}},
	}, &recordingCallbackEndpoint{}, &manualScheduler{})
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	for range 2 {
		login, err := service.StartChatGPTLogin(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(login.AuthURL)
		if err != nil || parsed.Query().Get("originator") != "oauth-only-client" {
			t.Fatal(login.AuthURL, err)
		}
		policy = accountclient.Policy{FallbackClient: accountclient.FallbackSwitchA}
		state := loginStateFromAuthURL(t, login.AuthURL)
		recorder := httptest.NewRecorder()
		service.handleChatGPTOAuthCallback(recorder, httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state="+url.QueryEscape(state)+"&code=auth-code", nil))
		status, err := service.GetChatGPTLoginStatus(login.LoginID)
		if err != nil || status.Status != ChatGPTLoginStatusCompleted {
			t.Fatal(status, err, recorder.Body.String())
		}
		wantUA = buildinfo.Current().UserAgent()
	}
	if policyCalls != 2 || networkCalls != 4 {
		t.Fatal(policyCalls, networkCalls)
	}
}

func TestGlobalOfficialPolicyAppliesToUnboundTokenRefresh(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	calls := 0
	service := NewService(Config{
		CredentialStore: store, Clock: fixedClock{now: now},
		RuntimeConfig: oauthRuntimeConfigFunc(func(context.Context, string) (string, error) {
			t.Fatal("token refresh consulted OAuth login settings")
			return "", nil
		}),
		AccountClientPolicy: accountPolicyStoreFunc(func(context.Context) (accountclient.Policy, error) {
			return accountclient.Policy{FallbackClient: accountclient.FallbackOfficialStable, OfficialVersion: officialversion.Release{Version: "0.151.0"}}, nil
		}),
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Header.Get("User-Agent") != clientdisguise.BuiltinAccountProfile().UserAgent("0.151.0") || request.Header.Get("Originator") != "Codex Desktop" {
				t.Fatal(request.Header)
			}
			if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("client_id") != defaultOAuthClientID {
				t.Fatal(request.Form, err)
			}
			access := chatgptAccessJWT(t, "acct", "", "pro", now.Add(time.Hour))
			return accountResponse(`{"access_token":"` + access + `","refresh_token":"rotated"}`), nil
		}},
	})
	if attempted, err := service.RefreshCredentialSession(context.Background(), snapshot); !attempted || err != nil {
		t.Fatal(attempted, err)
	}
	if calls != 1 || store.casWrites != 1 {
		t.Fatal(calls, store.casWrites)
	}
}
