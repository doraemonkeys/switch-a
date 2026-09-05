package clientdisguise

import "time"

const (
	builtinVersion              = "0.150.0-alpha.8"
	builtinSourceURL            = "https://github.com/openai/codex/blob/rust-v0.150.0-alpha.8/codex-rs/login/src/auth/default_client.rs"
	builtinTUISourceURL         = "https://github.com/openai/codex/blob/rust-v0.150.0-alpha.8/codex-rs/tui/src/lib.rs"
	builtinDesktopCaptureSource = "internal/codex/headers/testdata/codex-desktop-0.150.0-alpha.8/manifest.json"
	builtinDesktopUserAgent     = "Codex Desktop/0.150.0-alpha.8 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.820.60940)"
	builtinDesktopBuild         = "26.820.60940"
	builtinDesktopOSVersion     = "10.0.26200"
)

// Public source fixes the release and originator, while host OS and terminal
// values are runtime observations. Only the repository's captured Windows
// Desktop tuple supplies those additional fields; other defaults preserve them.
func BuiltinProfiles() []ProfileRevision {
	result := make([]ProfileRevision, 0, 18)
	for _, clientType := range []string{"desktop", "tui", "cli"} {
		originator, source := "codex_cli_rs", builtinSourceURL
		if clientType == "desktop" {
			originator = "Codex Desktop"
		}
		if clientType == "tui" {
			originator, source = "codex-tui", builtinTUISourceURL
		}
		for _, platform := range []string{"windows", "linux", "macos"} {
			for _, arch := range []string{"amd64", "arm64"} {
				tuple := Tuple{ClientType: clientType, Platform: platform, Arch: arch}
				profile := ProfileRevision{ID: "builtin-" + clientType + "-" + platform + "-" + arch, Tuple: tuple, ClientVersion: builtinVersion, Features: Features{ClientVersion: builtinVersion, Originator: originator}, SourceID: "builtin", EvidenceKind: "source", SourceURL: source, CreatedAt: time.Unix(0, 0).UTC()}
				if tuple == (Tuple{ClientType: "desktop", Platform: "windows", Arch: "amd64"}) {
					profile.EvidenceKind, profile.SourceURL = "capture", builtinDesktopCaptureSource
					profile.Features.UserAgent, profile.Features.DesktopBuild, profile.Features.OSVersion = builtinDesktopUserAgent, builtinDesktopBuild, builtinDesktopOSVersion
				}
				result = append(result, profile)
			}
		}
	}
	return result
}
