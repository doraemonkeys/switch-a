package clientdisguise

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

func TestResolveLoginProfileDoesNotCreateBinding(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	if _, err := repo.SyncLoginAccount(ctx, "login", account("account")); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"login", "unknown"} {
		got, err := repo.ResolveLoginProfile(ctx, session)
		if err != nil || got.Binding.RevisionID != "" || got.UserAgent() != "" {
			t.Fatal(got, err)
		}
	}
	bindings, err := repo.ListBindings(ctx)
	if err != nil || len(bindings) != 0 {
		t.Fatal(bindings, err)
	}
}

func TestResolveLoginProfileUsesSessionBindingAndFrozenOfficialVersion(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	target, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	binding := target.Binding
	binding.Mode = ModePinned
	binding.VersionSource = officialversion.Source
	if _, err := repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	before, err := repo.ResolveLoginProfile(ctx, "login")
	if err != nil || before.UserAgent() != target.Profile.Features.ClientUserAgent() {
		t.Fatal(before, err)
	}
	state := officialversion.State{Release: officialversion.Release{Version: "0.151.0"}}
	if err := repo.SaveOfficialVersion(ctx, state); err != nil {
		t.Fatal(err)
	}
	frozen, err := repo.ResolveLoginProfile(ctx, "login")
	if err != nil {
		t.Fatal(err)
	}
	want := WithUserAgentVersion(target.Profile.Features.ClientUserAgent(), "0.151.0")
	if frozen.UserAgent() != want || frozen.Login.DeviceID != target.Login.DeviceID || frozen.Binding.Mode != ModePinned {
		t.Fatal(frozen)
	}
	state.Release.Version = "0.152.0"
	if err := repo.SaveOfficialVersion(ctx, state); err != nil {
		t.Fatal(err)
	}
	next, err := repo.ResolveLoginProfile(ctx, "login")
	if err != nil || next.UserAgent() == frozen.UserAgent() || frozen.UserAgent() != want {
		t.Fatal(next, frozen, err)
	}
	binding.VersionSource = ""
	if _, err := repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	pinned, err := repo.ResolveLoginProfile(ctx, "login")
	if err != nil || pinned.UserAgent() != target.Profile.Features.ClientUserAgent() {
		t.Fatal(pinned, err)
	}
	if _, err := repo.SyncLoginAccount(ctx, "login", account("new-account")); err != nil {
		t.Fatal(err)
	}
	replaced, err := repo.ResolveLoginProfile(ctx, "login")
	if err != nil || replaced.Binding.RevisionID != "" {
		t.Fatal(replaced, err)
	}
}

func TestResolveLoginProfileDoesNotHideStorageFailures(t *testing.T) {
	for _, table := range []any{&ProfileBinding{}, &LoginIdentity{}, &ProfileRevision{}, &officialversion.State{}} {
		t.Run(tableName(table), func(t *testing.T) {
			ctx := context.Background()
			repo := testRepository(t)
			target, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
			if err != nil {
				t.Fatal(err)
			}
			binding := target.Binding
			binding.VersionSource = officialversion.Source
			if _, err := repo.SetBinding(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if err := repo.db.Migrator().DropTable(table); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.ResolveLoginProfile(ctx, "login"); err == nil {
				t.Fatal("database failure became an unbound profile")
			}
		})
	}
}

func tableName(value any) string {
	return value.(interface{ TableName() string }).TableName()
}

func TestResolveLoginProfileRejectsDanglingRevision(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	target, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Delete(&ProfileRevision{}, "id = ?", target.Profile.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveLoginProfile(ctx, "login"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dangling revision error = %v", err)
	}
}
