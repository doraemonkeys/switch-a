package model

import "testing"

func TestIsValidRoutingPolicyModelMatchType(t *testing.T) {
	testCases := []struct {
		name  string
		value RoutingPolicyModelMatchType
		want  bool
	}{
		{name: "empty api-type-only rule is valid", value: "", want: true},
		{name: "exact is valid", value: RoutingPolicyModelMatchTypeExact, want: true},
		{name: "prefix is valid", value: RoutingPolicyModelMatchTypePrefix, want: true},
		{name: "unknown type is rejected", value: RoutingPolicyModelMatchType("suffix"), want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidRoutingPolicyModelMatchType(tc.value); got != tc.want {
				t.Fatalf("IsValidRoutingPolicyModelMatchType(%q) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}

func TestNewRoutingPolicyNaturalKey_NormalizesInput(t *testing.T) {
	key := NewRoutingPolicyNaturalKey(" codex ", RoutingPolicyModelMatchType(" exact "), " gpt-5.4 ")

	if key.APIType != "codex" {
		t.Fatalf("APIType = %q, want codex", key.APIType)
	}
	if key.ModelMatchType != RoutingPolicyModelMatchTypeExact {
		t.Fatalf("ModelMatchType = %q, want exact", key.ModelMatchType)
	}
	if key.ModelMatchValue != "gpt-5.4" {
		t.Fatalf("ModelMatchValue = %q, want gpt-5.4", key.ModelMatchValue)
	}
}

func TestNewRoutingPolicyNaturalKey_ClearsModelValueForAPITypesOnly(t *testing.T) {
	key := NewRoutingPolicyNaturalKey("codex", RoutingPolicyModelMatchTypeNone, "ignored")

	if key.ModelMatchType != RoutingPolicyModelMatchTypeNone {
		t.Fatalf("ModelMatchType = %q, want empty", key.ModelMatchType)
	}
	if key.ModelMatchValue != "" {
		t.Fatalf("ModelMatchValue = %q, want empty", key.ModelMatchValue)
	}
}

func TestRoutingPolicyNaturalKey_UsesCanonicalIdentity(t *testing.T) {
	targetProviderID := "provider-exact"
	policy := RoutingPolicy{
		APIType:          " codex ",
		ModelMatchType:   RoutingPolicyModelMatchType(" prefix "),
		ModelMatchValue:  " gpt- ",
		Enabled:          true,
		TargetProviderID: &targetProviderID,
	}

	key := policy.NaturalKey()

	if key.APIType != "codex" {
		t.Fatalf("APIType = %q, want codex", key.APIType)
	}
	if key.ModelMatchType != RoutingPolicyModelMatchTypePrefix {
		t.Fatalf("ModelMatchType = %q, want prefix", key.ModelMatchType)
	}
	if key.ModelMatchValue != "gpt-" {
		t.Fatalf("ModelMatchValue = %q, want gpt-", key.ModelMatchValue)
	}
}
