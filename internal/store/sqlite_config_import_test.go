package store

import (
	"context"
	"errors"
	"testing"

	"switch-a/internal/model"
)

func TestApplyConfigImport_RollsBackEarlierWritesOnProviderRoutingPolicyConflict(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	targetProviderID := "p-exact"
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:      targetProviderID,
		Name:    "Exact Provider",
		APIKey:  "key-exact",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
		},
	}); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if err := store.CreateRoutingPolicy(ctx, &model.RoutingPolicy{
		APIType:          "codex",
		Enabled:          true,
		TargetProviderID: &targetProviderID,
	}); err != nil {
		t.Fatalf("CreateRoutingPolicy() error = %v", err)
	}

	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		Groups: []model.Group{
			{ID: "g-import", Name: "Imported Group", Strategy: "priority", Enabled: true},
		},
		RoutingPolicies: []model.RoutingPolicy{
			{
				APIType:          "codex",
				Enabled:          true,
				TargetProviderID: &targetProviderID,
			},
		},
		Providers: []model.Provider{
			{
				ID:      targetProviderID,
				Name:    "Exact Provider",
				APIKey:  "key-exact",
				Enabled: true,
				APITypes: []model.ProviderAPIType{
					{ProviderID: targetProviderID, APIType: "claude", BaseURL: "https://claude.example"},
				},
			},
		},
	})
	if !errors.Is(err, ErrRoutingPolicyReferenceConflict) {
		t.Fatalf("ApplyConfigImport() error = %v, want ErrRoutingPolicyReferenceConflict", err)
	}

	if _, err := store.GetGroup(ctx, "g-import"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGroup(g-import) error = %v, want ErrNotFound after rollback", err)
	}

	provider, err := store.GetProvider(ctx, targetProviderID)
	if err != nil {
		t.Fatalf("GetProvider(%q) error = %v", targetProviderID, err)
	}
	if len(provider.APITypes) != 1 || provider.APITypes[0].APIType != "codex" {
		t.Fatalf("provider APITypes = %+v, want original codex config after rollback", provider.APITypes)
	}
}

