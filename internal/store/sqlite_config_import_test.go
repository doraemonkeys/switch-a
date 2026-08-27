package store

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestApplyConfigImport_RollsBackEarlierWritesOnProviderRoutingPolicyConflict(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	targetProviderID := "p-exact"
	if err := store.CreateProvider(ctx, credentialBackedTestProvider(t, store, &model.Provider{
		ID:      targetProviderID,
		Name:    "Exact Provider",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
		},
	})); err != nil {
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
		RoutingPolicyMode: ConfigImportRoutingPolicyModeReplace,
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
				Enabled: true,
				APITypes: []model.ProviderAPIType{
					{ProviderID: targetProviderID, APIType: "claude", BaseURL: "https://claude.example"},
				},
				CredentialSessions: []credentialsession.RouteSnapshot{{
					RouteTargetID: targetProviderID, APIType: "claude", Credential: credentialsession.Snapshot{SessionID: targetProviderID + "-codex-session"},
				}},
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
	if err := store.CreateProvider(ctx, credentialBackedTestProvider(t, store, &model.Provider{
		ID:      targetProviderID,
		Name:    "Target Provider",
		Vendor:  "openai",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
		},
	})); err != nil {
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
		RoutingPolicyMode: ConfigImportRoutingPolicyModeReplace,
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
	if err := store.CreateProvider(ctx, credentialBackedTestProvider(t, store, &model.Provider{
		ID:      targetProviderID,
		Name:    "Existing Provider",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://old.example"},
		},
	})); err != nil {
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

	pNewSession := testStaticCredentialSession("p-new-claude-session", "vendor-b", "key-new")
	updatedSession := testStaticCredentialSession("p-existing-updated-session", "vendor-a", "key-updated")
	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		CredentialSessions: []credentialsession.Session{pNewSession, updatedSession},
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
				Enabled: true,
				Vendor:  "vendor-a",
				APITypes: []model.ProviderAPIType{
					{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://updated.example"},
				},
				CredentialSessions: []credentialsession.RouteSnapshot{{
					RouteTargetID: targetProviderID, APIType: "codex", Credential: credentialsession.Snapshot{SessionID: updatedSession.ID},
				}},
			},
			{
				ID:      "p-new",
				Name:    "New Provider",
				Vendor:  "vendor-b",
				Enabled: true,
				APITypes: []model.ProviderAPIType{
					{ProviderID: "p-new", APIType: "claude", BaseURL: "https://new.example"},
				},
				CredentialSessions: []credentialsession.RouteSnapshot{{
					RouteTargetID: "p-new", APIType: "claude", Credential: credentialsession.Snapshot{SessionID: pNewSession.ID},
				}},
			},
		},
		RoutingPolicyMode: ConfigImportRoutingPolicyModeReplace,
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

