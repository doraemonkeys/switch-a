package proxy

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"switch-a/internal/defaults"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

func (h *Handler) prepareForwardRequest(ctx context.Context, pctx *proxyContext, provider *model.Provider) (*http.Request, forwardResult, bool) {
	upstreamReq, err := h.buildProviderRequest(ctx, pctx, provider)
	if err != nil {
		return nil, h.failedProviderRequest(ctx, provider.ID, err), false
	}
	if err := h.applyForwardCredentials(ctx, upstreamReq.Header, provider, pctx); err != nil {
		return nil, h.failedProviderRequest(ctx, provider.ID, err), false
	}
	return upstreamReq, forwardResult{}, true
}

func (h *Handler) applyForwardCredentials(ctx context.Context, headers http.Header, provider *model.Provider, pctx *proxyContext) error {
	if h.auth != nil {
		return h.auth.ApplyProviderCredentials(ctx, headers, provider, pctx.apiType, pctx.cfg.globalAuthMode, pctx.r)
	}

	SetAuthHeader(headers, provider.APIKeyForAPIType(pctx.apiType), provider.AuthMode, pctx.cfg.globalAuthMode, pctx.r)
	return nil
}

func (h *Handler) failedProviderRequest(ctx context.Context, providerID string, err error) forwardResult {
	// Provider configuration/auth preparation failures are not runtime health
	// signals, so we fail the current selection without degrading health state.
	return forwardResult{
		err:     err,
		success: false,
	}
}

func (h *Handler) fetchForwardResponse(
	ctx context.Context,
	pctx *proxyContext,
	provider *model.Provider,
	upstreamReq *http.Request,
) (*UpstreamResponse, forwardResult, bool) {
	upstreamResp, err := pctx.transport.FetchUpstream(ctx, upstreamReq)
	if err != nil {
		return nil, h.failedUpstreamFetch(ctx, provider.ID, err, false), false
	}

	return h.retryUnauthorizedForwardResponse(ctx, pctx, provider, upstreamResp)
}

func (h *Handler) retryUnauthorizedForwardResponse(
	ctx context.Context,
	pctx *proxyContext,
	provider *model.Provider,
	upstreamResp *UpstreamResponse,
) (*UpstreamResponse, forwardResult, bool) {
	if upstreamResp.StatusCode != defaults.StatusUnauthorized || h.auth == nil {
		return upstreamResp, forwardResult{}, true
	}

	refreshed, refreshErr := h.auth.RefreshProviderCredentials(ctx, provider)
	if !refreshed {
		return upstreamResp, forwardResult{}, true
	}
	if refreshErr != nil {
		h.logger.Warn("provider credential refresh failed",
			zap.String("provider_id", provider.ID),
			zap.Error(refreshErr),
		)
		return upstreamResp, forwardResult{}, true
	}

	refreshedProvider, err := h.eligibleProviderByID(ctx, pctx.selectReq, provider.ID)
	if err != nil {
		h.logger.Warn("failed to revalidate provider after credential refresh",
			zap.String("provider_id", provider.ID),
			zap.Error(err),
		)
		return upstreamResp, forwardResult{}, true
	}
	if refreshedProvider == nil {
		return upstreamResp, forwardResult{}, true
	}

	// Drain the rejected response before retrying so keep-alive reuse is still possible
	// when the provider issued 401 only because the credential snapshot was stale.
	upstreamResp.Drain()

	retryReq, result, ok := h.prepareForwardRequest(ctx, pctx, refreshedProvider)
	if !ok {
		return nil, result, false
	}

	retryResp, err := pctx.transport.FetchUpstream(ctx, retryReq)
	if err != nil {
		return nil, h.failedUpstreamFetch(ctx, refreshedProvider.ID, err, true), false
	}
	return retryResp, forwardResult{}, true
}

func (h *Handler) failedUpstreamFetch(ctx context.Context, providerID string, err error, afterRefresh bool) forwardResult {
	message := "upstream request failed"
	if afterRefresh {
		message = "upstream request failed after credential refresh"
	}

	h.logger.Warn(message,
		zap.String("provider_id", providerID),
		zap.Error(err),
	)
	if !isClientCancellation(err) {
		h.markFailure(ctx, providerID, err)
	}

	return forwardResult{
		err:     err,
		success: false,
	}
}

