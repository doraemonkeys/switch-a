package clientcredential

import (
	"strings"
	"testing"
)

func TestExtractCredentialGrammar(t *testing.T) {
	maximum := strings.Repeat("x", MaxClientCredentialBytes)
	tests := []struct {
		name    string
		headers map[string][]string
		state   State
		token   string
	}{
		{name: "absent", state: StateAbsent},
		{name: "bearer", headers: map[string][]string{"Authorization": {"Bearer token"}}, state: StateSingle, token: "token"},
		{name: "case insensitive scheme and name", headers: map[string][]string{"authorization": {"bEaReR\t token\t"}}, state: StateSingle, token: "token"},
		{name: "multiple separators", headers: map[string][]string{"Authorization": {"Bearer \t token"}}, state: StateSingle, token: "token"},
		{name: "internal spaces preserved", headers: map[string][]string{"Authorization": {"Bearer  value trailing  "}}, state: StateSingle, token: "value trailing"},
		{name: "internal tabs preserved", headers: map[string][]string{"Authorization": {"Bearer\tvalue\tinside"}}, state: StateSingle, token: "value\tinside"},
		{name: "api key trims only optional whitespace", headers: map[string][]string{"X-Api-Key": {" \tapi key\t "}}, state: StateSingle, token: "api key"},
		{name: "matching dual carrier", headers: map[string][]string{"Authorization": {"Bearer same bytes"}, "X-Api-Key": {"same bytes"}}, state: StateSingle, token: "same bytes"},
		{name: "different dual carrier", headers: map[string][]string{"Authorization": {"Bearer first"}, "X-Api-Key": {"second"}}, state: StateAmbiguous},
		{name: "different length dual carrier", headers: map[string][]string{"Authorization": {"Bearer first"}, "X-Api-Key": {"first-extra"}}, state: StateAmbiguous},
		{name: "duplicate authorization values", headers: map[string][]string{"Authorization": {"Bearer first", "Bearer second"}}, state: StateInvalid},
		{name: "duplicate authorization casing", headers: map[string][]string{"Authorization": {"Bearer first"}, "authorization": {"Bearer second"}}, state: StateInvalid},
		{name: "duplicate api keys", headers: map[string][]string{"X-Api-Key": {"first", "second"}}, state: StateInvalid},
		{name: "unsupported scheme", headers: map[string][]string{"Authorization": {"Basic token"}}, state: StateInvalid},
		{name: "missing separator", headers: map[string][]string{"Authorization": {"Bearertoken"}}, state: StateInvalid},
		{name: "empty bearer", headers: map[string][]string{"Authorization": {"Bearer \t"}}, state: StateInvalid},
		{name: "leading whitespace before scheme", headers: map[string][]string{"Authorization": {" Bearer token"}}, state: StateInvalid},
		{name: "non optional whitespace separator", headers: map[string][]string{"Authorization": {"Bearer\u00a0token"}}, state: StateInvalid},
		{name: "blank api key", headers: map[string][]string{"X-Api-Key": {" \t"}}, state: StateInvalid},
		{name: "bearer boundary accepted", headers: map[string][]string{"Authorization": {"Bearer " + maximum}}, state: StateSingle, token: maximum},
		{name: "api key boundary accepted", headers: map[string][]string{"X-Api-Key": {maximum}}, state: StateSingle, token: maximum},
		{name: "dual carrier boundary accepted", headers: map[string][]string{"Authorization": {"Bearer " + maximum}, "X-Api-Key": {maximum}}, state: StateSingle, token: maximum},
		{name: "bearer boundary exceeded", headers: map[string][]string{"Authorization": {"Bearer " + maximum + "x"}}, state: StateInvalid},
		{name: "api key boundary exceeded", headers: map[string][]string{"X-Api-Key": {maximum + "x"}}, state: StateInvalid},
		{name: "invalid api clears valid bearer", headers: map[string][]string{"Authorization": {"Bearer secret"}, "X-Api-Key": {""}}, state: StateInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Extract(test.headers)
			defer result.Clear()
			if result.State != test.state {
				t.Fatalf("state = %q, want %q", result.State, test.state)
			}
			if got := string(result.Token); got != test.token {
				t.Fatalf("token = %q, want %q", got, test.token)
			}
		})
	}
}

func TestResultClear(t *testing.T) {
	result := Extract(map[string][]string{"Authorization": {"Bearer secret"}})
	alias := result.Token
	result.Clear()
	if result.Token != nil {
		t.Fatal("Clear must release the result token")
	}
	if got := string(alias); got != strings.Repeat("\x00", len("secret")) {
		t.Fatalf("Clear left plaintext bytes: %q", got)
	}

	var absent *Result
	absent.Clear()
}