func TestApplyConfigImport_NaturalKeyRoutingPolicyUpdateClearsExactProviderTarget(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateGroup(ctx, &model.Group{
		ID:       "g-filter",
		Name:     "Filter Group",
		Strategy: "priority",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}

	targetProviderID := "p-target"
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:      targetProviderID,
		Name:    "Target Provider",
		APIKey:  "key-target",
		Vendor:  "openai",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
		},
	}); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if err := store.CreateRoutingPolicy(ctx, &model.RoutingPolicy{
		APIType:          "codex",
		ModelMatchType:   model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue:  "gpt-5",
		Enabled:          true,
		TargetProviderID: &targetProviderID,
	}); err != nil {
		t.Fatalf("CreateRoutingPolicy() error = %v", err)
	}

	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		RoutingPolicies: []model.RoutingPolicy{
			{
				APIType:         "codex",
				ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
				ModelMatchValue: "gpt-5",
				Enabled:         false,
				Groups:          []model.RoutingPolicyGroup{{GroupID: "g-filter"}},
				Vendors:         []model.RoutingPolicyVendor{{Vendor: "openai"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfigImport() error = %v", err)
	}

	policies, err := store.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies() error = %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("len(policies) = %d, want 1", len(policies))
	}

	policy := policies[0]
	if policy.TargetProviderID != nil {
		t.Fatalf("TargetProviderID = %#v, want nil after natural-key import update", policy.TargetProviderID)
	}
	if policy.Enabled {
		t.Fatal("Enabled = true, want false from imported replacement rule")
	}
	if len(policy.Groups) != 1 || policy.Groups[0].GroupID != "g-filter" {
		t.Fatalf("Groups = %#v, want g-filter", policy.Groups)
	}
	if len(policy.Vendors) != 1 || policy.Vendors[0].Vendor != "openai" {
		t.Fatalf("Vendors = %#v, want openai", policy.Vendors)
	}
}

func TestApplyConfigImport_UpsertsGroupsProvidersPoliciesAndSettings(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	existingGroup := &model.Group{
		ID:       "g-existing",
		Name:     "Existing Group",
		Strategy: "priority",
		Weight:   1,
		Enabled:  true,
	}
	if err := store.CreateGroup(ctx, existingGroup); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}

	targetProviderID := "p-existing"
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:      targetProviderID,
		Name:    "Existing Provider",
		APIKey:  "key-existing",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://old.example"},
		},
	}); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	existingPolicy := &model.RoutingPolicy{
		APIType: "codex",
		Enabled: true,
		Groups: []model.RoutingPolicyGroup{
			{GroupID: existingGroup.ID},
		},
	}
	if err := store.CreateRoutingPolicy(ctx, existingPolicy); err != nil {
		t.Fatalf("CreateRoutingPolicy() error = %v", err)
	}
	deletedPolicy := &model.RoutingPolicy{
		APIType: "legacy",
		Enabled: true,
		Groups: []model.RoutingPolicyGroup{
			{GroupID: existingGroup.ID},
		},
	}
	if err := store.CreateRoutingPolicy(ctx, deletedPolicy); err != nil {
		t.Fatalf("CreateRoutingPolicy(deletedPolicy) error = %v", err)
	}
	if err := store.SetConfigs(ctx, map[string]string{"sticky_mode": "off"}); err != nil {
		t.Fatalf("SetConfigs() error = %v", err)
	}

	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		Groups: []model.Group{
			{
				ID:       existingGroup.ID,
				Name:     "Updated Group",
				Strategy: "weight",
				Weight:   2,
				Enabled:  true,
			},
			{
				ID:       "g-new",
				Name:     "New Group",
				Strategy: "priority",
				Weight:   1,
				Enabled:  true,
			},
		},
		Providers: []model.Provider{
			{
				ID:      targetProviderID,
				Name:    "Updated Provider",
				APIKey:  "key-updated",
				Enabled: true,
				Vendor:  "vendor-a",
				APITypes: []model.ProviderAPIType{
					{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://updated.example"},
				},
			},
			{
				ID:      "p-new",
				Name:    "New Provider",
				APIKey:  "key-new",
				Enabled: true,
				APITypes: []model.ProviderAPIType{
					{ProviderID: "p-new", APIType: "claude", BaseURL: "https://new.example"},
				},
			},
		},
		RoutingPolicies: []model.RoutingPolicy{
			{
				APIType:          "codex",
				Enabled:          false,
				TargetProviderID: &targetProviderID,
			},
			{
				APIType: "claude",
				Enabled: true,
				Groups: []model.RoutingPolicyGroup{
					{GroupID: "g-new"},
				},
			},
		},
		Settings: map[string]string{
			"sticky_mode":         "api_type",
			"global_max_attempts": "5",
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfigImport() error = %v", err)
	}

	updatedGroup, err := store.GetGroup(ctx, existingGroup.ID)
	if err != nil {
		t.Fatalf("GetGroup(existing) error = %v", err)
	}
	if updatedGroup.Name != "Updated Group" || updatedGroup.Weight != 2 {
		t.Fatalf("updated group = %+v, want renamed weighted group", updatedGroup)
	}
	if _, err := store.GetGroup(ctx, "g-new"); err != nil {
		t.Fatalf("GetGroup(g-new) error = %v", err)
	}

	updatedProvider, err := store.GetProvider(ctx, targetProviderID)
	if err != nil {
		t.Fatalf("GetProvider(existing) error = %v", err)
	}
	if updatedProvider.Name != "Updated Provider" || updatedProvider.Vendor != "vendor-a" {
		t.Fatalf("updated provider = %+v, want renamed vendor-aware provider", updatedProvider)
	}
	if _, err := store.GetProvider(ctx, "p-new"); err != nil {
		t.Fatalf("GetProvider(p-new) error = %v", err)
	}

	policies, err := store.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies() error = %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("ListRoutingPolicies() len = %d, want 2", len(policies))
	}
	if _, err := store.GetRoutingPolicy(ctx, deletedPolicy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRoutingPolicy(deletedPolicy) error = %v, want ErrNotFound", err)
	}

	var codexPolicy *model.RoutingPolicy
	var claudePolicy *model.RoutingPolicy
	for i := range policies {
		switch policies[i].APIType {
		case "codex":
			codexPolicy = &policies[i]
		case "claude":
			claudePolicy = &policies[i]
		}
	}
	if codexPolicy == nil || claudePolicy == nil {
		t.Fatalf("policies = %#v, want codex and claude rules", policies)
	}
	if codexPolicy.ID != existingPolicy.ID {
		t.Fatalf("codex policy ID = %d, want existing ID %d", codexPolicy.ID, existingPolicy.ID)
	}
	if codexPolicy.Enabled {
		t.Fatal("codex policy Enabled = true, want false")
	}
	if codexPolicy.TargetProviderID == nil || *codexPolicy.TargetProviderID != targetProviderID {
		t.Fatalf("codex TargetProviderID = %#v, want %q", codexPolicy.TargetProviderID, targetProviderID)
	}
	if len(codexPolicy.Groups) != 0 || len(codexPolicy.Vendors) != 0 {
		t.Fatalf("codex policy scope = %#v, want exact-provider-only", codexPolicy)
	}
	if len(claudePolicy.Groups) != 1 || claudePolicy.Groups[0].GroupID != "g-new" {
		t.Fatalf("claude policy groups = %#v, want g-new", claudePolicy.Groups)
	}

	stickyMode, err := store.GetConfig(ctx, "sticky_mode")
	if err != nil {
		t.Fatalf("GetConfig(sticky_mode) error = %v", err)
	}
	if stickyMode != "api_type" {
		t.Fatalf("sticky_mode = %q, want api_type", stickyMode)
	}
	globalMaxAttempts, err := store.GetConfig(ctx, "global_max_attempts")
	if err != nil {
		t.Fatalf("GetConfig(global_max_attempts) error = %v", err)
	}
	if globalMaxAttempts != "5" {
		t.Fatalf("global_max_attempts = %q, want 5", globalMaxAttempts)
	}
}

func TestApplyConfigImport_NilBundleAndSettingslessBundle(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.ApplyConfigImport(ctx, nil); err != nil {
		t.Fatalf("ApplyConfigImport(nil) error = %v", err)
	}

	if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		Groups: []model.Group{
			{
				ID:       "g-no-settings",
				Name:     "No Settings",
				Strategy: "priority",
				Weight:   1,
				Enabled:  true,
			},
		},
	}); err != nil {
		t.Fatalf("ApplyConfigImport(settingsless) error = %v", err)
	}

	if _, err := store.GetGroup(ctx, "g-no-settings"); err != nil {
		t.Fatalf("GetGroup(g-no-settings) error = %v", err)
	}
}
