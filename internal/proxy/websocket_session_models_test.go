package proxy

import (
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"
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
