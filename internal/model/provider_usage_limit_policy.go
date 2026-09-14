package model

import "github.com/doraemonkeys/switch-a/internal/codex/credentialsession"

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

// UsageLimitPolicyOrDefault returns the effective policy for runtime decisions.
func (p *Provider) UsageLimitPolicyOrDefault() ProviderUsageLimitPolicy {
	if p == nil {
		return ProviderUsageLimitPolicySwitchProvider
	}
	if p.UsageLimitPolicy != "" {
		return p.UsageLimitPolicy
	}
	if len(p.CredentialSessions) == 0 {
		return ProviderUsageLimitPolicySwitchProvider
	}
	// Suspension affects the whole provider, so mixed credential routes require
	// an explicit choice instead of inheriting a GPT-only account policy.
	for _, route := range p.CredentialSessions {
		if route.Credential.Kind != credentialsession.KindChatGPT {
			return ProviderUsageLimitPolicySwitchProvider
		}
	}
	return ProviderUsageLimitPolicySuspend
}
