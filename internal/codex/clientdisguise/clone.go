package clientdisguise

import "maps"

func (p Policy) Clone() Policy {
	if p.MatchPlatform != nil {
		value := *p.MatchPlatform
		p.MatchPlatform = &value
	}
	return p
}
func (f Features) Clone() Features               { f.Headers = maps.Clone(f.Headers); return f }
func (p ProfileRevision) Clone() ProfileRevision { p.Features = p.Features.Clone(); return p }
func (l LoginIdentity) Clone() LoginIdentity {
	l.AccountBasis.Value = append([]byte(nil), l.AccountBasis.Value...)
	return l
}
func (b ProfileBinding) Clone() ProfileBinding {
	b.TelemetryPathMappings = maps.Clone(b.TelemetryPathMappings)
	return b
}
func (f PlatformFacts) Clone() PlatformFacts {
	f.Evidence = append([]PlatformEvidence(nil), f.Evidence...)
	return f
}
func (c Candidate) Clone() Candidate {
	c.AccountBasis.Value = append([]byte(nil), c.AccountBasis.Value...)
	c.Policy = c.Policy.Clone()
	c.Facts = c.Facts.Clone()
	c.Profile = c.Profile.Clone()
	c.Decision.Facts = c.Decision.Facts.Clone()
	if c.Binding != nil {
		binding := c.Binding.Clone()
		c.Binding = &binding
	}
	if c.Transport != nil {
		transport := *c.Transport
		transport.Config = append([]byte(nil), transport.Config...)
		c.Transport = &transport
	}
	return c
}
func (s TargetSnapshot) Clone() TargetSnapshot {
	s.Policy = s.Policy.Clone()
	s.Login = s.Login.Clone()
	s.Binding = s.Binding.Clone()
	s.Profile = s.Profile.Clone()
	if s.Transport != nil {
		transport := *s.Transport
		transport.Config = append([]byte(nil), transport.Config...)
		s.Transport = &transport
	}
	return s
}
