package codexws

import (
	"context"
	"crypto/sha256"
	"errors"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func (o *Operation) prepareClaims(ctx context.Context, result codexheaders.Result, options claimOptions) (*Permit, error) {
	permit := &Permit{operation: o}
	responseLeases := make(map[[sha256.Size]byte]codexcontinuity.Lease)
	if err := o.prepareNewClaims(ctx, permit, result.Claims(), options.visible, responseLeases); err != nil {
		permit.abandon(ctx)
		return nil, err
	}
	if err := o.prepareAdoptions(ctx, permit, result.Adoptions()); err != nil {
		permit.abandon(ctx)
		return nil, err
	}
	if err := o.prepareExistingClaims(ctx, permit, result.Decisions(), options.resolutions, responseLeases); err != nil {
		permit.abandon(ctx)
		return nil, err
	}
	attachResponseTransition(permit, result, options.response, responseLeases)
	attachCommitPins(permit, result, options)
	return permit, nil
}

func (o *Operation) prepareNewClaims(
	ctx context.Context,
	permit *Permit,
	decisions []codexheaders.Decision,
	visible bool,
	responseLeases map[[sha256.Size]byte]codexcontinuity.Lease,
) error {
	for _, decision := range decisions {
		if decision.Claim().Lifetime() == codexheaders.ClaimLifetimeOperation {
			continue
		}
		lease, err := o.prepareClaimLease(ctx, decision, visible)
		if err != nil {
			if !visible && continuityPersistenceUnavailable(err) && o.hasProviderAnchor() {
				continue
			}
			return err
		}
		permit.leases = append(permit.leases, lease)
		if decision.Field() == codexheaders.FieldResponseReference {
			responseLeases[candidateKey(decision.Candidate())] = lease
		}
	}
	return nil
}

func (o *Operation) hasProviderAnchor() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.requiredProtocolScope != nil || o.physicalCandidate != nil && o.generation != nil
}

func (o *Operation) prepareAdoptions(
	ctx context.Context,
	permit *Permit,
	decisions []codexheaders.Decision,
) error {
	for _, decision := range decisions {
		lease, err := o.prepareAdoptionLease(ctx, decision)
		if err != nil {
			return err
		}
		permit.leases = append(permit.leases, lease)
	}
	return nil
}

func (o *Operation) prepareExistingClaims(
	ctx context.Context,
	permit *Permit,
	decisions []codexheaders.Decision,
	resolutions map[[sha256.Size]byte]ownerResolution,
	responseLeases map[[sha256.Size]byte]codexcontinuity.Lease,
) error {
	for _, decision := range decisions {
		if decision.Action() != codexheaders.ActionForward {
			continue
		}
		resolution, exists := resolutions[candidateKey(decision.Candidate())]
		if !exists || resolution.status != codexheaders.OwnerCurrent {
			continue
		}
		lease, err := o.acquireExistingLease(ctx, decision)
		if err != nil {
			if continuityPersistenceUnavailable(err) {
				continue
			}
			return err
		}
		if !containsLease(permit.leases, lease) {
			permit.leases = append(permit.leases, lease)
		}
		if decision.Field() == codexheaders.FieldResponseReference {
			responseLeases[candidateKey(decision.Candidate())] = lease
		}
	}
	return nil
}

func attachCommitPins(permit *Permit, result codexheaders.Result, options claimOptions) {
	if permit == nil {
		return
	}
	if options.response != responseUnchanged {
		for _, decision := range result.Decisions() {
			if decision.Field() == codexheaders.FieldResponseReference {
				permit.pinProtocolScope = true
				break
			}
		}
	}
	for _, decision := range result.Claims() {
		switch decision.Claim().Boundary() {
		case codexheaders.ClaimBoundaryProtocolScope:
			permit.pinProtocolScope = true
		case codexheaders.ClaimBoundaryAuthority:
			permit.pinAuthority = true
		}
	}
	for _, decision := range result.Adoptions() {
		if decision.Claim().Boundary() == codexheaders.ClaimBoundaryProtocolScope {
			permit.pinProtocolScope = true
		}
	}
}

