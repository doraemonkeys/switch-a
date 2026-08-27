package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestImportConfig_RoutingPolicies(t *testing.T) {
	h, st, _ := testHandler()

	existingGroupID := "g-existing"
	st.groups[existingGroupID] = &model.Group{ID: existingGroupID, Name: "Existing Group", Strategy: "priority", Enabled: true}
	st.providers["p-existing"] = &model.Provider{
		ID:       "p-existing",
		Name:     "Existing Provider",
		APITypes: []model.ProviderAPIType{{ProviderID: "p-existing", APIType: "claude", BaseURL: "https://claude.example"}},
		Vendor:   "anthropic",
		Enabled:  true,
	}
	st.routingPolicies[1] = &model.RoutingPolicy{
		ID:      1,
		APIType: "legacy",
		Enabled: true,
		Groups: []model.RoutingPolicyGroup{
			{GroupID: existingGroupID},
		},
	}
	st.nextRoutingPolicyID = 1
	targetProviderID := "p-target"
	importReq := ImportConfigRequest{
		Version:            ConfigExportVersion,
		CredentialSessions: []ExportedCredentialSession{importedTestSession("target-session", "key-target")},
		Providers: []ExportedProvider{
			{
				ID:       targetProviderID,
				Name:     "Target Provider",
				APITypes: []ExportedAPIType{{APIType: "codex", BaseURL: "https://codex.example", CredentialSessionID: "target-session"}},
				Enabled:  true,
			},
		},
		RoutingPolicies: []ExportedRoutingPolicy{
			{
				APIType:         "claude",
				Enabled:         true,
				AllowedGroupIDs: []string{existingGroupID},
				AllowedVendors:  []string{"anthropic"},
			},
			{
				APIType:          "codex",
				ModelMatchType:   model.RoutingPolicyModelMatchTypeExact,
				ModelMatchValue:  "gpt-5",
				Enabled:          false,
				TargetProviderID: &targetProviderID,
			},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Changes.Providers.Add != 1 {
		t.Fatalf("providers.add = %d, want 1", preview.Changes.Providers.Add)
	}
	if preview.Changes.RoutingPolicies.Add != 2 {
		t.Fatalf("routing_policies.add = %d, want 2", preview.Changes.RoutingPolicies.Add)
	}
	if preview.Changes.RoutingPolicies.Delete != 1 {
		t.Fatalf("routing_policies.delete = %d, want 1", preview.Changes.RoutingPolicies.Delete)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var result ImportResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Applied.RoutingPolicies.Added != 2 {
		t.Fatalf("routing_policies.added = %d, want 2", result.Applied.RoutingPolicies.Added)
	}
	if result.Applied.RoutingPolicies.Deleted != 1 {
		t.Fatalf("routing_policies.deleted = %d, want 1", result.Applied.RoutingPolicies.Deleted)
	}
	if len(st.routingPolicies) != 2 {
		t.Fatalf("len(routingPolicies) = %d, want 2", len(st.routingPolicies))
	}

	var exact *model.RoutingPolicy
	var filter *model.RoutingPolicy
	for _, policy := range st.routingPolicies {
		if policy.TargetProviderID != nil {
			exact = policy
			continue
		}
		filter = policy
	}
	if exact == nil || filter == nil {
		t.Fatalf("routing policies = %#v, want exact and filter rules", st.routingPolicies)
	}
	if exact.Enabled {
		t.Fatal("exact routing policy should persist enabled=false")
	}
	if exact.TargetProviderID == nil || *exact.TargetProviderID != targetProviderID {
		t.Fatalf("exact target_provider_id = %v, want %q", exact.TargetProviderID, targetProviderID)
	}
	if len(exact.Groups) != 0 || len(exact.Vendors) != 0 {
		t.Fatalf("exact routing policy should not persist filter scopes: %#v", exact)
	}
	if len(filter.Groups) != 1 || filter.Groups[0].GroupID != existingGroupID {
		t.Fatalf("filter.Groups = %#v, want %q", filter.Groups, existingGroupID)
	}
	if len(filter.Vendors) != 1 || filter.Vendors[0].Vendor != "anthropic" {
		t.Fatalf("filter.Vendors = %#v, want anthropic", filter.Vendors)
	}
}

func TestImportConfig_RejectsDuplicateRoutingPolicyNaturalKeys(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p-existing"] = &model.Provider{
		ID:       "p-existing",
		Name:     "Existing Provider",
		APITypes: []model.ProviderAPIType{{ProviderID: "p-existing", APIType: "claude", BaseURL: "https://claude.example"}},
		Vendor:   "anthropic",
		Enabled:  true,
	}

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		RoutingPolicies: []ExportedRoutingPolicy{
			{APIType: "claude", Enabled: true, AllowedVendors: []string{"anthropic"}},
			{APIType: "claude", Enabled: true, AllowedVendors: []string{"anthropic"}},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if len(st.routingPolicies) != 0 {
		t.Fatalf("routingPolicies = %#v, want none persisted", st.routingPolicies)
	}
}

func TestImportConfig_NaturalKeyUpdateCanSwitchExactProviderRuleBackToFilterMode(t *testing.T) {
	h, st, _ := testHandler()

	groupID := "group-filter"
	st.groups[groupID] = &model.Group{ID: groupID, Name: "Filter Group"}
	targetProviderID := "p-target"
	st.providers[targetProviderID] = &model.Provider{
		ID:       targetProviderID,
		Name:     "Target Provider",
		Vendor:   "openai",
		APITypes: []model.ProviderAPIType{{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"}},
		Enabled:  true,
	}
	st.routingPolicies[1] = &model.RoutingPolicy{
		ID:               1,
		APIType:          "codex",
		ModelMatchType:   model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue:  "gpt-5",
		Enabled:          true,
		TargetProviderID: &targetProviderID,
	}

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		RoutingPolicies: []ExportedRoutingPolicy{
			{
				APIType:         "codex",
				ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
				ModelMatchValue: "gpt-5",
				Enabled:         false,
				AllowedGroupIDs: []string{groupID},
				AllowedVendors:  []string{"openai"},
			},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(preview.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", preview.Warnings)
	}
	if preview.Changes.RoutingPolicies.Update != 1 {
		t.Fatalf("routing_policies.update = %d, want 1", preview.Changes.RoutingPolicies.Update)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var result ImportResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Applied.RoutingPolicies.Updated != 1 {
		t.Fatalf("routing_policies.updated = %d, want 1", result.Applied.RoutingPolicies.Updated)
	}

	policy := st.routingPolicies[1]
	if policy.TargetProviderID != nil {
		t.Fatalf("TargetProviderID = %#v, want nil after import update", policy.TargetProviderID)
	}
	if policy.Enabled {
		t.Fatal("Enabled = true, want false from import payload")
	}
	if len(policy.Groups) != 1 || policy.Groups[0].GroupID != groupID {
		t.Fatalf("Groups = %#v, want %q", policy.Groups, groupID)
	}
	if len(policy.Vendors) != 1 || policy.Vendors[0].Vendor != "openai" {
		t.Fatalf("Vendors = %#v, want openai", policy.Vendors)
	}
}

func TestImportConfig_RejectsProviderUpdateThatBreaksExistingExactRoutingPolicyDuringStaging(t *testing.T) {
	h, st, _ := testHandler()

	targetProviderID := "p-exact"
	st.providers[targetProviderID] = &model.Provider{
		ID:       targetProviderID,
		Name:     "Exact Provider",
		APITypes: []model.ProviderAPIType{{ProviderID: targetProviderID, APIType: "codex", BaseURL: "https://codex.example"}},
		Enabled:  true,
	}
	st.routingPolicies[1] = &model.RoutingPolicy{
		ID:               1,
		APIType:          "codex",
		Enabled:          false,
		TargetProviderID: &targetProviderID,
	}

	importReq := ImportConfigRequest{
		Version:            ConfigExportVersion,
		CredentialSessions: []ExportedCredentialSession{importedTestSession("exact-session", "key-exact")},
		Providers: []ExportedProvider{
			{
				ID:       targetProviderID,
				Name:     "Exact Provider",
				APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://claude.example", CredentialSessionID: "exact-session"}},
				Enabled:  true,
			},
		},
		RoutingPolicies: []ExportedRoutingPolicy{
			{
				APIType:          "codex",
				Enabled:          false,
				TargetProviderID: &targetProviderID,
			},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(preview.Warnings) == 0 {
		t.Fatal("warnings = nil, want provider/api_type conflict")
	}
	combinedWarnings := strings.Join(preview.Warnings, " | ")
	if !strings.Contains(combinedWarnings, `provider "p-exact"`) || !strings.Contains(combinedWarnings, `api_type "codex"`) {
		t.Fatalf("warnings = %q, want provider/api_type staged catalog conflict", combinedWarnings)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("apply status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if got := st.providers[targetProviderID].APITypes[0].APIType; got != "codex" {
		t.Fatalf("provider api_type = %q, want unchanged codex after rejected import", got)
	}
}
