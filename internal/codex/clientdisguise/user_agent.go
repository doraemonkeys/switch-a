package clientdisguise

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/useragent"
)

var versionPattern = regexp.MustCompile(`(?i)codex[_ /-]*(?:desktop|cli(?:_rs)?|tui|exec|browser-use)?[/ ]([0-9]+(?:\.[0-9]+){1,3}(?:[-+][A-Za-z0-9.-]+)?)`)

// CLI suffixes can repeat the Codex release. App-server callers such as Desktop
// and Browser Use report independent versions, even when those happen to match.
var productSuffixVersion = regexp.MustCompile(`(?i)(\((?:codex_cli_rs|codex-tui|codex_exec|codex-exec|codex); )([^ )]+)(\))`)

// An explicit release selection changes only the Codex product version. Desktop
// and Browser Use builds, OS releases and terminal/runtime versions remain observations.
func WithUserAgentVersion(ua, version string) string {
	match := versionPattern.FindStringSubmatchIndex(ua)
	if len(match) != 4 || version == "" {
		return ua
	}
	previous := ua[match[2]:match[3]]
	ua = ua[:match[2]] + version + ua[match[3]:]
	return productSuffixVersion.ReplaceAllStringFunc(ua, func(suffix string) string {
		parts := productSuffixVersion.FindStringSubmatch(suffix)
		if parts[2] != previous {
			return suffix
		}
		return parts[1] + version + parts[3]
	})
}

// UserAgent projects the same selected release into proxy and account requests.
func (p ProfileRevision) UserAgent(version string) string {
	return WithUserAgentVersion(p.Features.ClientUserAgent(), version)
}

// A partial profile can select a known entry point without inventing its host
// environment. Complete observations remain authoritative, including captures
// whose thread originator differs from the process named in the UA.
func (p ProfileRevision) RequestUserAgent(original, version string) string {
	return p.ForRequest(ProjectPlatform(http.Header{"User-Agent": {original}}), version).UserAgent(original)
}

func (p ProfileRevision) primaryUserAgent(original, version string) string {
	if sampled := p.UserAgent(version); sampled != "" {
		return sampled
	}
	// Resolve version ownership before renaming the entry point; otherwise a CLI
	// suffix projected into an app-server caller would lose its release linkage.
	ua := withUserAgentOriginator(WithUserAgentVersion(original, version), p.Features.ClientOriginator())
	return useragent.ProjectEnvironment(ua, "", p.Tuple.Platform, p.Tuple.Arch, p.Features.OSVersion)
}

func withUserAgentOriginator(ua, originator string) string {
	knownEntryPoint := false
	for _, client := range clientTypes {
		if originator == client.originator {
			knownEntryPoint = true
			break
		}
	}
	if !knownEntryPoint {
		return ua
	}
	match := versionPattern.FindStringSubmatchIndex(ua)
	if len(match) != 4 || match[0] != 0 {
		return ua
	}
	previousProduct := strings.TrimRight(ua[:match[2]], "/ ")
	if previousProduct == originator {
		return ua
	}
	previousVersion := ua[match[2]:match[3]]
	ua = originator + "/" + ua[match[2]:]
	return productSuffixVersion.ReplaceAllStringFunc(ua, func(suffix string) string {
		parts := productSuffixVersion.FindStringSubmatch(suffix)
		// Repeated product/version pairs follow the selected entry point. A
		// different suffix build identifies the embedding app and stays intact.
		if !strings.EqualFold(parts[1], "("+previousProduct+"; ") || parts[2] != previousVersion {
			return suffix
		}
		return "(" + originator + "; " + previousVersion + ")"
	})
}

func userAgentVersion(userAgent string) string {
	match := versionPattern.FindStringSubmatch(userAgent)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

// Typed features take precedence over imported header aliases everywhere a
// profile is consumed, so learning and transmission cannot select different UAs.
func (f Features) ClientUserAgent() string {
	if f.UserAgent != "" {
		return f.UserAgent
	}
	return profileHeader(f.Headers, "User-Agent")
}

func (f Features) ClientOriginator() string {
	if f.Originator != "" {
		return f.Originator
	}
	return profileHeader(f.Headers, "Originator")
}

// A selected release does not depend on capturing a complete host fingerprint.
// When a UA is present, its actual release wins over inconsistent imported fields.
func (p ProfileRevision) CodexVersion() string {
	ua := p.Features.ClientUserAgent()
	if version := userAgentVersion(ua); version != "" {
		return version
	}
	if p.ClientVersion != "" {
		return p.ClientVersion
	}
	if p.Features.ClientVersion != "" {
		return p.Features.ClientVersion
	}
	if version := profileHeader(p.Features.Headers, "Version"); version != "" {
		return version
	}
	return profileHeader(p.Features.Headers, "X-Client-Version")
}

func profileHeader(headers map[string]string, name string) string {
	if value, ok := headers[name]; ok {
		return value
	}
	// Imported maps are case-sensitive; selecting aliases must still be stable
	// across calls that project the UA and its version into different carriers.
	selected := ""
	for key := range headers {
		if strings.EqualFold(key, name) && (selected == "" || key < selected) {
			selected = key
		}
	}
	return headers[selected]
}

func withoutPreviousRelease(features Features) Features {
	result := features.Clone()
	result.UserAgent, result.ClientVersion = "", ""
	for name := range result.Headers {
		switch strings.ToLower(name) {
		case "user-agent", "version", "x-client-version", "x-codex-client-version":
			delete(result.Headers, name)
		}
	}
	return result
}
