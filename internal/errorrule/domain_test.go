package errorrule

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestRuleIdentityAndRevision(t *testing.T) {
	validID := "11111111-1111-4111-8111-111111111111"
	validGeneration := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	generator := &sequenceGenerator{values: []string{validID, validGeneration}}
	id, generation, err := GenerateRuleIdentity(generator)
	if err != nil {
		t.Fatalf("GenerateRuleIdentity() error = %v", err)
	}
	if string(id) != validID || generation.String() != validGeneration || generation.IsZero() {
		t.Fatalf("identity = (%q, %q), want fixture values", id, generation.String())
	}

	if err := Revision(0).Validate(); err != nil || Revision(42).String() != "42" {
		t.Fatalf("revision value semantics failed: %v", err)
	}
	if err := Revision(-1).Validate(); err == nil {
		t.Fatal("negative revision unexpectedly valid")
	}
	if _, _, err := GenerateRuleIdentity(nil); err == nil {
		t.Fatal("nil generator unexpectedly accepted")
	}
	if _, _, err := GenerateRuleIdentity(&sequenceGenerator{values: []string{"not-a-uuid"}}); err == nil {
		t.Fatal("invalid generated rule ID unexpectedly accepted")
	}
	if _, _, err := GenerateRuleIdentity(&sequenceGenerator{values: []string{validID, "not-a-uuid"}}); err == nil {
		t.Fatal("invalid generated generation unexpectedly accepted")
	}
	if _, err := ParseRuleGeneration(""); err == nil {
		t.Fatal("empty generation unexpectedly accepted")
	}
	if err := RuleID(strings.ToUpper(validGeneration)).Validate(); err == nil {
		t.Fatal("non-canonical rule ID unexpectedly accepted")
	}
	generated := UUIDGenerator{}.NewID()
	if err := RuleID(generated).Validate(); err != nil {
		t.Fatalf("UUIDGenerator produced invalid ID %q: %v", generated, err)
	}
}

func TestTargetUnionJSON(t *testing.T) {
	provider, err := NewProviderTarget("provider-a")
	if err != nil {
		t.Fatalf("NewProviderTarget() error = %v", err)
	}
	if provider.Kind() != TargetProvider {
		t.Fatalf("provider Kind() = %q", provider.Kind())
	}
	if providerID, ok := provider.ProviderID(); !ok || providerID != "provider-a" {
		t.Fatalf("provider ProviderID() = (%q, %v)", providerID, ok)
	}
	if providerID, ok := NewGlobalTarget().ProviderID(); ok || providerID != "" {
		t.Fatalf("global ProviderID() = (%q, %v)", providerID, ok)
	}
	cases := []struct {
		name   string
		target Target
		wire   string
	}{
		{name: "global", target: NewGlobalTarget(), wire: `{"kind":"global"}`},
		{name: "provider", target: provider, wire: `{"kind":"provider","provider_id":"provider-a"}`},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			encoded, err := json.Marshal(current.target)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(encoded) != current.wire {
				t.Fatalf("Marshal() = %s, want %s", encoded, current.wire)
			}
			var decoded Target
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if decoded != current.target {
				t.Fatalf("round trip = %#v, want %#v", decoded, current.target)
			}
		})
	}

	invalid := []string{
		`{"kind":"global","provider_id":"provider-a"}`,
		`{"kind":"provider"}`,
		`{"kind":"provider","provider_id":""}`,
		`{"kind":"unknown"}`,
		`{"kind":"global","unexpected":true}`,
		`{"kind":"global"} {"kind":"global"}`,
	}
	for _, wire := range invalid {
		var target Target
		if err := json.Unmarshal([]byte(wire), &target); err == nil {
			t.Errorf("invalid target %s unexpectedly accepted", wire)
		}
	}
	if _, err := NewProviderTarget(" provider-a"); err == nil {
		t.Fatal("provider target with whitespace unexpectedly accepted")
	}
	if _, err := json.Marshal(Target{}); err == nil {
		t.Fatal("zero target unexpectedly marshaled")
	}
}

