package clientdisguise

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestWireClientVersionRequiresAUserAgent(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile ProfileRevision
		want    string
	}{
		{"release only", ProfileRevision{ClientVersion: "2.0.0"}, ""},
		{"header version only", ProfileRevision{Features: Features{Headers: map[string]string{"Version": "2.0.0"}}}, ""},
		{"known UA wins over stale release", ProfileRevision{ClientVersion: "2.0.0", Features: Features{UserAgent: "codex-tui/1.0.0 (Linux; x86_64)"}}, "1.0.0"},
		{"imported UA alias", ProfileRevision{Features: Features{Headers: map[string]string{"user-agent": "codex-tui/2.0.0 (Linux; x86_64)"}}}, "2.0.0"},
		{"typed UA wins over alias", ProfileRevision{Features: Features{UserAgent: "codex-tui/2.0.0", Headers: map[string]string{"User-Agent": "codex-tui/1.0.0"}}}, "2.0.0"},
		{"canonical header wins over casing alias", ProfileRevision{Features: Features{Headers: map[string]string{"User-Agent": "codex-tui/2.0.0", "user-agent": "codex-tui/1.0.0"}}}, "2.0.0"},
		{"stable noncanonical aliases", ProfileRevision{Features: Features{Headers: map[string]string{"USER-AGENT": "codex-tui/2.0.0", "user-agent": "codex-tui/1.0.0"}}}, "2.0.0"},
		{"opaque UA release", ProfileRevision{ClientVersion: "2.0.0", Features: Features{UserAgent: "custom-client"}}, "2.0.0"},
		{"opaque UA feature", ProfileRevision{Features: Features{UserAgent: "custom-client", ClientVersion: "2.0.0"}}, "2.0.0"},
		{"opaque UA header", ProfileRevision{Features: Features{UserAgent: "custom-client", Headers: map[string]string{"version": "2.0.0"}}}, "2.0.0"},
		{"opaque UA alternate header", ProfileRevision{Features: Features{UserAgent: "custom-client", Headers: map[string]string{"X-Client-Version": "2.0.0"}}}, "2.0.0"},
		{"opaque UA without version", ProfileRevision{Features: Features{UserAgent: "custom-client"}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.profile.WireClientVersion(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestExplicitUserAgentVersionMismatchIsRejected(t *testing.T) {
	for _, features := range []Features{
		{UserAgent: "codex-tui/1.0.0 (Linux; x86_64)"},
		{Headers: map[string]string{"user-agent": "codex-tui/1.0.0 (Linux; x86_64)"}},
	} {
		profile := ProfileRevision{ID: "profile", Tuple: windowsDesktop, ClientVersion: "2.0.0", SourceID: "reference", Features: features}
		if err := validateRevision(profile); !errors.Is(err, ErrInvalid) {
			t.Fatalf("inconsistent release accepted: %v", err)
		}
	}
}

func TestLearningDoesNotInheritUserAgentAcrossReleases(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	if err := r.SaveReference(ctx, ReferenceSource{ID: "reference", Name: "Desktop", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	oldUA := "Codex Desktop/1.0.0 (Windows 10.0.26200; x86_64)"
	initial, err := r.LearnSample(ctx, Sample{
		ID: "initial", SourceID: "reference", Tuple: windowsDesktop, ClientVersion: "1.0.0", CapturedAt: at,
		Features: Features{UserAgent: oldUA, ClientVersion: "1.0.0", Originator: "Codex Desktop", Headers: map[string]string{
			"user-agent": oldUA, "Version": "1.0.0", "X-Client-Version": "1.0.0", "X-Stainless-OS": "Windows",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := r.SyncLoginAccount(ctx, "login", account("account"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.SetBinding(ctx, ProfileBinding{CredentialSessionID: "login", Tuple: windowsDesktop, Mode: ModeAuto, RevisionID: initial.Revision.ID, ReferenceSourceID: "reference"})
	if err != nil {
		t.Fatal(err)
	}
	same, err := r.LearnSample(ctx, Sample{ID: "same-release", SourceID: "reference", Tuple: windowsDesktop, ClientVersion: "1.0.0", CapturedAt: at.Add(time.Hour), Features: Features{OSVersion: "10.0.26200"}})
	if err != nil || same.Revision.Features.ClientUserAgent() != oldUA {
		t.Fatal("same-release observation lost UA", err)
	}
	partial, err := r.LearnSample(ctx, Sample{ID: "new-release", SourceID: "reference", Tuple: windowsDesktop, ClientVersion: "2.0.0", CapturedAt: at.Add(2 * time.Hour), Features: Features{ClientVersion: "2.0.0", Headers: map[string]string{"Version": "2.0.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	if partial.Revision.Features.ClientUserAgent() != "" || partial.Revision.WireClientVersion() != "" {
		t.Fatal("partial release reused an older UA", partial.Revision.Features)
	}
	if partial.Revision.Features.Headers["X-Stainless-OS"] != "Windows" || partial.Revision.Features.Originator != "Codex Desktop" {
		t.Fatal("unrelated observations were discarded")
	}
	if initial.Revision.Features.Headers["Version"] != "1.0.0" {
		t.Fatal("previous revision mutated")
	}
	newUA := "Codex Desktop/2.0.0 (Windows 10.0.26200; x86_64)"
	if err := r.ObserveClient(ctx, "client", http.Header{"User-Agent": {newUA}, "Originator": {"Codex Desktop"}}, at.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	candidate, err := r.EvaluateCandidate(ctx, "login", account("account"), Policy{Enabled: true}, PlatformFacts{Tuple: windowsDesktop})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Profile.Features.ClientUserAgent() != newUA || candidate.Profile.WireClientVersion() != "2.0.0" {
		t.Fatal("complete observation did not advance automatic binding")
	}
	preserved, err := r.GetLogin(ctx, "login")
	if err != nil || preserved.DeviceID != login.DeviceID {
		t.Fatal("profile update changed device identity", err)
	}
}

func TestHeaderUserAgentObservationReplacesInheritedTypedValue(t *testing.T) {
	merged := overlayFeatures(Features{UserAgent: "old-agent"}, Features{Headers: map[string]string{"user-agent": "new-agent"}})
	if merged.ClientUserAgent() != "new-agent" {
		t.Fatal("new header observation hidden by inherited typed UA")
	}
}