func TestApplyConfigImport_RoutingPolicyModeControlsEmptySliceSemantics(t *testing.T) {
	t.Run("preserve keeps existing policies", func(t *testing.T) {
		store := setupTestStore(t)
		ctx := context.Background()

		existingGroup := &model.Group{
			ID:       "g-existing",
			Name:     "Existing Group",
			Strategy: "priority",
			Enabled:  true,
		}
		if err := store.CreateGroup(ctx, existingGroup); err != nil {
			t.Fatalf("CreateGroup() error = %v", err)
		}

		targetProviderID := "p-existing"
		if err := store.CreateProvider(ctx, credentialBackedTestProvider(t, store, &model.Provider{
			ID:      targetProviderID,
			Name:    "Existing Provider",
			Enabled: true,
			APITypes: []model.ProviderAPIType{
				{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
			},
		})); err != nil {
			t.Fatalf("CreateProvider() error = %v", err)
		}

		if err := store.CreateRoutingPolicy(ctx, &model.RoutingPolicy{
			APIType:          "codex",
			Enabled:          true,
			TargetProviderID: &targetProviderID,
		}); err != nil {
			t.Fatalf("CreateRoutingPolicy(exact) error = %v", err)
		}
		if err := store.CreateRoutingPolicy(ctx, &model.RoutingPolicy{
			APIType: "claude",
			Enabled: true,
			Groups: []model.RoutingPolicyGroup{
				{GroupID: existingGroup.ID},
			},
		}); err != nil {
			t.Fatalf("CreateRoutingPolicy(filter) error = %v", err)
		}

		err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
			RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve,
			RoutingPolicies:   []model.RoutingPolicy{},
			Settings: map[string]string{
				"sticky_mode": "api_type",
			},
		})
		if err != nil {
			t.Fatalf("ApplyConfigImport(preserve) error = %v", err)
		}

		policies, err := store.ListRoutingPolicies(ctx)
		if err != nil {
			t.Fatalf("ListRoutingPolicies() error = %v", err)
		}
		if len(policies) != 2 {
			t.Fatalf("len(policies) = %d, want 2", len(policies))
		}

		var preservedExact *model.RoutingPolicy
		var preservedFilter *model.RoutingPolicy
		for i := range policies {
			switch policies[i].APIType {
			case "codex":
				preservedExact = &policies[i]
			case "claude":
				preservedFilter = &policies[i]
			}
		}
		if preservedExact == nil || preservedFilter == nil {
			t.Fatalf("policies = %#v, want existing codex and claude rules", policies)
		}
		if preservedExact.TargetProviderID == nil || *preservedExact.TargetProviderID != targetProviderID {
			t.Fatalf("preserved exact TargetProviderID = %#v, want %q", preservedExact.TargetProviderID, targetProviderID)
		}
		if len(preservedFilter.Groups) != 1 || preservedFilter.Groups[0].GroupID != existingGroup.ID {
			t.Fatalf("preserved filter Groups = %#v, want %q", preservedFilter.Groups, existingGroup.ID)
		}
	})

	t.Run("replace deletes all when the slice is empty", func(t *testing.T) {
		store := setupTestStore(t)
		ctx := context.Background()

		existingGroup := &model.Group{
			ID:       "g-existing",
			Name:     "Existing Group",
			Strategy: "priority",
			Enabled:  true,
		}
		if err := store.CreateGroup(ctx, existingGroup); err != nil {
			t.Fatalf("CreateGroup() error = %v", err)
		}

		targetProviderID := "p-existing"
		if err := store.CreateProvider(ctx, credentialBackedTestProvider(t, store, &model.Provider{
			ID:      targetProviderID,
			Name:    "Existing Provider",
			Enabled: true,
			APITypes: []model.ProviderAPIType{
				{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
			},
		})); err != nil {
			t.Fatalf("CreateProvider() error = %v", err)
		}

		for _, policy := range []*model.RoutingPolicy{
			{
				APIType:          "codex",
				Enabled:          true,
				TargetProviderID: &targetProviderID,
			},
			{
				APIType: "claude",
				Enabled: true,
				Groups: []model.RoutingPolicyGroup{
					{GroupID: existingGroup.ID},
				},
			},
		} {
			if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
				t.Fatalf("CreateRoutingPolicy(%q) error = %v", policy.APIType, err)
			}
		}

		err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
			RoutingPolicyMode: ConfigImportRoutingPolicyModeReplace,
			RoutingPolicies:   []model.RoutingPolicy{},
		})
		if err != nil {
			t.Fatalf("ApplyConfigImport(replace empty) error = %v", err)
		}

		policies, err := store.ListRoutingPolicies(ctx)
		if err != nil {
			t.Fatalf("ListRoutingPolicies() error = %v", err)
		}
		if len(policies) != 0 {
			t.Fatalf("len(policies) = %d, want 0 after replace", len(policies))
		}
	})
}

