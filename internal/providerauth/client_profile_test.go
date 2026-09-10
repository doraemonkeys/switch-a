package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

type loginProfileStoreFunc func(context.Context, string) (clientdisguise.LoginProfile, error)

func (f loginProfileStoreFunc) ResolveLoginProfile(ctx context.Context, sessionID string) (clientdisguise.LoginProfile, error) {
	return f(ctx, sessionID)
}

func selectedLoginProfile() clientdisguise.LoginProfile {
	return clientdisguise.LoginProfile{
		Binding: clientdisguise.ProfileBinding{CredentialSessionID: "session", RevisionID: "selected", VersionSource: officialversion.Source},
		Profile: clientdisguise.ProfileRevision{Features: clientdisguise.Features{
			UserAgent: "codex-tui/0.150.0 (Linux 6.8; x86_64)",
			Headers:   map[string]string{"Cookie": "unused", "Thread-Id": "unused", "Version": "0.150.0"},
		}},
		OfficialVersion: officialversion.Release{Version: "0.151.0"},
	}
}

const selectedAccountUA = "codex-tui/0.151.0 (Linux 6.8; x86_64)"

func accountResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRefreshUsesCredentialProfileAcrossProviderRoutes(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "proactive", true: "forced"}[force], func(t *testing.T) {
			now := time.Now().UTC()
			snapshot := profileSnapshot(t, now)
			expired := now.Add(-time.Minute)
			snapshot.AuthState.ExpiresAt = &expired
			store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
			profileCalls, networkCalls := 0, 0
			service := NewService(Config{
				CredentialStore: store, Clock: fixedClock{now: now},
				ClientProfiles: loginProfileStoreFunc(func(_ context.Context, sessionID string) (clientdisguise.LoginProfile, error) {
					profileCalls++
					if sessionID != snapshot.SessionID {
						t.Fatalf("profile resolved for %q, want credential %q", sessionID, snapshot.SessionID)
					}
					return selectedLoginProfile(), nil
				}),
				HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
					networkCalls++
					if request.Method != http.MethodPost || request.URL.String() != defaultOAuthIssuer+"/oauth/token" {
						t.Fatalf("unexpected refresh target: %s %s", request.Method, request.URL)
					}
					wantHeaders := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "User-Agent": {selectedAccountUA}, "Originator": {"codex-tui"}}
					if !reflect.DeepEqual(request.Header, wantHeaders) {
						t.Fatalf("refresh headers = %#v", request.Header)
					}
					if err := request.ParseForm(); err != nil {
						t.Fatal(err)
					}
					wantForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"refresh"}, "client_id": {defaultOAuthClientID}}
					if !reflect.DeepEqual(request.PostForm, wantForm) {
						t.Fatalf("refresh payload changed: %#v", request.PostForm)
					}
					access := chatgptAccessJWT(t, "acct", "", "pro", now.Add(time.Hour))
					return accountResponse(`{"access_token":"` + access + `","refresh_token":"rotated"}`), nil
				}},
			})
			finalURL := mustAppliedIdentityURL(t, chatGPTCodexBaseURL+"/responses")
			if force {
				if attempted, err := service.RefreshCredentialSession(context.Background(), snapshot); !attempted || err != nil {
					t.Fatal(attempted, err)
				}
			}
			for _, route := range []string{"provider-a", "provider-b"} {
				candidate := mustAppliedIdentityCandidate(t, route, codexAPIType, "openai", snapshot, finalURL)
				original, _ := http.NewRequest(http.MethodPost, "https://downstream.test/responses", nil)
				original.Header.Set("User-Agent", route+"/unrelated")
				original.Header.Set("Thread-Id", "conversation")
				if _, err := service.ApplyProviderCredentials(context.Background(), make(http.Header), candidate, "bearer", "bearer", original, finalURL); err != nil {
					t.Fatal(err)
				}
			}
			if networkCalls != 1 || profileCalls != 1 || store.casWrites != 1 {
				t.Fatalf("network=%d profiles=%d credential writes=%d", networkCalls, profileCalls, store.casWrites)
			}
		})
	}
}

