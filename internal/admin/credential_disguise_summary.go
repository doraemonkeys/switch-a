package admin

import (
	"context"
	"errors"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

type CredentialDisguiseSummary struct {
	DeviceID   string `json:"device_id"`
	RevisionID string `json:"revision_id"`
	Mode       string `json:"mode"`
}

func credentialDisguiseSummary(ctx context.Context, source any, id string) (*CredentialDisguiseSummary, error) {
	provider, ok := source.(interface {
		ClientDisguiseRepository() *clientdisguise.Repository
	})
	if !ok || provider.ClientDisguiseRepository() == nil {
		return nil, nil
	}
	repo := provider.ClientDisguiseRepository()
	identity, err := repo.GetLogin(ctx, id)
	if errors.Is(err, clientdisguise.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := &CredentialDisguiseSummary{DeviceID: identity.DeviceID}
	bindings, err := repo.ListBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.CredentialSessionID == id {
			result.RevisionID = binding.RevisionID
			result.Mode = binding.Mode
			break
		}
	}
	return result, nil
}
