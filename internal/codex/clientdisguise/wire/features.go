package wire

import "strings"

// Structured profile features replace only observed protocol positions. Missing
// OS/build samples must not manufacture an environment from a matching tuple.
func (s *Session) profileFeature(name string) string {
	features := s.target.Profile.Features
	switch name {
	case "user_agent":
		return features.UserAgent
	case "originator":
		return features.Originator
	case "client_version":
		return s.target.Profile.ClientVersion
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
