package selector

import (
	"context"
	"errors"
	"testing"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

func TestProviderSelectionEligibilityRequestReturnsSnapshot(t *testing.T) {
	var nilEligibility *ProviderSelectionEligibility
	if got := nilEligibility.Request(); got != nil {
		t.Fatalf("nil eligibility request = %#v, want nil", got)
	}

	req := &model.SelectRequest{APIType: "codex", Model: "gpt-5.4"}
	eligibility := &ProviderSelectionEligibility{req: req}
	if got := eligibility.Request(); got != req {
		t.Fatalf("eligibility request = %#v, want original pointer %#v", got, req)
	}
}

func TestRoutingPolicyRankRecognizesSupportedMatchTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		policy       *model.RoutingPolicy
		requestModel string
		wantRank     int
		wantPrefix   int
		wantMatched  bool
	}{
		{
			name:        "api-type only",
			policy:      &model.RoutingPolicy{ModelMatchType: model.RoutingPolicyModelMatchTypeNone},
			wantRank:    1,
			wantMatched: true,
		},
		{
			name:         "exact",
			policy:       &model.RoutingPolicy{ModelMatchType: model.RoutingPolicyModelMatchTypeExact, ModelMatchValue: "gpt-5.4"},
			requestModel: "gpt-5.4",
			wantRank:     3,
			wantPrefix:   len("gpt-5.4"),
			wantMatched:  true,
		},
		{
			name:         "prefix",
			policy:       &model.RoutingPolicy{ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-"},
			requestModel: "gpt-5.4",
			wantRank:     2,
			wantPrefix:   len("gpt-"),
			wantMatched:  true,
		},
		{
			name:         "unsupported",
			policy:       &model.RoutingPolicy{ModelMatchType: "regex", ModelMatchValue: "gpt-.*"},
			requestModel: "gpt-5.4",
			wantMatched:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rank, prefixLen, matched := routingPolicyRank(tt.policy, tt.requestModel)
			if rank != tt.wantRank || prefixLen != tt.wantPrefix || matched != tt.wantMatched {
				t.Fatalf(
					"routingPolicyRank() = (%d, %d, %t), want (%d, %d, %t)",
					rank,
					prefixLen,
					matched,
					tt.wantRank,
					tt.wantPrefix,
					tt.wantMatched,
				)
			}
		})
	}
}

func TestResolveRoutingPolicyPrefersMostSpecificRule(t *testing.T) {
	t.Parallel()

	req := &model.SelectRequest{APIType: "codex", Model: "gpt-5.4"}
	resolution := resolveRoutingPolicy([]model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "group-api"}},
			Vendors: []model.RoutingPolicyVendor{{Vendor: " vendor-api "}},
		},
		{
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
			ModelMatchValue: "gpt-",
			Groups:          []model.RoutingPolicyGroup{{GroupID: "group-prefix"}},
			Vendors:         []model.RoutingPolicyVendor{{Vendor: " vendor-prefix "}, {Vendor: ""}},
		},
		{
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
			ModelMatchValue: "gpt-5.4",
			Groups:          []model.RoutingPolicyGroup{{GroupID: "group-exact"}},
			Vendors:         []model.RoutingPolicyVendor{{Vendor: " vendor-exact "}, {Vendor: "   "}},
		},
	}, req)

	if !resolution.constrained || !resolution.matched {
		t.Fatalf("resolution = %#v, want constrained matched result", resolution)
	}
	if _, ok := resolution.groupIDs["group-exact"]; !ok || len(resolution.groupIDs) != 1 {
		t.Fatalf("groupIDs = %#v, want only group-exact", resolution.groupIDs)
	}
	if _, ok := resolution.vendors["vendor-exact"]; !ok || len(resolution.vendors) != 1 {
		t.Fatalf("vendors = %#v, want only trimmed vendor-exact", resolution.vendors)
	}
}

