// Package internal provides shared errors for the switch-a proxy.
package internal

import (
	"errors"
	"fmt"
)

// Shared errors used across packages.
var (
	// ErrNoProvider indicates no provider is available.
	ErrNoProvider = errors.New("no available provider")
)

// ProviderSelectionFailureReason identifies why an otherwise valid selection
// request could not produce a provider. It is intentionally narrower than the
// individual eligibility reasons: only failures that change the client recovery
// contract belong here.
type ProviderSelectionFailureReason string

const (
	ProviderSelectionFailureContinuityRoutingConflict ProviderSelectionFailureReason = "continuity_routing_conflict"
)

// ProviderSelectionError preserves machine-readable selection semantics while
// still matching ErrNoProvider for callers that only need the legacy sentinel.
// Route identifiers are diagnostic context and must not be copied into client
// responses.
type ProviderSelectionError struct {
	Reason                        ProviderSelectionFailureReason
	APIType                       string
	PreferredRouteTargetID        string
	RoutingPolicyConstraint       string
	RoutingPolicyTargetProviderID string
}

func (e *ProviderSelectionError) Error() string {
	if e == nil {
		return ErrNoProvider.Error()
	}
	return fmt.Sprintf("provider selection failed: %s", e.Reason)
}

func (e *ProviderSelectionError) Unwrap() error {
	return ErrNoProvider
}

// IsProviderSelectionFailure lets transport adapters branch on stable domain
// semantics without parsing error text.
func IsProviderSelectionFailure(err error, reason ProviderSelectionFailureReason) bool {
	var selectionErr *ProviderSelectionError
	return errors.As(err, &selectionErr) && selectionErr != nil && selectionErr.Reason == reason
}
