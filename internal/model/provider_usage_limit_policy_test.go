package model

import "testing"

func TestIsValidProviderUsageLimitPolicy(t *testing.T) {
	testCases := []struct {
		name  string
		value ProviderUsageLimitPolicy
		want  bool
	}{
		{name: "empty uses derived default", value: "", want: true},
		{name: "switch provider is supported", value: ProviderUsageLimitPolicySwitchProvider, want: true},
		{name: "suspend is supported", value: ProviderUsageLimitPolicySuspend, want: true},
		{name: "unknown policy is rejected", value: ProviderUsageLimitPolicy("drop"), want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidProviderUsageLimitPolicy(tc.value); got != tc.want {
				t.Fatalf("IsValidProviderUsageLimitPolicy(%q) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}

func TestDefaultProviderUsageLimitPolicy(t *testing.T) {
	if got := DefaultProviderUsageLimitPolicy(ProviderCredentialTypeChatGPT); got != ProviderUsageLimitPolicySuspend {
		t.Fatalf("DefaultProviderUsageLimitPolicy(chatgpt) = %q, want %q", got, ProviderUsageLimitPolicySuspend)
	}
	if got := DefaultProviderUsageLimitPolicy(ProviderCredentialTypeAPIKey); got != ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("DefaultProviderUsageLimitPolicy(api_key) = %q, want %q", got, ProviderUsageLimitPolicySwitchProvider)
	}
}

func TestNormalizeProviderUsageLimitPolicy(t *testing.T) {
	testCases := []struct {
		name           string
		value          ProviderUsageLimitPolicy
		credentialType ProviderCredentialType
		want           ProviderUsageLimitPolicy
	}{
		{
			name:           "blank chatgpt defaults to suspend",
			value:          "",
			credentialType: ProviderCredentialTypeChatGPT,
			want:           ProviderUsageLimitPolicySuspend,
		},
		{
			name:           "blank api key defaults to switch",
			value:          "",
			credentialType: ProviderCredentialTypeAPIKey,
			want:           ProviderUsageLimitPolicySwitchProvider,
		},
		{
			name:           "explicit switch overrides chatgpt default",
			value:          ProviderUsageLimitPolicySwitchProvider,
			credentialType: ProviderCredentialTypeChatGPT,
			want:           ProviderUsageLimitPolicySwitchProvider,
		},
		{
			name:           "explicit suspend overrides api key default",
			value:          ProviderUsageLimitPolicySuspend,
			credentialType: ProviderCredentialTypeAPIKey,
			want:           ProviderUsageLimitPolicySuspend,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeProviderUsageLimitPolicy(tc.value, tc.credentialType); got != tc.want {
				t.Fatalf(
					"NormalizeProviderUsageLimitPolicy(%q, %q) = %q, want %q",
					tc.value,
					tc.credentialType,
					got,
					tc.want,
				)
			}
		})
	}
}

func TestProviderUsageLimitPolicyOrDefault(t *testing.T) {
	if got := (*Provider)(nil).UsageLimitPolicyOrDefault(); got != ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("nil provider policy = %q, want %q", got, ProviderUsageLimitPolicySwitchProvider)
	}

	provider := &Provider{
		CredentialType: ProviderCredentialTypeChatGPT,
	}
	if got := provider.UsageLimitPolicyOrDefault(); got != ProviderUsageLimitPolicySuspend {
		t.Fatalf("chatgpt provider policy = %q, want %q", got, ProviderUsageLimitPolicySuspend)
	}

	provider.UsageLimitPolicy = ProviderUsageLimitPolicySwitchProvider
	if got := provider.UsageLimitPolicyOrDefault(); got != ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("explicit provider policy = %q, want %q", got, ProviderUsageLimitPolicySwitchProvider)
	}
}