func TestApplyConfigImport_RollsBackEarlierWritesOnPreservedRoutingPolicyConflict(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	targetProviderID := "p-exact"
	if err := store.CreateProvider(ctx, credentialBackedTestProvider(t, store, &model.Provider{
		ID:      targetProviderID,
		Name:    "Exact Provider",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
		},
	})); err != nil {
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
		RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve,
		RoutingPolicies:   []model.RoutingPolicy{},
		Providers: []model.Provider{
			{
				ID:      targetProviderID,
				Name:    "Exact Provider",
				Enabled: true,
				APITypes: []model.ProviderAPIType{
					{ProviderID: targetProviderID, APIType: "claude", BaseURL: "https://claude.example"},
				},
				CredentialSessions: []credentialsession.RouteSnapshot{{
					RouteTargetID: targetProviderID, APIType: "claude", Credential: credentialsession.Snapshot{SessionID: targetProviderID + "-codex-session"},
				}},
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

	policies, err := store.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies() error = %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("len(policies) = %d, want 1 preserved policy after rollback", len(policies))
	}
	if policies[0].TargetProviderID == nil || *policies[0].TargetProviderID != targetProviderID {
		t.Fatalf("preserved TargetProviderID = %#v, want %q", policies[0].TargetProviderID, targetProviderID)
	}
}

func TestApplyConfigImport_ReplaceModeDeletesConflictingPoliciesBeforeProviderUpdate(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	targetProviderID := "p-exact"
	if err := store.CreateProvider(ctx, credentialBackedTestProvider(t, store, &model.Provider{
		ID:      targetProviderID,
		Name:    "Exact Provider",
		Enabled: true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"},
		},
	})); err != nil {
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
		RoutingPolicyMode: ConfigImportRoutingPolicyModeReplace,
		RoutingPolicies:   []model.RoutingPolicy{},
		Providers: []model.Provider{
			{
				ID:      targetProviderID,
				Name:    "Exact Provider",
				Enabled: true,
				APITypes: []model.ProviderAPIType{
					{ProviderID: targetProviderID, APIType: "claude", BaseURL: "https://claude.example"},
				},
				CredentialSessions: []credentialsession.RouteSnapshot{{
					RouteTargetID: targetProviderID, APIType: "claude", Credential: credentialsession.Snapshot{SessionID: targetProviderID + "-codex-session"},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfigImport() error = %v", err)
	}

	if _, err := store.GetGroup(ctx, "g-import"); err != nil {
		t.Fatalf("GetGroup(g-import) error = %v", err)
	}

	provider, err := store.GetProvider(ctx, targetProviderID)
	if err != nil {
		t.Fatalf("GetProvider(%q) error = %v", targetProviderID, err)
	}
	if len(provider.APITypes) != 1 || provider.APITypes[0].APIType != "claude" {
		t.Fatalf("provider APITypes = %+v, want updated claude config", provider.APITypes)
	}

	policies, err := store.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies() error = %v", err)
	}
	if len(policies) != 0 {
		t.Fatalf("len(policies) = %d, want 0 after replacement import", len(policies))
	}
}

func TestApplyConfigImport_NilBundleAndExplicitRoutingPolicyMode(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.ApplyConfigImport(ctx, nil); err != nil {
		t.Fatalf("ApplyConfigImport(nil) error = %v", err)
	}

	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		Groups: []model.Group{
			{
				ID:       "g-missing-mode",
				Name:     "Missing Mode",
				Strategy: "priority",
				Weight:   1,
				Enabled:  true,
			},
		},
	})
	if err == nil || err.Error() != "routing policy import mode is required" {
		t.Fatalf("ApplyConfigImport(missing mode) error = %v, want required-mode failure", err)
	}
	if _, err := store.GetGroup(ctx, "g-missing-mode"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGroup(g-missing-mode) error = %v, want ErrNotFound after validation failure", err)
	}

	if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve,
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
		t.Fatalf("ApplyConfigImport(explicit preserve) error = %v", err)
	}

	if _, err := store.GetGroup(ctx, "g-no-settings"); err != nil {
		t.Fatalf("GetGroup(g-no-settings) error = %v", err)
	}
}

func TestApplyConfigImport_RejectsPreserveModeRoutingPolicyPayload(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		Groups: []model.Group{
			{
				ID:       "g-preserve-invalid",
				Name:     "Preserve Invalid",
				Strategy: "priority",
				Weight:   1,
				Enabled:  true,
			},
		},
		RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve,
		RoutingPolicies: []model.RoutingPolicy{
			{
				APIType: "codex",
				Enabled: true,
			},
		},
	})
	if err == nil || err.Error() != `routing policy import mode "preserve" cannot include imported routing policies` {
		t.Fatalf("ApplyConfigImport(preserve with routing policies) error = %v, want preserve-mode rejection", err)
	}
	if _, err := store.GetGroup(ctx, "g-preserve-invalid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGroup(g-preserve-invalid) error = %v, want ErrNotFound after validation failure", err)
	}
}

func TestApplyConfigImport_RejectsUnsupportedRoutingPolicyMode(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		Groups: []model.Group{
			{
				ID:       "g-invalid-mode",
				Name:     "Invalid Mode",
				Strategy: "priority",
				Weight:   1,
				Enabled:  true,
			},
		},
		RoutingPolicyMode: ConfigImportRoutingPolicyMode("unexpected"),
	})
	if err == nil || err.Error() != `unsupported routing policy import mode "unexpected"` {
		t.Fatalf("ApplyConfigImport(invalid mode) error = %v, want unsupported-mode rejection", err)
	}
	if _, err := store.GetGroup(ctx, "g-invalid-mode"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGroup(g-invalid-mode) error = %v, want ErrNotFound after validation failure", err)
	}
}
