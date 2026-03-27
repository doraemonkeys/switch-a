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
