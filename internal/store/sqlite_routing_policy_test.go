package store

import (
	"context"
	"errors"
	"testing"

	"switch-a/internal/model"
)

func TestRoutingPolicyCRUD(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	policy := &model.RoutingPolicy{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5.1-codex",
		Groups: []model.RoutingPolicyGroup{
			{GroupID: "group-b"},
			{GroupID: "group-a"},
		},
		Vendors: []model.RoutingPolicyVendor{
			{Vendor: "openai"},
			{Vendor: "azure"},
		},
	}

	if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("CreateRoutingPolicy failed: %v", err)
	}
	if policy.ID == 0 {
		t.Fatal("CreateRoutingPolicy did not assign an ID")
	}

	got, err := store.GetRoutingPolicy(ctx, policy.ID)
	if err != nil {
		t.Fatalf("GetRoutingPolicy failed: %v", err)
	}
	if got.APIType != "codex" {
		t.Fatalf("APIType = %q, want %q", got.APIType, "codex")
	}
	if got.Groups[0].GroupID != "group-a" || got.Groups[1].GroupID != "group-b" {
		t.Fatalf("Groups = %#v, want sorted group IDs", got.Groups)
	}
	if got.Vendors[0].Vendor != "azure" || got.Vendors[1].Vendor != "openai" {
		t.Fatalf("Vendors = %#v, want sorted vendors", got.Vendors)
	}

	policies, err := store.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies failed: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("len(policies) = %d, want 1", len(policies))
	}

	policy.ModelMatchType = model.RoutingPolicyModelMatchTypePrefix
	policy.ModelMatchValue = "gpt-5"
	policy.Groups = []model.RoutingPolicyGroup{{GroupID: "group-c"}}
	policy.Vendors = []model.RoutingPolicyVendor{{Vendor: "anthropic"}}
	if err := store.UpdateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("UpdateRoutingPolicy failed: %v", err)
	}

	got, err = store.GetRoutingPolicy(ctx, policy.ID)
	if err != nil {
		t.Fatalf("GetRoutingPolicy after update failed: %v", err)
	}
	if got.ModelMatchType != model.RoutingPolicyModelMatchTypePrefix {
		t.Fatalf("ModelMatchType = %q, want %q", got.ModelMatchType, model.RoutingPolicyModelMatchTypePrefix)
	}
	if len(got.Groups) != 1 || got.Groups[0].GroupID != "group-c" {
		t.Fatalf("Groups after update = %#v, want group-c", got.Groups)
	}
	if len(got.Vendors) != 1 || got.Vendors[0].Vendor != "anthropic" {
		t.Fatalf("Vendors after update = %#v, want anthropic", got.Vendors)
	}

	if err := store.DeleteRoutingPolicy(ctx, policy.ID); err != nil {
		t.Fatalf("DeleteRoutingPolicy failed: %v", err)
	}
	got, err = store.GetRoutingPolicy(ctx, policy.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRoutingPolicy after delete error = %v, want ErrNotFound", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestRoutingPolicyCreateConflict(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	first := &model.RoutingPolicy{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5.1-codex",
		Vendors:         []model.RoutingPolicyVendor{{Vendor: "openai"}},
	}
	if err := store.CreateRoutingPolicy(ctx, first); err != nil {
		t.Fatalf("CreateRoutingPolicy(first) failed: %v", err)
	}

	duplicate := &model.RoutingPolicy{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5.1-codex",
		Vendors:         []model.RoutingPolicyVendor{{Vendor: "azure"}},
	}
	err := store.CreateRoutingPolicy(ctx, duplicate)
	if !errors.Is(err, ErrRoutingPolicyConflict) {
		t.Fatalf("CreateRoutingPolicy(duplicate) error = %v, want ErrRoutingPolicyConflict", err)
	}
}

