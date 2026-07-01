package model

import (
	"slices"
	"time"
)

// SwitchMode explains why the next provider selection is happening.
// Replacement keeps the request in its pre-visible window; failover means the
// request has already attached visible continuity and must honor isolation.
type SwitchMode string

const (
	SwitchModeInitial     SwitchMode = "initial"
	SwitchModeReplacement SwitchMode = "replacement"
	SwitchModeFailover    SwitchMode = "failover"
)

// NormalizeSwitchMode keeps unset or unknown values from widening semantics
// unexpectedly. Initial is the safest default because it carries no failover-only
// isolation policy.
func NormalizeSwitchMode(mode SwitchMode) SwitchMode {
	switch mode {
	case SwitchModeReplacement, SwitchModeFailover:
		return mode
	case SwitchModeInitial, "":
		return SwitchModeInitial
	default:
		return SwitchModeInitial
	}
}

func IsValidSwitchMode(mode SwitchMode) bool {
	switch mode {
	case SwitchModeInitial, SwitchModeReplacement, SwitchModeFailover, "":
		return true
	default:
		return false
	}
}

// ProviderSwitchBudgetFromAttemptBudget keeps provider-switch budgeting separate
// from total attempt budgeting. One attempt can stay on the same provider, so
// the largest possible number of cross-provider moves is max(0, attempts-1).
func ProviderSwitchBudgetFromAttemptBudget(maxAttempts int) int {
	if maxAttempts <= 1 {
		return 0
	}
	return maxAttempts - 1
}

func NormalizeMaxProviderSwitches(maxProviderSwitches int) int {
	if maxProviderSwitches < 0 {
		return 0
	}
	return maxProviderSwitches
}

// ProviderSwitchHistory tracks only cross-provider movement inside one request
// chain. It intentionally does not say whether those switches are replacement or
// failover; that semantic lives in SwitchMode.
type ProviderSwitchHistory struct {
	OriginProviderID    string
	AttemptChain        []string
	ProviderSwitchCount int
}

func NewProviderSwitchHistory(provider *Provider) *ProviderSwitchHistory {
	history := &ProviderSwitchHistory{
		AttemptChain: make([]string, 0, 4),
	}
	if provider == nil || provider.ID == "" {
		return history
	}
	history.OriginProviderID = provider.ID
	history.AttemptChain = append(history.AttemptChain, provider.ID)
	return history
}

func (h *ProviderSwitchHistory) Clone() *ProviderSwitchHistory {
	if h == nil {
		return nil
	}
	clone := *h
	clone.AttemptChain = append([]string(nil), h.AttemptChain...)
	return &clone
}

// RecordProvider ignores same-provider retries because switch history exists to
// govern provider replacement/failover budgets rather than per-provider retries.
func (h *ProviderSwitchHistory) RecordProvider(provider *Provider) {
	if h == nil || provider == nil || provider.ID == "" {
		return
	}
	if len(h.AttemptChain) > 0 && h.AttemptChain[len(h.AttemptChain)-1] == provider.ID {
		return
	}
	if h.OriginProviderID == "" {
		h.OriginProviderID = provider.ID
		h.AttemptChain = append(h.AttemptChain, provider.ID)
		return
	}
	h.AttemptChain = append(h.AttemptChain, provider.ID)
	h.ProviderSwitchCount++
}

func (h *ProviderSwitchHistory) IsInChain(providerID string) bool {
	if h == nil || providerID == "" {
		return false
	}
	return slices.Contains(h.AttemptChain, providerID)
}

// ProviderContinuityContext is request-local state derived from visible
// continuity. It carries exactly the inputs needed for later failover isolation
// without coupling selector logic to shared cross-request storage.
type ProviderContinuityContext struct {
	VisibleOriginProviderID string
	VisibleOriginVendor     string
	ContaminatedVendors     []string
	StrictestScope          Scope
	ObservedAt              time.Time
}

