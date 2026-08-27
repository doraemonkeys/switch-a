package codexws

import (
	"context"

	"github.com/doraemonkeys/switch-a/internal/codex/headers"
)

type ClientFrameDisposition string

const (
	ClientFrameForward ClientFrameDisposition = "forward"
	ClientFrameReject  ClientFrameDisposition = "reject"
)

type ClientFrameKind string

const (
	ClientFrameOpaque         ClientFrameKind = "opaque"
	ClientFrameResponseCreate ClientFrameKind = "response_create"
	ClientFrameResponseAppend ClientFrameKind = "response_append"
	ClientFrameResponseInject ClientFrameKind = "response_inject"
)

// ClientFrameTrace contains only stable protocol labels. Keeping wire values out
// of this value lets relay diagnostics explain a decision without exposing state.
type ClientFrameTrace struct {
	Kind      ClientFrameKind
	EventType string
	Decision  ClientFrameDisposition
}

// ClientFramePermit is the immutable protocol decision for one fully-read frame.
// Connection-specific persistence work is prepared separately for each physical
// delivery, so replay never parses or reclassifies the wire bytes.
type ClientFramePermit struct {
	operation                 *Operation
	decision                  codexheaders.Result
	resolutions               map[[32]byte]ownerResolution
	disposition               ClientFrameDisposition
	replayEligible            bool
	replacementEligible       bool
	currentConnectionRequired bool
	rejection                 error
	trace                     ClientFrameTrace
}

func (p *ClientFramePermit) Disposition() ClientFrameDisposition {
	if p == nil {
		return ClientFrameForward
	}
	return p.disposition
}

func (p *ClientFramePermit) ReplayEligible() bool {
	return p != nil && p.disposition == ClientFrameForward && p.replayEligible
}

func (p *ClientFramePermit) ReplacementEligible() bool {
	return p != nil && p.disposition == ClientFrameForward && p.replacementEligible
}

func (p *ClientFramePermit) CurrentConnectionRequired() bool {
	return p != nil && p.currentConnectionRequired
}

func (p *ClientFramePermit) Rejection() error {
	if p == nil {
		return nil
	}
	return p.rejection
}

func (p *ClientFramePermit) Trace() ClientFrameTrace {
	if p == nil {
		return ClientFrameTrace{Kind: ClientFrameOpaque, Decision: ClientFrameForward}
	}
	return p.trace
}

func (p *ClientFramePermit) IsResponseCreate() bool {
	return p != nil && p.trace.Kind == ClientFrameResponseCreate
}

// PrepareDelivery binds the immutable frame decision to the currently open
// upstream connection. A replacement gets fresh leases while retaining the
// exact original classification and bytes.
func (p *ClientFramePermit) PrepareDelivery(ctx context.Context) (*Permit, error) {
	if p == nil || p.operation == nil {
		return nil, nil
	}
	if p.disposition == ClientFrameReject {
		return nil, p.rejection
	}
	if p.currentConnectionRequired {
		if _, active := p.operation.currentGeneration(); !active {
			return nil, reconnectRequiredFailure("client_frame_connection")
		}
	}
	if len(p.decision.Decisions()) == 0 && p.replacementEligible {
		return nil, nil
	}
	permit, err := p.operation.prepareClaims(ctx, p.decision, claimOptions{resolutions: p.resolutions})
	if err != nil {
		return nil, err
	}
	permit.closeReplacement = !p.replacementEligible
	return permit, nil
}