func TestResolveRoutingPolicyUnknownModelFallsBackToAPITypeRule(t *testing.T) {
	t.Parallel()

	req := &model.SelectRequest{APIType: "codex", Model: unknownModelSentinel}
	resolution := resolveRoutingPolicy([]model.RoutingPolicy{
		{
			APIType: "codex",
			Groups:  []model.RoutingPolicyGroup{{GroupID: "group-api"}},
		},
		{
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
			ModelMatchValue: "gpt-",
			Groups:          []model.RoutingPolicyGroup{{GroupID: "group-prefix"}},
		},
	}, req)

	if !resolution.constrained || !resolution.matched {
		t.Fatalf("resolution = %#v, want constrained matched result", resolution)
	}
	if !resolution.consumesHiddenModel {
		t.Fatalf("resolution.consumesHiddenModel = false, want true when a model-specific rule is bypassed")
	}
	if _, ok := resolution.groupIDs["group-api"]; !ok || len(resolution.groupIDs) != 1 {
		t.Fatalf("groupIDs = %#v, want only api-type fallback group", resolution.groupIDs)
	}
}

func TestResolveRoutingPolicyUnknownModelSkipsModelOnlyRules(t *testing.T) {
	t.Parallel()

	req := &model.SelectRequest{APIType: "codex", Model: unknownModelSentinel}
	resolution := resolveRoutingPolicy([]model.RoutingPolicy{
		{
			APIType:         "codex",
			ModelMatchType:  model.RoutingPolicyModelMatchTypeExact,
			ModelMatchValue: "gpt-5.4",
			Groups:          []model.RoutingPolicyGroup{{GroupID: "group-model"}},
		},
	}, req)

	if resolution.constrained {
		t.Fatalf("resolution = %#v, want unconstrained result when only hidden-model rules exist", resolution)
	}
	if resolution.matched {
		t.Fatalf("resolution = %#v, want unmatched result when no api-type rule applies", resolution)
	}
	if !resolution.consumesHiddenModel {
		t.Fatalf("resolution.consumesHiddenModel = false, want true when selection would use a hidden model if available")
	}
}

