package codexws

import (
	"context"
	"crypto/sha256"
	"errors"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func (o *Operation) AllowsAccountSwitch() bool {
	return o != nil && o.recoveryPolicy == model.ConversationRecoverySwitchAccountPreserveConversation
}

func (o *Operation) ClientScope() codexidentity.ClientScope {
	if o == nil {
		return codexidentity.ClientScope{}
	}
	return o.currentClientScope
}

func (o *Operation) resolveRequestOwners(ctx context.Context, result codexheaders.Result) (map[[sha256.Size]byte]ownerResolution, error) {
	if !o.AllowsAccountSwitch() {
		return o.resolveOwners(ctx, result)
	}
	owners := make(map[[sha256.Size]byte]ownerResolution)
	for _, decision := range result.Decisions() {
		candidate := decision.Candidate()
		if _, persistent := candidate.PersistentNamespace(); !persistent {
			continue
		}
		_, err := o.ledger.ObserveRequest(ctx, evidence(candidate))
		if err != nil {
			return nil, continuityFailure("request_provenance", err)
		}
		owners[candidateKey(candidate)] = ownerResolution{status: codexheaders.OwnerOpaquePassthrough}
	}
	return owners, nil
}

func (o *Operation) resolveServerOwners(ctx context.Context, result codexheaders.Result) (map[[sha256.Size]byte]ownerResolution, error) {
	if !o.AllowsAccountSwitch() {
		return o.resolveOwners(ctx, result)
	}
	owners := make(map[[sha256.Size]byte]ownerResolution)
	for _, decision := range result.Decisions() {
		candidate := decision.Candidate()
		if _, persistent := candidate.PersistentNamespace(); !persistent {
			continue
		}
		key := candidateKey(candidate)
		if _, found := o.ledger.LookupRequest(evidence(candidate)); found {
			// The source of an echoed value is its request provenance, even when a
			// different account physically returns it or the store has since changed.
			owners[key] = ownerResolution{status: codexheaders.OwnerOpaquePassthrough}
			continue
		}
		resolution, err := o.runtime.continuity.Resolve(ctx, codexcontinuity.ResolveRequest{Evidence: evidence(candidate), ClientScopeCandidates: o.clientScopes, OperationID: o.operationID})
		if err != nil {
			return nil, continuityFailure("response_provenance", err)
		}
		physical, ok := o.candidateSnapshot()
		if !ok {
			return nil, &Failure{Class: FailureIdentity, Stage: "response_provenance", Cause: errors.New("physical candidate is unavailable")}
		}
		if err := o.ledger.ValidateOwner(resolution, physical.ProtocolScope()); err != nil {
			return nil, continuityFailure("response_provenance", err)
		}
		entry := ownerResolution{status: codexheaders.OwnerOpaquePassthrough}
		switch resolution.Status {
		case codexcontinuity.ResolutionUnknown:
			entry.status = codexheaders.OwnerUnknown
		case codexcontinuity.ResolutionOwned:
			if resolution.Owner != nil && resolution.Owner.ProtocolScope.Equal(physical.ProtocolScope()) {
				entry.status = codexheaders.OwnerCurrent
				entry.binding.Owner = *resolution.Owner
			}
		}
		owners[key] = entry
	}
	return owners, nil
}

// ReplacePhysicalAttempt releases only transient routing and connection state.
// Durable provenance is evidence of origin and survives account replacement.
func (o *Operation) ReplacePhysicalAttempt() error {
	if o == nil || !o.AllowsAccountSwitch() {
		return nil
	}
	o.mu.Lock()
	if o.visibilityCommitted || o.visibleRouteTargetID != "" || o.replacementClosed {
		o.mu.Unlock()
		return reconnectRequiredFailure("replace_physical_attempt")
	}
	generation := o.generation
	o.generation = nil
	o.physicalCandidate = nil
	o.requiredProtocolScope = nil
	o.requiredAuthority = nil
	o.preferredRouteTargetID = ""
	o.routeTargetPreference = codexcontinuity.RouteTargetPreference{}
	o.mu.Unlock()
	if generation != nil {
		o.runtime.continuity.CloseConnection(*generation)
	}
	return nil
}