func TestActionUnionJSON(t *testing.T) {
	backoff := model.BackoffPolicy{InitialDelay: model.Duration(time.Second), Multiplier: 1}
	retryOnly, err := NewRetryOnlyAction(2, backoff)
	if err != nil {
		t.Fatalf("NewRetryOnlyAction() error = %v", err)
	}
	retrySwitch, err := NewRetryThenSwitchAction(0, model.BackoffPolicy{})
	if err != nil {
		t.Fatalf("NewRetryThenSwitchAction() error = %v", err)
	}
	maxRetry, err := NewRetryOnlyAction(MaxRuleRetries, model.BackoffPolicy{})
	if err != nil {
		t.Fatalf("NewRetryOnlyAction(max) error = %v", err)
	}
	cases := []Action{NewPassthroughAction(), retryOnly, retrySwitch, maxRetry}
	for _, action := range cases {
		encoded, err := json.Marshal(action)
		if err != nil {
			t.Fatalf("Marshal(%s) error = %v", action.Type(), err)
		}
		var decoded Action
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", action.Type(), err)
		}
		if decoded != action {
			t.Fatalf("round trip = %#v, want %#v", decoded, action)
		}
	}

	invalid := []string{
		`{"type":"passthrough","max_retries":0}`,
		`{"type":"passthrough","backoff":{"initial_delay":"0s","max_delay":"0s"}}`,
		`{"type":"retry_only","backoff":{"initial_delay":"0s","max_delay":"0s"}}`,
		`{"type":"retry_then_switch","max_retries":1}`,
		`{"type":"retry_only","max_retries":1001,"backoff":{"initial_delay":"0s","max_delay":"0s"}}`,
		`{"type":"unknown"}`,
		`{"type":"passthrough","exhaustion_behavior":"commit"}`,
		`{"type":"retry_only","max_retries":1,"backoff":{"initial_delay":"0s","max_delay":"0s","unexpected":true}}`,
	}
	for _, wire := range invalid {
		var action Action
		if err := json.Unmarshal([]byte(wire), &action); err == nil {
			t.Errorf("invalid action %s unexpectedly accepted", wire)
		}
	}
	if _, err := json.Marshal(Action{}); err == nil {
		t.Fatal("zero action unexpectedly marshaled")
	}
	if _, ok := NewPassthroughAction().RetryPolicy(); ok {
		t.Fatal("passthrough exposed retry policy")
	}
	policy, ok := retryOnly.RetryPolicy()
	if !ok || policy.MaxRetries != 2 || policy.Backoff != backoff {
		t.Fatalf("RetryPolicy() = (%#v, %v)", policy, ok)
	}
}

func TestBackoffPolicySharedFixture(t *testing.T) {
	type validationCase struct {
		Name         string              `json:"name"`
		MaxRetries   int                 `json:"max_retries"`
		Backoff      model.BackoffPolicy `json:"backoff"`
		Valid        bool                `json:"valid"`
		Error        string              `json:"error"`
		BaseDelaysMS []int64             `json:"base_delays_ms"`
	}
	var fixture struct {
		SchemaVersion   int              `json:"schema_version"`
		ValidationCases []validationCase `json:"validation_cases"`
	}
	data, err := os.ReadFile("../../contracts/internal-error/v1/backoff-policy.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("schema version = %d", fixture.SchemaVersion)
	}
	for _, current := range fixture.ValidationCases {
		t.Run(current.Name, func(t *testing.T) {
			err := ValidateRetryPolicy(current.MaxRetries, current.Backoff)
			if current.Valid && err != nil {
				t.Fatalf("ValidateRetryPolicy() error = %v", err)
			}
			if !current.Valid {
				if err == nil || err.Error() != current.Error {
					t.Fatalf("ValidateRetryPolicy() error = %v, want %q", err, current.Error)
				}
				return
			}
			withoutJitter := current.Backoff
			withoutJitter.Jitter = false
			for index, expectedMS := range current.BaseDelaysMS {
				if actual := withoutJitter.DelayForRetry(index).Milliseconds(); actual != expectedMS {
					t.Errorf("delay[%d] = %dms, want %dms", index, actual, expectedMS)
				}
			}
		})
	}
	if err := ValidateRetryPolicy(1, model.BackoffPolicy{Multiplier: math.NaN()}); err == nil {
		t.Fatal("NaN multiplier unexpectedly accepted")
	}
	if err := ValidateRetryPolicy(1, model.BackoffPolicy{Multiplier: math.Inf(1)}); err == nil {
		t.Fatal("infinite multiplier unexpectedly accepted")
	}
}

