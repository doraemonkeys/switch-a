package model

import (
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

func TestProviderCredentialSessionIDsAreStableUniqueAndNonBlank(t *testing.T) {
	t.Parallel()

	if ids := (*Provider)(nil).CredentialSessionIDs(); ids != nil {
		t.Fatalf("nil provider IDs = %#v, want nil", ids)
	}
	provider := &Provider{CredentialSessions: []credentialsession.RouteSnapshot{
		{APIType: "codex", Credential: credentialsession.Snapshot{SessionID: " session-b "}},
		{APIType: "responses", Credential: credentialsession.Snapshot{SessionID: ""}},
		{APIType: "claude", Credential: credentialsession.Snapshot{SessionID: "session-a"}},
		{APIType: "chat", Credential: credentialsession.Snapshot{SessionID: "session-b"}},
	}}
	if got, want := provider.CredentialSessionIDs(), []string{"session-b", "session-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CredentialSessionIDs() = %#v, want stable unique %#v", got, want)
	}
	if snapshot, ok := (*Provider)(nil).CredentialSessionForAPIType("codex"); ok || snapshot != nil {
		t.Fatalf("nil provider CredentialSessionForAPIType() = (%#v, %t), want missing", snapshot, ok)
	}
}

func TestVisibleContinuitySeedCopiesRequestLocalStateAndClampsClockSkew(t *testing.T) {
	t.Parallel()

	if (*VisibleContinuitySeed)(nil).Clone() != nil ||
		(*VisibleContinuitySeed)(nil).Candidate(time.Now()) != nil ||
		(*VisibleContinuitySeed)(nil).ProviderContinuityContext() != nil {
		t.Fatal("nil seed methods must remain nil-safe")
	}

	observedAt := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	seed := &VisibleContinuitySeed{
		SeedID:              "seed-1",
		OriginProviderID:    "provider-a",
		OriginVendor:        "openai",
		ContaminatedVendors: []string{"openai"},
		ObservedAt:          observedAt,
	}
	clone := seed.Clone()
	clone.ContaminatedVendors[0] = "mutated"
	if seed.ContaminatedVendors[0] != "openai" {
		t.Fatalf("Clone() aliased shared vendor state: %#v", seed.ContaminatedVendors)
	}

	candidate := seed.Candidate(observedAt.Add(-time.Second))
	if candidate.Age != 0 {
		t.Fatalf("Candidate() age = %s, want zero under clock skew", candidate.Age)
	}
	continuity := seed.ProviderContinuityContext()
	if continuity.StrictestScope != ScopeAny ||
		!reflect.DeepEqual(continuity.ContaminatedVendors, []string{"openai"}) {
		t.Fatalf("ProviderContinuityContext() = %#v, want origin fallback and default scope", continuity)
	}
	continuity.ContaminatedVendors[0] = "mutated"
	if seed.ContaminatedVendors[0] != "openai" {
		t.Fatalf("ProviderContinuityContext() aliased seed state: %#v", seed.ContaminatedVendors)
	}
	fallback := (&VisibleContinuitySeed{OriginVendor: "anthropic"}).ProviderContinuityContext()
	if !reflect.DeepEqual(fallback.ContaminatedVendors, []string{"anthropic"}) {
		t.Fatalf("origin-only seed continuity = %#v", fallback)
	}
}

func TestSwitchStateNilSafetyAndDetachedCopies(t *testing.T) {
	t.Parallel()

	if NormalizeSwitchMode("future-mode") != SwitchModeInitial {
		t.Fatal("unknown switch mode widened routing semantics")
	}
	if IsValidSwitchMode("future-mode") {
		t.Fatal("unknown switch mode reported valid")
	}
	for _, mode := range []SwitchMode{"", SwitchModeInitial, SwitchModeReplacement, SwitchModeFailover} {
		if !IsValidSwitchMode(mode) {
			t.Fatalf("supported switch mode %q reported invalid", mode)
		}
	}
	if history := NewProviderSwitchHistory(nil); history.OriginProviderID != "" || len(history.AttemptChain) != 0 {
		t.Fatalf("NewProviderSwitchHistory(nil) = %#v", history)
	}
	if (*ProviderSwitchHistory)(nil).Clone() != nil {
		t.Fatal("nil switch history clone must stay nil")
	}

	history := &ProviderSwitchHistory{}
	history.RecordProvider(nil)
	history.RecordProvider(&Provider{})
	history.RecordProvider(&Provider{ID: "origin"})
	history.RecordProvider(&Provider{ID: "next"})
	clone := history.Clone()
	clone.AttemptChain[0] = "mutated"
	if history.OriginProviderID != "origin" || history.ProviderSwitchCount != 1 ||
		!reflect.DeepEqual(history.AttemptChain, []string{"origin", "next"}) {
		t.Fatalf("RecordProvider() state = %#v", history)
	}
	if history.IsInChain("") || (*ProviderSwitchHistory)(nil).IsInChain("origin") {
		t.Fatal("empty or nil history unexpectedly matched a provider")
	}
	if IsProviderSwitchAllowed(nil, history, 2) {
		t.Fatal("nil candidate was allowed")
	}
	if !IsProviderSwitchAllowed(&Provider{ID: "fresh"}, nil, 1) {
		t.Fatal("nil history should not restrict a valid candidate")
	}

	observedAt := time.Date(2026, 8, 27, 4, 5, 0, 0, time.UTC)
	continuity := NewProviderContinuityContext(nil, observedAt)
	if continuity.ObservedAt != observedAt || continuity.StrictestScope != ScopeAny {
		t.Fatalf("NewProviderContinuityContext(nil) = %#v", continuity)
	}
	continuity.ObserveProvider(nil)
	continuity.ObserveProvider(&Provider{ID: "origin", Vendor: "openai", FailoverScope: ScopeVendor})
	continuityClone := continuity.Clone()
	continuityClone.ContaminatedVendors[0] = "mutated"
	if continuity.VisibleOriginProviderID != "origin" ||
		continuity.VisibleOriginVendor != "openai" ||
		continuity.StrictestScope != ScopeVendor ||
		continuity.ContaminatedVendors[0] != "openai" {
		t.Fatalf("ObserveProvider() or Clone() state = %#v", continuity)
	}
	fromProvider := NewProviderContinuityContext(
		&Provider{ID: "provider", Vendor: "anthropic", FailoverScope: ScopeNone},
		observedAt,
	)
	if fromProvider.VisibleOriginProviderID != "provider" ||
		fromProvider.VisibleOriginVendor != "anthropic" ||
		!reflect.DeepEqual(fromProvider.ContaminatedVendors, []string{"anthropic"}) ||
		fromProvider.StrictestScope != ScopeNone {
		t.Fatalf("NewProviderContinuityContext(provider) = %#v", fromProvider)
	}
	if (*ProviderContinuityContext)(nil).Clone() != nil {
		t.Fatal("nil continuity clone must stay nil")
	}
}

