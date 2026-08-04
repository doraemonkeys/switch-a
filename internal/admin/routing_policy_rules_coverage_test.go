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

	"go.uber.org/zap"
)

type routingPolicyCatalogErrorStore struct {
	*mockStore
	listGroupsErr    error
	listProvidersErr error
}

func (s *routingPolicyCatalogErrorStore) ListGroups(ctx context.Context) ([]model.Group, error) {
	if s.listGroupsErr != nil {
		return nil, s.listGroupsErr
	}
	return s.mockStore.ListGroups(ctx)
}

func (s *routingPolicyCatalogErrorStore) ListProviders(ctx context.Context) ([]model.Provider, error) {
	if s.listProvidersErr != nil {
		return nil, s.listProvidersErr
	}
	return s.mockStore.ListProviders(ctx)
}

func newRoutingPolicyCoverageHandler(st Store) *Handler {
	logger, _ := zap.NewDevelopment()
	return NewHandler(Config{
		Store:              st,
		Health:             &mockHealthManager{},
		Concurrency:        &mockConcurrencyTracker{},
		ProviderLifecycles: &mockProviderLifecycleCoordinator{},
		Logger:             logger,
	})
}

func stringPtr(value string) *string {
	return &value
}

func testBoolPtr(value bool) *bool {
	return &value
}

func TestRoutingPolicyOptionalStringUnmarshalJSON_InvalidType(t *testing.T) {
	t.Parallel()

	var field routingPolicyOptionalString
	if err := field.UnmarshalJSON([]byte(`123`)); err == nil {
		t.Fatal("UnmarshalJSON(number) error = nil, want error")
	}

	if !field.IsSet() {
		t.Fatal("IsSet() = false, want true")
	}
}

func TestRoutingPolicyPayload_TrimsTargetProviderID(t *testing.T) {
	t.Parallel()

	payload := routingPolicyPayload(&model.RoutingPolicy{
		ID:               9,
		APIType:          "codex",
		Enabled:          true,
		TargetProviderID: stringPtr(" provider-1 "),
	})
	if payload.TargetProviderID == nil || *payload.TargetProviderID != "provider-1" {
		t.Fatalf("TargetProviderID = %#v, want trimmed provider-1", payload.TargetProviderID)
	}

	payload = routingPolicyPayload(&model.RoutingPolicy{
		ID:               10,
		APIType:          "codex",
		Enabled:          true,
		TargetProviderID: stringPtr("   "),
	})
	if payload.TargetProviderID != nil {
		t.Fatalf("TargetProviderID = %#v, want nil", payload.TargetProviderID)
	}
}

func TestNewRoutingPolicyCatalogFromMaps_IgnoresNilEntries(t *testing.T) {
	t.Parallel()

	catalog := newRoutingPolicyCatalogFromMaps(
		map[string]*model.Group{
			"group-1": {ID: "group-1", Name: "Primary"},
			"nil":     nil,
		},
		map[string]*model.Provider{
			"provider-1": {
				ID:     "provider-1",
				Vendor: " OpenAI ",
				APITypes: []model.ProviderAPIType{
					{ProviderID: "provider-1", APIType: "codex"},
				},
			},
			"provider-2": {
				ID:     "provider-2",
				Vendor: "   ",
				APITypes: []model.ProviderAPIType{
					{ProviderID: "provider-2", APIType: "codex"},
				},
			},
			"nil": nil,
		},
	)

	if len(catalog.groupsByID) != 1 {
		t.Fatalf("len(groupsByID) = %d, want 1", len(catalog.groupsByID))
	}
	if len(catalog.providersByID) != 2 {
		t.Fatalf("len(providersByID) = %d, want 2", len(catalog.providersByID))
	}
	if _, ok := catalog.vendorsByAPIType["codex"]["OpenAI"]; !ok {
		t.Fatalf("vendorsByAPIType[codex] = %#v, want OpenAI", catalog.vendorsByAPIType["codex"])
	}
	if len(catalog.vendorsByAPIType["codex"]) != 1 {
		t.Fatalf("vendorsByAPIType[codex] len = %d, want 1", len(catalog.vendorsByAPIType["codex"]))
	}
}

func TestNewRoutingPolicyCatalog_EmptyAPITypesDoNotAdvertiseVendorAvailability(t *testing.T) {
	t.Parallel()

	catalog := newRoutingPolicyCatalog(nil, []model.Provider{
		{
			ID:       "provider-empty",
			Vendor:   "OpenAI",
			APITypes: nil,
		},
	})

	if vendors := catalog.vendorsByAPIType["codex"]; len(vendors) != 0 {
		t.Fatalf("vendorsByAPIType[codex] = %#v, want empty for provider without explicit api types", vendors)
	}
}

