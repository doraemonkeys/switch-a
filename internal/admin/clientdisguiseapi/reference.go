package clientdisguiseapi

import (
	"context"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
)

// References must identify a client that observations and restored ownership can
// resolve; accepting an arbitrary label would create an unrestorable graph.
func (h *Handler) requireClient(ctx context.Context, id string) error {
	clients, err := h.clients.ListClients(ctx)
	if err != nil {
		return err
	}
	for _, client := range clients {
		if client.ID == id {
			return nil
		}
	}
	return clientidentity.ErrNotFound
}