func TestFailoverAdaptersPreserveIsolationWithoutAliasing(t *testing.T) {
	t.Parallel()

	var nilContext *FailoverContext
	if nilContext.SwitchHistory() != nil ||
		nilContext.ProviderContinuityContext() != nil ||
		nilContext.VisibleContinuitySeed(StickyKey{}, time.Now(), "seed") != nil {
		t.Fatal("nil failover adapter must remain nil-safe")
	}

	ctx := &FailoverContext{
		OriginProviderID:    "origin",
		ContaminatedVendors: []string{"openai", "anthropic"},
		StrictestScope:      ScopeVendor,
		AttemptChain:        []string{"origin", "second"},
		RetryCount:          1,
	}
	history := ctx.SwitchHistory()
	continuity := ctx.ProviderContinuityContext()
	seed := ctx.VisibleContinuitySeed(
		StickyKey{IP: "127.0.0.1", APIType: "codex"},
		time.Date(2026, 8, 27, 4, 10, 0, 0, time.UTC),
		"seed-1",
	)
	history.AttemptChain[0] = "mutated"
	continuity.ContaminatedVendors[0] = "mutated"
	seed.ContaminatedVendors[0] = "mutated"
	if ctx.AttemptChain[0] != "origin" || ctx.ContaminatedVendors[0] != "openai" {
		t.Fatalf("legacy adapters aliased mutable request state: %#v", ctx)
	}
	if continuity.VisibleOriginVendor != "openai" || seed.OriginVendor != "openai" {
		t.Fatalf("origin vendor projection lost: continuity=%#v seed=%#v", continuity, seed)
	}

	empty := (&FailoverContext{OriginProviderID: "origin"}).VisibleContinuitySeed(
		StickyKey{}, time.Time{}, "seed-empty",
	)
	if empty.OriginVendor != "" {
		t.Fatalf("empty vendor chain projected origin vendor %q", empty.OriginVendor)
	}
	if got := StricterScope(Scope("future"), ScopeAny); got != ScopeAny {
		t.Fatalf("unknown scope ordering produced %q, want %q", got, ScopeAny)
	}
}

func TestSelectRequestNilAccessorsAndFailoverGuards(t *testing.T) {
	t.Parallel()

	if cloneTimePtr(nil) != nil {
		t.Fatal("nil timestamp clone must stay nil")
	}
	var request *SelectRequest
	if request.EffectiveSwitchMode() != SwitchModeInitial ||
		request.EffectiveProviderSwitchHistory() != nil ||
		request.EffectiveProviderContinuityContext() != nil ||
		request.EffectiveVisibleContinuitySeedCandidate() != nil {
		t.Fatal("nil request accessors widened switch state")
	}

	if IsFailoverVendorAllowed(nil, nil) {
		t.Fatal("nil failover candidate was allowed")
	}
	if !IsFailoverVendorAllowed(&Provider{Vendor: "openai", AcceptFailover: ScopeAny}, nil) {
		t.Fatal("nil continuity should not impose vendor isolation")
	}
	if IsFailoverVendorAllowed(
		&Provider{Vendor: "openai", AcceptFailover: ScopeAny},
		&ProviderContinuityContext{StrictestScope: ScopeNone},
	) {
		t.Fatal("ScopeNone continuity allowed failover")
	}
	if got := (*Provider)(nil).UsageLimitPolicyOrDefault(); got != ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("nil provider usage policy = %q", got)
	}
}
