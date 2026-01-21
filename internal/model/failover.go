package model

// FailoverContext tracks state across failover attempts using "contamination tracking + strictest policy".
// It aggregates information from all providers in the failover chain to enforce isolation rules.
//
// Thread Safety: NOT thread-safe. Each HTTP request should have its own instance.
// The context is mutated during request processing via the Update method.
type FailoverContext struct {
	// OriginProviderID is the ID of the first provider that was attempted.
	OriginProviderID string
	// ContaminatedVendors tracks all vendors that have been attempted in the failover chain.
	// Used for vendor-based isolation decisions.
	ContaminatedVendors []string
	// StrictestScope is the most restrictive FailoverScope encountered in the chain.
	// Uses "strictest wins" policy: none < vendor < any.
	StrictestScope Scope
	// AttemptChain tracks provider IDs for cycle detection.
	AttemptChain []string
	// RetryCount is the number of failover attempts made (not counting the initial request).
	RetryCount int
}

// NewFailoverContext creates a new FailoverContext for the given initial provider.
func NewFailoverContext(p *Provider) *FailoverContext {
	ctx := &FailoverContext{
		OriginProviderID:    p.ID,
		ContaminatedVendors: make([]string, 0, 4),
		StrictestScope:      ScopeAny, // Start with least restrictive
		AttemptChain:        make([]string, 0, 4),
		RetryCount:          0,
	}
	// Initialize with the first provider's data
	ctx.AttemptChain = append(ctx.AttemptChain, p.ID)
	if p.Vendor != "" {
		ctx.ContaminatedVendors = append(ctx.ContaminatedVendors, p.Vendor)
	}
	ctx.StrictestScope = p.FailoverScope
	return ctx
}

// Update adds a provider to the failover chain and updates tracking state.
// Call this after each successful failover attempt.
func (ctx *FailoverContext) Update(p *Provider) {
	ctx.AttemptChain = append(ctx.AttemptChain, p.ID)
	if p.Vendor != "" {
		ctx.ContaminatedVendors = append(ctx.ContaminatedVendors, p.Vendor)
	}
	ctx.StrictestScope = StricterScope(ctx.StrictestScope, p.FailoverScope)
	ctx.RetryCount++
}

// IsInChain checks if a provider ID is already in the attempt chain.
func (ctx *FailoverContext) IsInChain(providerID string) bool {
	for _, id := range ctx.AttemptChain {
		if id == providerID {
			return true
		}
	}
	return false
}

// VendorMatch checks if two vendor strings match according to vendor isolation rules.
// - Empty vendor ("") is an "island" - matches nothing.
// - Wildcard ("*") matches any non-empty vendor.
// - Specific values require exact match.
func VendorMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == VendorWildcard || b == VendorWildcard {
		return true
	}
	return a == b
}

// AnyVendorMatch checks if any contaminated vendor matches the candidate.
// Used for outbound failover check (FailoverScope: vendor).
// Skips wildcards in the contaminated list to prevent scope expansion.
//
// Example: For contaminated=["yescode", "*"], candidate="openrouter":
//   - Skips "*" (would incorrectly match openrouter)
//   - Checks "yescode" vs "openrouter" → no match → returns false
//
// This prevents yescode → * → openrouter being allowed when yescode
// has FailoverScope: vendor.
func AnyVendorMatch(contaminated []string, candidate string) bool {
	for _, v := range contaminated {
		if v == VendorWildcard {
			continue // Skip wildcard to prevent scope expansion
		}
		if VendorMatch(v, candidate) {
			return true
		}
	}
	return false
}

// AllVendorsMatch checks if the candidate matches ALL vendors in the contaminated list.
// Used for inbound failover check (AcceptFailover: vendor).
// Returns false for empty contaminated list or empty candidate.
func AllVendorsMatch(contaminated []string, candidate string) bool {
	if len(contaminated) == 0 || candidate == "" {
		return false // Empty chain or empty candidate doesn't match
	}
	for _, v := range contaminated {
		if !VendorMatch(v, candidate) {
			return false
		}
	}
	return true
}

// scopeOrder returns the restrictiveness order for a scope.
// Order from most to least restrictive: none (0) < vendor (1) < any (2).
// Unknown scopes are treated as "any" (least restrictive) for forward compatibility.
func scopeOrder(s Scope) int {
	switch s {
	case ScopeNone:
		return 0
	case ScopeVendor:
		return 1
	case ScopeAny, "":
		return 2
	default:
		return 2 // Treat unknown scopes as least restrictive
	}
}

// StricterScope returns the more restrictive of two scopes.
// Order from most to least restrictive: none < vendor < any.
func StricterScope(a, b Scope) Scope {
	if scopeOrder(a) < scopeOrder(b) {
		return a
	}
	return b
}

// IsFailoverAllowed checks if a candidate provider can be used as a failover target.
// It considers: cycle detection, retry depth, outbound scope, and inbound acceptance.
//
// Parameters:
//   - candidate: the provider being considered as failover target
//   - ctx: failover context tracking the chain state
//   - maxProviderSwitches: maximum number of provider switches (failover attempts) allowed, 0 = no limit
//     Note: This is distinct from globalMaxAttempts in handler.go which limits total loop iterations
//     (including per-provider retries). maxProviderSwitches only counts provider switches.
//
// Returns true if the candidate can accept this failover.
func IsFailoverAllowed(candidate *Provider, ctx *FailoverContext, maxProviderSwitches int) bool {
	// 0. Depth limit check (counts provider switches, not total attempts)
	if maxProviderSwitches > 0 && ctx.RetryCount >= maxProviderSwitches {
		return false
	}

	// 1. Cycle detection
	if ctx.IsInChain(candidate.ID) {
		return false
	}

	// 2. Outbound check (strictest scope policy)
	switch ctx.StrictestScope {
	case ScopeNone:
		return false
	case ScopeVendor:
		if !AnyVendorMatch(ctx.ContaminatedVendors, candidate.Vendor) {
			return false
		}
	}
	// ScopeAny or empty: allow

	// 3. Inbound check (candidate's acceptance policy)
	switch candidate.AcceptFailover {
	case ScopeNone:
		return false
	case ScopeVendor:
		return AllVendorsMatch(ctx.ContaminatedVendors, candidate.Vendor)
	}
	// ScopeAny or empty: allow

	return true
}

// HasContradictoryConfig checks if a provider has contradictory failover configuration.
// Returns a slice of warning messages if configuration is contradictory, empty slice otherwise.
//
// Contradictory configurations:
// - FailoverScope is "vendor" but Vendor is empty
// - AcceptFailover is "vendor" but Vendor is empty
func HasContradictoryConfig(p *Provider) []string {
	var warnings []string
	if p.Vendor == "" {
		if p.FailoverScope == ScopeVendor {
			warnings = append(warnings, "FailoverScope is 'vendor' but Vendor is empty - this provider cannot failover to any vendor")
		}
		if p.AcceptFailover == ScopeVendor {
			warnings = append(warnings, "AcceptFailover is 'vendor' but Vendor is empty - this provider cannot accept failover from any vendor")
		}
	}
	return warnings
}
