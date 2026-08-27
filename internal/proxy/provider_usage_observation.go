package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
	"go.uber.org/zap"
)

const providerUsageObservationTimeout = 5 * time.Second

func (h *Handler) captureProviderUsageObservation(
	pctx *proxyContext,
	attempt httpAttemptContext,
	header http.Header,
	observedAt time.Time,
	operationID string,
) {
	credential := attempt.candidate.Credential()
	if h.usageObserver == nil || pctx == nil || attempt.provider == nil ||
		credential.Kind != credentialsession.KindChatGPT || credential.SessionID == "" {
		return
	}

	snapshot, rejectedHeaders := codexquota.ParseResponseHeaders(header, observedAt)
	if len(rejectedHeaders) > 0 {
		h.logger.Debug("rejected malformed Codex quota response headers",
			zap.String("request_id", pctx.requestID),
			zap.String("operation_id", operationID),
			zap.String("provider_id", attempt.provider.ID),
			zap.String("session_id", credential.SessionID),
			zap.Strings("rejected_headers", rejectedHeaders),
		)
	}
	if snapshot == nil {
		return
	}
	if pctx.usageObservations == nil {
		pctx.usageObservations = make(map[string]*model.ProviderUsageSnapshot)
	}
	current := pctx.usageObservations[credential.SessionID]
	if current == nil || current.FetchedAt == nil || snapshot.FetchedAt.After(*current.FetchedAt) {
		pctx.usageObservations[credential.SessionID] = snapshot
	}
	h.logger.Debug("captured Codex quota response observation",
		zap.String("request_id", pctx.requestID),
		zap.String("operation_id", operationID),
		zap.String("provider_id", attempt.provider.ID),
		zap.String("session_id", credential.SessionID),
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
	for sessionID, snapshot := range pctx.usageObservations {
		observations[sessionID] = model.CloneProviderUsageSnapshot(snapshot)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), providerUsageObservationTimeout)
		defer cancel()
		for sessionID, snapshot := range observations {
			if err := h.usageObserver.ObserveCredentialSessionUsage(ctx, sessionID, snapshot); err != nil {
				h.logger.Warn("failed to persist Codex quota response observation",
					zap.String("request_id", requestID),
					zap.String("session_id", sessionID),
					zap.Error(err),
				)
			}
		}
	}()
}
