package internal

import (
	"errors"
	"testing"
)

func TestProviderSelectionErrorPreservesSentinelAndStableReason(t *testing.T) {
	selectionErr := &ProviderSelectionError{
		Reason:                 ProviderSelectionFailureContinuityRoutingConflict,
		APIType:                "codex",
		PreferredRouteTargetID: "provider-owner",
	}
	wrapped := errors.Join(errors.New("adapter context"), selectionErr)

	if !errors.Is(wrapped, ErrNoProvider) {
		t.Fatal("ProviderSelectionError must preserve ErrNoProvider compatibility")
	}
	if !IsProviderSelectionFailure(wrapped, ProviderSelectionFailureContinuityRoutingConflict) {
		t.Fatal("typed selection failure reason was not discoverable through wrapping")
	}
	if IsProviderSelectionFailure(ErrNoProvider, ProviderSelectionFailureContinuityRoutingConflict) {
		t.Fatal("generic availability error was misclassified as a continuity conflict")
	}
	if got := selectionErr.Error(); got != "provider selection failed: continuity_routing_conflict" {
		t.Fatalf("Error() = %q", got)
	}

	var nilSelectionErr *ProviderSelectionError
	if nilSelectionErr.Error() != ErrNoProvider.Error() {
		t.Fatal("nil ProviderSelectionError must retain the safe availability text")
	}
}
