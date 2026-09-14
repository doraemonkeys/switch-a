package model

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

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

func TestProviderUsageLimitPolicyFollowsCredentialKinds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kinds []credentialsession.Kind
		want  ProviderUsageLimitPolicy
	}{
		{"unbound", nil, ProviderUsageLimitPolicySwitchProvider},
		{"API key", []credentialsession.Kind{credentialsession.KindAPIKey}, ProviderUsageLimitPolicySwitchProvider},
		{"GPT login", []credentialsession.Kind{credentialsession.KindChatGPT}, ProviderUsageLimitPolicySuspend},
		{"GPT routes", []credentialsession.Kind{credentialsession.KindChatGPT, credentialsession.KindChatGPT}, ProviderUsageLimitPolicySuspend},
		{"mixed", []credentialsession.Kind{credentialsession.KindChatGPT, credentialsession.KindAPIKey}, ProviderUsageLimitPolicySwitchProvider},
		{"unresolved", []credentialsession.Kind{credentialsession.KindChatGPT, ""}, ProviderUsageLimitPolicySwitchProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &Provider{}
			for _, kind := range tc.kinds {
				provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{
					Credential: credentialsession.Snapshot{Kind: kind},
				})
			}
			if got := provider.UsageLimitPolicyOrDefault(); got != tc.want {
				t.Fatalf("inherited policy = %q, want %q", got, tc.want)
			}
			for _, explicit := range []ProviderUsageLimitPolicy{ProviderUsageLimitPolicySwitchProvider, ProviderUsageLimitPolicySuspend} {
				provider.UsageLimitPolicy = explicit
				if got := provider.UsageLimitPolicyOrDefault(); got != explicit {
					t.Fatalf("explicit policy = %q, want %q", got, explicit)
				}
			}
		})
	}
}
