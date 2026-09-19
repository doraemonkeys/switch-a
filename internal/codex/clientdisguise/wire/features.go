package wire

import (
	"net/http"
	"strings"
)

func (s *Session) applyProfileHeaders(result http.Header) {
	features := s.target.Profile.Features
	updates := cloneMap(features.Headers)
	if updates == nil {
		updates = make(map[string]string)
	}
	originalUA := result.Get("User-Agent")
	if ua := s.profileFeature("user_agent", originalUA); ua != "" && (ua != originalUA || features.ClientUserAgent() != "") {
		updates["User-Agent"] = ua
	}
	if originator := features.ClientOriginator(); originator != "" {
		updates["Originator"] = originator
	}
	for name, observed := range updates {
		value, apply := s.profileHeaderValue(name, observed)
		if !apply {
			continue
		}
		old := result.Get(name)
		result.Set(name, value)
		s.difference("header", name, old, value)
	}
}

// Header aliases share the structured feature decision, so a partial sample
// cannot bypass UA/version coherence through an explicit Version override.
func (s *Session) profileHeaderValue(name, observed string) (string, bool) {
	if !featureHeader(name) {
		return "", false
	}
	switch {
	case strings.EqualFold(name, "User-Agent"):
		value := s.profileFeature("user_agent", observed)
		return value, value != ""
	case strings.EqualFold(name, "Originator"):
		return s.profileFeature("originator", observed), true
	case headerFeatureKind(name) == "feature:client_version":
		value := s.profileFeature("client_version", observed)
		return value, value != ""
	default:
		return observed, true
	}
}

// Structured profile features replace only observed protocol positions. Missing
// OS/build samples must not manufacture an environment from a matching tuple.
func (s *Session) profileFeature(name, original string) string {
	features := s.target.Profile.Features
	switch name {
	case "user_agent":
		return s.target.Profile.RequestUserAgent(original, s.target.OfficialVersion.Version)
	case "originator":
		return features.ClientOriginator()
	case "client_version":
		if s.target.OfficialVersion.Version != "" {
			return s.target.OfficialVersion.Version
		}
		return s.target.Profile.WireClientVersion()
	case "desktop_build":
		return features.DesktopBuild
	case "os_version":
		return features.OSVersion
	}
	return ""
}
func protocolFeatureKind(name string) string {
	switch name {
	case "user_agent", "originator", "client_version", "desktop_build", "os_version":
		return "feature:" + name
	case "version":
		return "feature:client_version"
	}
	return ""
}
func headerFeatureKind(name string) string {
	switch strings.ToLower(name) {
	case "version", "x-client-version", "x-codex-client-version":
		return "feature:client_version"
	case "x-codex-desktop-build":
		return "feature:desktop_build"
	case "x-codex-os-version":
		return "feature:os_version"
	}
	return ""
}
