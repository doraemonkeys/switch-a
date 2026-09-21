package useragent

import (
	"regexp"
	"strings"
)

// Only the Codex host tuple has environment semantics. Caller and terminal
// suffixes may contain similar text but belong to independent applications.
var hostPattern = regexp.MustCompile(`(?i)^codex(?:[ _-](?:desktop|cli(?:_rs)?|tui|exec|browser-use))?/[^ ()]+ \((Windows|Linux|Darwin|macOS|Mac OS X)([^;()]*); ([^()]+)\)`)

var platforms = map[string]string{"windows": "Windows", "linux": "Linux", "macos": "Darwin"}
var architectures = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}

func platform(system string) string {
	switch strings.ToLower(system) {
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	case "darwin", "macos", "mac os x":
		return "macos"
	}
	return ""
}

func architecture(value string) string {
	switch strings.ToLower(value) {
	case "x86_64", "amd64", "x64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return ""
}

// ProjectEnvironment shares host observations across request roles without
// copying the primary client's terminal or embedding-app identity.
func ProjectEnvironment(original, sampled, targetPlatform, targetArch, osVersion string) string {
	match := hostPattern.FindStringSubmatchIndex(original)
	if match == nil {
		return original
	}
	if target := hostPattern.FindStringSubmatchIndex(sampled); target != nil {
		return original[:match[2]] + sampled[target[2]:target[7]] + original[match[7]:]
	}
	system := original[match[2]:match[3]]
	release := original[match[4]:match[5]]
	arch := original[match[6]:match[7]]
	if selected := platforms[targetPlatform]; selected != "" && platform(system) != targetPlatform {
		system, release = selected, ""
	}
	if osVersion != "" {
		release = " " + osVersion
	}
	if selected := architectures[targetArch]; selected != "" && architecture(arch) != targetArch {
		arch = selected
	}
	return original[:match[2]] + system + release + "; " + arch + original[match[7]:]
}

func OSVersion(ua string) string {
	match := hostPattern.FindStringSubmatch(ua)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[2])
}
