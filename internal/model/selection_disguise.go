package model

import (
	"context"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

// SelectDisguise keeps one logical operation's client profile view independent
// of the live routing and health values revalidated by the selector.
type SelectDisguise interface {
	Evaluate(context.Context, *Provider) (clientdisguise.Candidate, error)
	Commit(context.Context, *Provider) (clientdisguise.TargetSnapshot, error)
	Exclusions() []DisguiseExclusion
}

type DisguiseExclusion struct {
	ProviderID          string                          `json:"provider_id"`
	CredentialSessionID string                          `json:"credential_session_id"`
	Reason              string                          `json:"reason"`
	Decision            clientdisguise.PlatformDecision `json:"decision"`
}
