package websocketproxy

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap/zaptest"
)

func TestWebSocketSessionResultRequestAttemptsUsesSelectionTimeContinuitySeedAge(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)
	selectedAt := observedAt.Add(1500 * time.Millisecond)
	attemptCompletedAt := selectedAt.Add(45 * time.Second)

	selectionMetadata := selector.BuildSelectionMetadataAt(&model.SelectRequest{
		SwitchMode: model.SwitchModeInitial,
		VisibleContinuitySeedCandidate: &model.VisibleContinuitySeedCandidate{
			SeedID:           "seed-1",
			OriginProviderID: "provider-origin",
			ObservedAt:       observedAt,
		},
	}, selector.SelectionSourceStickyContinuity, selectedAt)

	result := &WebSocketSessionResult{
		RequestID: "req-1",
		Attempts: []WebSocketAttemptResult{
			{
				Provider:            &model.Provider{ID: "provider-origin"},
				Attempt:             0,
				SelectionMode:       providerSwitchModeInitial,
				SelectionMetadata:   selectionMetadata,
				ProviderAttempt:     1,
				ProviderSwitchCount: 0,
				CreatedAt:           attemptCompletedAt,
			},
		},
	}

	attempts := result.RequestAttempts()
	if len(attempts) != 1 {
		t.Fatalf("RequestAttempts() len = %d, want 1", len(attempts))
	}
	if attempts[0].ContinuitySeedAgeMs == nil {
		t.Fatal("expected continuity seed age to be persisted")
	}
	if got := *attempts[0].ContinuitySeedAgeMs; got != selectedAt.Sub(observedAt).Milliseconds() {
		t.Fatalf("ContinuitySeedAgeMs = %d, want %d", got, selectedAt.Sub(observedAt).Milliseconds())
	}
	if got := *attempts[0].ContinuitySeedAgeMs; got == attemptCompletedAt.Sub(observedAt).Milliseconds() {
		t.Fatalf("ContinuitySeedAgeMs = %d, want selection-time age instead of attempt-completion age", got)
	}
}

func TestProviderSwitchTracker_ConsumesContinuitySeedAndCarriesFailoverProvenance(t *testing.T) {
	observedAt := time.Date(2026, time.August, 3, 5, 0, 0, 0, time.UTC)
	key := model.StickyKey{IP: "192.0.2.10", APIType: APITypeCodex, Model: "gpt-5"}
	seed := &model.VisibleContinuitySeed{
		SeedID: "seed-1", ContinuityKey: key, OriginProviderID: "origin", OriginVendor: "openai",
		ContaminatedVendors: []string{"openai"}, StrictestScope: model.ScopeVendor, ObservedAt: observedAt,
	}
	store := &routingTestSeedStore{candidate: seed.Candidate(observedAt.Add(time.Second)), seed: seed}
	req := &model.SelectRequest{
		ClientIP: key.IP, APIType: key.APIType, Model: key.Model, StickyMode: model.StickyModeModel,
	}
	tracker := newProviderSwitchTracker(req, 3, store)
	if !tracker.lookupVisibleContinuityCandidate() {
		t.Fatal("expected continuity seed candidate")
	}
	origin := &model.Provider{ID: "origin", Vendor: "openai"}
	mode := tracker.recordSelection(origin, selector.SelectionMetadata{Source: selector.SelectionSourceStickyContinuity})
	if mode != model.SwitchModeInitial || tracker.continuityContext == nil {
		t.Fatalf("initial continuity selection = (%q, %#v)", mode, tracker.continuityContext)
	}
	if tracker.prepareProviderSwitch() != model.SwitchModeFailover {
		t.Fatal("visible continuity must switch in failover mode")
	}
	replacement := &model.Provider{ID: "replacement", Vendor: "azure"}
	if mode := tracker.recordSelection(replacement, selector.SelectionMetadata{Source: selector.SelectionSourceStrategy}); mode != model.SwitchModeFailover {
		t.Fatalf("replacement mode = %q, want failover", mode)
	}
	if tracker.providerSwitchCount() != 1 {
		t.Fatalf("provider switch count = %d, want 1", tracker.providerSwitchCount())
	}

	stored := tracker.visibleContinuitySeed(observedAt.Add(2 * time.Second))
	if stored == nil || stored.OriginProviderID != "origin" || stored.StrictestScope != model.ScopeVendor {
		t.Fatalf("derived continuity seed = %#v", stored)
	}
	if len(stored.ContaminatedVendors) == 0 || stored.ContaminatedVendors[0] != "openai" {
		t.Fatalf("derived contaminated vendors = %v", stored.ContaminatedVendors)
	}
}

