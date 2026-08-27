package codexcontinuity

import "testing"

func TestRouteTargetPreferenceIsPermutationInvariant(t *testing.T) {
	tests := []struct {
		name                 string
		hints                []string
		wantPermutationCount int
		wantValue            string
		wantValueOK          bool
		wantConflicted       bool
	}{
		{
			name:                 "A B A",
			hints:                []string{"route-a", "route-b", "route-a"},
			wantPermutationCount: 3,
			wantConflicted:       true,
		},
		{
			name:                 "repeated A",
			hints:                []string{"route-a", "route-a", "route-a"},
			wantPermutationCount: 1,
			wantValue:            "route-a",
			wantValueOK:          true,
		},
		{
			name:                 "empty and non-empty",
			hints:                []string{"", "route-a", ""},
			wantPermutationCount: 3,
			wantValue:            "route-a",
			wantValueOK:          true,
		},
		{
			name:                 "empty between conflicting hints",
			hints:                []string{"route-a", "", "route-b"},
			wantPermutationCount: 6,
			wantConflicted:       true,
		},
		{
			name:                 "three distinct owners",
			hints:                []string{"route-a", "route-b", "route-c"},
			wantPermutationCount: 6,
			wantConflicted:       true,
		},
		{
			name:                 "four owners with repeated and empty evidence",
			hints:                []string{"route-a", "route-b", "route-a", ""},
			wantPermutationCount: 12,
			wantConflicted:       true,
		},
		{
			name:                 "empty hints only",
			hints:                []string{"", "", ""},
			wantPermutationCount: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			permutations := uniquePermutations(test.hints)
			if len(permutations) != test.wantPermutationCount {
				t.Fatalf("unique permutation count = %d, want %d", len(permutations), test.wantPermutationCount)
			}
			for _, hints := range permutations {
				var preference RouteTargetPreference
				for _, hint := range hints {
					preference = preference.Add(hint)
				}

				value, ok := preference.Value()
				if value != test.wantValue || ok != test.wantValueOK {
					t.Fatalf("hints %q: Value() = (%q, %t), want (%q, %t)", hints, value, ok, test.wantValue, test.wantValueOK)
				}
				if conflicted := preference.Conflicted(); conflicted != test.wantConflicted {
					t.Fatalf("hints %q: Conflicted() = %t, want %t", hints, conflicted, test.wantConflicted)
				}
			}
		})
	}
}

func TestRouteTargetPreferenceConflictIsAbsorbing(t *testing.T) {
	preference := RouteTargetPreference{}.
		Add("route-a").
		Add("route-b")
	if !preference.Conflicted() {
		t.Fatal("different non-empty hints did not conflict")
	}

	for _, hint := range []string{"", "route-a", "route-b", "route-c"} {
		preference = preference.Add(hint)
		if value, ok := preference.Value(); value != "" || ok {
			t.Fatalf("after conflict Add(%q): Value() = (%q, %t), want empty and false", hint, value, ok)
		}
		if !preference.Conflicted() {
			t.Fatalf("after conflict Add(%q) left the terminal state", hint)
		}
	}
}

func uniquePermutations(values []string) [][]string {
	if len(values) == 0 {
		return [][]string{{}}
	}

	permutations := make([][]string, 0)
	current := make([]string, 0, len(values))
	used := make([]bool, len(values))
	var visit func()
	visit = func() {
		if len(current) == len(values) {
			permutations = append(permutations, append([]string(nil), current...))
			return
		}
		seenAtDepth := make(map[string]struct{}, len(values)-len(current))
		for index, value := range values {
			if used[index] {
				continue
			}
			if _, seen := seenAtDepth[value]; seen {
				continue
			}
			seenAtDepth[value] = struct{}{}
			used[index] = true
			current = append(current, value)
			visit()
			current = current[:len(current)-1]
			used[index] = false
		}
	}
	visit()
	return permutations
}
