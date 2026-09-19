// Package continuation governs Codex conversation movement independently of
// vendor failover and of the provenance of individual upstream state values.
package continuation

import (
	"errors"
	"fmt"
)

type Boundary string

const (
	None         Boundary = "none"
	SameIdentity Boundary = "same_identity"
	Any          Boundary = "any"
)

type Policy struct {
	Outbound Boundary `json:"outbound" gorm:"default:any"`
	Inbound  Boundary `json:"inbound" gorm:"default:none"`
}

func (p Policy) Effective() Policy {
	if p.Outbound == "" {
		p.Outbound = Any
	}
	if p.Inbound == "" {
		p.Inbound = None
	}
	return p
}

func (p Policy) Validate() error {
	p = p.Effective()
	for _, boundary := range []Boundary{p.Outbound, p.Inbound} {
		if boundary != None && boundary != SameIdentity && boundary != Any {
			return fmt.Errorf("invalid Codex continuation boundary %q", boundary)
		}
	}
	return nil
}

type Route struct {
	ProviderID    string
	ProtocolScope string
	Policy        Policy
}

type Decision struct {
	Allowed          bool
	Reason           string
	SourceProviderID string
	TargetProviderID string
}

// Denied is a routing decision, not an upstream or conversation-state failure.
// Transports can reselect before visibility without charging provider health.
type Denied struct{ Decision Decision }

func (e *Denied) Error() string {
	return fmt.Sprintf("Codex continuation %s: %s -> %s", e.Decision.Reason, e.Decision.SourceProviderID, e.Decision.TargetProviderID)
}

func IsDenied(err error) bool {
	var denied *Denied
	return errors.As(err, &denied)
}

func Evaluate(source, target Route) Decision {
	d := Decision{SourceProviderID: source.ProviderID, TargetProviderID: target.ProviderID}
	sameIdentity := source.ProtocolScope != "" && source.ProtocolScope == target.ProtocolScope
	if source.ProviderID == target.ProviderID && sameIdentity {
		d.Allowed, d.Reason = true, "current_route"
		return d
	}
	if !permits(source.Policy.Effective().Outbound, sameIdentity) {
		d.Reason = "outbound_denied"
		return d
	}
	if !permits(target.Policy.Effective().Inbound, sameIdentity) {
		d.Reason = "inbound_denied"
		return d
	}
	d.Allowed, d.Reason = true, "continuation_allowed"
	return d
}

func permits(boundary Boundary, sameIdentity bool) bool {
	return boundary == Any || boundary == SameIdentity && sameIdentity
}
