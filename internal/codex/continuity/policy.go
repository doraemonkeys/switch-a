package codexcontinuity

import (
	"fmt"
	"time"
)

type Limits struct {
	PendingTTL   time.Duration
	CommittedTTL time.Duration
	TombstoneTTL time.Duration
	MaxBindings  int64
}

func (l Limits) validate(kind Kind) error {
	if l.PendingTTL <= 0 || l.CommittedTTL <= 0 || l.TombstoneTTL <= 0 || l.MaxBindings <= 0 {
		return errorOf(
			ErrorInvalidInput,
			kind,
			"",
			"pending, committed, and tombstone TTLs plus capacity must be positive",
			nil,
		)
	}
	return nil
}

// Policy is immutable after construction. Requiring every kind prevents a new
// protocol field from silently inheriting another field's retention boundary.
type Policy struct {
	limits map[Kind]Limits
}

func NewPolicy(configured map[Kind]Limits) (Policy, error) {
	if len(configured) != len(allKinds) {
		return Policy{}, fmt.Errorf("continuity policy must configure exactly %d binding kinds", len(allKinds))
	}
	limits := make(map[Kind]Limits, len(configured))
	for kind, value := range configured {
		if err := kind.Validate(); err != nil {
			return Policy{}, err
		}
		if err := value.validate(kind); err != nil {
			return Policy{}, err
		}
		limits[kind] = value
	}
	for _, kind := range allKinds {
		if _, exists := limits[kind]; !exists {
			return Policy{}, errorOf(ErrorInvalidInput, kind, "", "binding kind has no retention policy", nil)
		}
	}
	return Policy{limits: limits}, nil
}

func (p Policy) Limits(kind Kind) (Limits, bool) {
	limits, exists := p.limits[kind]
	return limits, exists
}

func (p Policy) Entries() map[Kind]Limits {
	result := make(map[Kind]Limits, len(p.limits))
	for kind, limits := range p.limits {
		result[kind] = limits
	}
	return result
}
