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

type profileStoreFunc func(context.Context, string) (clientdisguise.LoginProfile, error)

func (f profileStoreFunc) ResolveLoginProfile(ctx context.Context, sessionID string) (clientdisguise.LoginProfile, error) {
	return f(ctx, sessionID)
}

func TestOperationFreezesSelectedUserAgentAndPreservesEndpointHeaders(t *testing.T) {
	const sampled = "codex-tui/0.150.0 (Linux 6.8; x86_64)"
	const selected = "codex-tui/0.151.0 (Linux 6.8; x86_64)"
	profile := clientdisguise.LoginProfile{
		Login:   clientdisguise.LoginIdentity{GenerationID: "generation"},
		Binding: clientdisguise.ProfileBinding{RevisionID: "revision", VersionSource: officialversion.Source},
		Profile: clientdisguise.ProfileRevision{Features: clientdisguise.Features{
			UserAgent: sampled,
			Headers:   map[string]string{"Cookie": "never copy", "Thread-Id": "never copy", "Originator": "never copy"},
		}},
		OfficialVersion: officialversion.Release{Version: "0.151.0"},
	}
	calls := 0
	store := profileStoreFunc(func(_ context.Context, sessionID string) (clientdisguise.LoginProfile, error) {
		calls++
		if sessionID != "credential" {
			t.Fatalf("resolved session %q", sessionID)
		}
		return profile, nil
	})
	core, logs := observer.New(zap.DebugLevel)
	resolver := NewResolver(store, zap.New(core))
	operation, err := resolver.Resolve(context.Background(), "credential", UsageQuery)
	if err != nil {
		t.Fatal(err)
	}
	profile.Profile.Features.UserAgent = "changed/2"
	profile.OfficialVersion.Version = "0.152.0"
	for _, path := range []string{"/backend-api/wham/usage", "/wham/usage"} {
		request, err := http.NewRequest(http.MethodGet, "https://example.test"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer secret")
		request.Header.Set("Accept", "application/json")
		operation.Apply(request)
		want := http.Header{
			"User-Agent": {selected}, "Authorization": {"Bearer secret"}, "Accept": {"application/json"},
		}
		if !reflect.DeepEqual(request.Header, want) {
			t.Fatalf("headers = %#v, want %#v", request.Header, want)
		}
	}
	if calls != 1 {
		t.Fatalf("profile resolved %d times", calls)
	}
	entries := logs.All()
	if len(entries) != 3 {
		t.Fatalf("logs = %#v", entries)
	}
	operationID := entries[0].ContextMap()["operation_id"]
	for _, entry := range entries {
		fields := entry.ContextMap()
		if operationID == "" || fields["operation_id"] != operationID || fields["user_agent_source"] != "profile" ||
			fields["credential_session_id"] != "credential" || fields["profile_revision_id"] != "revision" {
			t.Fatalf("incomplete identity diagnostics: %#v", fields)
		}
	}
}

func TestResolveDefaultIdentityReasons(t *testing.T) {
	for _, tc := range []struct {
		name, sessionID, source string
		store                   ProfileStore
	}{
		{"new login", "", "unassigned_login", profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
			t.Fatal("new login looked up an unrelated profile")
			return clientdisguise.LoginProfile{}, nil
		})},
		{"no store", "session", "profile_store_unavailable", nil},
		{"unbound session", "session", "unbound_session", profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
			return clientdisguise.LoginProfile{}, nil
		})},
		{"unsampled UA", "session", "profile_without_user_agent", profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
			return clientdisguise.LoginProfile{Binding: clientdisguise.ProfileBinding{RevisionID: "partial"}}, nil
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			op, err := NewResolver(tc.store, zap.New(core)).Resolve(context.Background(), tc.sessionID, OAuthLogin)
			if err != nil {
				t.Fatal(err)
			}
			if op.userAgent != buildinfo.Current().UserAgent() || logs.All()[0].ContextMap()["user_agent_source"] != tc.source {
				t.Fatalf("operation=%#v logs=%#v", op, logs.All())
			}
		})
	}
}

func TestResolveErrorDoesNotSubstituteDefaultIdentity(t *testing.T) {
	want := errors.New("profile database unavailable")
	resolver := NewResolver(profileStoreFunc(func(context.Context, string) (clientdisguise.LoginProfile, error) {
		return clientdisguise.LoginProfile{}, want
	}), nil)
	op, err := resolver.Resolve(context.Background(), "session", TokenRefresh)
	if !errors.Is(err, want) || op.userAgent != "" {
		t.Fatalf("operation=%#v error=%v", op, err)
	}
}
