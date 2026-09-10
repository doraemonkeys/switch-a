package clientdisguise

import (
	"regexp"
	"strings"
)

var productSuffixVersion = regexp.MustCompile(`(?i)(\((?:codex_cli_rs|codex-tui|codex_desktop|Codex Desktop|codex); )([^ )]+)(\))`)

// An explicit release selection changes only the Codex product version. Desktop
// build numbers, OS releases and terminal/runtime versions remain observations.
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

// A known release without a sampled UA is not a complete client fingerprint.
// Existing stored profiles may also contain a UA inherited from an older release;
// its actual version remains authoritative until a complete observation arrives.
func (p ProfileRevision) WireClientVersion() string {
	ua := p.Features.ClientUserAgent()
	if ua == "" {
		return ""
	}
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
