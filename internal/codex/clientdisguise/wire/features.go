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
	if ua := features.ClientUserAgent(); ua != "" {
		updates["User-Agent"] = ua
	}
	if features.Originator != "" {
		updates["Originator"] = features.Originator
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
		value := s.profileFeature("user_agent")
		return value, value != ""
	case headerFeatureKind(name) == "feature:client_version":
		value := s.profileFeature("client_version")
		return value, value != ""
	default:
		return observed, true
	}
}

// Structured profile features replace only observed protocol positions. Missing
// OS/build samples must not manufacture an environment from a matching tuple.
func (s *Session) profileFeature(name string) string {
	features := s.target.Profile.Features
	switch name {
	case "user_agent":
		return features.ClientUserAgent()
	case "originator":
		return features.Originator
	case "client_version":
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
