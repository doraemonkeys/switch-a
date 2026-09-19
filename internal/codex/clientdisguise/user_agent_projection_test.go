package clientdisguise

import "testing"

func TestPartialProfileProjectsOnlyKnownEntryPointIdentity(t *testing.T) {
	const incoming = "codex-tui/0.149.0 (Windows 10.0.26200; x86_64) Terminal/1.2 (codex-tui; 0.149.0)"
	for _, tc := range []struct {
		name, originator, original, version, want string
	}{
		{"exec", "codex_exec", incoming, "", "codex_exec/0.149.0 (Windows 10.0.26200; x86_64) Terminal/1.2 (codex_exec; 0.149.0)"},
		{"default", "codex_cli_rs", incoming, "", "codex_cli_rs/0.149.0 (Windows 10.0.26200; x86_64) Terminal/1.2 (codex_cli_rs; 0.149.0)"},
		{"desktop without a host sample", "Codex Desktop", incoming, "", "Codex Desktop/0.149.0 (Windows 10.0.26200; x86_64) Terminal/1.2 (Codex Desktop; 0.149.0)"},
		{"same entry point", "codex-tui", incoming, "", incoming},
		{"official release", "codex_exec", incoming, "0.151.0", "codex_exec/0.151.0 (Windows 10.0.26200; x86_64) Terminal/1.2 (codex_exec; 0.151.0)"},
		{"exec alias", "codex-tui", "codex-exec/0.149.0 (Linux 6.8; arm64) unknown", "", "codex-tui/0.149.0 (Linux 6.8; arm64) unknown"},
		{"embedding app build", "codex_exec", builtinDesktopUserAgent, "", "codex_exec/0.150.0-alpha.8 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.820.60940)"},
		{"unrelated suffix", "codex_exec", incoming + " (codex_cli_rs; 0.149.0)", "", "codex_exec/0.149.0 (Windows 10.0.26200; x86_64) Terminal/1.2 (codex_exec; 0.149.0) (codex_cli_rs; 0.149.0)"},
		{"thread originator", "custom-thread", incoming, "", incoming},
		{"no selected originator", "", incoming, "", incoming},
		{"no incoming UA", "codex_exec", "", "", ""},
		{"opaque incoming UA", "codex_exec", "custom-client/1.0 (Windows; x86_64)", "", "custom-client/1.0 (Windows; x86_64)"},
		{"embedded Codex token", "codex_exec", "custom-client/1.0 (" + incoming + ")", "", "custom-client/1.0 (" + incoming + ")"},
		{"unknown product version", "codex_exec", "codex-tui/unknown (Linux; x86_64)", "", "codex-tui/unknown (Linux; x86_64)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := ProfileRevision{ClientVersion: "9.9.9", Features: Features{Originator: tc.originator}}
			if got := profile.RequestUserAgent(tc.original, tc.version); got != tc.want {
				t.Fatalf("request UA = %q, want %q", got, tc.want)
			}
			if profile.UserAgent(tc.version) != "" {
				t.Fatal("request projection manufactured an account UA sample")
			}
		})
	}
}

func TestRequestUserAgentPreservesCompleteObservations(t *testing.T) {
	const observed = "codex-tui/0.149.0 (Linux 6.8; arm64) xterm"
	for _, features := range []Features{
		{UserAgent: observed, Originator: "codex_exec"},
		{Headers: map[string]string{"user-agent": observed, "originator": "codex_exec"}},
	} {
		profile := ProfileRevision{Features: features}
		if got := profile.RequestUserAgent("Codex Desktop/0.148.0 (Windows; x86_64)", ""); got != observed {
			t.Fatalf("complete sample was rewritten to match its thread originator: %q", got)
		}
	}
}

func TestOriginatorAliasesUseTheSamePrecedenceAsTheUserAgent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		features Features
		want     string
	}{
		{"typed", Features{Originator: "codex_exec", Headers: map[string]string{"Originator": "codex-tui", "originator": "codex_cli_rs"}}, "codex_exec"},
		{"canonical", Features{Headers: map[string]string{"Originator": "codex_exec", "originator": "codex-tui"}}, "codex_exec"},
		{"noncanonical", Features{Headers: map[string]string{"originator": "codex_exec"}}, "codex_exec"},
		{"missing", Features{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.features.ClientOriginator(); got != tc.want {
				t.Fatalf("originator = %q, want %q", got, tc.want)
			}
			profile := ProfileRevision{Features: tc.features}
			wantUA := "codex-tui/0.149.0 (Linux; x86_64)"
			if tc.want != "" {
				wantUA = tc.want + "/0.149.0 (Linux; x86_64)"
			}
			if got := profile.RequestUserAgent("codex-tui/0.149.0 (Linux; x86_64)", ""); got != wantUA {
				t.Fatalf("UA ignored the selected originator: %q", got)
			}
		})
	}
}