func TestProviderSelectionEligibilityWouldConsumeHiddenModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *model.SelectRequest
		want bool
	}{
		{
			name: "model sticky without usable model",
			req: &model.SelectRequest{
				APIType:    "codex",
				Model:      unknownModelSentinel,
				StickyMode: model.StickyModeModel,
			},
			want: true,
		},
		{
			name: "model sticky with usable model already present",
			req: &model.SelectRequest{
				APIType:    "codex",
				Model:      "gpt-5.4",
				StickyMode: model.StickyModeModel,
			},
			want: false,
		},
		{
			name: "api type sticky does not consume hidden model",
			req: &model.SelectRequest{
				APIType:    "codex",
				Model:      unknownModelSentinel,
				StickyMode: model.StickyModeAPIType,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eligibility := &ProviderSelectionEligibility{req: tt.req}
			if got := eligibility.WouldConsumeHiddenModel(); got != tt.want {
				t.Fatalf("WouldConsumeHiddenModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProviderSelectionEligibilityWouldConsumeHiddenModelForRoutingPolicy(t *testing.T) {
	t.Parallel()

	req := &model.SelectRequest{APIType: "codex", Model: unknownModelSentinel}
	eligibility := &ProviderSelectionEligibility{
		req: req,
		routing: resolveRoutingPolicy([]model.RoutingPolicy{
			{
				APIType:         "codex",
				ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
				ModelMatchValue: "gpt-",
			},
		}, req),
	}

	if !eligibility.WouldConsumeHiddenModel() {
		t.Fatal("WouldConsumeHiddenModel() = false, want true when a routing rule depends on a hidden model")
	}

	req.Model = "gpt-5.4"
	eligibility.req = req
	if eligibility.WouldConsumeHiddenModel() {
		t.Fatal("WouldConsumeHiddenModel() = true, want false once the request already has a usable model")
	}
}

func TestResolveSelectionHiddenModelDemandModelStickyShortCircuitsRoutingPolicyLookup(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	store.routingPolicyErr = errors.New("routing policy store unavailable")

	consumesHiddenModel, err := ResolveSelectionHiddenModelDemand(context.Background(), store, &model.SelectRequest{
		APIType:    "codex",
		Model:      unknownModelSentinel,
		StickyMode: model.StickyModeModel,
	})
	if err != nil {
		t.Fatalf("ResolveSelectionHiddenModelDemand() error = %v, want nil", err)
	}
	if !consumesHiddenModel {
		t.Fatal("ResolveSelectionHiddenModelDemand() = false, want true when model sticky still needs continuity precision")
	}
}

func TestResolveSelectionHiddenModelDemandReturnsErrorWhenRoutingPolicyLookupIsRequired(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("routing policy store unavailable")
	store := newMockStore()
	store.routingPolicyErr = wantErr

	consumesHiddenModel, err := ResolveSelectionHiddenModelDemand(context.Background(), store, &model.SelectRequest{
		APIType:    "codex",
		Model:      unknownModelSentinel,
		StickyMode: model.StickyModeOff,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ResolveSelectionHiddenModelDemand() error = %v, want %v", err, wantErr)
	}
	if consumesHiddenModel {
		t.Fatal("ResolveSelectionHiddenModelDemand() = true, want false when policy lookup failed before demand was known")
	}
}

func TestSelectorSelectWithMetadataReportsStickyOrigin(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-primary",
			Name:     "Primary",
			Enabled:  true,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-primary", APIType: "codex"}},
		},
		{
			ID:       "p-sticky",
			Name:     "Sticky",
			Enabled:  true,
			Priority: 1,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-sticky", APIType: "codex"}},
		},
	}
	store.authStates["p-primary"] = &model.ProviderAuthState{ProviderID: "p-primary", Status: model.ProviderAuthStatusActive}
	store.authStates["p-sticky"] = &model.ProviderAuthState{ProviderID: "p-sticky", Status: model.ProviderAuthStatusActive}

	req := &model.SelectRequest{
		ClientIP:   "192.168.1.10",
		User:       "user-1",
		APIType:    "codex",
		StickyMode: model.StickyModeAPIType,
	}

	sticky := NewMemoryStickyCache(internal.RealClock{})
	sticky.Set(BuildContinuityKey(req), "p-sticky", time.Minute)

	sel := NewSelector(Config{
		Store:         store,
		StickyCache:   sticky,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	result, err := sel.SelectWithMetadata(context.Background(), req)
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	if result == nil || result.Provider == nil {
		t.Fatal("expected non-nil select result")
	}
	if !result.FromStickyCache {
		t.Fatal("expected sticky cache hit to be reported")
	}
	if result.Provider.ID != "p-sticky" {
		t.Fatalf("provider = %q, want sticky provider", result.Provider.ID)
	}
}

func TestSelectorSelectWithMetadataReportsFreshSelection(t *testing.T) {
	store := newMockStore()
	store.providers = []model.Provider{
		{
			ID:       "p-primary",
			Name:     "Primary",
			Enabled:  true,
			Priority: 1,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-primary", APIType: "codex"}},
		},
		{
			ID:       "p-secondary",
			Name:     "Secondary",
			Enabled:  true,
			Priority: 10,
			APITypes: []model.ProviderAPIType{{ProviderID: "p-secondary", APIType: "codex"}},
		},
	}
	store.authStates["p-primary"] = &model.ProviderAuthState{ProviderID: "p-primary", Status: model.ProviderAuthStatusActive}
	store.authStates["p-secondary"] = &model.ProviderAuthState{ProviderID: "p-secondary", Status: model.ProviderAuthStatusActive}

	sel := NewSelector(Config{
		Store:         store,
		HealthChecker: newMockHealthChecker(),
		Clock:         internal.RealClock{},
		Logger:        zap.NewNop(),
	})

	result, err := sel.SelectWithMetadata(context.Background(), &model.SelectRequest{
		APIType:    "codex",
		StickyMode: model.StickyModeOff,
	})
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	if result == nil || result.Provider == nil {
		t.Fatal("expected non-nil select result")
	}
	if result.FromStickyCache {
		t.Fatal("expected fresh selection to report no sticky cache hit")
	}
	if result.Provider.ID != "p-primary" {
		t.Fatalf("provider = %q, want highest priority provider", result.Provider.ID)
	}
}
