package accountclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRefreshOriginatorFollowsFrozenClientAndRequestProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, userAgent, version, wantUA, wantOriginator string
		fallback                                         FallbackClient
	}{
		{"tui release", "codex-tui/0.150.0 (Linux 6.8; x86_64)", "0.151.0", "codex-tui/0.151.0 (Linux 6.8; x86_64)", "codex-tui", FallbackSwitchA},
		{"desktop", "Codex Desktop/0.150.0 (Windows 10.0; x86_64)", "", "Codex Desktop/0.150.0 (Windows 10.0; x86_64)", "Codex Desktop", FallbackSwitchA},
		{"cli", "codex_cli_rs/0.150.0 (Linux 6.8; x86_64)", "", "codex_cli_rs/0.150.0 (Linux 6.8; x86_64)", "codex_cli_rs", FallbackSwitchA},
		{"vscode", "codex_vscode/0.150.0 (Windows 10.0; x86_64)", "", "codex_vscode/0.150.0 (Windows 10.0; x86_64)", "codex_vscode", FallbackSwitchA},
		{"unstructured sample", "sample without product version", "", "sample without product version", "", FallbackSwitchA},
		{"incomplete sample", "codex-tui/", "", "codex-tui/", "", FallbackSwitchA},
		{"gateway fallback", "", "", buildinfo.Current().UserAgent(), "", FallbackSwitchA},
		{"official fallback", "", "0.151.0", clientdisguise.BuiltinAccountProfile().UserAgent("0.151.0"), "Codex Desktop", FallbackOfficialStable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := clientdisguise.LoginProfile{
				Profile: clientdisguise.ProfileRevision{Features: clientdisguise.Features{
					Originator: "thread-override",
					Headers: map[string]string{
						"user-agent": tc.userAgent, "Originator": "header-thread-override",
						"Cookie": "not an auth feature", "Thread-Id": "not an auth feature",
					},
				}},
				OfficialVersion: officialversion.Release{Version: tc.version},
			}
			policy := Policy{FallbackClient: tc.fallback, OfficialVersion: officialversion.Release{Version: tc.version}}
			profileCalls := 0
			core, logs := observer.New(zap.DebugLevel)
			resolver := NewResolver(Config{
				Profiles: profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
					profileCalls++
					return profile, nil
				}),
				Policy: policyStoreFunc(func(context.Context) (Policy, error) { return policy, nil }),
				Logger: zap.New(core),
			})
			operation, err := resolver.Resolve(context.Background(), "credential", OAuthLogin)
			if err != nil {
				t.Fatal(err)
			}
			profile.Profile.Features.Headers["user-agent"] = "changed-client/2.0"
			profile.OfficialVersion.Version = "0.152.0"
			policy.FallbackClient = FallbackSwitchA
			for _, requestKind := range []string{"refresh", "authorization_code", "refresh"} {
				request := httptest.NewRequest(http.MethodPost, "https://example.test/oauth/token", nil)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				want := http.Header{
					"Content-Type": {"application/x-www-form-urlencoded"},
					"User-Agent":   {tc.wantUA},
				}
				if requestKind == "refresh" {
					operation.ApplyTokenRefresh(request)
					if tc.wantOriginator != "" {
						want.Set("Originator", tc.wantOriginator)
					}
				} else {
					operation.Apply(request)
				}
				if !reflect.DeepEqual(request.Header, want) {
					t.Fatalf("%s headers = %#v, want %#v", requestKind, request.Header, want)
				}
				entries := logs.All()
				fields := entries[len(entries)-1].ContextMap()
				if fields["originator"] != want.Get("Originator") || fields["user_agent"] != tc.wantUA ||
					fields["refresh_originator"] != tc.wantOriginator || fields["operation_id"] != entries[0].ContextMap()["operation_id"] {
					t.Fatalf("request identity diagnostics = %#v", fields)
				}
			}
			if profileCalls != 1 {
				t.Fatalf("profile resolved %d times", profileCalls)
			}
		})
	}
}
