package codexws

import (
	"context"
	"errors"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/provenance"
)

// Permit represents persistence work prepared before a physical disclosure.
// A failed or uncertain write deliberately leaves durable rows pending.
type Permit struct {
	operation  *Operation
	leases     []codexcontinuity.Lease
	activate   []codexcontinuity.Lease
	deactivate []codexcontinuity.Binding

	pinProtocolScope bool
	pinAuthority     bool
	pinRouteTarget   bool
	closeReplacement bool
	mu               sync.Mutex
	committed        bool
	abandoned        bool
}

func (p *Permit) Commit(ctx context.Context) error {
	if p == nil || p.operation == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.committed {
		return nil
	}
	if p.abandoned {
		return continuityFailure("commit_visibility", errors.New("permit was abandoned before disclosure"))
	}
	commitContext := context.WithoutCancel(ctx)
	for _, lease := range p.leases {
		if _, err := p.operation.runtime.continuity.Commit(commitContext, lease); err != nil {
			if p.operation.AllowsAccountSwitch() && codexprovenance.IsOpaqueDegradation(err) {
				continue
			}
			return continuityFailure("commit_visibility", err)
		}
	}
	if err := p.commitResponseLifecycle(); err != nil {
		return err
	}
	if err := p.operation.pinPhysicalCandidate(p.pinProtocolScope, p.pinAuthority, p.pinRouteTarget); err != nil {
		return err
	}
	if p.closeReplacement {
		p.operation.closeReplacement()
	}
	p.committed = true
	return nil
}

func (p *Permit) PinsRouteTarget() bool {
	return p != nil && p.pinRouteTarget
}

func (p *Permit) commitResponseLifecycle() error {
	if len(p.activate) == 0 && len(p.deactivate) == 0 {
		return nil
	}
	generation, ok := p.operation.currentGeneration()
	if !ok {
		return &Failure{Class: FailureIdentity, Stage: "response_lifecycle", Cause: errors.New("connection generation is inactive")}
	}
	for _, lease := range p.activate {
		if err := p.operation.runtime.continuity.ActivateResponse(generation, lease); err != nil {
			return continuityFailure("activate_response", err)
		}
	}
	for _, binding := range p.deactivate {
		if err := p.operation.runtime.continuity.DeactivateResponse(generation, binding); err != nil {
			return continuityFailure("deactivate_response", err)
		}
	}
	return nil
}

func containsLease(haystack []codexcontinuity.Lease, needle codexcontinuity.Lease) bool {
	for _, candidate := range haystack {
		if candidate.Binding().Kind == needle.Binding().Kind &&
			candidate.Binding().Digest.Equal(needle.Binding().Digest) {
			return true
		}
	}
	return false
}

func (p *Permit) abandon(ctx context.Context) {
	_ = p.AbandonBeforeDisclosure(ctx)
}

// AbandonBeforeDisclosure releases only newly claimed ownership when delivery
// preparation fails before any physical write can have exposed the identifiers.
// Callers must retain pending claims after an uncertain physical write instead.
func (p *Permit) AbandonBeforeDisclosure(ctx context.Context) error {
	if p == nil || p.operation == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.committed || p.abandoned {
		return nil
	}
	var failures []error
	for _, lease := range p.leases {
		if lease.NewlyClaimed() {
			if err := p.operation.runtime.continuity.AbandonBeforeDisclosure(context.WithoutCancel(ctx), lease); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return continuityFailure("abandon_before_disclosure", err)
	}
	p.abandoned = true
	return nil
}
