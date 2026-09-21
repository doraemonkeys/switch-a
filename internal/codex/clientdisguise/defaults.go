package clientdisguise

import "time"

const (
	builtinVersion              = "0.150.0-alpha.8"
	builtinSourceURL            = "https://github.com/openai/codex/blob/rust-v0.150.0-alpha.8/codex-rs/login/src/auth/default_client.rs"
	builtinTUISourceURL         = "https://github.com/openai/codex/blob/rust-v0.150.0-alpha.8/codex-rs/tui/src/lib.rs"
	builtinExecSourceURL        = "https://github.com/openai/codex/blob/rust-v0.150.0-alpha.8/codex-rs/exec/src/lib.rs"
	builtinBrowserUseSourceURL  = "https://github.com/openai/codex/blob/rust-v0.150.0-alpha.8/codex-rs/app-server/src/request_processors/initialize_processor.rs"
	builtinDesktopCaptureSource = "internal/codex/headers/testdata/codex-desktop-0.150.0-alpha.8/manifest.json"
	builtinDesktopUserAgent     = "Codex Desktop/0.150.0-alpha.8 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.820.60940)"
	builtinDesktopBuild         = "26.820.60940"
	builtinDesktopOSVersion     = "10.0.26200"
)

// BuiltinAccountProfile supplies the captured environment for account operations
// that have no sampled UA. Reusing this tuple keeps its provenance and version
// projection identical to the client-disguise feature.
func BuiltinAccountProfile() ProfileRevision {
	return ProfileRevision{
		ID:            "builtin-desktop-windows-amd64",
		Tuple:         Tuple{ClientType: clientTypeDesktop, Platform: "windows", Arch: "amd64"},
		ClientVersion: builtinVersion,
		Features: Features{
			ClientVersion: builtinVersion, Originator: "Codex Desktop",
			UserAgent: builtinDesktopUserAgent, DesktopBuild: builtinDesktopBuild, OSVersion: builtinDesktopOSVersion,
		},
		SourceID: "builtin", EvidenceKind: "capture", SourceURL: builtinDesktopCaptureSource,
		CreatedAt: time.Unix(0, 0).UTC(),
	}
}

// Public source fixes the release and originator, while host OS and terminal
// values are runtime observations. Only the repository's captured Windows
// Desktop tuple supplies a complete UA; other defaults project their selected
// release, entry point and host tuple while retaining unobserved caller details.
func BuiltinProfiles() []ProfileRevision {
	platforms := []string{"windows", "linux", "macos"}
	architectures := []string{"amd64", "arm64"}
	result := make([]ProfileRevision, 0, len(clientTypes)*len(platforms)*len(architectures))
	for _, client := range clientTypes {
		for _, platform := range platforms {
			for _, arch := range architectures {
				tuple := Tuple{ClientType: client.name, Platform: platform, Arch: arch}
				profile := ProfileRevision{ID: "builtin-" + client.name + "-" + platform + "-" + arch, Tuple: tuple, ClientVersion: builtinVersion, Features: Features{ClientVersion: builtinVersion, Originator: client.originator}, SourceID: "builtin", EvidenceKind: "source", SourceURL: client.sourceURL, CreatedAt: time.Unix(0, 0).UTC()}
				if tuple == (Tuple{ClientType: clientTypeDesktop, Platform: "windows", Arch: "amd64"}) {
					profile = BuiltinAccountProfile()
				}
				result = append(result, profile)
			}
		}
	}
	return result
}
