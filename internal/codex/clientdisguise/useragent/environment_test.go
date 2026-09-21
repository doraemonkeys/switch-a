package useragent

import "testing"

func TestEnvironmentProjectionPreservesCallerAndRuntime(t *testing.T) {
	const original = "codex-browser-use/0.155.0 (Linux 6.8; aarch64) Terminal/9 (codex-browser-use; 0.1.0)"
	const suffix = " Terminal/9 (codex-browser-use; 0.1.0)"
	for _, tc := range []struct {
		name, input, sample, platform, arch, os, want string
	}{
		{"captured Desktop host", original, "Codex Desktop/0.156.0 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.9)", "windows", "amd64", "", "codex-browser-use/0.155.0 (Windows 10.0.26200; x86_64)" + suffix},
		{"selected platform without OS sample", original, "", "windows", "amd64", "", "codex-browser-use/0.155.0 (Windows; x86_64)" + suffix},
		{"explicit OS sample", original, "", "windows", "amd64", "10.0.26200", "codex-browser-use/0.155.0 (Windows 10.0.26200; x86_64)" + suffix},
		{"same platform keeps unobserved release", original, "", "linux", "arm64", "", original},
		{"macOS selection", original, "", "macos", "arm64", "25.0", "codex-browser-use/0.155.0 (Darwin 25.0; aarch64)" + suffix},
		{"unknown target does not invent host", original, "opaque", "unknown", "unknown", "", original},
		{"unknown input is opaque", "custom/1 (Linux 6.8; arm64)", "", "windows", "amd64", "", "custom/1 (Linux 6.8; arm64)"},
		{"no input", "", "Codex Desktop/1.0 (Windows; x86_64)", "windows", "amd64", "", ""},
		{"same Windows and alias", "codex-tui/1.0 (Windows 10; x64)", "", "windows", "amd64", "", "codex-tui/1.0 (Windows 10; x64)"},
		{"Mac OS X alias", "codex_exec/1.0 (Mac OS X 15_2; arm64)", "", "macos", "amd64", "", "codex_exec/1.0 (Mac OS X 15_2; x86_64)"},
		{"macOS alias", "codex_cli_rs/1.0 (macOS 15.2; amd64)", "", "macos", "arm64", "", "codex_cli_rs/1.0 (macOS 15.2; aarch64)"},
		{"unknown input arch", "codex-tui/1.0 (Linux 6.8; other)", "", "linux", "amd64", "", "codex-tui/1.0 (Linux 6.8; x86_64)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectEnvironment(tc.input, tc.sample, tc.platform, tc.arch, tc.os); got != tc.want {
				t.Fatalf("UA = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOSVersionRequiresAHostTuple(t *testing.T) {
	for _, tc := range []struct{ ua, want string }{
		{"Codex Desktop/1.0 (Windows 10.0.26200; x86_64) unknown", "10.0.26200"},
		{"codex-browser-use/1.0 (Linux; x86_64) (caller; 9.0)", ""},
		{"custom/1.0 (Windows 10; x86_64)", ""},
	} {
		if got := OSVersion(tc.ua); got != tc.want {
			t.Fatalf("OSVersion(%q) = %q, want %q", tc.ua, got, tc.want)
		}
	}
}

func TestRequestRoleUsesProcessProductBeforeThreadOriginator(t *testing.T) {
	for _, tc := range []struct {
		ua, originator string
		want           Role
	}{
		{"codex-browser-use/0.155.0 (Windows; x86_64)", "Codex Desktop", BrowserUse},
		{"CODEX-BROWSER-USE/0.155.0", "", BrowserUse},
		{"", "codex-browser-use", BrowserUse},
		{"codex-tui/0.155.0 (Linux; x86_64)", "codex-browser-use", Primary},
		{"Codex Desktop/0.155.0 (Windows; x86_64) (codex-browser-use; 0.1.0)", "", Primary},
		{"other/codex-browser-use/0.155.0", "", Primary},
		{"codex-browser-use-extra/0.155.0", "", Primary},
		{"", "", Primary},
	} {
		if got := RequestRole(tc.ua, tc.originator); got != tc.want {
			t.Fatalf("role(%q, %q) = %q, want %q", tc.ua, tc.originator, got, tc.want)
		}
	}
}