func TestBuildRoutingPolicyFromCatalog_ExactProviderValidation(t *testing.T) {
	t.Parallel()

	catalog := newRoutingPolicyCatalog(nil, []model.Provider{
		{
			ID:     "provider-1",
			Vendor: "OpenAI",
			APITypes: []model.ProviderAPIType{
				{ProviderID: "provider-1", APIType: "chat"},
			},
		},
		{
			ID:       "provider-empty",
			Vendor:   "Legacy",
			APITypes: nil,
		},
	})

	tests := []struct {
		name    string
		spec    routingPolicySpec
		wantErr string
	}{
		{
			name: "unknown target provider",
			spec: routingPolicySpec{
				APIType:             "codex",
				TargetProviderID:    stringPtr("missing"),
				TargetProviderIDSet: true,
			},
			wantErr: "Target provider not found: missing",
		},
		{
			name: "target provider missing api type",
			spec: routingPolicySpec{
				APIType:             "codex",
				TargetProviderID:    stringPtr("provider-1"),
				TargetProviderIDSet: true,
			},
			wantErr: "Target provider does not support api_type: codex",
		},
		{
			name: "target provider without explicit api types",
			spec: routingPolicySpec{
				APIType:             "codex",
				TargetProviderID:    stringPtr("provider-empty"),
				TargetProviderIDSet: true,
			},
			wantErr: "Target provider does not support api_type: codex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy, err := buildRoutingPolicyFromCatalog(tt.spec, catalog, nil)
			if err == nil {
				t.Fatalf("buildRoutingPolicyFromCatalog() error = nil, want %q", tt.wantErr)
			}
			if policy != nil {
				t.Fatalf("buildRoutingPolicyFromCatalog() policy = %#v, want nil", policy)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("buildRoutingPolicyFromCatalog() error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildRoutingPolicyFromCatalog_TargetProviderCannotCombineWithFilters(t *testing.T) {
	t.Parallel()

	_, err := buildRoutingPolicyFromCatalog(routingPolicySpec{
		APIType:             "codex",
		TargetProviderID:    stringPtr("provider-1"),
		TargetProviderIDSet: true,
		AllowedGroupIDs:     []string{"group-1"},
	}, newRoutingPolicyCatalog(nil, []model.Provider{
		{
			ID: "provider-1",
			APITypes: []model.ProviderAPIType{
				{ProviderID: "provider-1", APIType: "codex"},
			},
		},
	}), nil)
	if err == nil || err.Error() != "Target provider cannot be combined with allowed groups or vendors" {
		t.Fatalf("buildRoutingPolicyFromCatalog(mixed scope) error = %v, want mixed-scope validation error", err)
	}
}

func TestNormalizeRoutingPolicyTargetProviderID_BlankStringBecomesNil(t *testing.T) {
	t.Parallel()

	if got := normalizeRoutingPolicyTargetProviderID(nil); got != nil {
		t.Fatalf("normalizeRoutingPolicyTargetProviderID(nil) = %#v, want nil", got)
	}

	if got := normalizeRoutingPolicyTargetProviderID(stringPtr("   ")); got != nil {
		t.Fatalf("normalizeRoutingPolicyTargetProviderID(blank) = %#v, want nil", got)
	}

	got := normalizeRoutingPolicyTargetProviderID(stringPtr(" provider-1 "))
	if got == nil || *got != "provider-1" {
		t.Fatalf("normalizeRoutingPolicyTargetProviderID(trimmed) = %#v, want provider-1", got)
	}
}

func TestNormalizeRoutingPolicyStrings_EmptyAfterTrimming(t *testing.T) {
	t.Parallel()

	got := normalizeRoutingPolicyStrings([]string{"   ", "\t"})
	if len(got) != 0 {
		t.Fatalf("normalizeRoutingPolicyStrings(blank values) = %#v, want empty slice", got)
	}
}

func TestRoutingPolicyVendorSetPreserved_Branches(t *testing.T) {
	t.Parallel()

	current := &model.RoutingPolicy{
		APIType: "codex",
		Vendors: []model.RoutingPolicyVendor{
			{Vendor: "vendor-a"},
			{Vendor: "vendor-b"},
		},
	}
	exactCurrent := &model.RoutingPolicy{
		APIType:          "codex",
		TargetProviderID: stringPtr("provider-1"),
	}

	tests := []struct {
		name             string
		current          *model.RoutingPolicy
		apiType          string
		requested        []string
		targetProviderID *string
		want             bool
	}{
		{name: "current nil", apiType: "codex", requested: []string{"vendor-a"}, want: false},
		{name: "current exact target", current: exactCurrent, apiType: "codex", requested: []string{"vendor-a"}, want: false},
		{name: "requested exact target", current: current, apiType: "codex", requested: []string{"vendor-a", "vendor-b"}, targetProviderID: stringPtr("provider-2"), want: false},
		{name: "api type changed", current: current, apiType: "chat", requested: []string{"vendor-a", "vendor-b"}, want: false},
		{name: "vendor count changed", current: current, apiType: "codex", requested: []string{"vendor-a"}, want: false},
		{name: "vendor order mismatch", current: current, apiType: "codex", requested: []string{"vendor-a", "vendor-c"}, want: false},
		{name: "vendor set preserved", current: current, apiType: "codex", requested: []string{"vendor-a", "vendor-b"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := routingPolicyVendorSetPreserved(tt.current, tt.apiType, tt.requested, tt.targetProviderID); got != tt.want {
				t.Fatalf("routingPolicyVendorSetPreserved() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdateRoutingPolicy_InternalFailureBranches(t *testing.T) {
	t.Parallel()

	t.Run("get current routing policy error", func(t *testing.T) {
		t.Parallel()

		store := &routingPolicyCatalogErrorStore{mockStore: newMockStore()}
		store.getErr = errors.New("lookup failed")

		handler := newRoutingPolicyCoverageHandler(store)
		req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/5", bytes.NewBufferString(`{
			"api_type": "codex",
			"enabled": true,
			"allowed_group_ids": ["group-1"]
		}`))
		setPathValue(req, "id", "5")
		resp := httptest.NewRecorder()

		handler.UpdateRoutingPolicy(resp, req)
		assertRoutingPolicyErrorCode(t, resp, http.StatusInternalServerError, ErrCodeInternal)
	})

	t.Run("catalog load failure during validation", func(t *testing.T) {
		t.Parallel()

		store := &routingPolicyCatalogErrorStore{
			mockStore:        newMockStore(),
			listProvidersErr: errors.New("providers failed"),
		}
		store.routingPolicies[5] = &model.RoutingPolicy{ID: 5, APIType: "codex"}
		store.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}

		handler := newRoutingPolicyCoverageHandler(store)
		req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/5", bytes.NewBufferString(`{
			"api_type": "codex",
			"enabled": false,
			"allowed_group_ids": ["group-1"]
		}`))
		setPathValue(req, "id", "5")
		resp := httptest.NewRecorder()

		handler.UpdateRoutingPolicy(resp, req)
		assertRoutingPolicyErrorCode(t, resp, http.StatusInternalServerError, ErrCodeInternal)
	})
}

func TestUpdateRoutingPolicy_ValidationAndNotFoundBranches(t *testing.T) {
	t.Parallel()

	t.Run("validation error writes bad request", func(t *testing.T) {
		t.Parallel()

		store := &routingPolicyCatalogErrorStore{mockStore: newMockStore()}
		store.routingPolicies[5] = &model.RoutingPolicy{ID: 5, APIType: "codex"}
		store.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}
		store.providers["provider-1"] = &model.Provider{
			ID: "provider-1",
			APITypes: []model.ProviderAPIType{
				{ProviderID: "provider-1", APIType: "codex"},
			},
		}

		handler := newRoutingPolicyCoverageHandler(store)
		req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/5", bytes.NewBufferString(`{
			"api_type": "codex",
			"enabled": true,
			"target_provider_id": "provider-1",
			"allowed_group_ids": ["group-1"]
		}`))
		setPathValue(req, "id", "5")
		resp := httptest.NewRecorder()

		handler.UpdateRoutingPolicy(resp, req)
		assertRoutingPolicyErrorCode(t, resp, http.StatusBadRequest, ErrCodeValidation)
	})

	t.Run("store update not found returns 404", func(t *testing.T) {
		t.Parallel()

		st := &routingPolicyCatalogErrorStore{mockStore: newMockStore()}
		st.routingPolicies[6] = &model.RoutingPolicy{ID: 6, APIType: "codex"}
		st.groups["group-1"] = &model.Group{ID: "group-1", Name: "Primary"}
		st.updateErr = store.ErrNotFound

		handler := newRoutingPolicyCoverageHandler(st)
		req := httptest.NewRequest(http.MethodPut, "/admin/api/routing-policies/6", bytes.NewBufferString(`{
			"api_type": "codex",
			"enabled": true,
			"allowed_group_ids": ["group-1"]
		}`))
		setPathValue(req, "id", "6")
		resp := httptest.NewRecorder()

		handler.UpdateRoutingPolicy(resp, req)
		assertRoutingPolicyErrorCode(t, resp, http.StatusNotFound, ErrCodeNotFound)
	})
}

func TestBuildRoutingPolicy_LoadRoutingPolicyCatalogProviderError(t *testing.T) {
	t.Parallel()

	store := &routingPolicyCatalogErrorStore{
		mockStore:        newMockStore(),
		listProvidersErr: errors.New("providers failed"),
	}
	handler := newRoutingPolicyCoverageHandler(store)

	policy, err := handler.buildRoutingPolicy(context.Background(), RoutingPolicyRequest{
		APIType:         "codex",
		Enabled:         testBoolPtr(true),
		AllowedGroupIDs: []string{"group-1"},
	}, nil)
	if err == nil {
		t.Fatal("buildRoutingPolicy() error = nil, want error")
	}
	if policy != nil {
		t.Fatalf("buildRoutingPolicy() policy = %#v, want nil", policy)
	}
}

func TestRoutingPolicyOptionalString_RoundTripJSON(t *testing.T) {
	t.Parallel()

	body := []byte(`{"target_provider_id":"provider-1"}`)
	var req struct {
		TargetProviderID routingPolicyOptionalString `json:"target_provider_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !req.TargetProviderID.IsSet() {
		t.Fatal("TargetProviderID.IsSet() = false, want true")
	}
	if value := req.TargetProviderID.Value(); value == nil || *value != "provider-1" {
		t.Fatalf("TargetProviderID.Value() = %#v, want provider-1", value)
	}
}
