package clientdisguise

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

func TestOfficialVersionBindingSnapshotAndRestore(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	target, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	binding := target.Binding
	binding.VersionSource = officialversion.Source
	if _, err = repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if enabled, err := repo.HasOfficialVersionFollowers(ctx); err != nil || !enabled {
		t.Fatal(enabled, err)
	}
	// The opt-in remains usable before the first successful synchronization.
	if got := candidateFor(t, repo, "login", windowsDesktop); got.OfficialVersion.Version != "" {
		t.Fatal(got)
	}
	state := officialversion.State{Release: officialversion.Release{Version: "0.151.0", Tag: "rust-v0.151.0"}, CheckedAt: time.Now().UTC()}
	if err = repo.SaveOfficialVersion(ctx, state); err != nil {
		t.Fatal(err)
	}
	candidate := candidateFor(t, repo, "login", windowsDesktop)
	state.Release.Version = "0.152.0"
	if err = repo.SaveOfficialVersion(ctx, state); err != nil {
		t.Fatal(err)
	}
	snap, err := repo.CommitTarget(ctx, candidate.Clone())
	if err != nil || snap.OfficialVersion.Version != "0.151.0" || snap.Profile.ClientVersion != target.Profile.ClientVersion || snap.Login.DeviceID != target.Login.DeviceID {
		t.Fatal(snap, err)
	}
	if got := candidateFor(t, repo, "login", windowsDesktop); got.OfficialVersion.Version != "0.152.0" {
		t.Fatal(got)
	}
	exported, err := repo.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := testRepository(t)
	if err = restored.Import(ctx, exported); err != nil {
		t.Fatal(err)
	}
	bindings, err := restored.ListBindings(ctx)
	if err != nil || bindings[0].VersionSource != officialversion.Source {
		t.Fatal(bindings, err)
	}
	binding.VersionSource = ""
	if _, err = repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if got := candidateFor(t, repo, "login", windowsDesktop); got.OfficialVersion.Version != "" {
		t.Fatal(got)
	}
	if enabled, err := repo.HasOfficialVersionFollowers(ctx); err != nil || enabled {
		t.Fatal(enabled, err)
	}
	binding.VersionSource = "alpha"
	if _, err = repo.SetBinding(ctx, binding); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	exported.Bindings[0].VersionSource = "alpha"
	if err = restored.Import(ctx, exported); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	state.Release.Version = "0.152.0-alpha.1"
	if err = repo.SaveOfficialVersion(ctx, state); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestOfficialVersionStorageFailures(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	if err := repo.db.Migrator().DropTable(&officialversion.State{}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.OfficialVersion(ctx); err == nil {
		t.Fatal("missing table")
	}
	target, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	binding := target.Binding
	binding.VersionSource = officialversion.Source
	if _, err = repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EvaluateCandidate(ctx, "login", account("account"), Policy{Enabled: true}, PlatformFacts{Tuple: windowsDesktop}); err == nil {
		t.Fatal("release query failure ignored")
	}
}
func TestUserAgentReleaseChangesOnlyProductVersion(t *testing.T) {
	for _, tc := range []struct{ ua, want string }{
		{"codex-tui/0.150.0 (Linux 6.8; x86_64) Terminal/1.0 (codex-tui; 0.150.0)", "codex-tui/0.151.0 (Linux 6.8; x86_64) Terminal/1.0 (codex-tui; 0.151.0)"},
		{"Codex Desktop/0.150.0-alpha.8 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.820.60940)", "Codex Desktop/0.151.0 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.820.60940)"},
		{"other-client/1.0", "other-client/1.0"},
		{"", ""},
	} {
		if got := WithUserAgentVersion(tc.ua, "0.151.0"); got != tc.want {
			t.Fatal(got, tc.want)
		}
		if got := WithUserAgentVersion(tc.ua, ""); got != tc.ua {
			t.Fatal(got)
		}
	}
}
