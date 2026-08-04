package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
	"go.uber.org/zap"
)

const providerUsageObservationTimeout = 5 * time.Second

func (h *Handler) captureProviderUsageObservation(
	pctx *proxyContext,
	provider *model.Provider,
	header http.Header,
	observedAt time.Time,
	operationID string,
) {
	if h.usageObserver == nil || pctx == nil || provider == nil ||
		model.NormalizeProviderCredentialType(provider.CredentialType) != model.ProviderCredentialTypeChatGPT {
		return
	}

	snapshot, rejectedHeaders := codexquota.ParseResponseHeaders(header, observedAt)
	if len(rejectedHeaders) > 0 {
		h.logger.Debug("rejected malformed Codex quota response headers",
			zap.String("request_id", pctx.requestID),
			zap.String("operation_id", operationID),
			zap.String("provider_id", provider.ID),
			zap.Strings("rejected_headers", rejectedHeaders),
		)
	}
	if snapshot == nil {
		return
	}
	if pctx.usageObservations == nil {
		pctx.usageObservations = make(map[string]*model.ProviderUsageSnapshot)
	}
	current := pctx.usageObservations[provider.ID]
	if current == nil || current.FetchedAt == nil || snapshot.FetchedAt.After(*current.FetchedAt) {
		pctx.usageObservations[provider.ID] = snapshot
	}
	h.logger.Debug("captured Codex quota response observation",
		zap.String("request_id", pctx.requestID),
		zap.String("operation_id", operationID),
		zap.String("provider_id", provider.ID),
		zap.Bool("primary_window_observed", snapshot.FiveHour != nil),
		zap.Bool("secondary_window_observed", snapshot.OneWeek != nil),
	)
}

func (h *Handler) scheduleProviderUsagePersistence(pctx *proxyContext) {
	if h.usageObserver == nil || pctx == nil || len(pctx.usageObservations) == 0 {
		return
	}
	requestID := pctx.requestID
	observations := make(map[string]*model.ProviderUsageSnapshot, len(pctx.usageObservations))
	for providerID, snapshot := range pctx.usageObservations {
		observations[providerID] = model.CloneProviderUsageSnapshot(snapshot)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), providerUsageObservationTimeout)
		defer cancel()
		for providerID, snapshot := range observations {
			if err := h.usageObserver.ObserveProviderUsage(ctx, providerID, snapshot); err != nil {
				h.logger.Warn("failed to persist Codex quota response observation",
					zap.String("request_id", requestID),
					zap.String("provider_id", providerID),
					zap.Error(err),
				)
			}
		}
	}()
}
