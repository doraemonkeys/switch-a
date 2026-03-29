package admin

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// ExportConfig handles GET /admin/api/config/export.
func (h *Handler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fetch all providers
	providers, err := h.store.ListProviders(ctx)
	if err != nil {
		h.logger.Error("failed to list providers for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}

	// Fetch all groups
	groups, err := h.store.ListGroups(ctx)
	if err != nil {
		h.logger.Error("failed to list groups for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}
	routingPolicies, err := h.store.ListRoutingPolicies(ctx)
	if err != nil {
		h.logger.Error("failed to list routing policies for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}

	// Fetch all settings
	settings, err := h.store.GetAllConfig(ctx)
	if err != nil {
		h.logger.Error("failed to get config for export", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to export config")
		return
	}

	// Convert to export format
	exportedProviders := make([]ExportedProvider, len(providers))
	for i := range providers {
		exportedProviders[i] = buildExportedProvider(&providers[i])
	}

	exportedGroups := make([]ExportedGroup, len(groups))
	for i := range groups {
		exportedGroups[i] = buildExportedGroup(&groups[i])
	}
	exportedRoutingPolicies := make([]ExportedRoutingPolicy, len(routingPolicies))
	for i := range routingPolicies {
		exportedRoutingPolicies[i] = buildExportedRoutingPolicy(&routingPolicies[i])
	}

	export := ExportedConfig{
		Version:         ConfigExportVersion,
		ExportedAt:      time.Now().UTC(),
		Providers:       exportedProviders,
		Groups:          exportedGroups,
		RoutingPolicies: exportedRoutingPolicies,
		// Export normalized settings so a current backup can always round-trip
		// through the current import contract, even if the store still contains
		// legacy keys from an older release.
		Settings: normalizeSupportedSettings(settings),
	}

	writeJSON(w, http.StatusOK, export)
}
