package model

import "testing"

func TestProviderUsageLimitPolicyValidation(t *testing.T) {
	for _, value := range []ProviderUsageLimitPolicy{"", ProviderUsageLimitPolicySwitchProvider, ProviderUsageLimitPolicySuspend} {
		if !IsValidProviderUsageLimitPolicy(value) {
			t.Fatalf("IsValidProviderUsageLimitPolicy(%q) = false", value)
		}
	}
	if IsValidProviderUsageLimitPolicy("invalid") {
		t.Fatal("invalid policy was accepted")
	}
}

func TestProviderUsageLimitPolicyDefaultIsCredentialIndependent(t *testing.T) {
	if got := DefaultProviderUsageLimitPolicy(); got != ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("default = %q", got)
	}
	if got := NormalizeProviderUsageLimitPolicy(""); got != ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("normalized default = %q", got)
	}
	provider := &Provider{}
	if got := provider.UsageLimitPolicyOrDefault(); got != ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("provider default = %q", got)
	}
	provider.UsageLimitPolicy = ProviderUsageLimitPolicySuspend
	if got := provider.UsageLimitPolicyOrDefault(); got != ProviderUsageLimitPolicySuspend {
		t.Fatalf("explicit policy = %q", got)
	}
}
