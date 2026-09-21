package clientdisguise

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/useragent"
)

func TestPrimaryIdentityAndBrowserRoleShareTargetEnvironment(t *testing.T) {
	const browser = "codex-browser-use/0.155.0 (Linux 6.8; aarch64) Terminal/9 (codex-browser-use; 0.1.0)"
	const desktop = "Codex Desktop/0.156.0 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.9)"
	const wantBrowser = "codex-browser-use/0.156.0 (Windows 10.0.26200; x86_64) Terminal/9 (codex-browser-use; 0.1.0)"
	profile := ProfileRevision{Tuple: windowsDesktop, ClientVersion: "0.156.0", Features: Features{
		UserAgent: desktop, Originator: "Codex Desktop", DesktopBuild: "26.9",
		Headers: map[string]string{"user-agent": desktop, "originator": "Codex Desktop", "X-Codex-Desktop-Build": "26.9", "Version": "0.156.0"},
	}}
	browserProfile := profile.ForRequest(PlatformFacts{RequestRole: useragent.BrowserUse}, "")
	if got := browserProfile.UserAgent(browser); got != wantBrowser {
		t.Fatalf("browser UA = %q, want %q", got, wantBrowser)
	}
	features := browserProfile.Features()
	if features.ClientOriginator() != "codex-browser-use" || features.DesktopBuild != "" || features.ClientUserAgent() != "" || features.OSVersion != "10.0.26200" {
		t.Fatal("primary caller leaked into browser features", features)
	}
	if _, exists := features.Headers["X-Codex-Desktop-Build"]; exists {
		t.Fatal("primary build alias survived role projection")
	}
	if profile.Features.Headers["originator"] != "Codex Desktop" || profile.UserAgent("") != desktop {
		t.Fatal("request projection mutated the login/account profile")
	}
	primary := profile.ForRequest(PlatformFacts{RequestRole: useragent.Primary}, "")
	if got := primary.UserAgent("codex-tui/0.155.0 (Linux 6.8; aarch64) xterm"); got != desktop {
		t.Fatal("ordinary request stopped following selected primary client", got)
	}
	if got := profile.ForRequest(PlatformFacts{RequestRole: useragent.BrowserUse}, "0.157.0").UserAgent(browser); got != strings.Replace(wantBrowser, "/0.156.0", "/0.157.0", 1) {
		t.Fatal("official release lost browser role or caller build", got)
	}
	profile.Features.UserAgent = "Codex Desktop/9.0.0 (Windows 99; x86_64)"
	profile.Features.Headers["Version"] = "9.0.0"
	features.Headers["Version"] = "changed"
	if got := browserProfile.UserAgent(browser); got != wantBrowser || browserProfile.ClientVersion() != "0.156.0" || browserProfile.Features().Headers["Version"] != "0.156.0" {
		t.Fatal("frozen request profile changed after configuration edits", got)
	}
}

func TestBrowserRoleUsesCompleteBrowserSamplesAndTypedAliases(t *testing.T) {
	const observed = "codex-browser-use/0.156.0 (Windows 10; x86_64) unknown (codex-browser-use; 0.2.0)"
	for _, features := range []Features{
		{UserAgent: observed, Originator: "different-thread"},
		{Headers: map[string]string{"user-agent": observed, "originator": "different-thread"}},
	} {
		profile := ProfileRevision{Features: features}.ForRequest(PlatformFacts{RequestRole: useragent.BrowserUse}, "")
		if got := profile.UserAgent("codex-browser-use/0.155.0 (Linux; arm64) unknown (codex-browser-use; 0.1.0)"); got != observed {
			t.Fatal("complete Browser Use observation lost", got)
		}
		if profile.Features().ClientOriginator() != "different-thread" {
			t.Fatal("sampled thread originator was confused with process product")
		}
	}
}

func TestPartialRequestProfileAppliesSelectedReleaseAndHostTuple(t *testing.T) {
	const incoming = "codex-browser-use/0.155.0 (Linux 6.8; aarch64) unknown (codex-browser-use; 0.155.0)"
	profile := ProfileRevision{Tuple: windowsDesktop, ClientVersion: "0.156.0", Features: Features{Originator: "Codex Desktop"}}
	want := "codex-browser-use/0.156.0 (Windows; x86_64) unknown (codex-browser-use; 0.155.0)"
	if got := profile.RequestUserAgent(incoming, ""); got != want {
		t.Fatalf("partial profile UA = %q, want %q", got, want)
	}
	if profile.UserAgent("") != "" {
		t.Fatal("request projection invented an account UA sample")
	}
	if got := profile.ForRequest(PlatformFacts{RequestRole: useragent.BrowserUse}, "").UserAgent(""); got != "" {
		t.Fatal("missing request UA copied the primary client")
	}
}

func TestReferenceFollowingKeepsPrimaryAndBrowserRequestsInOneEnvironment(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	login, err := repo.SyncLoginAccount(ctx, "role-login", account("account"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveReference(ctx, ReferenceSource{ID: "reference", Name: "Desktop reference", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	binding, err := repo.SetBinding(ctx, ProfileBinding{CredentialSessionID: login.CredentialSessionID, Mode: ModeAuto, RevisionID: "builtin-desktop-windows-amd64", ReferenceSourceID: "reference"})
	if err != nil {
		t.Fatal(err)
	}
	const browser = "codex-browser-use/0.155.0 (Windows 10; x86_64) unknown (codex-browser-use; 0.1.0)"
	facts := ProjectPlatform(http.Header{"User-Agent": {browser}, "Originator": {"Codex Desktop"}})
	if facts.RequestRole != useragent.BrowserUse || facts.Tuple.ClientType != clientTypeBrowserUse {
		t.Fatal("thread originator hid Browser Use process", facts)
	}
	at := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	var frozen RequestProfile
	for i, version := range []string{"0.156.0", "0.157.0"} {
		ua := "Codex Desktop/" + version + " (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.9)"
		if err := repo.ObserveClient(ctx, "client", http.Header{"User-Agent": {ua}, "Originator": {"Codex Desktop"}}, at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		candidate, err := repo.EvaluateCandidate(ctx, login.CredentialSessionID, account("account"), Policy{Enabled: true}, facts)
		if err != nil || !candidate.Decision.Allowed || candidate.Binding.Tuple != windowsDesktop {
			t.Fatal("request role changed selected environment", candidate, err)
		}
		target, err := repo.CommitTarget(ctx, candidate)
		if err != nil {
			t.Fatal(err)
		}
		resolved := target.Profile.ForRequest(facts, target.OfficialVersion.Version)
		want := "codex-browser-use/" + version + " (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)"
		if got := resolved.UserAgent(browser); got != want || target.Login.DeviceID != login.DeviceID || target.Binding.ReferenceSourceID != binding.ReferenceSourceID {
			t.Fatal("reference update lost role, shared release or login identity", got, target)
		}
		if i == 0 {
			frozen = resolved
		}
	}
	if got := frozen.UserAgent(browser); !strings.Contains(got, "/0.156.0 ") {
		t.Fatal("later observations changed an in-flight request", got)
	}
}
