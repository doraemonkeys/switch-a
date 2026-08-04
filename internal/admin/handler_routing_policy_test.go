package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
)

func TestListRoutingPolicies(t *testing.T) {
	h, st, _ := testHandler()
	st.routingPolicies[2] = &model.RoutingPolicy{
		ID:              2,
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
		ModelMatchValue: "gpt-5",
		Groups: []model.RoutingPolicyGroup{
			{RoutingPolicyID: 2, GroupID: "group-b"},
			{RoutingPolicyID: 2, GroupID: "group-a"},
		},
		Vendors: []model.RoutingPolicyVendor{
			{RoutingPolicyID: 2, Vendor: "openai"},
			{RoutingPolicyID: 2, Vendor: "azure"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies", nil)
	w := httptest.NewRecorder()

	h.ListRoutingPolicies(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var payloads []RoutingPolicyPayload
	if err := json.NewDecoder(w.Body).Decode(&payloads); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("len(payloads) = %d, want 1", len(payloads))
	}
	if payloads[0].ID != "2" {
		t.Fatalf("ID = %q, want %q", payloads[0].ID, "2")
	}
	if got, want := payloads[0].AllowedGroupIDs, []string{"group-a", "group-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AllowedGroupIDs = %#v, want %#v", got, want)
	}
	if got, want := payloads[0].AllowedVendors, []string{"azure", "openai"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AllowedVendors = %#v, want %#v", got, want)
	}
}

func TestListRoutingPolicies_EmptySlice(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies", nil)
	w := httptest.NewRecorder()

	h.ListRoutingPolicies(w, req)

	if body := w.Body.String(); body != "[]\n" {
		t.Fatalf("body = %q, want %q", body, "[]\n")
	}
}

func TestGetRoutingPolicy(t *testing.T) {
	h, st, _ := testHandler()
	st.routingPolicies[3] = &model.RoutingPolicy{
		ID:      3,
		APIType: "claude",
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies/3", nil)
	setPathValue(req, "id", "3")
	w := httptest.NewRecorder()

	h.GetRoutingPolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload RoutingPolicyPayload
	if err := json.NewDecoder(w.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.ID != "3" {
		t.Fatalf("ID = %q, want %q", payload.ID, "3")
	}
}

func TestGetRoutingPolicy_InvalidID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies/not-a-number", nil)
	setPathValue(req, "id", "not-a-number")
	w := httptest.NewRecorder()

	h.GetRoutingPolicy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateRoutingPolicy(t *testing.T) {
	h, st, _ := testHandler()
	lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)
	st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}
	st.providers["provider-openai"] = &model.Provider{
		ID:       "provider-openai",
		Vendor:   "openai",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://openai.example"}},
	}

	body := `{
		"api_type": "codex",
		"model_match_type": "exact",
		"model_match_value": "gpt-5.1-codex",
		"allowed_group_ids": ["group-1"],
		"allowed_vendors": ["openai"]
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/routing-policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateRoutingPolicy(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if len(st.routingPolicies) != 1 {
		t.Fatalf("len(routingPolicies) = %d, want 1", len(st.routingPolicies))
	}
	for _, policy := range st.routingPolicies {
		if policy.APIType != "codex" {
			t.Fatalf("APIType = %q, want %q", policy.APIType, "codex")
		}
		if len(policy.Groups) != 1 || policy.Groups[0].GroupID != "group-1" {
			t.Fatalf("Groups = %#v, want group-1", policy.Groups)
		}
		if len(policy.Vendors) != 1 || policy.Vendors[0].Vendor != "openai" {
			t.Fatalf("Vendors = %#v, want openai", policy.Vendors)
		}
	}
	if lifecycles.allRetirements != 1 {
		t.Fatalf("global lifecycle retirements = %d, want 1", lifecycles.allRetirements)
	}
}

func TestCreateRoutingPolicy_ValidationError(t *testing.T) {
	h, st, _ := testHandler()
	st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/routing-policies", bytes.NewBufferString(`{
		"api_type": "codex",
		"allowed_group_ids": [],
		"allowed_vendors": []
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateRoutingPolicy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateRoutingPolicy_UnknownGroup(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/routing-policies", bytes.NewBufferString(`{
		"api_type": "codex",
		"allowed_group_ids": ["missing-group"],
		"allowed_vendors": []
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateRoutingPolicy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateRoutingPolicy_Conflict(t *testing.T) {
	h, st, _ := testHandler()
	st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}
	st.createErr = &store.RoutingPolicyConflictError{
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5.1-codex",
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/routing-policies", bytes.NewBufferString(`{
		"api_type": "codex",
		"model_match_type": "exact",
		"model_match_value": "gpt-5.1-codex",
		"allowed_group_ids": ["group-1"],
		"allowed_vendors": []
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateRoutingPolicy(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestUpdateRoutingPolicy(t *testing.T) {
	h, st, _ := testHandler()
	lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)
	st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}
	st.groups["group-2"] = &model.Group{ID: "group-2", Name: "Secondary"}
	st.providers["provider-openai"] = &model.Provider{
		ID:       "provider-openai",
		Vendor:   "openai",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://openai.example"}},
	}
	st.routingPolicies[7] = &model.RoutingPolicy{
		ID:      7,
		APIType: "codex",
		Groups:  []model.RoutingPolicyGroup{{RoutingPolicyID: 7, GroupID: "group-1"}},
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/7", bytes.NewBufferString(`{
		"api_type": "codex",
		"model_match_type": "prefix",
		"model_match_value": "gpt-5",
		"allowed_group_ids": ["group-2"],
		"allowed_vendors": ["openai"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	setPathValue(req, "id", "7")
	w := httptest.NewRecorder()

	h.UpdateRoutingPolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	policy := st.routingPolicies[7]
	if policy.ModelMatchType != model.RoutingPolicyModelMatchTypePrefix {
		t.Fatalf("ModelMatchType = %q, want %q", policy.ModelMatchType, model.RoutingPolicyModelMatchTypePrefix)
	}
	if policy.ModelMatchValue != "gpt-5" {
		t.Fatalf("ModelMatchValue = %q, want %q", policy.ModelMatchValue, "gpt-5")
	}
	if len(policy.Groups) != 1 || policy.Groups[0].GroupID != "group-2" {
		t.Fatalf("Groups = %#v, want group-2", policy.Groups)
	}
	if lifecycles.allRetirements != 1 {
		t.Fatalf("global lifecycle retirements = %d, want 1", lifecycles.allRetirements)
	}
}

func TestUpdateRoutingPolicy_ClearsExactProviderWhenSwitchingBackToFilterMode(t *testing.T) {
	h, st, _ := testHandler()
	st.groups["group-filter"] = &model.Group{ID: "group-filter", Name: "Filter"}
	st.providers["provider-openai"] = &model.Provider{
		ID:       "provider-openai",
		Vendor:   "openai",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://openai.example"}},
	}
	targetProviderID := "provider-openai"
	st.routingPolicies[7] = &model.RoutingPolicy{
		ID:               7,
		APIType:          "codex",
		ModelMatchType:   model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue:  "gpt-5",
		Enabled:          true,
		TargetProviderID: &targetProviderID,
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/7", bytes.NewBufferString(`{
		"api_type": "codex",
		"model_match_type": "exact",
		"model_match_value": "gpt-5",
		"enabled": false,
		"target_provider_id": null,
		"allowed_group_ids": ["group-filter"],
		"allowed_vendors": ["openai"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	setPathValue(req, "id", "7")
	w := httptest.NewRecorder()

	h.UpdateRoutingPolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	policy := st.routingPolicies[7]
	if policy.TargetProviderID != nil {
		t.Fatalf("TargetProviderID = %#v, want nil after switching to filter mode", policy.TargetProviderID)
	}
	if policy.Enabled {
		t.Fatal("Enabled = true, want false from update payload")
	}
	if len(policy.Groups) != 1 || policy.Groups[0].GroupID != "group-filter" {
		t.Fatalf("Groups = %#v, want group-filter", policy.Groups)
	}
	if len(policy.Vendors) != 1 || policy.Vendors[0].Vendor != "openai" {
		t.Fatalf("Vendors = %#v, want openai", policy.Vendors)
	}
}

func TestUpdateRoutingPolicy_ClearsExactProviderWhenFilterModeOmitsTargetProviderField(t *testing.T) {
	h, st, _ := testHandler()
	st.groups["group-filter"] = &model.Group{ID: "group-filter", Name: "Filter"}
	st.providers["provider-openai"] = &model.Provider{
		ID:       "provider-openai",
		Vendor:   "openai",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://openai.example"}},
	}
	targetProviderID := "provider-openai"
	st.routingPolicies[7] = &model.RoutingPolicy{
		ID:               7,
		APIType:          "codex",
		ModelMatchType:   model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue:  "gpt-5",
		Enabled:          true,
		TargetProviderID: &targetProviderID,
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/7", bytes.NewBufferString(`{
		"api_type": "codex",
		"model_match_type": "exact",
		"model_match_value": "gpt-5",
		"enabled": false,
		"allowed_group_ids": ["group-filter"],
		"allowed_vendors": ["openai"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	setPathValue(req, "id", "7")
	w := httptest.NewRecorder()

	h.UpdateRoutingPolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	policy := st.routingPolicies[7]
	if policy.TargetProviderID != nil {
		t.Fatalf("TargetProviderID = %#v, want nil after switching to filter mode with omitted target_provider_id", policy.TargetProviderID)
	}
	if policy.Enabled {
		t.Fatal("Enabled = true, want false from update payload")
	}
	if len(policy.Groups) != 1 || policy.Groups[0].GroupID != "group-filter" {
		t.Fatalf("Groups = %#v, want group-filter", policy.Groups)
	}
	if len(policy.Vendors) != 1 || policy.Vendors[0].Vendor != "openai" {
		t.Fatalf("Vendors = %#v, want openai", policy.Vendors)
	}
}

func TestUpdateRoutingPolicy_NotFound(t *testing.T) {
	h, st, _ := testHandler()
	st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/99", bytes.NewBufferString(`{
		"api_type": "codex",
		"allowed_group_ids": ["group-1"],
		"allowed_vendors": []
	}`))
	req.Header.Set("Content-Type", "application/json")
	setPathValue(req, "id", "99")
	w := httptest.NewRecorder()

	h.UpdateRoutingPolicy(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteRoutingPolicy(t *testing.T) {
	h, st, _ := testHandler()
	lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)
	st.routingPolicies[11] = &model.RoutingPolicy{ID: 11, APIType: "codex"}

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/routing-policies/11", nil)
	setPathValue(req, "id", "11")
	w := httptest.NewRecorder()

	h.DeleteRoutingPolicy(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if _, ok := st.routingPolicies[11]; ok {
		t.Fatal("routing policy was not deleted")
	}
	if lifecycles.allRetirements != 1 {
		t.Fatalf("global lifecycle retirements = %d, want 1", lifecycles.allRetirements)
	}
}

func TestDeleteRoutingPolicy_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/routing-policies/17", nil)
	setPathValue(req, "id", "17")
	w := httptest.NewRecorder()

	h.DeleteRoutingPolicy(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestCreateRoutingPolicy_GroupLookupError(t *testing.T) {
	h, st, _ := testHandler()
	st.listErr = errors.New("db unavailable")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/routing-policies", bytes.NewBufferString(`{
		"api_type": "codex",
		"allowed_group_ids": ["group-1"],
		"allowed_vendors": []
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateRoutingPolicy(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
