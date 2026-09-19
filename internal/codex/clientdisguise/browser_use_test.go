package clientdisguise

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

func TestBrowserUseBuiltinEnvironments(t *testing.T) {
	repo := testRepository(t)
	for _, environment := range []struct{ platform, os string }{
		{"windows", "Windows 10.0.26200"},
		{"linux", "Linux 6.8"},
		{"macos", "Darwin 24.6.0"},
	} {
		for _, architecture := range []struct{ arch, ua string }{{"amd64", "x86_64"}, {"arm64", "aarch64"}} {
			t.Run(environment.platform+"/"+architecture.arch, func(t *testing.T) {
				ua := "codex-browser-use/0.155.0-alpha.2.6 (" + environment.os + "; " + architecture.ua + ") unknown (codex-browser-use; 0.1.0)"
				facts := ProjectPlatform(http.Header{"User-Agent": {ua}})
				want := Tuple{ClientType: "browser-use", Platform: environment.platform, Arch: architecture.arch}
				if facts.Tuple != want || facts.Conflict {
					t.Fatalf("platform facts = %+v, want %+v", facts, want)
				}
				candidate, err := repo.EvaluateCandidate(context.Background(), "login", account("account"), Policy{Enabled: true}, facts)
				if err != nil || !candidate.Decision.Allowed || candidate.Profile.Tuple != want {
					t.Fatalf("browser use default unavailable: %+v, %v", candidate, err)
				}
				profile := candidate.Profile
				if profile.Features.Originator != "codex-browser-use" || profile.Features.UserAgent != "" || profile.SourceURL != builtinBrowserUseSourceURL {
					t.Fatalf("default invented observations or lost provenance: %+v", profile)
				}
				if got := profile.RequestUserAgent(ua, ""); got != ua {
					t.Fatalf("default changed observed environment: %q", got)
				}
			})
		}
	}
}

func TestBrowserUseReferenceFollowingAndRestore(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	const originalUA = "codex-browser-use/0.155.0-alpha.2.6 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)"
	headers := http.Header{"User-Agent": {originalUA}, "Originator": {"codex-browser-use"}}
	wantTuple := Tuple{ClientType: "browser-use", Platform: "windows", Arch: "amd64"}
	candidate, err := repo.EvaluateCandidate(ctx, "browser-login", account("account"), Policy{Enabled: true}, ProjectPlatform(headers))
	if err != nil {
		t.Fatal(err)
	}
	target, err := repo.CommitTarget(ctx, candidate)
	if err != nil || target.Binding.Tuple != wantTuple {
		t.Fatal("browser use could not bind its default", target.Binding, err)
	}
	if err := repo.SaveReference(ctx, ReferenceSource{ID: "browser-reference", Name: "Browser Use", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	binding := target.Binding
	binding.ReferenceSourceID = "browser-reference"
	if _, err := repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	if err := repo.ObserveClient(ctx, "client", headers, at); err != nil {
		t.Fatal(err)
	}
	first := candidateFor(t, repo, "browser-login", wantTuple)
	if first.Profile.Features.UserAgent != originalUA || first.Profile.WireClientVersion() != "0.155.0-alpha.2.6" {
		t.Fatal("reference did not capture the Codex release", first.Profile)
	}
	const nextUA = "codex-browser-use/0.155.0-alpha.2.7 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.2.0)"
	headers.Set("User-Agent", nextUA)
	if err := repo.ObserveClient(ctx, "client", headers, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	learned := candidateFor(t, repo, "browser-login", wantTuple)
	if learned.Profile.ID == first.Profile.ID || learned.Profile.RequestUserAgent(originalUA, "") != nextUA {
		t.Fatal("automatic following did not advance the complete observation", learned.Profile)
	}
	requests, err := repo.ListClientRequests(ctx)
	if err != nil || len(requests) != 1 || requests[0].Tuple != wantTuple || requests[0].ClientVersion != "0.155.0-alpha.2.7" {
		t.Fatal("browser use activity lost its entry point or release", requests, err)
	}
	binding = *learned.Binding
	binding.VersionSource = officialversion.Source
	if _, err := repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOfficialVersion(ctx, officialversion.State{Release: officialversion.Release{Version: "0.156.0"}}); err != nil {
		t.Fatal(err)
	}
	const wantUA = "codex-browser-use/0.156.0 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.2.0)"
	followed := candidateFor(t, repo, "browser-login", wantTuple)
	if followed.Profile.RequestUserAgent(originalUA, followed.OfficialVersion.Version) != wantUA {
		t.Fatal("official release changed the caller version or lost the sample", followed)
	}
	snapshot, err := repo.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := testRepository(t)
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	final := candidateFor(t, restored, "browser-login", wantTuple)
	if !final.Decision.Allowed || final.Profile.ID != learned.Profile.ID || final.Profile.RequestUserAgent(originalUA, final.OfficialVersion.Version) != nextUA {
		t.Fatal("restored binding lost its reference sample", final)
	}
	// Only the selection is portable; a fresh deployment discovers its own release.
	if final.Binding.VersionSource != officialversion.Source || final.OfficialVersion.Version != "" {
		t.Fatal("restored binding lost its version source or imported the deployment cache", final)
	}
	if err := restored.SaveOfficialVersion(ctx, officialversion.State{Release: officialversion.Release{Version: "0.156.0"}}); err != nil {
		t.Fatal(err)
	}
	final = candidateFor(t, restored, "browser-login", wantTuple)
	if final.Profile.RequestUserAgent(originalUA, final.OfficialVersion.Version) != wantUA {
		t.Fatal("restored official version follower did not resume", final)
	}
	login, err := restored.GetLogin(ctx, "browser-login")
	if err != nil || login.DeviceID != target.Login.DeviceID {
		t.Fatal("profile updates changed device identity", login, err)
	}
}
