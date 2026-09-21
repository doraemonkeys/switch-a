package clientdisguise

import (
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/useragent"
)

// RequestProfile freezes the selected login environment and the request's role
// together. All protocol carriers must consume this same decision, including
// metadata that omits the UA or lists its version before its product.
type RequestProfile struct {
	Role    useragent.Role
	source  Tuple
	profile ProfileRevision
	version string
}

func (p ProfileRevision) ForRequest(facts PlatformFacts, version string) RequestProfile {
	return RequestProfile{Role: facts.RequestRole, source: facts.Tuple, profile: p.Clone(), version: version}
}

func (p RequestProfile) projectsRole() bool {
	return p.Role == useragent.BrowserUse &&
		useragent.RequestRole(p.profile.Features.ClientUserAgent(), p.profile.Features.ClientOriginator()) != p.Role
}

func (p RequestProfile) ClientVersion() string {
	if p.version != "" {
		return p.version
	}
	return p.profile.CodexVersion()
}

func (p RequestProfile) UserAgent(original string) string {
	if !p.projectsRole() {
		return p.profile.primaryUserAgent(original, p.ClientVersion())
	}
	// A Browser Use request supplies its own caller build. The primary profile
	// supplies the shared Codex release and host, never a Desktop build suffix.
	ua := WithUserAgentVersion(original, p.ClientVersion())
	return useragent.ProjectEnvironment(ua, p.profile.Features.ClientUserAgent(),
		p.profile.Tuple.Platform, p.profile.Tuple.Arch, p.profile.Features.OSVersion)
}

// An OS release cannot survive a change of operating system when the target
// release is unknown. Empty is then a deliberate projection, not a missing sample.
func (p RequestProfile) OSVersion() (string, bool) {
	if version := p.Features().OSVersion; version != "" {
		return version, true
	}
	return "", p.source.Platform != "" && p.profile.Tuple.Platform != "" && p.source.Platform != p.profile.Tuple.Platform
}

func (p RequestProfile) Features() Features {
	features := p.profile.Features.Clone()
	if version := useragent.OSVersion(p.profile.Features.ClientUserAgent()); version != "" {
		features.OSVersion = version
	}
	if !p.projectsRole() {
		return features
	}
	features.UserAgent = ""
	features.Originator = useragent.BrowserUseOriginator
	features.DesktopBuild = ""
	// Imported aliases are observations of the primary client too; leaving one
	// here would reinsert the Desktop UA after the role projection.
	for name := range features.Headers {
		switch strings.ToLower(name) {
		case "user-agent", "originator", "x-codex-desktop-build":
			delete(features.Headers, name)
		}
	}
	return features
}
