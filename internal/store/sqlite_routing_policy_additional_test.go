package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"switch-a/internal/model"
)

func TestNormalizeRoutingPolicyRecord(t *testing.T) {
	t.Parallel()

	record := normalizeRoutingPolicyRecord(&model.RoutingPolicy{
		ID:              12,
		APIType:         " codex ",
		ModelMatchType:  model.RoutingPolicyModelMatchType(" "),
		ModelMatchValue: " gpt-5.4 ",
		Groups: []model.RoutingPolicyGroup{
			{RoutingPolicyID: 12, GroupID: " group-b "},
			{RoutingPolicyID: 12, GroupID: ""},
			{RoutingPolicyID: 12, GroupID: "group-a"},
			{RoutingPolicyID: 12, GroupID: "group-a"},
		},
		Vendors: []model.RoutingPolicyVendor{
			{RoutingPolicyID: 12, Vendor: " openai "},
			{RoutingPolicyID: 12, Vendor: ""},
			{RoutingPolicyID: 12, Vendor: "azure"},
			{RoutingPolicyID: 12, Vendor: "azure"},
		},
	})

	if record.APIType != "codex" {
		t.Fatalf("normalizeRoutingPolicyRecord().APIType = %q, want %q", record.APIType, "codex")
	}
	if record.ModelMatchType != model.RoutingPolicyModelMatchTypeNone {
		t.Fatalf("normalizeRoutingPolicyRecord().ModelMatchType = %q, want none", record.ModelMatchType)
	}
	if record.ModelMatchValue != "" {
		t.Fatalf("normalizeRoutingPolicyRecord().ModelMatchValue = %q, want empty string", record.ModelMatchValue)
	}
	if len(record.Groups) != 2 || record.Groups[0].GroupID != "group-a" || record.Groups[1].GroupID != "group-b" {
		t.Fatalf("normalizeRoutingPolicyRecord().Groups = %#v, want sorted unique groups", record.Groups)
	}
	if len(record.Vendors) != 2 || record.Vendors[0].Vendor != "azure" || record.Vendors[1].Vendor != "openai" {
		t.Fatalf("normalizeRoutingPolicyRecord().Vendors = %#v, want sorted unique vendors", record.Vendors)
	}
	if normalizeRoutingPolicyGroups(nil) != nil {
		t.Fatal("normalizeRoutingPolicyGroups(nil) != nil")
	}
	if normalizeRoutingPolicyVendors(nil) != nil {
		t.Fatal("normalizeRoutingPolicyVendors(nil) != nil")
	}
}

func TestRoutingPolicyStore_NotFoundAndNormalization(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if got, err := store.GetRoutingPolicy(ctx, 999); !errors.Is(err, ErrNotFound) || got != nil {
		t.Fatalf("GetRoutingPolicy missing = (%+v, %v), want (nil, ErrNotFound)", got, err)
	}

	err := store.UpdateRoutingPolicy(ctx, &model.RoutingPolicy{
		ID:      999,
		APIType: "codex",
		Vendors: []model.RoutingPolicyVendor{{Vendor: "openai"}},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateRoutingPolicy missing error = %v, want ErrNotFound", err)
	}

	err = store.DeleteRoutingPolicy(ctx, 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteRoutingPolicy missing error = %v, want ErrNotFound", err)
	}

	createdAt := time.Date(2026, time.March, 27, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	policy := &model.RoutingPolicy{
		APIType:         " codex ",
		ModelMatchValue: " should-clear ",
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		Groups: []model.RoutingPolicyGroup{
			{GroupID: " group-b "},
			{GroupID: ""},
			{GroupID: "group-a"},
			{GroupID: "group-a"},
		},
		Vendors: []model.RoutingPolicyVendor{
			{Vendor: " openai "},
			{Vendor: ""},
			{Vendor: "azure"},
			{Vendor: "azure"},
		},
	}

	if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("CreateRoutingPolicy error = %v", err)
	}
	if !policy.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %s, want %s", policy.CreatedAt, createdAt)
	}
	if !policy.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("UpdatedAt = %s, want %s", policy.UpdatedAt, updatedAt)
	}
	if policy.ModelMatchValue != "" {
		t.Fatalf("ModelMatchValue = %q, want empty string", policy.ModelMatchValue)
	}
	if len(policy.Groups) != 2 || policy.Groups[0].GroupID != "group-a" || policy.Groups[1].GroupID != "group-b" {
		t.Fatalf("Groups = %#v, want sorted unique groups", policy.Groups)
	}
	if len(policy.Vendors) != 2 || policy.Vendors[0].Vendor != "azure" || policy.Vendors[1].Vendor != "openai" {
		t.Fatalf("Vendors = %#v, want sorted unique vendors", policy.Vendors)
	}
}

func TestRoutingPolicyConflictError_MessageAndIs(t *testing.T) {
	t.Parallel()

	apiOnly := &RoutingPolicyConflictError{APIType: "codex"}
	if apiOnly.Error() != `routing policy for api_type "codex" already exists` {
		t.Fatalf("api-only conflict error = %q", apiOnly.Error())
	}
	if !errors.Is(apiOnly, ErrRoutingPolicyConflict) {
		t.Fatal("errors.Is(apiOnly, ErrRoutingPolicyConflict) = false, want true")
	}

	exact := &RoutingPolicyConflictError{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5.4",
	}
	if exact.Error() != `routing policy for api_type "codex" and exact model match "gpt-5.4" already exists` {
		t.Fatalf("exact conflict error = %q", exact.Error())
	}
}

func TestNormalizeRoutingPolicyRecord_ExactProviderClearsFilterScope(t *testing.T) {
	t.Parallel()

	targetProviderID := " provider-exact "
	record := normalizeRoutingPolicyRecord(&model.RoutingPolicy{
		APIType:          " codex ",
		Enabled:          false,
		TargetProviderID: &targetProviderID,
		Groups:           []model.RoutingPolicyGroup{{GroupID: "group-a"}},
		Vendors:          []model.RoutingPolicyVendor{{Vendor: "openai"}},
	})

	if record.TargetProviderID == nil || *record.TargetProviderID != "provider-exact" {
		t.Fatalf("TargetProviderID = %#v, want provider-exact", record.TargetProviderID)
	}
	if record.Enabled {
		t.Fatal("Enabled = true, want false")
	}
	if len(record.Groups) != 0 || len(record.Vendors) != 0 {
		t.Fatalf("exact-provider record should clear filter scope: %#v", record)
	}
}

func TestListRoutingPoliciesByAPIType_OnlyReturnsEnabledRules(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	enabledPolicy := &model.RoutingPolicy{
		APIType: "codex",
		Enabled: true,
		Groups:  []model.RoutingPolicyGroup{{GroupID: "group-enabled"}},
	}
	disabledPolicy := &model.RoutingPolicy{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
		ModelMatchValue: "gpt-disabled",
		Enabled:         false,
		Groups:          []model.RoutingPolicyGroup{{GroupID: "group-disabled"}},
	}
	if err := store.CreateRoutingPolicy(ctx, enabledPolicy); err != nil {
		t.Fatalf("CreateRoutingPolicy(enabled) error = %v", err)
	}
	if err := store.CreateRoutingPolicy(ctx, disabledPolicy); err != nil {
		t.Fatalf("CreateRoutingPolicy(disabled) error = %v", err)
	}

	policies, err := store.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType() error = %v", err)
	}
	if len(policies) != 1 || policies[0].ID != enabledPolicy.ID {
		t.Fatalf("policies = %#v, want only enabled policy", policies)
	}
}
