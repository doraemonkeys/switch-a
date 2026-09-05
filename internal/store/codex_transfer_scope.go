package store

import (
	"bytes"
	"encoding/hex"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"slices"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
)

type codexTransferSelection struct {
	result                                              *CodexState
	providerIDs, sessionIDs                             []string
	generations, clients, sources, profiles, transports map[string]bool
}

// Select follows references rather than truncating tables independently:
// archived login generations still own mappings and conversations.
func (state *CodexState) Select(providerIDs, sessionIDs []string) *CodexState {
	if state == nil {
		return nil
	}
	selection := codexTransferSelection{
		result:      &CodexState{Version: state.Version, HMAC: slices.Clone(state.HMAC)},
		providerIDs: providerIDs, sessionIDs: sessionIDs,
		generations: map[string]bool{}, clients: map[string]bool{}, sources: map[string]bool{}, profiles: map[string]bool{}, transports: map[string]bool{},
	}
	selection.selectLogins(state.Disguise)
	selection.selectFeatures(state.Disguise)
	selection.selectContinuity(state.Continuity)
	selection.selectSticky(state)
	for _, owner := range selection.result.Continuity {
		for _, alias := range state.ClientIdentity.Aliases {
			if alias.Version == owner.ClientKeyVersion && bytes.Equal(alias.Digest, owner.ClientDigest) {
				selection.clients[alias.ClientID] = true
			}
		}
	}
	for _, client := range state.ClientIdentity.Clients {
		if selection.clients[client.ID] {
			selection.result.ClientIdentity.Clients = append(selection.result.ClientIdentity.Clients, client)
		}
	}
	for _, alias := range state.ClientIdentity.Aliases {
		if selection.clients[alias.ClientID] {
			selection.result.ClientIdentity.Aliases = append(selection.result.ClientIdentity.Aliases, alias)
		}
	}
	return selection.result
}
func (s *codexTransferSelection) selectLogins(state clientdisguise.Snapshot) {
	for _, login := range state.Logins {
		if slices.Contains(s.sessionIDs, login.CredentialSessionID) {
			s.result.Disguise.Logins = append(s.result.Disguise.Logins, login)
			s.generations[login.GenerationID] = true
		}
	}
	for _, history := range state.LoginHistory {
		if slices.Contains(s.sessionIDs, history.Identity.CredentialSessionID) {
			s.result.Disguise.LoginHistory = append(s.result.Disguise.LoginHistory, history)
			s.generations[history.GenerationID] = true
		}
	}
	for _, binding := range state.Bindings {
		if !slices.Contains(s.sessionIDs, binding.CredentialSessionID) {
			continue
		}
		s.result.Disguise.Bindings = append(s.result.Disguise.Bindings, binding)
		s.sources[binding.ReferenceSourceID] = true
		s.profiles[binding.RevisionID] = true
		s.transports[binding.TransportSampleID] = true
	}
	for _, mapping := range state.Mappings {
		if s.generations[mapping.GenerationID] {
			s.result.Disguise.Mappings = append(s.result.Disguise.Mappings, mapping)
			s.clients[mapping.ClientIdentityID] = true
		}
	}
}
func (s *codexTransferSelection) selectFeatures(state clientdisguise.Snapshot) {
	// A manually pinned revision may name its source without an automatic-follow
	// source on the binding; discover it before selecting the learning watermark.
	for _, profile := range state.Profiles {
		if s.profiles[profile.ID] {
			s.sources[profile.SourceID] = true
		}
	}
	for _, transport := range state.TransportSamples {
		if s.transports[transport.ID] {
			s.sources[transport.SourceID] = true
		}
	}
	for _, track := range state.Tracks {
		if s.sources[track.SourceID] {
			s.result.Disguise.Tracks = append(s.result.Disguise.Tracks, track)
			s.profiles[track.RevisionID] = true
		}
	}
	for _, profile := range state.Profiles {
		if s.profiles[profile.ID] || s.sources[profile.SourceID] {
			s.result.Disguise.Profiles = append(s.result.Disguise.Profiles, profile)
		}
	}
	for _, transport := range state.TransportSamples {
		if s.transports[transport.ID] {
			s.result.Disguise.TransportSamples = append(s.result.Disguise.TransportSamples, transport)
			s.sources[transport.SourceID] = true
		}
	}
	for _, sample := range state.Samples {
		if s.sources[sample.SourceID] {
			s.result.Disguise.Samples = append(s.result.Disguise.Samples, sample)
		}
	}
	for _, reference := range state.References {
		if s.sources[reference.ID] {
			s.result.Disguise.References = append(s.result.Disguise.References, reference)
			s.clients[reference.ClientIdentityID] = true
		}
	}
}
func (s *codexTransferSelection) selectContinuity(rows []continuitysqlite.TransferBinding) {
	for _, owner := range rows {
		if s.includesOwner(owner) {
			s.result.Continuity = append(s.result.Continuity, owner)
		}
	}
}
func (s *codexTransferSelection) includesOwner(owner continuitysqlite.TransferBinding) bool {
	if slices.Contains(s.providerIDs, owner.RouteTargetHint) {
		return true
	}
	for _, login := range s.result.Disguise.Logins {
		if ownerMatchesBasis(owner, login.AccountBasis) {
			return true
		}
	}
	for _, history := range s.result.Disguise.LoginHistory {
		if ownerMatchesBasis(owner, history.Identity.AccountBasis) {
			return true
		}
	}
	return false
}
func ownerMatchesBasis(owner continuitysqlite.TransferBinding, basis clientdisguise.AccountBasis) bool {
	if owner.ProtocolSubjectAccount != nil && basis.Kind == "account" && string(basis.Value) == *owner.ProtocolSubjectAccount {
		return true
	}
	return owner.ProtocolSubjectKeyVersion != nil && basis.KeyVersion == *owner.ProtocolSubjectKeyVersion && bytes.Equal(basis.Value, owner.ProtocolSubjectDigest)
}

func (s *codexTransferSelection) selectSticky(state *CodexState) {
	for _, entry := range state.Sticky {
		if !slices.Contains(s.providerIDs, entry.ProviderID) {
			continue
		}
		s.result.Sticky = append(s.result.Sticky, entry)
		for _, alias := range state.ClientIdentity.Aliases {
			var digest [codexidentity.DigestSize]byte
			if len(alias.Digest) != len(digest) {
				continue
			}
			copy(digest[:], alias.Digest)
			scope, err := codexidentity.ClientScopeFromDigest(alias.Version, digest)
			if err != nil {
				continue
			}
			encoded, err := scope.MarshalBinary()
			if err == nil && hex.EncodeToString(encoded) == entry.Key.ClientScope {
				s.clients[alias.ClientID] = true
			}
		}
	}
}