func (h *Handler) commitForwardResponse(
	ctx context.Context,
	pctx *proxyContext,
	provider *model.Provider,
	upstreamResp *UpstreamResponse,
) forwardResult {
	result := forwardResult{
		statusCode: upstreamResp.StatusCode,
		isSSE:      upstreamResp.IsSSE(),
	}
	if failoverResult, handled := h.failoverForwardResponse(ctx, provider, upstreamResp, result); handled {
		return failoverResult
	}

	snippetBuf := teeClientErrorSnippet(result.statusCode, upstreamResp)
	interceptor, sseInterceptor := h.setupTokenInterceptor(result.statusCode, result.isSSE, upstreamResp)
	if interceptor != nil {
		upstreamResp.Body = interceptor.Wrap(upstreamResp.Body)
	}

	if h.activeRegistry != nil && result.isSSE {
		h.activeRegistry.UpdateSSE(pctx.requestID, true)
	}

	wrappedWriter := &firstWriteResponseWriter{
		ResponseWriter: pctx.w,
		onFirstWrite: func() {
			if h.activeRegistry != nil {
				h.activeRegistry.MarkDataReceived(pctx.requestID)
			}
		},
		onWrite: func(writeTime time.Time) {
			if h.activeRegistry != nil {
				h.activeRegistry.Touch(pctx.requestID, writeTime)
			}
		},
	}

	writeErr := pctx.transport.WriteToClient(ctx, wrappedWriter, upstreamResp)
	upstreamResp.Close()
	result.headersWritten = true
	result.done = true
	result.tokenUsage = h.extractTokenUsage(result.statusCode, interceptor, sseInterceptor)
	if result.isSSE && wrappedWriter.written && !wrappedWriter.firstWriteTime.IsZero() {
		ttft := wrappedWriter.firstWriteTime.Sub(pctx.startTime).Milliseconds()
		result.firstTokenMs = &ttft
	}
	result.responseBytes = wrappedWriter.bytesWritten

	if pctx.cfg.stickyMode != model.StickyModeOff && h.selector != nil {
		h.selector.UpdateStickyWithTTL(pctx.selectReq, provider.ID, pctx.cfg.stickyTTL)
	}
	if writeErr != nil { // coverage-ignore -- write errors occur when client disconnects
		h.handleWriteError(ctx, writeErr, provider.ID, &result)
		return result
	}

	return h.completeWrittenResponse(ctx, provider.ID, snippetBuf, result)
}

func (h *Handler) failoverForwardResponse(
	ctx context.Context,
	provider *model.Provider,
	upstreamResp *UpstreamResponse,
	result forwardResult,
) (forwardResult, bool) {
	if !shouldFailover(result.statusCode) {
		return result, false
	}

	statusErr := fmt.Errorf("upstream returned status %d", result.statusCode)
	h.logger.Warn("upstream returned error status",
		zap.String("provider_id", provider.ID),
		zap.Int("status_code", result.statusCode),
	)
	result.err = statusErr
	result.success = false
	result.bodySnippet = upstreamResp.DrainWithSnippet(0)
	result.failureDisposition = classifyProviderFailureForProvider(
		provider,
		result.statusCode,
		upstreamResp.Header,
		result.bodySnippet,
		time.Now(),
	)
	if shouldTrackStatusFailureInHealth(result.statusCode) {
		h.markFailure(ctx, provider.ID, statusErr)
	}
	if result.failureDisposition.autoDisableUntil != nil {
		h.suspendProviderUntil(
			ctx,
			provider.ID,
			*result.failureDisposition.autoDisableUntil,
			result.failureDisposition.autoDisableReason,
		)
	}
	return result, true
}

func teeClientErrorSnippet(statusCode int, upstreamResp *UpstreamResponse) *limitedBuffer {
	if statusCode < defaults.StatusClientError || statusCode >= defaults.StatusServerError {
		return nil
	}
	return upstreamResp.TeeBody(0)
}

func (h *Handler) completeWrittenResponse(
	ctx context.Context,
	providerID string,
	snippetBuf *limitedBuffer,
	result forwardResult,
) forwardResult {
	if result.statusCode < defaults.StatusClientError {
		result.success = true
		h.markSuccess(ctx, providerID)
		return result
	}

	result.success = false
	if snippetBuf != nil {
		result.bodySnippet = snippetBuf.String()
	}
	h.logger.Info("upstream returned client error",
		zap.String("provider_id", providerID),
		zap.Int("status_code", result.statusCode),
	)
	return result
}
