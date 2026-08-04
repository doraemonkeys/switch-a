package websocketproxy

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
	"go.uber.org/zap"
)

const providerUsageObservationTimeout = 5 * time.Second

func (h *Gateway) scheduleProviderUsageObservation(
	requestID string,
	provider *model.Provider,
	header http.Header,
	observedAt time.Time,
) {
	if h.usageObserver == nil || provider == nil ||
		model.NormalizeProviderCredentialType(provider.CredentialType) != model.ProviderCredentialTypeChatGPT {
		return
	}
	snapshot, rejectedHeaders := codexquota.ParseResponseHeaders(header, observedAt)
	if len(rejectedHeaders) > 0 {
		h.logger.Debug("rejected malformed Codex quota WebSocket handshake headers",
			zap.String("request_id", requestID),
			zap.String("provider_id", provider.ID),
			zap.Strings("rejected_headers", rejectedHeaders),
		)
	}
	if snapshot == nil {
		return
	}

	providerID := provider.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), providerUsageObservationTimeout)
		defer cancel()
		if err := h.usageObserver.ObserveProviderUsage(ctx, providerID, snapshot); err != nil {
			h.logger.Warn("failed to persist Codex quota WebSocket handshake observation",
				zap.String("request_id", requestID),
				zap.String("provider_id", providerID),
				zap.Error(err),
			)
		}
	}()
}
