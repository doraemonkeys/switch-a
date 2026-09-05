package codexhttp

import (
	"context"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/provenance"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func (o *Operation) ClientScope() codexidentity.ClientScope {
	if o == nil {
		return codexidentity.ClientScope{}
	}
	return o.currentClientScope
}

func (o *Operation) allowsAccountSwitch() bool {
	return o != nil && o.recoveryPolicy == model.ConversationRecoverySwitchAccountPreserveConversation
}

func (o *Operation) classifyProvenanceResolution(resolution codexcontinuity.Resolution, err error) ownerResolution {
	var binding codexcontinuity.Binding
	if resolution.Owner != nil {
		binding.Owner = *resolution.Owner
	}
	if err != nil {
		return classifyOwnerResolution(binding, err)
	}
	if o.allowsAccountSwitch() {
		return ownerResolution{status: codexheaders.OwnerOpaquePassthrough, binding: binding}
	}
	switch resolution.Status {
	case codexcontinuity.ResolutionUnknown:
		err = &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown, OperationID: o.operationID}
	case codexcontinuity.ResolutionExpired:
		err = &codexcontinuity.Error{Kind: codexcontinuity.ErrorExpired, OperationID: o.operationID}
	case codexcontinuity.ResolutionUnavailable:
		err = &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable, OperationID: o.operationID}
	}
	return classifyOwnerResolution(binding, err)
}

// A source lease refreshes provenance, never ownership in the selected account.
// Unresolved entrance evidence remains opaque throughout every physical attempt.
func (o *Operation) prepareRecoveryRequestLease(ctx context.Context, candidate codexheaders.BindingCandidate) (codexcontinuity.Lease, bool, error) {
	resolution, exists := o.provenance.LookupRequest(evidence(candidate))
	if !exists || resolution.Status != codexcontinuity.ResolutionOwned || resolution.Owner == nil {
		return codexcontinuity.Lease{}, false, nil
	}
	lease, err := o.runtime.continuity.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
		Evidence: evidence(candidate), ClientScopeCandidates: o.clientScopes,
		ProtocolScope: resolution.Owner.ProtocolScope, OperationID: o.operationID,
	})
	if codexprovenance.IsOpaqueDegradation(err) {
		return codexcontinuity.Lease{}, false, nil
	}
	if err != nil {
		if continuityPersistenceUnavailable(err) {
			return codexcontinuity.Lease{}, false, dependencyError("continuity_validate", err)
		}
		return codexcontinuity.Lease{}, false, clientError("continuity_validate", err)
	}
	return lease, true, nil
}

func (o *Operation) recoveryResponseStatus(ctx context.Context, candidate codexheaders.BindingCandidate, scope codexidentity.ProtocolScope) (codexheaders.OwnerStatus, bool, error) {
	if _, exists := o.provenance.LookupRequest(evidence(candidate)); exists {
		return codexheaders.OwnerOpaquePassthrough, true, nil
	}
	resolution, err := o.runtime.continuity.Resolve(ctx, codexcontinuity.ResolveRequest{
		Evidence: evidence(candidate), ClientScopeCandidates: o.clientScopes, OperationID: o.operationID,
	})
	if err == nil {
		err = o.provenance.ValidateOwner(resolution, scope)
	}
	if err != nil {
		return classifyOwnerResolution(codexcontinuity.Binding{}, err).status, true, err
	}
	switch resolution.Status {
	case codexcontinuity.ResolutionUnknown:
		return codexheaders.OwnerUnknown, true, nil
	case codexcontinuity.ResolutionExpired, codexcontinuity.ResolutionUnavailable:
		return codexheaders.OwnerOpaquePassthrough, true, nil
	default:
		return codexheaders.OwnerCurrent, false, nil
	}
}

func (o *Operation) permitsContinuityDegradation(err error) bool {
	return o.allowsAccountSwitch() && codexprovenance.IsOpaqueDegradation(err)
}