func TestUsageQueryFreezesProfileAcrossFallbackEndpoints(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	profile := selectedLoginProfile()
	profileCalls, networkCalls := 0, 0
	service := NewService(Config{
		CredentialStore: store, Clock: fixedClock{now: now},
		ClientProfiles: loginProfileStoreFunc(func(_ context.Context, id string) (clientdisguise.LoginProfile, error) {
			profileCalls++
			if id != snapshot.SessionID {
				t.Fatal(id)
			}
			return profile, nil
		}),
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			networkCalls++
			wantUA := selectedAccountUA
			if networkCalls > 2 {
				wantUA = "codex-tui/0.152.0 (Linux 6.8; x86_64)"
			}
			want := http.Header{"Authorization": {"Bearer access"}, "Chatgpt-Account-Id": {"acct"}, "Accept": {"application/json"}, "User-Agent": {wantUA}}
			if !reflect.DeepEqual(request.Header, want) {
				t.Fatalf("usage headers = %#v", request.Header)
			}
			if networkCalls == 1 {
				profile.OfficialVersion.Version = "0.152.0"
				return nil, errors.New("first endpoint unavailable")
			}
			return accountResponse(`{"plan_type":"pro"}`), nil
		}},
	})
	for range 2 {
		if _, err := service.RefreshCredentialSessionUsage(context.Background(), snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if profileCalls != 2 || networkCalls != 3 || store.stateWrites != 2 {
		t.Fatalf("profiles=%d network=%d state writes=%d", profileCalls, networkCalls, store.stateWrites)
	}
}

func TestOAuthLoginFreezesReauthenticationProfileBeforeCallback(t *testing.T) {
	for _, sessionID := range []string{"", "session"} {
		t.Run("target="+sessionID, func(t *testing.T) {
			now := time.Now().UTC()
			profile := selectedLoginProfile()
			calls, profileCalls := 0, 0
			wantUA := buildinfo.Current().UserAgent()
			if sessionID != "" {
				wantUA = selectedAccountUA
			}
			service := newCallbackLifecycleTestService(Config{
				Clock: fixedClock{now: now},
				ClientProfiles: loginProfileStoreFunc(func(_ context.Context, id string) (clientdisguise.LoginProfile, error) {
					profileCalls++
					if id != sessionID || id == "" {
						t.Fatal(id)
					}
					return profile, nil
				}),
				HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
					calls++
					if request.Header.Get("User-Agent") != wantUA || request.Header.Get("Originator") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Thread-Id") != "" {
						t.Fatalf("login headers = %#v", request.Header)
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
			login, err := service.StartChatGPTLogin(context.Background(), sessionID)
			if err != nil {
				t.Fatal(err)
			}
			profile.OfficialVersion.Version = "0.152.0"
			state := loginStateFromAuthURL(t, login.AuthURL)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state="+url.QueryEscape(state)+"&code=auth-code", nil)
			service.handleChatGPTOAuthCallback(recorder, request)
			status, err := service.GetChatGPTLoginStatus(login.LoginID)
			if err != nil || status.Status != ChatGPTLoginStatusCompleted || calls != 2 {
				t.Fatal(status, calls, err, recorder.Body.String())
			}
			if (sessionID == "" && profileCalls != 0) || (sessionID != "" && profileCalls != 1) {
				t.Fatalf("profile calls=%d", profileCalls)
			}
		})
	}
}

func TestTokenImportUsesReauthenticationProfile(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(Config{
		Clock: fixedClock{now: now},
		ClientProfiles: loginProfileStoreFunc(func(_ context.Context, id string) (clientdisguise.LoginProfile, error) {
			if id != "session" {
				t.Fatal(id)
			}
			return selectedLoginProfile(), nil
		}),
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("User-Agent") != selectedAccountUA {
				t.Fatal(request.Header)
			}
			return accountResponse(`{"plan_type":"pro"}`), nil
		}},
	})
	raw := `{"access_token":"` + chatgptAccessJWT(t, "acct", "", "pro", now.Add(time.Hour)) + `","refresh_token":"refresh"}`
	if result, err := service.ImportChatGPTLogin(context.Background(), raw, "session"); err != nil || result.Status != ChatGPTLoginStatusCompleted {
		t.Fatal(result, err)
	}
}

func TestProfileResolutionFailureDoesNotMutateCredentialsOrSendRequests(t *testing.T) {
	now := time.Now().UTC()
	snapshot := profileSnapshot(t, now)
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, snapshot)}
	want := errors.New("profile storage unavailable")
	service := newCallbackLifecycleTestService(Config{
		Clock: fixedClock{now: now}, CredentialStore: store,
		ClientProfiles: loginProfileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
			return clientdisguise.LoginProfile{}, want
		}),
		HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
			t.Fatal("profile failure sent a request")
			return nil, nil
		}},
	}, &recordingCallbackEndpoint{}, &manualScheduler{})
	if _, err := service.RefreshCredentialSession(context.Background(), snapshot); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if _, err := service.RefreshCredentialSessionUsage(context.Background(), snapshot); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if _, err := service.StartChatGPTLogin(context.Background(), "session"); !errors.Is(err, want) {
		t.Fatal(err)
	}
	raw := `{"access_token":"` + chatgptAccessJWT(t, "acct", "", "pro", now.Add(time.Hour)) + `","refresh_token":"refresh"}`
	if _, err := service.ImportChatGPTLogin(context.Background(), raw, "session"); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if store.casWrites != 0 || store.stateWrites != 0 || len(service.pendingByLoginID) != 0 || len(service.completed) != 0 {
		t.Fatal("profile lookup error mutated credential lifecycle")
	}
}
