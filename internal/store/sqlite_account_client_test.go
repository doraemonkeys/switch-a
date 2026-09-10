package store

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/accountclient"
)

type accountReleaseFetcher struct {
	calls int
	err   error
}

func (f *accountReleaseFetcher) Latest(context.Context) (officialversion.Release, error) {
	f.calls++
	return officialversion.Release{Version: "0.151.0", Tag: "rust-v0.151.0"}, f.err
}

func TestGlobalAccountModeOwnsReleaseFollowingWithoutBindings(t *testing.T) {
	ctx := context.Background()
	st := newCredentialSessionStore(t)
	versions := st.OfficialVersionRepository()
	fetcher := &accountReleaseFetcher{}
	service := officialversion.NewService(versions, fetcher, nil)
	policy, err := st.ResolveAccountClientPolicy(ctx)
	if err != nil || policy.FallbackClient != accountclient.FallbackSwitchA || policy.OfficialVersion.Version != "" {
		t.Fatal(policy, err)
	}
	if _, err := service.Sync(ctx, false); err != nil || fetcher.calls != 0 {
		t.Fatal(fetcher.calls, err)
	}
	if err := st.SetConfig(ctx, defaults.ConfigKeyGPTAccountFallbackClient, string(accountclient.FallbackOfficialStable)); err != nil {
		t.Fatal(err)
	}
	pending, err := st.ResolveAccountClientPolicy(ctx)
	if err != nil || pending.FallbackClient != accountclient.FallbackOfficialStable || pending.OfficialVersion.Version != "" {
		t.Fatal(pending, err)
	}
	if _, err := service.Sync(ctx, false); err != nil || fetcher.calls != 1 {
		t.Fatal(fetcher.calls, err)
	}
	frozen, err := st.ResolveAccountClientPolicy(ctx)
	if err != nil || frozen.OfficialVersion.Version != "0.151.0" {
		t.Fatal(frozen, err)
	}
	fetcher.err = errors.New("offline")
	if _, err := service.Sync(ctx, true); !errors.Is(err, fetcher.err) {
		t.Fatal(err)
	}
	retained, err := st.ResolveAccountClientPolicy(ctx)
	if err != nil || retained != frozen {
		t.Fatal(retained, frozen, err)
	}
	if err := st.SetConfigs(ctx, map[string]string{defaults.ConfigKeyGPTAccountFallbackClient: defaults.DefaultGPTAccountFallbackClient}); err != nil {
		t.Fatal(err)
	}
	reset, err := st.ResolveAccountClientPolicy(ctx)
	if err != nil || reset.FallbackClient != accountclient.FallbackSwitchA || reset.OfficialVersion.Version != "" {
		t.Fatal(reset, err)
	}
	if enabled, err := versions.HasOfficialVersionFollowers(ctx); err != nil || enabled {
		t.Fatal(enabled, err)
	}
	bindings, err := st.ClientDisguiseRepository().ListBindings(ctx)
	if err != nil || len(bindings) != 0 {
		t.Fatal(bindings, err)
	}
	// Existing login followers remain sufficient when the global mode is disabled.
	if err := st.db.Create(&clientdisguise.ProfileBinding{CredentialSessionID: "test", VersionSource: officialversion.Source}).Error; err != nil {
		t.Fatal(err)
	}
	if enabled, err := versions.HasOfficialVersionFollowers(ctx); err != nil || !enabled {
		t.Fatal(enabled, err)
	}
}

func TestAccountPolicyStorageFailuresAreVisible(t *testing.T) {
	for _, table := range []any{&model.RuntimeConfig{}, &officialversion.State{}} {
		t.Run(stableAccountTableName(table), func(t *testing.T) {
			ctx := context.Background()
			st := newCredentialSessionStore(t)
			if err := st.SetConfig(ctx, defaults.ConfigKeyGPTAccountFallbackClient, string(accountclient.FallbackOfficialStable)); err != nil {
				t.Fatal(err)
			}
			if err := st.db.Migrator().DropTable(table); err != nil {
				t.Fatal(err)
			}
			if _, err := st.ResolveAccountClientPolicy(ctx); err == nil {
				t.Fatal("storage failure hidden")
			}
			if _, ok := table.(*model.RuntimeConfig); ok {
				if _, err := st.OfficialVersionRepository().HasOfficialVersionFollowers(ctx); err == nil {
					t.Fatal("settings failure hidden")
				}
			}
		})
	}
}

func stableAccountTableName(table any) string {
	switch table.(type) {
	case *model.RuntimeConfig:
		return "settings"
	default:
		return "release"
	}
}
