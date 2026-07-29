package selector

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestListRoutingPoliciesByAPITypeHandlesNilAndEmptyInputs(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	store.routingPolicies = []model.RoutingPolicy{
		{APIType: "codex", Enabled: true},
		{APIType: "chat", Enabled: true},
	}

	policies, err := listRoutingPoliciesByAPIType(context.Background(), nil, "codex")
	if err != nil {
		t.Fatalf("listRoutingPoliciesByAPIType(nil source) error = %v", err)
	}
	if len(policies) != 0 {
		t.Fatalf("policies = %#v, want nil for nil source", policies)
	}

	policies, err = listRoutingPoliciesByAPIType(context.Background(), store, "")
	if err != nil {
		t.Fatalf("listRoutingPoliciesByAPIType(empty api type) error = %v", err)
	}
	if len(policies) != 0 {
		t.Fatalf("policies = %#v, want nil for empty api type", policies)
	}

	policies, err = listRoutingPoliciesByAPIType(context.Background(), store, "codex")
	if err != nil {
		t.Fatalf("listRoutingPoliciesByAPIType(store) error = %v", err)
	}
	if len(policies) != 1 || policies[0].APIType != "codex" {
		t.Fatalf("policies = %#v, want only codex policy", policies)
	}
}

func TestNewProviderSelectionEligibilityAndAllowsProvider(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	store.routingPolicies = []model.RoutingPolicy{
		{
			Enabled:          true,
			APIType:          "codex",
			TargetProviderID: stringPtr("p-eligible"),
		},
	}
	store.authStates["p-eligible"] = &model.ProviderAuthState{
		ProviderID: "p-eligible",
		Status:     model.ProviderAuthStatusActive,
	}

	health := newMockHealthChecker()
	req := &model.SelectRequest{APIType: " codex "}
	eligibility, err := NewProviderSelectionEligibility(context.Background(), store, health, req)
	if err != nil {
		t.Fatalf("NewProviderSelectionEligibility() error = %v", err)
	}

	newProvider := func() *model.Provider {
		return &model.Provider{
			ID:             "p-eligible",
			Enabled:        true,
			CredentialType: model.ProviderCredentialTypeAPIKey,
			Credential: &model.ProviderCredential{
				ProviderID: "p-eligible",
				SecretData: "api-key",
			},
			APITypes: []model.ProviderAPIType{
				{ProviderID: "p-eligible", APIType: "codex"},
			},
		}
	}

	if !eligibility.IsEligible(context.Background(), newProvider()) {
		t.Fatal("IsEligible() = false, want true for matching healthy provider")
	}

	if allowed, err := eligibility.AllowsProvider(context.Background(), nil); err != nil || allowed {
		t.Fatalf("AllowsProvider(nil) = (%v, %v), want (false, nil)", allowed, err)
	}

	disabled := newProvider()
	disabled.Enabled = false
	if allowed, err := eligibility.AllowsProvider(context.Background(), disabled); err != nil || allowed {
		t.Fatalf("AllowsProvider(disabled) = (%v, %v), want (false, nil)", allowed, err)
	}

	wrongAPIType := newProvider()
	wrongAPIType.APITypes = []model.ProviderAPIType{{ProviderID: "p-eligible", APIType: "chat"}}
	if allowed, err := eligibility.AllowsProvider(context.Background(), wrongAPIType); err != nil || allowed {
		t.Fatalf("AllowsProvider(wrong api type) = (%v, %v), want (false, nil)", allowed, err)
	}

	eligibility.routing = routingPolicyResolution{
		constrained:      true,
		matched:          true,
		targetProviderID: "other-provider",
	}
	if allowed, err := eligibility.AllowsProvider(context.Background(), newProvider()); err != nil || allowed {
		t.Fatalf("AllowsProvider(routing mismatch) = (%v, %v), want (false, nil)", allowed, err)
	}

	eligibility.routing = routingPolicyResolution{}
	store.authStateErr = errors.New("auth state unavailable")
	if allowed, err := eligibility.AllowsProvider(context.Background(), newProvider()); err == nil || allowed {
		t.Fatalf("AllowsProvider(auth error) = (%v, %v), want (false, error)", allowed, err)
	}

	store.authStateErr = nil
	store.authStates["p-eligible"] = &model.ProviderAuthState{
		ProviderID: "p-eligible",
		Status:     model.ProviderAuthStatusNotConnected,
	}
	if allowed, err := eligibility.AllowsProvider(context.Background(), newProvider()); err != nil || allowed {
		t.Fatalf("AllowsProvider(inactive auth) = (%v, %v), want (false, nil)", allowed, err)
	}

	store.authStates["p-eligible"] = &model.ProviderAuthState{
		ProviderID: "p-eligible",
		Status:     model.ProviderAuthStatusActive,
	}
	health.available["p-eligible"] = false
	if allowed, err := eligibility.AllowsProvider(context.Background(), newProvider()); err != nil || allowed {
		t.Fatalf("AllowsProvider(unhealthy) = (%v, %v), want (false, nil)", allowed, err)
	}

	health.available["p-eligible"] = true
	eligibility.req = &model.SelectRequest{
		APIType:    "codex",
		SwitchMode: model.SwitchModeFailover,
		ProviderSwitchHistory: &model.ProviderSwitchHistory{
			OriginProviderID: "origin",
			AttemptChain:     []string{"origin"},
		},
		ProviderContinuityContext: &model.ProviderContinuityContext{
			VisibleOriginProviderID: "origin",
			ContaminatedVendors:     []string{"origin-vendor"},
			StrictestScope:          model.ScopeNone,
		},
		MaxProviderSwitches: 1,
	}
	if allowed, err := eligibility.AllowsProvider(context.Background(), newProvider()); err != nil || allowed {
		t.Fatalf("AllowsProvider(failover disallowed) = (%v, %v), want (false, nil)", allowed, err)
	}

	eligibility.req = req
	if allowed, err := eligibility.AllowsProvider(context.Background(), newProvider()); err != nil || !allowed {
		t.Fatalf("AllowsProvider(reset happy path) = (%v, %v), want (true, nil)", allowed, err)
	}
}

