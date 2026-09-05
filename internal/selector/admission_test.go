package selector

import (
	"context"
	"github.com/doraemonkeys/switch-a/internal/model"
	"testing"
)

type admissionPolicies struct {
	policies []model.RoutingPolicy
	calls    int
	err      error
}

func (s *admissionPolicies) ListRoutingPoliciesByAPIType(context.Context, string) ([]model.RoutingPolicy, error) {
	s.calls++
	return s.policies, s.err
}

func TestAdmissionPinsRoutingConsumptionAndSelection(t *testing.T) {
	target := "original"
	policies := &admissionPolicies{policies: []model.RoutingPolicy{{APIType: "claude", Enabled: true,
		ModelMatchType: model.RoutingPolicyModelMatchTypeExact, ModelMatchValue: "late-model", TargetProviderID: &target}}}
	request := &model.SelectRequest{APIType: "claude", StickyMode: model.StickyModeOff, Model: "unknown"}
	demand, err := PrepareAdmission(t.Context(), policies, request)
	if err != nil || !demand {
		t.Fatalf("demand=%v err=%v", demand, err)
	}
	target = "mutated"
	policies.policies = nil
	request.Model = "late-model"
	scope, err := NewProviderSelectionEligibility(t.Context(), policies, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if scope.routing.targetProviderID != "original" {
		t.Fatalf("selected route = %q", scope.routing.targetProviderID)
	}
	if policies.calls != 1 {
		t.Fatalf("policy reads = %d", policies.calls)
	}
}

func TestAdmissionDoesNotGainBodyDependencyAfterCatalogChanges(t *testing.T) {
	policies := &admissionPolicies{}
	request := &model.SelectRequest{APIType: "claude", StickyMode: model.StickyModeOff, Model: "unknown"}
	demand, err := PrepareAdmission(t.Context(), policies, request)
	if err != nil || demand {
		t.Fatalf("demand=%v err=%v", demand, err)
	}
	policies.policies = []model.RoutingPolicy{{APIType: "claude", Enabled: true, ModelMatchType: model.RoutingPolicyModelMatchTypeExact, ModelMatchValue: "late"}}
	scope, err := NewProviderSelectionEligibility(t.Context(), policies, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if scope.WouldConsumeHiddenModel() {
		t.Fatal("selection gained an unadmitted model dependency")
	}
}
