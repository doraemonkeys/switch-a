package clientdisguise

import (
	"context"
	"errors"
	"net/http"
	"strings"
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
				wantUA := strings.Replace(ua, "/0.155.0-alpha.2.6", "/"+builtinVersion, 1)
				if got := profile.RequestUserAgent(ua, ""); got != wantUA {
					t.Fatalf("default changed observed environment: %q", got)
				}
			})
		}
	}
}

func TestBrowserUseSamplesDoNotReplacePrimaryEnvironment(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	target, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveReference(ctx, ReferenceSource{ID: "reference", Name: "Desktop and browser", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	binding := target.Binding
	binding.ReferenceSourceID = "reference"
	binding.VersionSource = officialversion.Source
	if _, err := repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOfficialVersion(ctx, officialversion.State{Release: officialversion.Release{Version: "0.156.0"}}); err != nil {
		t.Fatal(err)
	}
	const originalUA = "codex-browser-use/0.155.0-alpha.2.6 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)"
	const nextUA = "codex-browser-use/0.155.0-alpha.2.7 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.2.0)"
	at := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	for i, ua := range []string{originalUA, nextUA} {
		if err := repo.ObserveClient(ctx, "client", http.Header{"User-Agent": {ua}, "Originator": {"Codex Desktop"}}, at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	requests, err := repo.ListClientRequests(ctx)
	if err != nil || len(requests) != 1 || requests[0].Tuple.ClientType != clientTypeBrowserUse || requests[0].UserAgent != nextUA {
		t.Fatal("browser activity lost its original role or UA", requests, err)
	}
	primary := candidateFor(t, repo, "login", windowsDesktop)
	if primary.Profile.ID != target.Profile.ID {
		t.Fatal("browser observation advanced the primary profile", primary.Profile)
	}
	const wantUA = "codex-browser-use/0.156.0 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)"
	if got := primary.Profile.RequestUserAgent(originalUA, primary.OfficialVersion.Version); got != wantUA {
		t.Fatal("automatic Browser Use projection lost the selected release or original caller", got)
	}
	snapshot, err := repo.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var browser ProfileRevision
	for _, profile := range snapshot.Profiles {
		if profile.SourceID == "reference" && profile.Features.UserAgent == nextUA {
			browser = profile
		}
	}
	if browser.ID == "" || browser.Tuple.ClientType != clientTypeBrowserUse || len(snapshot.Samples) != 2 {
		t.Fatal("browser samples were not collected separately", browser, snapshot.Samples)
	}
	if _, err := repo.SelectProfile(ctx, "login", browser.ID); !errors.Is(err, ErrInvalid) {
		t.Fatal("browser sample was accepted as a primary environment", err)
	}
	if _, err := repo.SelectProfile(ctx, "login", "builtin-browser-use-windows-amd64"); !errors.Is(err, ErrInvalid) {
		t.Fatal("builtin browser role was accepted as a primary environment", err)
	}
	restored := testRepository(t)
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	final := candidateFor(t, restored, "login", windowsDesktop)
	if final.Profile.ID != primary.Profile.ID || final.Binding.VersionSource != officialversion.Source || final.OfficialVersion.Version != "" {
		t.Fatal("restore changed the primary environment or imported the release cache", final)
	}
	restoredSnapshot, err := restored.Export(ctx)
	if err != nil || len(restoredSnapshot.Samples) != 2 {
		t.Fatal("restore lost auxiliary samples", restoredSnapshot.Samples, err)
	}
	var restoredBrowser ProfileRevision
	if err := restored.db.First(&restoredBrowser, "id = ?", browser.ID).Error; err != nil || restoredBrowser.Features.UserAgent != nextUA {
		t.Fatal("restore lost the Browser Use observation", restoredBrowser, err)
	}
	login, err := restored.GetLogin(ctx, "login")
	if err != nil || login.DeviceID != target.Login.DeviceID {
		t.Fatal("sample collection changed device identity", login, err)
	}
}

func TestBrowserFirstRequestDefersPrimaryBinding(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	const browserUA = "codex-browser-use/0.155.0 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)"
	facts := ProjectPlatform(http.Header{"User-Agent": {browserUA}})
	candidate, err := repo.EvaluateCandidate(ctx, "login", account("account"), Policy{Enabled: true}, facts)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := repo.CommitTarget(ctx, candidate)
	if err != nil || browser.Login.DeviceID == "" || browser.Binding.RevisionID != "" {
		t.Fatal("browser-first request must use a device without binding a primary client", browser, err)
	}
	if ua := browser.Profile.RequestUserAgent(browserUA, ""); !strings.HasPrefix(ua, "codex-browser-use/"+builtinVersion) {
		t.Fatal("unbound browser request lost its role or builtin version", ua)
	}
	bindings, err := repo.ListBindings(ctx)
	if err != nil || len(bindings) != 0 {
		t.Fatal("browser request persisted a primary binding", bindings, err)
	}
	accountProfile, err := repo.ResolveLoginProfile(ctx, "login")
	if err != nil || accountProfile.Profile.ID != "" {
		t.Fatal("account maintenance inherited the auxiliary role", accountProfile, err)
	}
	primary, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
	if err != nil || primary.Binding.Tuple != windowsDesktop || primary.Login.DeviceID != browser.Login.DeviceID {
		t.Fatal("ordinary request could not establish its main environment", primary, err)
	}
	// An operation evaluated before another request chose the primary environment
	// must use that committed winner when it reaches the sending boundary.
	concurrent, err := repo.CommitTarget(ctx, candidate)
	if err != nil || concurrent.Profile.ID != primary.Profile.ID || concurrent.Binding.RevisionID != primary.Binding.RevisionID {
		t.Fatal("browser request ignored a concurrently committed primary environment", concurrent, err)
	}
}

func TestMigrationRetiresBrowserBindingsWithoutChangingDevices(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	primary, err := repo.CommitTarget(ctx, candidateFor(t, repo, "primary", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	login, err := repo.SyncLoginAccount(ctx, "browser", account("account"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := ProfileBinding{CredentialSessionID: "browser", Tuple: Tuple{ClientType: clientTypeBrowserUse, Platform: "windows", Arch: "amd64"}, Mode: ModePinned, RevisionID: "builtin-browser-use-windows-amd64"}
	if err := repo.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, repo.db); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := repo.ListBindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].RevisionID != primary.Profile.ID {
		t.Fatal("migration retained an auxiliary binding or removed a primary binding", bindings, err)
	}
	preserved, err := repo.GetLogin(ctx, "browser")
	if err != nil || preserved.DeviceID != login.DeviceID {
		t.Fatal("migration replaced the login's device", preserved, err)
	}
	if err := repo.Import(ctx, Snapshot{Bindings: []ProfileBinding{legacy}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("restore reintroduced an auxiliary primary binding", err)
	}
	rebound, err := repo.CommitTarget(ctx, candidateFor(t, repo, "browser", windowsDesktop))
	if err != nil || rebound.Binding.Tuple != windowsDesktop || rebound.Login.DeviceID != login.DeviceID {
		t.Fatal("retired binding did not admit a fresh primary selection", rebound, err)
	}
}
