package providerauth

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type oauthRuntimeConfigFunc func(context.Context, string) (string, error)

func (f oauthRuntimeConfigFunc) GetConfig(ctx context.Context, key string) (string, error) {
	return f(ctx, key)
}

func TestCodexOAuthOriginatorReadsSettingsForEachLogin(t *testing.T) {
	for _, sessionID := range []string{"", "existing-credential"} {
		t.Run("session="+sessionID, func(t *testing.T) {
			configured := ""
			core, logs := observer.New(zap.InfoLevel)
			service := newCallbackLifecycleTestService(Config{
				Logger: zap.New(core),
				RuntimeConfig: oauthRuntimeConfigFunc(func(_ context.Context, key string) (string, error) {
					if key != defaults.ConfigKeyCodexOAuthOriginator {
						t.Fatalf("config key = %q", key)
					}
					return configured, nil
				}),
			}, &recordingCallbackEndpoint{}, &manualScheduler{})
			t.Cleanup(func() { _ = service.Shutdown(context.Background()) })

			for _, tc := range []struct {
				value string
				want  string
			}{
				{"", "codex_cli_rs"},
				{"codex_vscode", "codex_vscode"},
				{"My Codex Client/1.0 + Work&Tools", "My Codex Client/1.0 + Work&Tools"},
				{"  codex_cli_rs  ", "codex_cli_rs"},
				{" \t ", "codex_cli_rs"},
			} {
				configured = tc.value
				login, err := service.StartChatGPTLogin(context.Background(), sessionID)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := url.Parse(login.AuthURL)
				if err != nil {
					t.Fatal(err)
				}
				query := parsed.Query()
				if got := query.Get("originator"); got != tc.want {
					t.Fatalf("originator = %q, want %q", got, tc.want)
				}
				if query.Get("scope") != defaultOAuthScope || query.Get("redirect_uri") != LoopbackCallbackAddress() {
					t.Fatal("originator selection changed other authorization parameters")
				}
				entries := logs.FilterMessage("chatgpt_oauth.login_started").All()
				fields := entries[len(entries)-1].ContextMap()
				if fields["login_id"] != login.LoginID || fields["oauth_originator"] != tc.want || fields["credential_session_id"] != sessionID {
					t.Fatalf("login trace = %#v", fields)
				}
			}
		})
	}
}

func TestCodexOAuthOriginatorConfigFailureDoesNotStartLogin(t *testing.T) {
	configErr := errors.New("settings unavailable")
	core, logs := observer.New(zap.WarnLevel)
	endpoint := &recordingCallbackEndpoint{}
	service := newCallbackLifecycleTestService(Config{
		Logger: zap.New(core),
		RuntimeConfig: oauthRuntimeConfigFunc(func(context.Context, string) (string, error) {
			return "", configErr
		}),
	}, endpoint, &manualScheduler{})
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })

	login, err := service.StartChatGPTLogin(context.Background(), "existing-credential")
	if login != nil || !errors.Is(err, configErr) {
		t.Fatalf("login = %#v, error = %v", login, err)
	}
	if active, starts, _ := endpoint.snapshot(); active || starts != 0 || len(service.pendingByLoginID) != 0 {
		t.Fatal("failed configuration read started a login")
	}
	entries := logs.FilterMessage("chatgpt_oauth.login_config_failed").All()
	if len(entries) != 1 || entries[0].ContextMap()["login_id"] == "" || entries[0].ContextMap()["credential_session_id"] != "existing-credential" {
		t.Fatalf("configuration failure trace = %#v", entries)
	}
}
