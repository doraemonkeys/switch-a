package model

import (
	"testing"
	"time"
)

func TestProviderSwitchHistoryRecordProviderIgnoresSameProviderRetries(t *testing.T) {
	t.Parallel()

	origin := &Provider{ID: "origin"}
	history := NewProviderSwitchHistory(origin)

	history.RecordProvider(origin)
	history.RecordProvider(&Provider{ID: "replacement"})

	if history.ProviderSwitchCount != 1 {
		t.Fatalf("ProviderSwitchCount = %d, want 1", history.ProviderSwitchCount)
	}
	if len(history.AttemptChain) != 2 {
		t.Fatalf("AttemptChain = %#v, want origin + replacement only", history.AttemptChain)
	}
}

func TestIsProviderSwitchAllowedSeparatesSwitchBudgetFromFailoverIsolation(t *testing.T) {
	t.Parallel()

	history := &ProviderSwitchHistory{
		OriginProviderID:    "origin",
		AttemptChain:        []string{"origin"},
		ProviderSwitchCount: 1,
	}
	if !IsProviderSwitchAllowed(&Provider{ID: "replacement"}, history, 2) {
		t.Fatal("expected replacement to be allowed while under the switch budget")
	}
	if IsProviderSwitchAllowed(&Provider{ID: "origin"}, history, 2) {
		t.Fatal("expected cycle detection to block the origin provider")
	}
	if IsProviderSwitchAllowed(&Provider{ID: "replacement"}, history, 1) {
		t.Fatal("expected max provider switches to block another cross-provider move")
	}
}

func TestSelectRequestEffectiveSwitchContractPrefersExplicitFields(t *testing.T) {
	t.Parallel()

	explicitHistory := &ProviderSwitchHistory{
		OriginProviderID:    "explicit-origin",
		AttemptChain:        []string{"explicit-origin"},
		ProviderSwitchCount: 0,
	}
	explicitContinuity := &ProviderContinuityContext{
		VisibleOriginProviderID: "explicit-origin",
		StrictestScope:          ScopeVendor,
	}
	req := &SelectRequest{
		SwitchMode:                SwitchModeReplacement,
		ProviderSwitchHistory:     explicitHistory,
		ProviderContinuityContext: explicitContinuity,
		FailoverContext:           NewFailoverContext(&Provider{ID: "legacy-origin", Vendor: "legacy", FailoverScope: ScopeNone}),
	}

	if got := req.EffectiveSwitchMode(); got != SwitchModeReplacement {
		t.Fatalf("EffectiveSwitchMode() = %q, want %q", got, SwitchModeReplacement)
	}
	if got := req.EffectiveProviderSwitchHistory(); got != explicitHistory {
		t.Fatalf("EffectiveProviderSwitchHistory() = %#v, want explicit history %#v", got, explicitHistory)
	}
	if got := req.EffectiveProviderContinuityContext(); got != explicitContinuity {
		t.Fatalf("EffectiveProviderContinuityContext() = %#v, want explicit continuity %#v", got, explicitContinuity)
	}
	if got := req.EffectiveMaxProviderSwitches(); got != 0 {
		t.Fatalf("EffectiveMaxProviderSwitches() = %d, want 0", got)
	}
}

func TestSelectRequestEffectiveSwitchContractDoesNotInferLegacyFailoverContext(t *testing.T) {
	t.Parallel()

	req := &SelectRequest{
		FailoverContext: NewFailoverContext(&Provider{
			ID:            "legacy-origin",
			Vendor:        "legacy-vendor",
			FailoverScope: ScopeVendor,
		}),
	}

	if got := req.EffectiveSwitchMode(); got != SwitchModeInitial {
		t.Fatalf("EffectiveSwitchMode() = %q, want %q when only legacy failover context is present", got, SwitchModeInitial)
	}
	if got := req.EffectiveProviderSwitchHistory(); got != nil {
		t.Fatalf("EffectiveProviderSwitchHistory() = %#v, want nil when only legacy failover context is present", got)
	}
	if got := req.EffectiveProviderContinuityContext(); got != nil {
		t.Fatalf("EffectiveProviderContinuityContext() = %#v, want nil when only legacy failover context is present", got)
	}
	if got := req.EffectiveVisibleContinuitySeedCandidate(); got != nil {
		t.Fatalf("EffectiveVisibleContinuitySeedCandidate() = %#v, want nil", got)
	}
}

func TestProviderSwitchBudgetFromAttemptBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxAttempts int
		want        int
	}{
		{name: "negative attempts collapse to zero", maxAttempts: -1, want: 0},
		{name: "zero attempts yields zero switches", maxAttempts: 0, want: 0},
		{name: "one attempt yields zero switches", maxAttempts: 1, want: 0},
		{name: "two attempts yields one switch", maxAttempts: 2, want: 1},
		{name: "four attempts yields three switches", maxAttempts: 4, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProviderSwitchBudgetFromAttemptBudget(tt.maxAttempts); got != tt.want {
				t.Fatalf("ProviderSwitchBudgetFromAttemptBudget(%d) = %d, want %d", tt.maxAttempts, got, tt.want)
			}
		})
	}
}

func TestSelectRequestEffectiveMaxProviderSwitchesNormalizesNegativeValues(t *testing.T) {
	t.Parallel()

	if got := (*SelectRequest)(nil).EffectiveMaxProviderSwitches(); got != 0 {
		t.Fatalf("nil EffectiveMaxProviderSwitches() = %d, want 0", got)
	}

	req := &SelectRequest{MaxProviderSwitches: -3}
	if got := req.EffectiveMaxProviderSwitches(); got != 0 {
		t.Fatalf("EffectiveMaxProviderSwitches() = %d, want 0 for negative values", got)
	}

	req.MaxProviderSwitches = 4
	if got := req.EffectiveMaxProviderSwitches(); got != 4 {
		t.Fatalf("EffectiveMaxProviderSwitches() = %d, want 4", got)
	}
}

func TestVisibleContinuitySeedBuildsCandidateAndRequestLocalContinuity(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 4, 5, 11, 0, 0, 0, time.UTC)
	seed := &VisibleContinuitySeed{
		SeedID:              "seed-1",
		ContinuityKey:       StickyKey{IP: "127.0.0.1", User: "user-1", APIType: "codex"},
		OriginProviderID:    "provider-origin",
		OriginVendor:        "vendor-a",
		ContaminatedVendors: []string{"vendor-a", "vendor-b"},
		StrictestScope:      ScopeVendor,
		ObservedAt:          observedAt,
	}

	candidate := seed.Candidate(observedAt.Add(1500 * time.Millisecond))
	if candidate == nil {
		t.Fatal("expected non-nil continuity candidate")
	}
	if candidate.Age != 1500*time.Millisecond {
		t.Fatalf("candidate.Age = %s, want %s", candidate.Age, 1500*time.Millisecond)
	}

	continuity := seed.ProviderContinuityContext()
	if continuity == nil {
		t.Fatal("expected request-local continuity context")
	}
	if continuity.VisibleOriginProviderID != "provider-origin" {
		t.Fatalf("VisibleOriginProviderID = %q, want %q", continuity.VisibleOriginProviderID, "provider-origin")
	}
	if len(continuity.ContaminatedVendors) != 2 {
		t.Fatalf("ContaminatedVendors = %#v, want preserved vendor list", continuity.ContaminatedVendors)
	}
	if continuity.StrictestScope != ScopeVendor {
		t.Fatalf("StrictestScope = %q, want %q", continuity.StrictestScope, ScopeVendor)
	}
}