func TestRoutingPolicyUpdateConflict(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	first := &model.RoutingPolicy{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5.1-codex",
		Vendors:         []model.RoutingPolicyVendor{{Vendor: "openai"}},
	}
	second := &model.RoutingPolicy{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
		ModelMatchValue: "gpt-5",
		Vendors:         []model.RoutingPolicyVendor{{Vendor: "openai"}},
	}
	if err := store.CreateRoutingPolicy(ctx, first); err != nil {
		t.Fatalf("CreateRoutingPolicy(first) failed: %v", err)
	}
	if err := store.CreateRoutingPolicy(ctx, second); err != nil {
		t.Fatalf("CreateRoutingPolicy(second) failed: %v", err)
	}

	second.ModelMatchType = first.ModelMatchType
	second.ModelMatchValue = first.ModelMatchValue
	err := store.UpdateRoutingPolicy(ctx, second)
	if !errors.Is(err, ErrRoutingPolicyConflict) {
		t.Fatalf("UpdateRoutingPolicy error = %v, want ErrRoutingPolicyConflict", err)
	}
}

func TestListRoutingPoliciesByAPIType(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	policies := []*model.RoutingPolicy{
		{
			Enabled: true,
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "group-1"}},
		},
		{
			Enabled:         true,
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
			ModelMatchValue: "gpt-5",
			Vendors:         []model.RoutingPolicyVendor{{Vendor: "openai"}},
		},
		{
			Enabled:         false,
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
			ModelMatchValue: "gpt-disabled",
			Vendors:         []model.RoutingPolicyVendor{{Vendor: "disabled"}},
		},
		{
			Enabled: true,
			APIType: "claude",
			Vendors: []model.RoutingPolicyVendor{{Vendor: "anthropic"}},
		},
	}
	for _, policy := range policies {
		if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
			t.Fatalf("CreateRoutingPolicy(%q) failed: %v", policy.APIType, err)
		}
	}

	got, err := store.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].APIType != "codex" || got[1].APIType != "codex" {
		t.Fatalf("unexpected API types: %#v", got)
	}
}

func TestRoutingPolicyExactProviderModeClearsFilterScopes(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	targetProviderID := "provider-exact"
	policy := &model.RoutingPolicy{
		Enabled:          true,
		APIType:          "codex",
		TargetProviderID: &targetProviderID,
		Groups:           []model.RoutingPolicyGroup{{GroupID: "group-1"}},
		Vendors:          []model.RoutingPolicyVendor{{Vendor: "openai"}},
	}
	if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("CreateRoutingPolicy(exact) failed: %v", err)
	}

	got, err := store.GetRoutingPolicy(ctx, policy.ID)
	if err != nil {
		t.Fatalf("GetRoutingPolicy(exact) failed: %v", err)
	}
	if got.TargetProviderID == nil || *got.TargetProviderID != "provider-exact" {
		t.Fatalf("TargetProviderID = %#v, want provider-exact", got.TargetProviderID)
	}
	if len(got.Groups) != 0 || len(got.Vendors) != 0 {
		t.Fatalf("exact-provider policy persisted filter scope: groups %#v vendors %#v", got.Groups, got.Vendors)
	}
}

func TestRoutingPolicyLifecycleReads(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	active := &model.RoutingPolicy{
		APIType: "codex",
		Enabled: true,
		Vendors: []model.RoutingPolicyVendor{{Vendor: "openai"}},
	}
	disabled := &model.RoutingPolicy{
		APIType:          "codex",
		ModelMatchType:   model.RoutingPolicyModelMatchTypePrefix,
		ModelMatchValue:  "gpt-5",
		Enabled:          false,
		TargetProviderID: strPtr("provider-1"),
	}
	for _, policy := range []*model.RoutingPolicy{active, disabled} {
		if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
			t.Fatalf("CreateRoutingPolicy(%q) failed: %v", policy.APIType, err)
		}
	}

	adminPolicies, err := store.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies failed: %v", err)
	}
	if len(adminPolicies) != 2 {
		t.Fatalf("len(adminPolicies) = %d, want 2", len(adminPolicies))
	}

	runtimePolicies, err := store.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType failed: %v", err)
	}
	if len(runtimePolicies) != 1 {
		t.Fatalf("len(runtimePolicies) = %d, want 1", len(runtimePolicies))
	}
	if !runtimePolicies[0].Enabled {
		t.Fatal("runtime policy should remain enabled")
	}
	if runtimePolicies[0].TargetProviderID != nil {
		t.Fatalf("runtime policy target_provider_id = %v, want nil", runtimePolicies[0].TargetProviderID)
	}
}