func TestEligibilityRoutingAndRequestHelpersNormalizeInputs(t *testing.T) {
	t.Parallel()

	if routingPolicyConsumesModel(nil) {
		t.Fatal("routingPolicyConsumesModel(nil) = true, want false")
	}
	if routingPolicyConsumesModel(&model.RoutingPolicy{
		ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "   ",
	}) {
		t.Fatal("routingPolicyConsumesModel(blank exact) = true, want false")
	}
	if !routingPolicyConsumesModel(&model.RoutingPolicy{
		ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
		ModelMatchValue: " gpt-",
	}) {
		t.Fatal("routingPolicyConsumesModel(prefix) = false, want true")
	}

	if got := routingPolicyTargetProviderID(nil); got != "" {
		t.Fatalf("routingPolicyTargetProviderID(nil) = %q, want empty", got)
	}
	if got := routingPolicyTargetProviderID(&model.RoutingPolicy{}); got != "" {
		t.Fatalf("routingPolicyTargetProviderID(nil target) = %q, want empty", got)
	}
	if got := routingPolicyTargetProviderID(&model.RoutingPolicy{TargetProviderID: stringPtr(" provider-exact ")}); got != "provider-exact" {
		t.Fatalf("routingPolicyTargetProviderID(trimmed) = %q, want %q", got, "provider-exact")
	}

	groupSet := buildRoutingPolicyGroupSet([]model.RoutingPolicyGroup{
		{GroupID: " group-a "},
		{GroupID: ""},
		{GroupID: "   "},
	})
	if _, ok := groupSet["group-a"]; !ok || len(groupSet) != 1 {
		t.Fatalf("buildRoutingPolicyGroupSet() = %#v, want only trimmed group-a", groupSet)
	}
	if got := buildRoutingPolicyGroupSet(nil); got != nil {
		t.Fatalf("buildRoutingPolicyGroupSet(nil) = %#v, want nil", got)
	}

	vendorSet := buildRoutingPolicyVendorSet([]model.RoutingPolicyVendor{
		{Vendor: " vendor-a "},
		{Vendor: ""},
		{Vendor: "   "},
	})
	if _, ok := vendorSet["vendor-a"]; !ok || len(vendorSet) != 1 {
		t.Fatalf("buildRoutingPolicyVendorSet() = %#v, want only trimmed vendor-a", vendorSet)
	}
	if got := buildRoutingPolicyVendorSet(nil); got != nil {
		t.Fatalf("buildRoutingPolicyVendorSet(nil) = %#v, want nil", got)
	}

	if got := normalizeRequestModel(" unknown "); got != "" {
		t.Fatalf("normalizeRequestModel(unknown) = %q, want empty", got)
	}
	if got := normalizeRequestModel(" gpt-5.4 "); got != "gpt-5.4" {
		t.Fatalf("normalizeRequestModel(trimmed) = %q, want %q", got, "gpt-5.4")
	}

	key := BuildContinuityKey(&model.SelectRequest{
		ClientIP:   "127.0.0.1",
		User:       "user-1",
		APIType:    " codex ",
		Model:      unknownModelSentinel,
		StickyMode: model.StickyModeModel,
	})
	if key.IP != "127.0.0.1" || key.User != "user-1" || key.APIType != "codex" || key.Model != "" {
		t.Fatalf("BuildContinuityKey() = %#v, want trimmed api type and empty model", key)
	}

	var nilEligibility *ProviderSelectionEligibility
	if got := reqClientIP(nil); got != "" {
		t.Fatalf("reqClientIP(nil) = %q, want empty", got)
	}
	if got := reqUser(nil); got != "" {
		t.Fatalf("reqUser(nil) = %q, want empty", got)
	}
	if got := reqAPIType(nil); got != "" {
		t.Fatalf("reqAPIType(nil) = %q, want empty", got)
	}
	if got := reqModel(nil); got != "" {
		t.Fatalf("reqModel(nil) = %q, want empty", got)
	}
	if got := reqStickyMode(nil); got != model.StickyModeOff {
		t.Fatalf("reqStickyMode(nil) = %q, want %q", got, model.StickyModeOff)
	}
	if got := reqSwitchMode(nil); got != model.SwitchModeInitial {
		t.Fatalf("reqSwitchMode(nil) = %q, want %q", got, model.SwitchModeInitial)
	}
	if got := reqProviderSwitchHistory(nil); got != nil {
		t.Fatalf("reqProviderSwitchHistory(nil) = %#v, want nil", got)
	}
	if got := reqProviderContinuityContext(nil); got != nil {
		t.Fatalf("reqProviderContinuityContext(nil) = %#v, want nil", got)
	}
	if got := reqVisibleContinuitySeedCandidate(nil); got != nil {
		t.Fatalf("reqVisibleContinuitySeedCandidate(nil) = %#v, want nil", got)
	}
	if got := nilEligibility.reqMaxProviderSwitches(); got != 0 {
		t.Fatalf("reqMaxProviderSwitches(nil eligibility) = %d, want 0", got)
	}
}