func TestProviderSwitchTracker_RejectsUnprovenSeedAndCreatesVisibilityContext(t *testing.T) {
	observedAt := time.Date(2026, time.August, 3, 6, 0, 0, 0, time.UTC)
	seed := &model.VisibleContinuitySeed{
		SeedID: "seed-2", ContinuityKey: model.StickyKey{APIType: APITypeCodex, Model: "gpt-5"},
		OriginProviderID: "origin", ObservedAt: observedAt,
	}
	store := &routingTestSeedStore{candidate: seed.Candidate(observedAt), seed: seed}
	req := &model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5", StickyMode: model.StickyModeModel}
	tracker := newProviderSwitchTracker(req, 2, store)
	tracker.lookupVisibleContinuityCandidate()
	selected := &model.Provider{ID: "different", Vendor: "anthropic"}
	tracker.recordSelection(selected, selector.SelectionMetadata{Source: selector.SelectionSourceStickyContinuity})
	if tracker.continuityCandidate != nil || tracker.continuityContext != nil {
		t.Fatalf("unproven seed attached: candidate=%#v context=%#v", tracker.continuityCandidate, tracker.continuityContext)
	}
	tracker.markClientVisible(selected, observedAt)
	if tracker.continuityContext == nil || tracker.continuityContext.VisibleOriginProviderID != selected.ID {
		t.Fatalf("visibility context = %#v", tracker.continuityContext)
	}
	if tracker.prepareProviderSwitch() != model.SwitchModeFailover {
		t.Fatal("client-visible provider must enter failover mode")
	}
}

func TestGatewayMaybeLookupVisibleContinuityCandidate_RespectsModelDemand(t *testing.T) {
	observedAt := time.Date(2026, time.August, 3, 7, 0, 0, 0, time.UTC)
	seed := (&model.VisibleContinuitySeed{
		SeedID: "seed-3", ContinuityKey: model.StickyKey{APIType: APITypeCodex, Model: "gpt-5"},
		OriginProviderID: "origin", ObservedAt: observedAt,
	}).Candidate(observedAt)
	seedStore := &routingTestSeedStore{candidate: seed}
	store := newMockStore()
	gateway := newTestGateway(t, Config{Store: store, VisibleContinuitySeedStore: seedStore, Logger: zaptest.NewLogger(t)})

	known := newProviderSwitchTracker(&model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5"}, 2, seedStore)
	gateway.maybeLookupVisibleContinuityCandidate(context.Background(), &known)
	if known.continuityCandidate == nil {
		t.Fatal("known model did not look up continuity candidate")
	}

	unknown := newProviderSwitchTracker(&model.SelectRequest{APIType: APITypeCodex, Model: ModelUnknown}, 2, seedStore)
	gateway.maybeLookupVisibleContinuityCandidate(context.Background(), &unknown)
	if unknown.continuityCandidate == nil {
		t.Fatal("model-independent selection did not look up continuity candidate")
	}

	store.routingPolicyErr = context.Canceled
	failed := newProviderSwitchTracker(&model.SelectRequest{APIType: APITypeCodex, Model: ModelUnknown}, 2, seedStore)
	gateway.maybeLookupVisibleContinuityCandidate(context.Background(), &failed)
	if failed.continuityCandidate != nil {
		t.Fatal("continuity candidate attached after policy lookup failure")
	}
	gateway.maybeLookupVisibleContinuityCandidate(context.Background(), nil)
	(*Gateway)(nil).maybeLookupVisibleContinuityCandidate(context.Background(), &failed)
}