func (o *Operation) acquireExistingLease(
	ctx context.Context,
	decision codexheaders.Decision,
) (codexcontinuity.Lease, error) {
	candidate, ok := o.candidateSnapshot()
	if !ok {
		return codexcontinuity.Lease{}, &Failure{Class: FailureIdentity, Stage: "acquire_existing", Cause: errors.New("provider identity is not bound")}
	}
	lease, err := o.runtime.continuity.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
		Evidence:              evidence(decision.Candidate()),
		ClientScopeCandidates: append([]codexidentity.ClientScope(nil), o.clientScopes...),
		ProtocolScope:         candidate.ProtocolScope(),
		OperationID:           o.operationID,
	})
	if err != nil {
		return codexcontinuity.Lease{}, continuityFailure("acquire_existing", err)
	}
	return lease, nil
}

func (o *Operation) prepareClaimLease(
	ctx context.Context,
	decision codexheaders.Decision,
	visible bool,
) (codexcontinuity.Lease, error) {
	candidate, ok := o.candidateSnapshot()
	if !ok {
		return codexcontinuity.Lease{}, &Failure{Class: FailureIdentity, Stage: "claim", Cause: errors.New("provider identity is not bound")}
	}
	request := codexcontinuity.ClaimRequest{
		Evidence: evidence(decision.Candidate()),
		Scope: codexcontinuity.Scope{
			CurrentClientScope:    o.currentClientScope,
			ClientScopeCandidates: append([]codexidentity.ClientScope(nil), o.clientScopes...),
			ProtocolScope:         candidate.ProtocolScope(),
			RouteTargetHint:       candidate.RouteTargetID(),
		},
		OperationID: o.operationID,
	}
	var (
		lease codexcontinuity.Lease
		err   error
	)
	if visible {
		lease, err = o.runtime.continuity.PrepareVisible(ctx, request)
	} else {
		lease, err = o.runtime.continuity.Claim(ctx, request)
	}
	if err != nil {
		return codexcontinuity.Lease{}, continuityFailure("claim", err)
	}
	return lease, nil
}

func (o *Operation) prepareAdoptionLease(
	ctx context.Context,
	decision codexheaders.Decision,
) (codexcontinuity.Lease, error) {
	candidate, ok := o.candidateSnapshot()
	if !ok {
		return codexcontinuity.Lease{}, &Failure{Class: FailureIdentity, Stage: "adopt", Cause: errors.New("provider identity is not bound")}
	}
	lease, err := o.runtime.continuity.Adopt(ctx, codexcontinuity.ClaimRequest{
		Evidence: evidence(decision.Candidate()),
		Scope: codexcontinuity.Scope{
			CurrentClientScope:    o.currentClientScope,
			ClientScopeCandidates: append([]codexidentity.ClientScope(nil), o.clientScopes...),
			ProtocolScope:         candidate.ProtocolScope(),
			RouteTargetHint:       candidate.RouteTargetID(),
		},
		OperationID: o.operationID,
	})
	if err != nil {
		return codexcontinuity.Lease{}, continuityFailure("adopt", err)
	}
	return lease, nil
}

func attachResponseTransition(
	permit *Permit,
	result codexheaders.Result,
	transition responseTransition,
	responseLeases map[[sha256.Size]byte]codexcontinuity.Lease,
) {
	if transition == responseUnchanged {
		return
	}
	for _, decision := range result.Decisions() {
		if decision.Field() != codexheaders.FieldResponseReference {
			continue
		}
		key := candidateKey(decision.Candidate())
		lease, claimed := responseLeases[key]
		if claimed {
			permit.attachResponseLease(lease, transition)
		}
	}
}

func (p *Permit) attachResponseLease(lease codexcontinuity.Lease, transition responseTransition) {
	switch transition {
	case responseActivated:
		p.activate = append(p.activate, lease)
	case responseTerminal:
		p.deactivate = append(p.deactivate, lease.Binding())
	}
}