func TestRoutingPolicyResolutionAllowsProviderCoversConstraintModes(t *testing.T) {
	t.Parallel()

	provider := &model.Provider{
		ID:      " provider-a ",
		Vendor:  "vendor-a",
		GroupID: stringPtr("group-a"),
	}

	if !(routingPolicyResolution{}).allowsProvider(provider) {
		t.Fatal("unconstrained resolution should allow provider")
	}
	if (routingPolicyResolution{constrained: true}).allowsProvider(provider) {
		t.Fatal("constrained unmatched resolution should reject provider")
	}
	if !(routingPolicyResolution{
		constrained:      true,
		matched:          true,
		targetProviderID: "provider-a",
	}).allowsProvider(provider) {
		t.Fatal("exact-provider resolution should allow trimmed matching provider id")
	}
	if (routingPolicyResolution{
		constrained:      true,
		matched:          true,
		targetProviderID: "provider-b",
	}).allowsProvider(provider) {
		t.Fatal("exact-provider resolution should reject different provider id")
	}

	groupOnly := routingPolicyResolution{
		constrained: true,
		matched:     true,
		groupIDs:    map[string]struct{}{"group-a": {}},
	}
	if !groupOnly.allowsProvider(provider) {
		t.Fatal("group-constrained resolution should allow matching group")
	}
	noGroup := *provider
	noGroup.GroupID = nil
	if groupOnly.allowsProvider(&noGroup) {
		t.Fatal("group-constrained resolution should reject provider without group")
	}

	vendorOnly := routingPolicyResolution{
		constrained: true,
		matched:     true,
		vendors:     map[string]struct{}{"vendor-a": {}},
	}
	if !vendorOnly.allowsProvider(provider) {
		t.Fatal("vendor-constrained resolution should allow matching vendor")
	}
	if vendorOnly.allowsProvider(&model.Provider{ID: "provider-a", Vendor: "vendor-b"}) {
		t.Fatal("vendor-constrained resolution should reject mismatched vendor")
	}
}

