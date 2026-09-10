package accountclient

import (
	"context"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"github.com/doraemonkeys/switch-a/internal/defaults"
)

// FallbackClient applies only when the credential has no usable profile UA.
type FallbackClient string

const (
	FallbackSwitchA        FallbackClient = defaults.DefaultGPTAccountFallbackClient
	FallbackOfficialStable FallbackClient = officialversion.Source
)

func (m FallbackClient) IsValid() bool {
	return m == FallbackSwitchA || m == FallbackOfficialStable
}

// Policy freezes the global fallback and cached release together. Account
// requests never depend on a live release lookup succeeding.
type Policy struct {
	FallbackClient  FallbackClient
	OfficialVersion officialversion.Release
}

type PolicyStore interface {
	ResolveAccountClientPolicy(context.Context) (Policy, error)
}

func (r *Resolver) resolvePolicy(ctx context.Context) (Policy, error) {
	if r.policy == nil {
		return Policy{FallbackClient: FallbackSwitchA}, nil
	}
	policy, err := r.policy.ResolveAccountClientPolicy(ctx)
	if err != nil {
		return Policy{}, fmt.Errorf("resolve account client policy: %w", err)
	}
	if !policy.FallbackClient.IsValid() {
		return Policy{}, fmt.Errorf("invalid account fallback client %q", policy.FallbackClient)
	}
	return policy, nil
}
