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
// Empty values remain valid so legacy rows can still derive a default policy.
func IsValidProviderUsageLimitPolicy(value ProviderUsageLimitPolicy) bool {
	switch value {
	case "", ProviderUsageLimitPolicySwitchProvider, ProviderUsageLimitPolicySuspend:
		return true
	default:
		return false
	}
}

// DefaultProviderUsageLimitPolicy derives the default from the provider's
// credential ownership. Managed ChatGPT logins have trustworthy reset windows,
// while other providers default to switching away without timed suspension.
func DefaultProviderUsageLimitPolicy(credentialType ProviderCredentialType) ProviderUsageLimitPolicy {
	if NormalizeProviderCredentialType(credentialType) == ProviderCredentialTypeChatGPT {
		return ProviderUsageLimitPolicySuspend
	}
	return ProviderUsageLimitPolicySwitchProvider
}

// NormalizeProviderUsageLimitPolicy applies the credential-derived default when
// no explicit override is stored.
func NormalizeProviderUsageLimitPolicy(
	value ProviderUsageLimitPolicy,
	credentialType ProviderCredentialType,
) ProviderUsageLimitPolicy {
	if value == "" {
		return DefaultProviderUsageLimitPolicy(credentialType)
	}
	return value
}

// UsageLimitPolicyOrDefault returns the effective policy for runtime decisions.
func (p *Provider) UsageLimitPolicyOrDefault() ProviderUsageLimitPolicy {
	if p == nil {
		return DefaultProviderUsageLimitPolicy(ProviderCredentialTypeAPIKey)
	}
	return NormalizeProviderUsageLimitPolicy(p.UsageLimitPolicy, p.CredentialType)
}
