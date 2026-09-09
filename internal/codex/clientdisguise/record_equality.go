package clientdisguise

import (
	"bytes"
	"maps"
)

// SQLite and backup JSON preserve instants, not monotonic clocks or timezone
// objects. Immutable records compare domain values across those round trips.
func (p ProfileRevision) equalImmutable(other ProfileRevision) bool {
	return p.ID == other.ID && p.EvidenceKind == other.EvidenceKind &&
		p.SourceURL == other.SourceURL && p.Tuple == other.Tuple &&
		p.ClientVersion == other.ClientVersion && p.SourceID == other.SourceID &&
		p.CapturedAt.Equal(other.CapturedAt) && p.CreatedAt.Equal(other.CreatedAt) &&
		p.Features.equal(other.Features)
}

func (s Sample) equalImmutable(other Sample) bool {
	return s.ID == other.ID && s.SourceID == other.SourceID &&
		s.CapturedAt.Equal(other.CapturedAt) && s.Tuple == other.Tuple &&
		s.ClientVersion == other.ClientVersion && s.Features.equal(other.Features)
}

func (s TransportSample) equalImmutable(other TransportSample) bool {
	return s.ID == other.ID && s.SourceID == other.SourceID &&
		s.CapturedAt.Equal(other.CapturedAt) && s.Name == other.Name &&
		s.TLSProfile == other.TLSProfile && s.HTTPProfile == other.HTTPProfile &&
		bytes.Equal(s.Config, other.Config)
}

func (i LoginIdentity) equalImmutable(other LoginIdentity) bool {
	return i.CredentialSessionID == other.CredentialSessionID &&
		i.GenerationID == other.GenerationID && i.DeviceID == other.DeviceID &&
		i.AccountBasis.Equal(other.AccountBasis) && i.CreatedAt.Equal(other.CreatedAt)
}

func (h LoginHistory) equalImmutable(other LoginHistory) bool {
	return h.GenerationID == other.GenerationID && h.Identity.equalImmutable(other.Identity)
}

func (f Features) equal(other Features) bool {
	return f.UserAgent == other.UserAgent && f.Originator == other.Originator &&
		f.ClientVersion == other.ClientVersion && f.DesktopBuild == other.DesktopBuild &&
		f.OSVersion == other.OSVersion && maps.Equal(f.Headers, other.Headers)
}
