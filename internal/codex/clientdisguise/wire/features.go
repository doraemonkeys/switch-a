package wire

import (
	"net/http"
	"strings"
)

func (s *Session) applyProfileHeaders(result http.Header) {
	features := s.features
	updates := cloneMap(features.Headers)
	if updates == nil {
		updates = make(map[string]string)
	}
	originalUA := result.Get("User-Agent")
	if ua, apply := s.profileFeature("user_agent", originalUA); apply && (ua != originalUA || features.ClientUserAgent() != "") {
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

// Header aliases share the resolved request profile, so selected versions and
// caller roles cannot diverge between typed features and imported headers.
func (s *Session) profileHeaderValue(name, observed string) (string, bool) {
	if !featureHeader(name) {
		return "", false
	}
	switch {
	case strings.EqualFold(name, "User-Agent"):
		return s.profileFeature("user_agent", observed)
	case strings.EqualFold(name, "Originator"):
		return s.features.ClientOriginator(), true
	case headerFeatureKind(name) == "feature:client_version":
		return s.profileFeature("client_version", observed)
	default:
		return observed, true
	}
}

// Structured fields use the same request profile as the UA. Independent caller
// builds are not replaced with the primary client's build.
func (s *Session) profileFeature(name, original string) (string, bool) {
	var value string
	switch name {
	case "user_agent":
		value = s.profile.UserAgent(original)
	case "originator":
		value = s.features.ClientOriginator()
	case "client_version":
		value = s.profile.ClientVersion()
	case "desktop_build":
		value = s.features.DesktopBuild
	case "os_version":
		return s.profile.OSVersion()
	}
	return value, value != ""
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
