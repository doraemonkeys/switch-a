package accountclient

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type policyStoreFunc func(context.Context) (Policy, error)

func (f policyStoreFunc) ResolveAccountClientPolicy(ctx context.Context) (Policy, error) {
	return f(ctx)
}

func TestGlobalOfficialModeSelectsFeaturesAndFreezesOperation(t *testing.T) {
	for _, tc := range []struct {
		name, sessionID, revisionID, version, want, reason string
	}{
		{"new login", "", "", "0.151.0", clientdisguise.BuiltinAccountProfile().UserAgent("0.151.0"), "unassigned_login"},
		{"unbound", "session", "", "0.151.0", clientdisguise.BuiltinAccountProfile().UserAgent("0.151.0"), "unbound_session"},
		{"unsampled UA", "session", "bound", "0.151.0", clientdisguise.BuiltinAccountProfile().UserAgent("0.151.0"), "profile_without_user_agent"},
		{"pending sync", "", "", "", clientdisguise.BuiltinAccountProfile().UserAgent(""), "unassigned_login"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			policy := Policy{FallbackClient: FallbackOfficialStable, OfficialVersion: officialversion.Release{Version: tc.version}}
			calls := 0
			resolver := NewResolver(Config{
				Policy: policyStoreFunc(func(context.Context) (Policy, error) { calls++; return policy, nil }),
				Profiles: profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
					return clientdisguise.LoginProfile{
						Binding:         clientdisguise.ProfileBinding{RevisionID: tc.revisionID},
						Profile:         clientdisguise.ProfileRevision{ID: tc.revisionID},
						OfficialVersion: officialversion.Release{Version: "0.150.0"},
					}, nil
				}),
				Logger: zap.New(core),
			})
			op, err := resolver.Resolve(context.Background(), tc.sessionID, OAuthLogin)
			if err != nil {
				t.Fatal(err)
			}
			policy = Policy{FallbackClient: FallbackSwitchA}
			for range 2 {
				req, err := http.NewRequest(http.MethodPost, "https://example.test/oauth/token", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				op.Apply(req)
				want := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "User-Agent": {tc.want}}
				if !reflect.DeepEqual(req.Header, want) {
					t.Fatalf("headers = %#v, want %#v", req.Header, want)
				}
			}
			if calls != 1 {
				t.Fatalf("policy reads = %d", calls)
			}
			fields := logs.All()[0].ContextMap()
			if fields["account_client_fallback"] != string(FallbackOfficialStable) || fields["user_agent_source"] != "official_stable_builtin" || fields["fallback_reason"] != tc.reason || fields["version_source"] != officialversion.Source ||
				fields["official_version_pending"] != (tc.version == "") {
				t.Fatalf("diagnostics = %#v", fields)
			}
			next, err := resolver.Resolve(context.Background(), tc.sessionID, OAuthLogin)
			if err != nil {
				t.Fatal(err)
			}
			want := buildinfo.Current().UserAgent()
			if next.userAgent != want {
				t.Fatalf("restored mode UA = %q, want %q", next.userAgent, want)
			}
		})
	}
}

func TestDisguiseUserAgentAlwaysPrecedesGlobalFallback(t *testing.T) {
	const sampled = "codex-tui/0.149.0 (Linux 6.8; x86_64)"
	for _, version := range []string{"", "0.150.0"} {
		for _, fallback := range []FallbackClient{FallbackSwitchA, FallbackOfficialStable} {
			t.Run(string(fallback)+"/"+version, func(t *testing.T) {
				profile := clientdisguise.LoginProfile{
					Profile:         clientdisguise.ProfileRevision{Features: clientdisguise.Features{UserAgent: sampled}},
					OfficialVersion: officialversion.Release{Version: version},
				}
				resolver := NewResolver(Config{
					Profiles: profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) { return profile, nil }),
					Policy: policyStoreFunc(func(context.Context) (Policy, error) {
						t.Error("global fallback consulted despite an authoritative disguise UA")
						return Policy{FallbackClient: fallback, OfficialVersion: officialversion.Release{Version: "0.200.0"}}, errors.New("global cache unavailable")
					}),
				})
				op, err := resolver.Resolve(context.Background(), "session", TokenRefresh)
				if err != nil || op.userAgent != profile.UserAgent() {
					t.Fatal(op, err)
				}
			})
		}
	}
}

func TestPolicyFailureDoesNotResolveProfileOrPrepareOperation(t *testing.T) {
	want := errors.New("settings unavailable")
	for _, tc := range []struct {
		name   string
		policy Policy
		err    error
	}{
		{"storage", Policy{}, want},
		{"invalid mode", Policy{FallbackClient: "invalid"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := NewResolver(Config{
				Policy: policyStoreFunc(func(context.Context) (Policy, error) { return tc.policy, tc.err }),
				Profiles: profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
					t.Fatal("failed policy resolved a profile")
					return clientdisguise.LoginProfile{}, nil
				}),
			})
			op, err := resolver.Resolve(context.Background(), "", TokenRefresh)
			if err == nil || op.userAgent != "" || (tc.err != nil && !errors.Is(err, want)) {
				t.Fatalf("operation = %#v, error = %v", op, err)
			}
		})
	}
}