func TestProviderSelectionEligibilityProviderAuthStateCoversFallbackPaths(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	eligibility := &ProviderSelectionEligibility{source: store}

	nilState, err := eligibility.providerAuthState(context.Background(), nil)
	if err != nil {
		t.Fatalf("providerAuthState(nil) error = %v", err)
	}
	if nilState.ProviderID != "" || nilState.Status != model.DefaultProviderAuthStatus(model.ProviderCredentialTypeAPIKey) {
		t.Fatalf("providerAuthState(nil) = %#v, want normalized zero-provider api-key state", nilState)
	}

	providerWithEmbeddedState := &model.Provider{
		ID:             "provider-embedded",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		AuthState: &model.ProviderAuthState{
			Status: model.ProviderAuthStatus("invalid"),
		},
	}
	embeddedState, err := eligibility.providerAuthState(context.Background(), providerWithEmbeddedState)
	if err != nil {
		t.Fatalf("providerAuthState(embedded) error = %v", err)
	}
	if embeddedState.ProviderID != "provider-embedded" || embeddedState.Status != model.ProviderAuthStatusNotConnected {
		t.Fatalf("providerAuthState(embedded) = %#v, want normalized chatgpt state", embeddedState)
	}

	store.authStates["provider-store"] = &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	}
	storeState, err := eligibility.providerAuthState(context.Background(), &model.Provider{
		ID:             "provider-store",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	})
	if err != nil {
		t.Fatalf("providerAuthState(store) error = %v", err)
	}
	if storeState.ProviderID != "provider-store" || storeState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("providerAuthState(store) = %#v, want active stored state", storeState)
	}

	store.authStateErr = errors.New("auth state unavailable")
	if _, err := eligibility.providerAuthState(context.Background(), &model.Provider{ID: "provider-error"}); err == nil {
		t.Fatal("providerAuthState(store error) = nil error, want propagated error")
	}

	store.authStateErr = nil
	fallbackState, err := eligibility.providerAuthState(context.Background(), &model.Provider{
		ID:             "provider-fallback",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Credential: &model.ProviderCredential{
			ProviderID: "provider-fallback",
			SecretData: "api-key",
		},
	})
	if err != nil {
		t.Fatalf("providerAuthState(fallback) error = %v", err)
	}
	if fallbackState.ProviderID != "provider-fallback" || fallbackState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("providerAuthState(fallback) = %#v, want active fallback credential state", fallbackState)
	}
}

func TestProviderSupportsAPITypeHandlesCommonShapes(t *testing.T) {
	t.Parallel()

	if providerSupportsAPIType(nil, "codex") {
		t.Fatal("providerSupportsAPIType(nil) = true, want false")
	}
	if providerSupportsAPIType(&model.Provider{}, "") {
		t.Fatal("providerSupportsAPIType(empty api type) = true, want false")
	}
	if providerSupportsAPIType(&model.Provider{ID: "provider-any"}, "codex") {
		t.Fatal("providerSupportsAPIType(no explicit api types) = true, want false")
	}

	provider := &model.Provider{
		ID: "provider-typed",
		APITypes: []model.ProviderAPIType{
			{ProviderID: "provider-typed", APIType: "codex"},
		},
	}
	if !providerSupportsAPIType(provider, "codex") {
		t.Fatal("providerSupportsAPIType(codex) = false, want true")
	}
	if providerSupportsAPIType(provider, "chat") {
		t.Fatal("providerSupportsAPIType(chat) = true, want false")
	}
}
