package model

import "time"

const VisibleContinuitySeedTTL = 5 * time.Second

// VisibleContinuitySeed is the shared cross-request breadcrumb left behind by a
// post-visible continuity break. It stores just enough provenance for a later
// request to prove re-entry before failover semantics attach locally.
type VisibleContinuitySeed struct {
	SeedID              string
	ContinuityKey       StickyKey
	OriginProviderID    string
	OriginVendor        string
	ContaminatedVendors []string
	StrictestScope      Scope
	ObservedAt          time.Time
}

func (s *VisibleContinuitySeed) Clone() *VisibleContinuitySeed {
	if s == nil {
		return nil
	}
	clone := *s
	clone.ContaminatedVendors = append([]string(nil), s.ContaminatedVendors...)
	return &clone
}

func (s *VisibleContinuitySeed) Candidate(observedAt time.Time) *VisibleContinuitySeedCandidate {
	if s == nil {
		return nil
	}
	age := observedAt.Sub(s.ObservedAt)
	age = max(age, 0)
	return &VisibleContinuitySeedCandidate{
		SeedID:           s.SeedID,
		ContinuityKey:    s.ContinuityKey,
		OriginProviderID: s.OriginProviderID,
		OriginVendor:     s.OriginVendor,
		ObservedAt:       s.ObservedAt,
		Age:              age,
	}
}

func (s *VisibleContinuitySeed) ProviderContinuityContext() *ProviderContinuityContext {
	if s == nil {
		return nil
	}
	contaminatedVendors := append([]string(nil), s.ContaminatedVendors...)
	if len(contaminatedVendors) == 0 && s.OriginVendor != "" {
		contaminatedVendors = append(contaminatedVendors, s.OriginVendor)
	}
	strictestScope := s.StrictestScope
	if strictestScope == "" {
		strictestScope = ScopeAny
	}
	return &ProviderContinuityContext{
		VisibleOriginProviderID: s.OriginProviderID,
		VisibleOriginVendor:     s.OriginVendor,
		ContaminatedVendors:     contaminatedVendors,
		StrictestScope:          strictestScope,
		ObservedAt:              s.ObservedAt,
	}
}

// VisibleContinuitySeedCandidate is request-local evidence that a shared seed was
// found within the short heuristic window. It is intentionally immutable so
// callers cannot mutate shared continuity state before compare-and-consume.
type VisibleContinuitySeedCandidate struct {
	SeedID           string
	ContinuityKey    StickyKey
	OriginProviderID string
	OriginVendor     string
	ObservedAt       time.Time
	Age              time.Duration
}

// VisibleContinuitySeedStore keeps cross-request continuity seeds independent
// from both live request tracking and request-local continuity context.
type VisibleContinuitySeedStore interface {
	Lookup(key StickyKey) (*VisibleContinuitySeedCandidate, bool)
	Store(seed VisibleContinuitySeed)
	CompareAndConsume(key StickyKey, seedID string) (*VisibleContinuitySeed, bool)
}
