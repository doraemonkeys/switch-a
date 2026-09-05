package clientdisguise

import (
	"net/http"
	"regexp"
	"strings"
)

type PlatformEvidence struct {
	Field    string `json:"field"`
	Value    string `json:"value"`
	Platform string `json:"platform"`
}
type PlatformFacts struct {
	Tuple    Tuple              `json:"tuple"`
	Conflict bool               `json:"conflict"`
	Evidence []PlatformEvidence `json:"evidence"`
}
type PlatformDecision struct {
	Allowed         bool          `json:"allowed"`
	Reason          string        `json:"reason"`
	Facts           PlatformFacts `json:"facts"`
	ProfilePlatform string        `json:"profile_platform"`
}
type Candidate struct {
	CredentialSessionID string
	AccountBasis        AccountBasis
	Policy              Policy
	Facts               PlatformFacts
	Profile             ProfileRevision
	Binding             *ProfileBinding
	Transport           *TransportSample
	Decision            PlatformDecision
}

var versionPattern = regexp.MustCompile(`(?i)(?:codex[_ /-]*(?:desktop|cli(?:_rs)?|tui)?[/ ]|codex_desktop/)([0-9]+(?:\.[0-9]+){1,3}(?:[-+][A-Za-z0-9.-]+)?)`)

func ProjectPlatform(headers http.Header) PlatformFacts {
	var facts PlatformFacts
	ua := headers.Get("User-Agent")
	for _, field := range []string{"User-Agent", "X-Stainless-OS", "X-Client-Platform", "Sec-CH-UA-Platform"} {
		value := headers.Get(field)
		platform := detectPlatform(value)
		if value == "" {
			continue
		}
		facts.Evidence = append(facts.Evidence, PlatformEvidence{Field: field, Value: value, Platform: platform})
		if platform == "" {
			continue
		}
		if facts.Tuple.Platform != "" && facts.Tuple.Platform != platform {
			facts.Conflict = true
		} else {
			facts.Tuple.Platform = platform
		}
	}
	combined := strings.ToLower(ua + " " + headers.Get("Originator"))
	switch {
	case strings.Contains(combined, "desktop"), strings.Contains(combined, "electron"):
		facts.Tuple.ClientType = "desktop"
	case strings.Contains(combined, "tui"):
		facts.Tuple.ClientType = "tui"
	case strings.Contains(combined, "codex_cli"), strings.Contains(combined, "codex-cli"), strings.Contains(combined, "codex/"):
		facts.Tuple.ClientType = "cli"
	}
	archInput := strings.ToLower(ua + " " + headers.Get("X-Stainless-Arch") + " " + headers.Get("Sec-CH-UA-Arch"))
	switch {
	case strings.Contains(archInput, "aarch64"), strings.Contains(archInput, "arm64"):
		facts.Tuple.Arch = "arm64"
	case strings.Contains(archInput, "x86_64"), strings.Contains(archInput, "x64"), strings.Contains(archInput, "amd64"), strings.Contains(archInput, "win64"):
		facts.Tuple.Arch = "amd64"
	}
	return facts
}

func detectPlatform(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "windows"), strings.Contains(value, "win32"), strings.Contains(value, "win64"):
		return "windows"
	case strings.Contains(value, "linux"):
		return "linux"
	case strings.Contains(value, "darwin"), strings.Contains(value, "mac os"), strings.Contains(value, "macos"), strings.Contains(value, "macintosh"):
		return "macos"
	default:
		return ""
	}
}

func EvaluatePlatform(policy Policy, facts PlatformFacts, profile Tuple) PlatformDecision {
	decision := PlatformDecision{Allowed: true, Reason: "allowed", Facts: facts, ProfilePlatform: profile.Platform}
	if !policy.Enabled {
		decision.Reason = "disabled"
		return decision
	}
	if !policy.PlatformMatching() {
		decision.Reason = "platform_matching_disabled"
		return decision
	}
	switch {
	case facts.Conflict:
		decision.Allowed = false
		decision.Reason = "platform_conflict"
	case facts.Tuple.Platform == "" && policy.UnknownPlatform != UnknownAllowCurrent:
		decision.Allowed = false
		decision.Reason = "platform_unknown"
	case facts.Tuple.Platform != "" && facts.Tuple.Platform != profile.Platform:
		decision.Allowed = false
		decision.Reason = "platform_mismatch"
	}
	return decision
}
