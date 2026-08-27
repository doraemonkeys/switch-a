package model

// ProviderUsageLimitPolicy controls whether provider-scoped usage-limit evidence
// should temporarily suspend the provider or only force routing away from it.
// This keeps relay-specific quota semantics explicit instead of inferring them
// from transport-specific payload shapes.
type ProviderUsageLimitPolicy string

const (
	ProviderUsageLimitPolicySwitchProvider ProviderUsageLimitPolicy = "switch_provider"
	ProviderUsageLimitPolicySuspend        ProviderUsageLimitPolicy = "suspend"
)

// IsValidProviderUsageLimitPolicy reports whether the policy is supported.
// Empty values remain valid and use the route-target default.
func IsValidProviderUsageLimitPolicy(value ProviderUsageLimitPolicy) bool {
	switch value {
	case "", ProviderUsageLimitPolicySwitchProvider, ProviderUsageLimitPolicySuspend:
		return true
	default:
		return false
	}
}

// DefaultProviderUsageLimitPolicy is independent of credential kind because one
// route target may reference different session kinds for different API types.
// Operators must opt into suspension explicitly when that behavior is desired.
func DefaultProviderUsageLimitPolicy() ProviderUsageLimitPolicy {
	return ProviderUsageLimitPolicySwitchProvider
}

// NormalizeProviderUsageLimitPolicy applies the route-target default when no
// explicit override is stored.
func NormalizeProviderUsageLimitPolicy(value ProviderUsageLimitPolicy) ProviderUsageLimitPolicy {
	if value == "" {
		return DefaultProviderUsageLimitPolicy()
	}
	return value
}

// UsageLimitPolicyOrDefault returns the effective policy for runtime decisions.
func (p *Provider) UsageLimitPolicyOrDefault() ProviderUsageLimitPolicy {
	if p == nil {
		return DefaultProviderUsageLimitPolicy()
	}
	return NormalizeProviderUsageLimitPolicy(p.UsageLimitPolicy)
}
