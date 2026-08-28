// Package codexidentity defines the one security identity boundary shared by
// Codex continuity, provider authentication, selection, and provider cookies.
package codexidentity

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

// AuthorityResolver consumes only the immutable credential-session projection;
// it never reaches back to Provider credential fields or persistence.
type AuthorityResolver struct{}

// NewAuthorityResolver documents that the resolver is stateless and its zero
// value is fully usable. The constructor exists for composition roots that
// prefer explicit capability wiring.
func NewAuthorityResolver() AuthorityResolver { return AuthorityResolver{} }

// CandidateSnapshot is the complete, immutable selection-time security
// projection. SecretData and mutable AuthState diagnostics are intentionally
// discarded during resolution.
type CandidateSnapshot struct {
	routeTargetID       string
	credentialSessionID string
	credentialVersion   int64
	credential          credentialsession.Snapshot
	protocolScope       ProtocolScope
}

func (AuthorityResolver) Resolve(
	route credentialsession.RouteSnapshot,
	apiType string,
	finalURL *url.URL,
) (CandidateSnapshot, error) {
	routeTargetID := strings.TrimSpace(route.RouteTargetID)
	requestedAPIType := strings.TrimSpace(apiType)
	snapshotAPIType := strings.TrimSpace(route.APIType)
	if routeTargetID == "" {
		return CandidateSnapshot{}, errorOf(ErrorInvalidInput, "route_target_id", "route target is empty", nil)
	}
	if requestedAPIType == "" || snapshotAPIType == "" {
		return CandidateSnapshot{}, errorOf(ErrorInvalidInput, "api_type", "API type is empty", nil)
	}
	if requestedAPIType != snapshotAPIType {
		return CandidateSnapshot{}, errorOf(ErrorSnapshotConflict, "api_type", "requested API type conflicts with the frozen route snapshot", nil)
	}
	credential := route.Credential
	if strings.TrimSpace(credential.SessionID) == "" || credential.Version < 1 || !credentialsession.IsValidKind(credential.Kind) {
		return CandidateSnapshot{}, errorOf(ErrorInvalidInput, "credential_snapshot", "credential identity fields are invalid", nil)
	}
	subject, err := CredentialSubjectFromSession(credential.Subject)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	if credential.Kind == credentialsession.KindChatGPT && subject.Kind() != CredentialSubjectAccount {
		return CandidateSnapshot{}, errorOf(ErrorSnapshotConflict, "credential_subject", "ChatGPT credentials require an account subject", nil)
	}
	origin, err := OriginFromRequestURL(finalURL)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	authority, err := NewUpstreamAuthority(route.VendorScope, origin, subject)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	scope, err := NewProtocolScope(authority, requestedAPIType)
	if err != nil {
		return CandidateSnapshot{}, err
	}
	return CandidateSnapshot{
		routeTargetID:       routeTargetID,
		credentialSessionID: strings.TrimSpace(credential.SessionID),
		credentialVersion:   credential.Version,
		credential:          cloneCredentialSnapshot(credential),
		protocolScope:       scope,
	}, nil
}

func (s CandidateSnapshot) RouteTargetID() string       { return s.routeTargetID }
func (s CandidateSnapshot) CredentialSessionID() string { return s.credentialSessionID }
func (s CandidateSnapshot) CredentialVersion() int64    { return s.credentialVersion }

// Credential returns a defensive clone of the exact session snapshot used to
// derive Authority. Authentication must use this value rather than re-reading
// mutable provider/session state and creating a torn selection lease.
func (s CandidateSnapshot) Credential() credentialsession.Snapshot {
	return cloneCredentialSnapshot(s.credential)
}
func (s CandidateSnapshot) Authority() UpstreamAuthority { return s.protocolScope.authority }
func (s CandidateSnapshot) ProtocolScope() ProtocolScope { return s.protocolScope }
func (s CandidateSnapshot) APIType() string              { return s.protocolScope.apiType }

func (s CandidateSnapshot) ValidateApplied(actual AppliedIdentity) error {
	return ValidateAppliedIdentity(s.Authority(), actual)
}

func (s CandidateSnapshot) String() string {
	return fmt.Sprintf(
		"candidate-snapshot(route_target=%s,credential_session=%s,credential_version=%d,scope=%s)",
		safeLabel(s.routeTargetID),
		safeLabel(s.credentialSessionID),
		s.credentialVersion,
		s.protocolScope,
	)
}

func (s CandidateSnapshot) GoString() string { return s.String() }

func (s CandidateSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RouteTargetID       string        `json:"route_target_id"`
		CredentialSessionID string        `json:"credential_session_id"`
		CredentialVersion   int64         `json:"credential_version"`
		ProtocolScope       ProtocolScope `json:"protocol_scope"`
	}{
		RouteTargetID:       safeLabel(s.routeTargetID),
		CredentialSessionID: safeLabel(s.credentialSessionID),
		CredentialVersion:   s.credentialVersion,
		ProtocolScope:       s.protocolScope,
	})
}

type AppliedIdentity struct {
	authority UpstreamAuthority
}

func NewAppliedIdentity(vendor string, origin NormalizedOrigin, subject CredentialSubject) (AppliedIdentity, error) {
	authority, err := NewUpstreamAuthority(vendor, origin, subject)
	if err != nil {
		return AppliedIdentity{}, err
	}
	return AppliedIdentity{authority: authority}, nil
}

// AppliedIdentityFromRequest forces auth adapters to use the final URL of the
// physical attempt instead of a base URL or route-target label.
func AppliedIdentityFromRequest(vendor string, finalURL *url.URL, subject CredentialSubject) (AppliedIdentity, error) {
	origin, err := OriginFromRequestURL(finalURL)
	if err != nil {
		return AppliedIdentity{}, err
	}
	return NewAppliedIdentity(vendor, origin, subject)
}

func (i AppliedIdentity) Authority() UpstreamAuthority            { return i.authority }
func (i AppliedIdentity) Equal(other AppliedIdentity) bool        { return i == other }
func (i AppliedIdentity) Matches(expected UpstreamAuthority) bool { return i.authority == expected }

func (i AppliedIdentity) String() string {
	return fmt.Sprintf("applied-identity(%s)", i.authority)
}

func (i AppliedIdentity) GoString() string { return i.String() }

func (i AppliedIdentity) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Authority UpstreamAuthority `json:"authority"`
	}{Authority: i.authority})
}

func ValidateAppliedIdentity(expected UpstreamAuthority, actual AppliedIdentity) error {
	if actual.Matches(expected) {
		return nil
	}
	return &AppliedIdentityMismatch{
		Vendor:  expected.vendor != actual.authority.vendor,
		Origin:  expected.origin != actual.authority.origin,
		Subject: expected.subject != actual.authority.subject,
	}
}

func cloneCredentialSnapshot(snapshot credentialsession.Snapshot) credentialsession.Snapshot {
	clone := snapshot
	clone.Subject = snapshot.Subject.Clone()
	clone.AuthState = snapshot.AuthState.Clone()
	return clone
}
