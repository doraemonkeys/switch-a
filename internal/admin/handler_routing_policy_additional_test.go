package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
)

func TestParseRoutingPolicyID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    uint
		wantErr string
	}{
		{name: "blank", raw: "   ", wantErr: "Routing policy ID is required"},
		{name: "zero", raw: "0", wantErr: "Routing policy ID must be a positive integer"},
		{name: "invalid", raw: "abc", wantErr: "Routing policy ID must be a positive integer"},
		{name: "valid", raw: "42", want: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRoutingPolicyID(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("parseRoutingPolicyID(%q) error = %v", tt.raw, err)
				}
				if got != tt.want {
					t.Fatalf("parseRoutingPolicyID(%q) = %d, want %d", tt.raw, got, tt.want)
				}
				return
			}

			if err == nil {
				t.Fatalf("parseRoutingPolicyID(%q) error = nil, want %q", tt.raw, tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("parseRoutingPolicyID(%q) error = %q, want %q", tt.raw, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNormalizeRoutingPolicyStrings(t *testing.T) {
	t.Parallel()

	if got := normalizeRoutingPolicyStrings(nil); len(got) != 0 {
		t.Fatalf("normalizeRoutingPolicyStrings(nil) = %#v, want empty slice", got)
	}

	got := normalizeRoutingPolicyStrings([]string{" vendor-b ", "", "vendor-a", "vendor-a", "   "})
	want := []string{"vendor-a", "vendor-b"}
	if len(got) != len(want) {
		t.Fatalf("normalizeRoutingPolicyStrings() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeRoutingPolicyStrings() = %#v, want %#v", got, want)
		}
	}
}

func TestRoutingPolicyPayloadHelpers(t *testing.T) {
	t.Parallel()

	policies := []model.RoutingPolicy{
		{
			ID:      7,
			APIType: "codex",
			Groups: []model.RoutingPolicyGroup{
				{GroupID: "group-b"},
				{GroupID: "group-a"},
			},
			Vendors: []model.RoutingPolicyVendor{
				{Vendor: "openai"},
				{Vendor: "azure"},
			},
		},
		{
			ID:      9,
			APIType: "claude",
		},
	}

	payload := routingPolicyPayload(&policies[0])
	if payload.ID != "7" {
		t.Fatalf("routingPolicyPayload().ID = %q, want %q", payload.ID, "7")
	}
	if len(payload.AllowedGroupIDs) != 2 || payload.AllowedGroupIDs[0] != "group-a" || payload.AllowedGroupIDs[1] != "group-b" {
		t.Fatalf("routingPolicyPayload().AllowedGroupIDs = %#v, want sorted groups", payload.AllowedGroupIDs)
	}
	if len(payload.AllowedVendors) != 2 || payload.AllowedVendors[0] != "azure" || payload.AllowedVendors[1] != "openai" {
		t.Fatalf("routingPolicyPayload().AllowedVendors = %#v, want sorted vendors", payload.AllowedVendors)
	}

	payloads := routingPolicyPayloads(policies)
	if len(payloads) != 2 {
		t.Fatalf("len(routingPolicyPayloads()) = %d, want 2", len(payloads))
	}
	if payloads[1].ID != "9" {
		t.Fatalf("routingPolicyPayloads()[1].ID = %q, want %q", payloads[1].ID, "9")
	}
	if len(payloads[1].AllowedGroupIDs) != 0 || len(payloads[1].AllowedVendors) != 0 {
		t.Fatalf("routingPolicyPayloads()[1] expected empty allow lists, got %#v", payloads[1])
	}
}

func TestBuildRoutingPolicy(t *testing.T) {
	t.Parallel()

	h, st, _ := testHandler()
	st.groups["group-a"] = &model.Group{ID: "group-a", Name: "A"}
	st.groups["group-b"] = &model.Group{ID: "group-b", Name: "B"}
	st.providers["provider-openai"] = &model.Provider{
		ID:       "provider-openai",
		Vendor:   "openai",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://openai.example"}},
	}
	st.providers["provider-azure"] = &model.Provider{
		ID:       "provider-azure",
		Vendor:   "azure",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://azure.example"}},
	}

	exact := model.RoutingPolicyModelMatchTypeExact
	invalidType := model.RoutingPolicyModelMatchType("regex")
	matchValue := "gpt-5.4"

	tests := []struct {
		name    string
		req     RoutingPolicyRequest
		wantErr string
	}{
		{
			name:    "missing api type",
			req:     RoutingPolicyRequest{AllowedVendors: []string{"openai"}},
			wantErr: "API type is required",
		},
		{
			name:    "invalid api type",
			req:     RoutingPolicyRequest{APIType: "invalid", AllowedVendors: []string{"openai"}},
			wantErr: "Invalid API type",
		},
		{
			name:    "invalid model match type",
			req:     RoutingPolicyRequest{APIType: "codex", ModelMatchType: &invalidType, ModelMatchValue: &matchValue, AllowedVendors: []string{"openai"}},
			wantErr: "Invalid model match type",
		},
		{
			name:    "match value without type",
			req:     RoutingPolicyRequest{APIType: "codex", ModelMatchValue: &matchValue, AllowedVendors: []string{"openai"}},
			wantErr: "Model match value requires a model match type",
		},
		{
			name:    "type without value",
			req:     RoutingPolicyRequest{APIType: "codex", ModelMatchType: &exact, AllowedVendors: []string{"openai"}},
			wantErr: "Model match value is required when model match type is set",
		},
		{
			name:    "missing allow list",
			req:     RoutingPolicyRequest{APIType: "codex"},
			wantErr: "At least one allowed group or vendor is required",
		},
		{
			name:    "missing group",
			req:     RoutingPolicyRequest{APIType: "codex", AllowedGroupIDs: []string{"missing-group"}},
			wantErr: "Group not found: missing-group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := h.buildRoutingPolicy(context.Background(), tt.req, nil)
			if err == nil {
				t.Fatalf("buildRoutingPolicy() policy = %#v, want error %q", policy, tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("buildRoutingPolicy() error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}

	policy, err := h.buildRoutingPolicy(context.Background(), RoutingPolicyRequest{
		APIType:         " codex ",
		ModelMatchType:  &exact,
		ModelMatchValue: &matchValue,
		AllowedGroupIDs: []string{" group-b ", "group-a", "group-a"},
		AllowedVendors:  []string{" openai ", "", "azure", "azure"},
	}, nil)
	if err != nil {
		t.Fatalf("buildRoutingPolicy(valid) error = %v", err)
	}
	if policy.APIType != "codex" {
		t.Fatalf("policy.APIType = %q, want %q", policy.APIType, "codex")
	}
	if policy.ModelMatchType != model.RoutingPolicyModelMatchTypeExact || policy.ModelMatchValue != "gpt-5.4" {
		t.Fatalf("policy model match = (%q, %q), want exact gpt-5.4", policy.ModelMatchType, policy.ModelMatchValue)
	}
	if len(policy.Groups) != 2 || policy.Groups[0].GroupID != "group-a" || policy.Groups[1].GroupID != "group-b" {
		t.Fatalf("policy.Groups = %#v, want sorted unique groups", policy.Groups)
	}
	if len(policy.Vendors) != 2 || policy.Vendors[0].Vendor != "azure" || policy.Vendors[1].Vendor != "openai" {
		t.Fatalf("policy.Vendors = %#v, want sorted unique vendors", policy.Vendors)
	}
	if !policy.Enabled {
		t.Fatal("policy.Enabled = false, want true by default")
	}
	if policy.TargetProviderID != nil {
		t.Fatalf("policy.TargetProviderID = %#v, want nil in filter mode", policy.TargetProviderID)
	}

	disabled := false
	targetProviderID := " provider-openai "
	exactPolicy, err := h.buildRoutingPolicy(context.Background(), RoutingPolicyRequest{
		APIType:          "codex",
		Enabled:          &disabled,
		TargetProviderID: routingPolicyOptionalString{set: true, value: &targetProviderID},
	}, nil)
	if err != nil {
		t.Fatalf("buildRoutingPolicy(exact-provider) error = %v", err)
	}
	if exactPolicy.Enabled {
		t.Fatal("exact-provider policy.Enabled = true, want false")
	}
	if exactPolicy.TargetProviderID == nil || *exactPolicy.TargetProviderID != "provider-openai" {
		t.Fatalf("exactPolicy.TargetProviderID = %#v, want provider-openai", exactPolicy.TargetProviderID)
	}
	if len(exactPolicy.Groups) != 0 || len(exactPolicy.Vendors) != 0 {
		t.Fatalf("exactPolicy scope = groups %#v vendors %#v, want empty filter scope", exactPolicy.Groups, exactPolicy.Vendors)
	}
}

func TestBuildRoutingPolicy_PreservesStaleVendorsUntilTheVendorSetChanges(t *testing.T) {
	t.Parallel()

	h, st, _ := testHandler()
	st.providers["provider-openai"] = &model.Provider{
		ID:       "provider-openai",
		Vendor:   "openai",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://openai.example"}},
	}

	prefix := model.RoutingPolicyModelMatchTypePrefix
	prefixValue := "gpt-5"
	disabled := false
	current := &model.RoutingPolicy{
		APIType: "codex",
		Enabled: true,
		Vendors: []model.RoutingPolicyVendor{{Vendor: "legacy-vendor"}},
		Groups:  []model.RoutingPolicyGroup{{GroupID: "legacy-group"}},
	}

	policy, err := h.buildRoutingPolicy(context.Background(), RoutingPolicyRequest{
		APIType:         "codex",
		ModelMatchType:  &prefix,
		ModelMatchValue: &prefixValue,
		Enabled:         &disabled,
		AllowedVendors:  []string{"legacy-vendor"},
	}, current)
	if err != nil {
		t.Fatalf("buildRoutingPolicy(preserve stale vendors) error = %v", err)
	}
	if policy.Enabled {
		t.Fatal("policy.Enabled = true, want false after lifecycle update")
	}
	if len(policy.Vendors) != 1 || policy.Vendors[0].Vendor != "legacy-vendor" {
		t.Fatalf("policy.Vendors = %#v, want legacy-vendor preserved", policy.Vendors)
	}

	_, err = h.buildRoutingPolicy(context.Background(), RoutingPolicyRequest{
		APIType:        "codex",
		AllowedVendors: []string{"legacy-vendor", "openai"},
	}, current)
	if err == nil || err.Error() != `Vendor not available for api_type codex: legacy-vendor` {
		t.Fatalf("buildRoutingPolicy(changed vendor set) error = %v, want stale vendor revalidation failure", err)
	}
}

func TestWriteRoutingPolicyError(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	handled := writeRoutingPolicyError(recorder, invalidRoutingPolicy("bad request"))
	if !handled {
		t.Fatal("writeRoutingPolicyError(validation) = false, want true")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var errResp model.ErrorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Code != ErrCodeValidation {
		t.Fatalf("error code = %q, want %q", errResp.Code, ErrCodeValidation)
	}

	if writeRoutingPolicyError(httptest.NewRecorder(), errors.New("boom")) {
		t.Fatal("writeRoutingPolicyError(non-validation) = true, want false")
	}
}

func TestRoutingPolicyHandlers_ErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("list internal error", func(t *testing.T) {
		h, st, _ := testHandler()
		st.listErr = errors.New("list failed")

		req := httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies", nil)
		w := httptest.NewRecorder()
		h.ListRoutingPolicies(w, req)

		assertRoutingPolicyErrorCode(t, w, http.StatusInternalServerError, ErrCodeInternal)
	})

	t.Run("get errors", func(t *testing.T) {
		h, st, _ := testHandler()

		req := httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies/1", nil)
		setPathValue(req, "id", "")
		w := httptest.NewRecorder()
		h.GetRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusBadRequest, ErrCodeValidation)

		req = httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies/1", nil)
		setPathValue(req, "id", "1")
		w = httptest.NewRecorder()
		h.GetRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusNotFound, ErrCodeNotFound)

		st.getErr = errors.New("db down")
		req = httptest.NewRequest(http.MethodGet, "/admin/api/routing-policies/1", nil)
		setPathValue(req, "id", "1")
		w = httptest.NewRecorder()
		h.GetRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusInternalServerError, ErrCodeInternal)
	})

	t.Run("create errors", func(t *testing.T) {
		h, st, _ := testHandler()
		st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}

		req := httptest.NewRequest(http.MethodPost, "/admin/api/routing-policies", bytes.NewBufferString("{"))
		w := httptest.NewRecorder()
		h.CreateRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusBadRequest, ErrCodeValidation)

		st.createErr = errors.New("create failed")
		req = httptest.NewRequest(http.MethodPost, "/admin/api/routing-policies", bytes.NewBufferString(`{
			"api_type": "codex",
			"allowed_group_ids": ["group-1"]
		}`))
		w = httptest.NewRecorder()
		h.CreateRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusInternalServerError, ErrCodeInternal)
	})

	t.Run("update errors", func(t *testing.T) {
		h, st, _ := testHandler()
		st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}
		st.routingPolicies[3] = &model.RoutingPolicy{ID: 3, APIType: "codex"}

		req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/3", nil)
		setPathValue(req, "id", "0")
		w := httptest.NewRecorder()
		h.UpdateRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusBadRequest, ErrCodeValidation)

		req = httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/3", bytes.NewBufferString("{"))
		setPathValue(req, "id", "3")
		w = httptest.NewRecorder()
		h.UpdateRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusBadRequest, ErrCodeValidation)

		st.updateErr = errors.New("update failed")
		req = httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/3", bytes.NewBufferString(`{
			"api_type": "codex",
			"allowed_group_ids": ["group-1"]
		}`))
		setPathValue(req, "id", "3")
		w = httptest.NewRecorder()
		h.UpdateRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusInternalServerError, ErrCodeInternal)
	})

	t.Run("delete errors", func(t *testing.T) {
		h, st, _ := testHandler()
		st.routingPolicies[5] = &model.RoutingPolicy{ID: 5, APIType: "codex"}

		req := httptest.NewRequest(http.MethodDelete, "/admin/api/routing-policies/5", nil)
		setPathValue(req, "id", "bad")
		w := httptest.NewRecorder()
		h.DeleteRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusBadRequest, ErrCodeValidation)

		st.deleteErr = errors.New("delete failed")
		req = httptest.NewRequest(http.MethodDelete, "/admin/api/routing-policies/5", nil)
		setPathValue(req, "id", "5")
		w = httptest.NewRecorder()
		h.DeleteRoutingPolicy(w, req)
		assertRoutingPolicyErrorCode(t, w, http.StatusInternalServerError, ErrCodeInternal)
	})
}

func assertRoutingPolicyErrorCode(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()

	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d", w.Code, wantStatus)
	}
	var errResp model.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Code != wantCode {
		t.Fatalf("error code = %q, want %q", errResp.Code, wantCode)
	}
}

func TestRoutingPolicyHandlers_ConflictResponses(t *testing.T) {
	t.Parallel()

	h, st, _ := testHandler()
	st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}
	st.routingPolicies[4] = &model.RoutingPolicy{ID: 4, APIType: "codex"}

	st.updateErr = &store.RoutingPolicyConflictError{APIType: "codex"}
	req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/4", bytes.NewBufferString(`{
		"api_type": "codex",
		"allowed_group_ids": ["group-1"]
	}`))
	setPathValue(req, "id", "4")
	w := httptest.NewRecorder()
	h.UpdateRoutingPolicy(w, req)
	assertRoutingPolicyErrorCode(t, w, http.StatusConflict, ErrCodeConflict)
}
