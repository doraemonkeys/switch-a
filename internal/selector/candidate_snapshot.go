package selector

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtarget"
)

// CandidateAuthorityResolver is the narrow selector-side port for resolving an
// immutable route/session snapshot into its security authority.
type CandidateAuthorityResolver interface {
	Resolve(
		route credentialsession.RouteSnapshot,
		apiType string,
		finalURL *url.URL,
	) (codexidentity.CandidateSnapshot, error)
}

type groupSource interface {
	GetGroup(ctx context.Context, id string) (*model.Group, error)
}

// providerCandidateSnapshot binds route facts, the selected credential revision,
// and its resolved security identity at one selection boundary. Revalidation
// may adopt a newer revision only while the session and authority remain equal.
type providerCandidateSnapshot struct {
	provider         *model.Provider
	credential       credentialsession.Snapshot
	identity         codexidentity.CandidateSnapshot
	identityResolved bool
	identityErr      error
	group            *model.Group
	groupErr         error
}

func (e *ProviderSelectionEligibility) candidate(
	ctx context.Context,
	provider *model.Provider,
) providerCandidateSnapshot {
	if e == nil || provider == nil {
		return providerCandidateSnapshot{}
	}
	if e.candidates != nil {
		return e.candidates[strings.TrimSpace(provider.ID)]
	}
	return e.resolveCandidate(ctx, provider)
}

func (e *ProviderSelectionEligibility) resolveCandidate(
	ctx context.Context,
	provider *model.Provider,
) providerCandidateSnapshot {
	provider = cloneProviderSelectionSnapshot(provider)
	candidate := providerCandidateSnapshot{provider: provider}
	if provider == nil {
		return candidate
	}
	apiType := reqAPIType(e.req)
	credential, ok := provider.CredentialSessionForAPIType(apiType)
	if ok && credential != nil {
		candidate.credential = cloneCredentialSessionSnapshot(*credential)
		route := credentialsession.RouteSnapshot{
			RouteTargetID: provider.ID,
			APIType:       apiType,
			VendorScope:   provider.Vendor,
			Credential:    candidate.credential,
		}
		finalURL, parseErr := upstreamtarget.ParseBaseURL(provider.BaseURLForAPIType(apiType))
		if parseErr == nil {
			candidate.identity, candidate.identityErr = e.resolver.Resolve(route, apiType, finalURL)
		} else {
			candidate.identityErr = parseErr
		}
		candidate.identityResolved = candidate.identityErr == nil
	}
	candidate.group, candidate.groupErr = e.resolveGroupSnapshot(ctx, provider.GroupID)
	return candidate
}

func (e *ProviderSelectionEligibility) resolveGroupSnapshot(
	ctx context.Context,
	groupID *string,
) (*model.Group, error) {
	if groupID == nil || strings.TrimSpace(*groupID) == "" {
		return nil, nil
	}
	source, ok := e.source.(groupSource)
	if !ok {
		return nil, fmt.Errorf("selector group source is unavailable")
	}
	group, err := source.GetGroup(ctx, strings.TrimSpace(*groupID))
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, fmt.Errorf("selector group snapshot is missing")
	}
	cloned := *group
	return &cloned, nil
}

func (e *ProviderSelectionEligibility) CandidateSnapshot(
	providerID string,
) (codexidentity.CandidateSnapshot, bool) {
	if e == nil || e.candidates == nil {
		return codexidentity.CandidateSnapshot{}, false
	}
	candidate, ok := e.candidates[strings.TrimSpace(providerID)]
	return candidate.identity, ok && candidate.identityResolved
}

func (e *ProviderSelectionEligibility) Provider(providerID string) *model.Provider {
	if e == nil || e.candidates == nil {
		return nil
	}
	return e.candidates[strings.TrimSpace(providerID)].provider
}

func (e *ProviderSelectionEligibility) Providers() []model.Provider {
	if e == nil || len(e.order) == 0 {
		return nil
	}
	providers := make([]model.Provider, 0, len(e.order))
	for _, providerID := range e.order {
		if provider := e.candidates[providerID].provider; provider != nil {
			providers = append(providers, *provider)
		}
	}
	return providers
}

func (e *ProviderSelectionEligibility) Group(providerID string) (*model.Group, bool) {
	if e == nil || e.candidates == nil {
		return nil, false
	}
	candidate, ok := e.candidates[strings.TrimSpace(providerID)]
	if !ok || candidate.group == nil || candidate.groupErr != nil {
		return nil, false
	}
	group := *candidate.group
	return &group, true
}

func credentialSessionUsable(snapshot credentialsession.Snapshot) bool {
	if strings.TrimSpace(snapshot.SessionID) == "" ||
		!snapshot.HasCredentialMaterial() ||
		snapshot.Version < 1 ||
		!credentialsession.IsValidKind(snapshot.Kind) {
		return false
	}
	state := credentialsession.NormalizeAuthState(snapshot.Kind, snapshot.AuthState)
	return state.Status == credentialsession.AuthStatusActive
}

func providerSupportsAPIType(provider *model.Provider, apiType string) bool {
	if provider == nil || apiType == "" {
		return false
	}
	_, ok := provider.APITypeConfig(apiType)
	return ok
}

func cloneProviderSelectionSnapshot(provider *model.Provider) *model.Provider {
	if provider == nil {
		return nil
	}
	clone := *provider
	clone.ClientDisguise = provider.ClientDisguise.Clone()
	clone.APITypes = append([]model.ProviderAPIType(nil), provider.APITypes...)
	clone.CredentialSessions = make([]credentialsession.RouteSnapshot, len(provider.CredentialSessions))
	for index := range provider.CredentialSessions {
		clone.CredentialSessions[index] = provider.CredentialSessions[index]
		clone.CredentialSessions[index].Credential = cloneCredentialSessionSnapshot(
			provider.CredentialSessions[index].Credential,
		)
	}
	if provider.GroupID != nil {
		groupID := *provider.GroupID
		clone.GroupID = &groupID
	}
	return &clone
}

func cloneCredentialSessionSnapshot(snapshot credentialsession.Snapshot) credentialsession.Snapshot {
	clone := snapshot
	clone.Subject = snapshot.Subject.Clone()
	clone.AuthState = snapshot.AuthState.Clone()
	return clone
}