func NewProviderContinuityContext(provider *Provider, observedAt time.Time) *ProviderContinuityContext {
	ctx := &ProviderContinuityContext{
		ContaminatedVendors: make([]string, 0, 4),
		StrictestScope:      ScopeAny,
		ObservedAt:          observedAt,
	}
	if provider == nil {
		return ctx
	}
	ctx.VisibleOriginProviderID = provider.ID
	ctx.VisibleOriginVendor = provider.Vendor
	if provider.Vendor != "" {
		ctx.ContaminatedVendors = append(ctx.ContaminatedVendors, provider.Vendor)
	}
	ctx.StrictestScope = provider.FailoverScope
	return ctx
}

func (ctx *ProviderContinuityContext) Clone() *ProviderContinuityContext {
	if ctx == nil {
		return nil
	}
	clone := *ctx
	clone.ContaminatedVendors = append([]string(nil), ctx.ContaminatedVendors...)
	return &clone
}

// ObserveProvider extends the continuity snapshot only when a later failover
// actually leaves the origin provider. Keeping this mutation request-local
// prevents shared seed state from drifting under concurrent requests.
func (ctx *ProviderContinuityContext) ObserveProvider(provider *Provider) {
	if ctx == nil || provider == nil {
		return
	}
	if ctx.VisibleOriginProviderID == "" {
		ctx.VisibleOriginProviderID = provider.ID
	}
	if ctx.VisibleOriginVendor == "" {
		ctx.VisibleOriginVendor = provider.Vendor
	}
	if provider.Vendor != "" {
		ctx.ContaminatedVendors = append(ctx.ContaminatedVendors, provider.Vendor)
	}
	ctx.StrictestScope = StricterScope(ctx.StrictestScope, provider.FailoverScope)
}

// IsProviderSwitchAllowed enforces request-local switch invariants that apply to
// both replacement and failover. Vendor isolation is intentionally excluded.
func IsProviderSwitchAllowed(candidate *Provider, history *ProviderSwitchHistory, maxProviderSwitches int) bool {
	if candidate == nil {
		return false
	}
	if history == nil {
		return true
	}
	if normalizedMaxProviderSwitches := NormalizeMaxProviderSwitches(maxProviderSwitches); normalizedMaxProviderSwitches > 0 &&
		history.ProviderSwitchCount >= normalizedMaxProviderSwitches {
		return false
	}
	return !history.IsInChain(candidate.ID)
}

// IsFailoverVendorAllowed applies the vendor-isolation half of failover
// semantics. Callers are expected to enforce cycle/max-switch rules separately
// via IsProviderSwitchAllowed so replacement can reuse those checks without
// inheriting failover-only isolation.
func IsFailoverVendorAllowed(candidate *Provider, continuity *ProviderContinuityContext) bool {
	if candidate == nil {
		return false
	}
	if continuity == nil {
		return true
	}
	switch continuity.StrictestScope {
	case ScopeNone:
		return false
	case ScopeVendor:
		if !AnyVendorMatch(continuity.ContaminatedVendors, candidate.Vendor) {
			return false
		}
	}
	switch candidate.AcceptFailover {
	case ScopeNone:
		return false
	case ScopeVendor:
		return AllVendorsMatch(continuity.ContaminatedVendors, candidate.Vendor)
	}
	return true
}

func (r *SelectRequest) EffectiveSwitchMode() SwitchMode {
	if r == nil {
		return SwitchModeInitial
	}
	return NormalizeSwitchMode(r.SwitchMode)
}

func (r *SelectRequest) EffectiveProviderSwitchHistory() *ProviderSwitchHistory {
	if r == nil {
		return nil
	}
	return r.ProviderSwitchHistory
}

func (r *SelectRequest) EffectiveProviderContinuityContext() *ProviderContinuityContext {
	if r == nil {
		return nil
	}
	return r.ProviderContinuityContext
}

func (r *SelectRequest) EffectiveVisibleContinuitySeedCandidate() *VisibleContinuitySeedCandidate {
	if r == nil {
		return nil
	}
	return r.VisibleContinuitySeedCandidate
}

func (r *SelectRequest) EffectiveMaxProviderSwitches() int {
	if r == nil {
		return 0
	}
	return NormalizeMaxProviderSwitches(r.MaxProviderSwitches)
}