func TestRuleAndStatsWireSeparation(t *testing.T) {
	data, err := os.ReadFile("../../contracts/internal-error/v1/rule-list.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixture struct {
		Rules []Rule `json:"rules"`
	}
	var rawFixture struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := json.Unmarshal(data, &rawFixture); err != nil {
		t.Fatalf("Unmarshal(raw fixture) error = %v", err)
	}
	if len(fixture.Rules) != 2 {
		t.Fatalf("rule count = %d", len(fixture.Rules))
	}
	encoded, err := json.Marshal(fixture.Rules[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var actualRule, expectedRule any
	if err := json.Unmarshal(encoded, &actualRule); err != nil {
		t.Fatalf("Unmarshal(encoded rule) error = %v", err)
	}
	if err := json.Unmarshal(rawFixture.Rules[0], &expectedRule); err != nil {
		t.Fatalf("Unmarshal(contract rule) error = %v", err)
	}
	if !reflect.DeepEqual(actualRule, expectedRule) {
		t.Fatalf("rule wire drifted from shared fixture:\nactual: %#v\nexpected: %#v", actualRule, expectedRule)
	}
	for _, forbidden := range []string{"hit_count", "last_hit_at", "generation"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("serialized rule contains runtime field %q: %s", forbidden, encoded)
		}
	}

	lastHit := testNow
	stats := RuleStats{RuleID: fixture.Rules[0].ID, HitCount: math.MaxUint64, LastHitAt: &lastHit}
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Marshal(stats) error = %v", err)
	}
	var decoded RuleStats
	if err := json.Unmarshal(statsJSON, &decoded); err != nil {
		t.Fatalf("Unmarshal(stats) error = %v", err)
	}
	if decoded.RuleID != stats.RuleID || decoded.HitCount != stats.HitCount || !decoded.LastHitAt.Equal(lastHit) {
		t.Fatalf("stats round trip = %#v, want %#v", decoded, stats)
	}
	statsFixtureData, err := os.ReadFile("../../contracts/internal-error/v1/rule-stats.json")
	if err != nil {
		t.Fatalf("ReadFile(stats fixture) error = %v", err)
	}
	var statsFixture struct {
		Stats []RuleStats `json:"stats"`
	}
	if err := json.Unmarshal(statsFixtureData, &statsFixture); err != nil {
		t.Fatalf("Unmarshal(stats fixture) error = %v", err)
	}
	if len(statsFixture.Stats) != 2 || statsFixture.Stats[0].HitCount != 42 || statsFixture.Stats[1].LastHitAt != nil {
		t.Fatalf("stats fixture decoded incorrectly: %#v", statsFixture.Stats)
	}
	for _, invalid := range []string{
		`{"rule_id":"11111111-1111-4111-8111-111111111111","hit_count":"01","last_hit_at":null}`,
		`{"rule_id":"bad","hit_count":"1","last_hit_at":null}`,
		`{"rule_id":"11111111-1111-4111-8111-111111111111","hit_count":"-1","last_hit_at":null}`,
		`{"rule_id":"11111111-1111-4111-8111-111111111111","hit_count":"1","last_hit_at":null,"extra":true}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &decoded); err == nil {
			t.Errorf("invalid stats %s unexpectedly accepted", invalid)
		}
	}

	nonUTC := testNow.In(time.FixedZone("offset", 3600))
	nonUTCWire, _ := json.Marshal(struct {
		RuleID    RuleID     `json:"rule_id"`
		HitCount  string     `json:"hit_count"`
		LastHitAt *time.Time `json:"last_hit_at"`
	}{stats.RuleID, "1", &nonUTC})
	if err := json.Unmarshal(nonUTCWire, &decoded); err == nil {
		t.Fatal("non-UTC stats timestamp unexpectedly accepted")
	}
}

func TestNewRuleClonesSpec(t *testing.T) {
	spec := testRule(t, 0, 0).RuleSpec
	rule := NewRule(spec, RuleMetadata{ID: "11111111-1111-4111-8111-111111111111", CreatedAt: testNow, UpdatedAt: testNow})
	spec.Keywords[0] = "mutated"
	*spec.APIType = apicontract.APITypeClaude
	if reflect.DeepEqual(rule.Keywords, spec.Keywords) || rule.Keywords[0] == "mutated" || *rule.APIType != apicontract.APITypeCodex {
		t.Fatalf("NewRule retained mutable spec aliases: %#v", rule.RuleSpec)
	}
}
