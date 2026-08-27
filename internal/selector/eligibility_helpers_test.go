package selector

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
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
	health := newMockHealthChecker()
	req := &model.SelectRequest{APIType: " codex "}
	eligibility, err := NewProviderSelectionEligibility(context.Background(), store, health, req)
	if err != nil {
		t.Fatalf("NewProviderSelectionEligibility() error = %v", err)
	}

	newProvider := func() *model.Provider {
		provider := store.credentialSessionProvider(model.Provider{
			ID:      "p-eligible",
			Enabled: true,
			APITypes: []model.ProviderAPIType{
				{ProviderID: "p-eligible", APIType: "codex"},
			},
		}, "codex")
		return &provider
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
	missingSession := newProvider()
	missingSession.CredentialSessions = nil
	if allowed, err := eligibility.AllowsProvider(context.Background(), missingSession); err != nil || allowed {
		t.Fatalf("AllowsProvider(missing session) = (%v, %v), want (false, nil)", allowed, err)
	}

	inactive := newProvider()
	inactive.CredentialSessions[0].Credential.AuthState.Status = credentialsession.AuthStatusNotConnected
	if allowed, err := eligibility.AllowsProvider(context.Background(), inactive); err != nil || allowed {
		t.Fatalf("AllowsProvider(inactive auth) = (%v, %v), want (false, nil)", allowed, err)
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

func TestCredentialSessionUsableRequiresActiveImmutableSnapshot(t *testing.T) {
	t.Parallel()

	active := credentialsession.Snapshot{
		SessionID:  "session-a",
		Vendor:     "vendor-a",
		Kind:       credentialsession.KindAPIKey,
		SecretData: "secret",
		Version:    1,
		AuthState:  credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if !credentialSessionUsable(active) {
		t.Fatal("active immutable credential snapshot was rejected")
	}
	for name, mutate := range map[string]func(*credentialsession.Snapshot){
		"missing session": func(snapshot *credentialsession.Snapshot) { snapshot.SessionID = "" },
		"missing secret":  func(snapshot *credentialsession.Snapshot) { snapshot.SecretData = "" },
		"invalid version": func(snapshot *credentialsession.Snapshot) { snapshot.Version = 0 },
		"inactive": func(snapshot *credentialsession.Snapshot) {
			snapshot.AuthState.Status = credentialsession.AuthStatusReauthRequired
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := active
			mutate(&snapshot)
			if credentialSessionUsable(snapshot) {
				t.Fatal("unusable credential session was accepted")
			}
		})
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
