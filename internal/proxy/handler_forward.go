package proxy

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/proxy/capturebridge"
	"github.com/doraemonkeys/switch-a/internal/proxy/capturefailure"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
)

func captureCredentialMaterial(headers http.Header) (
	requestcapture.SensitiveHeaderEvidence,
	requestcapture.CredentialEvidence,
) {
	return capturebridge.CredentialMaterial(headers)
}

func (h *Handler) prepareForwardRequest(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
) (*http.Request, forwardResult, bool) {
	upstreamReq, failureCode, err := h.buildProviderRequest(ctx, pctx, attempt.provider)
	if err != nil {
		h.captureHTTPPreparationFailure(ctx, pctx, attempt, phase, nil, failureCode, err)
		return nil, h.failedProviderRequest(err), false
	}
	if err := h.applyForwardCredentials(ctx, upstreamReq.Header, attempt.provider, pctx); err != nil {
		h.captureHTTPPreparationFailure(
			ctx,
			pctx,
			attempt,
			phase,
			upstreamReq,
			requestcapture.FailureCodeCredentialApply,
			err,
		)
		return upstreamReq, h.failedProviderRequest(err), false
	}
	return upstreamReq, forwardResult{}, true
}

func (h *Handler) captureHTTPPreparationFailure(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
	request *http.Request,
	failureCode requestcapture.FailureCode,
	err error,
) {
	if !pctx.captureParticipates {
		return
	}

	var target requestcapture.TransitionTargetInput
	var credentialEvidence requestcapture.CredentialEvidence
	if request != nil {
		target = requestcapture.HTTPTransitionTarget(request.URL)
		_, credentialEvidence = captureCredentialMaterial(request.Header)
	}
	reason, failure := capturefailure.HTTPPreparation(contextError(ctx), err, failureCode)
	pctx.capture.Transition(requestcapture.TransitionStart{
		Attempt:            attempt.metadata(pctx.apiType, phase),
		Target:             target,
		TerminationReason:  reason,
		Failure:            failure,
		CredentialEvidence: credentialEvidence,
	})
}

func (h *Handler) applyForwardCredentials(ctx context.Context, headers http.Header, provider *model.Provider, pctx *proxyContext) error {
	if h.auth != nil {
		return h.auth.ApplyProviderCredentials(ctx, headers, provider, pctx.apiType, pctx.cfg.globalAuthMode, pctx.r)
	}

	SetAuthHeader(headers, provider.APIKeyForAPIType(pctx.apiType), provider.AuthMode, pctx.cfg.globalAuthMode, pctx.r)
	return nil
}

func (h *Handler) failedProviderRequest(err error) forwardResult {
	// Provider configuration/auth preparation failures are not runtime health
	// signals, so we fail the current selection without degrading health state.
	return forwardResult{
		err:            err,
		success:        false,
		clientCanceled: isClientCancellation(err),
	}
}

func (h *Handler) fetchForwardResponse(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	upstreamReq *http.Request,
) (*UpstreamResponse, httpCaptureExchange, forwardResult, bool) {
	upstreamResp, exchange, err := h.fetchHTTPExchange(
		ctx,
		pctx,
		attempt,
		requestcapture.CredentialPhaseInitial,
		upstreamReq,
	)
	if err != nil {
		result := h.failedUpstreamFetch(ctx, attempt.provider.ID, err, false)
		return nil, httpCaptureExchange{}, result, false
	}

	return h.retryUnauthorizedForwardResponse(ctx, pctx, attempt, upstreamResp, exchange)
}

func (h *Handler) fetchHTTPExchange(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
	request *http.Request,
) (*UpstreamResponse, httpCaptureExchange, error) {
	exchange := h.beginHTTPExchange(pctx, attempt, phase, request)
	response, err := h.fetchTrackedUpstream(ctx, pctx, request)
	if err != nil {
		finishHTTPFetchFailure(ctx, pctx, exchange, err)
		return nil, exchange, err
	}
	exchange.observeResponse(response)
	return response, exchange, nil
}

func (h *Handler) retryUnauthorizedForwardResponse(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	upstreamResp *UpstreamResponse,
	exchange httpCaptureExchange,
) (*UpstreamResponse, httpCaptureExchange, forwardResult, bool) {
	provider := attempt.provider
	if upstreamResp.StatusCode != defaults.StatusUnauthorized || h.auth == nil {
		return upstreamResp, exchange, forwardResult{}, true
	}

	refreshed, refreshErr := h.auth.RefreshProviderCredentials(ctx, provider)
	if !refreshed {
		return upstreamResp, exchange, forwardResult{}, true
	}
	if refreshErr != nil {
		h.logger.Warn("provider credential refresh failed",
			zap.String("request_id", pctx.requestID),
			zap.String("provider_id", provider.ID),
			zap.Int("provider_attempt_index", attempt.providerAttemptIndex),
			zap.Error(refreshErr),
		)
		return upstreamResp, exchange, forwardResult{}, true
	}

	refreshedProvider, err := h.eligibleProviderByID(ctx, pctx.selectReq, provider.ID)
	if err != nil {
		h.logger.Warn("failed to revalidate provider after credential refresh",
			zap.String("request_id", pctx.requestID),
			zap.String("provider_id", provider.ID),
			zap.Int("provider_attempt_index", attempt.providerAttemptIndex),
			zap.Error(err),
		)
		return upstreamResp, exchange, forwardResult{}, true
	}
	if refreshedProvider == nil {
		return upstreamResp, exchange, forwardResult{}, true
	}

	if exchange.valid() {
		// The rejected response keeps the original drain boundary so capture cannot
		// turn credential refresh into an unbounded read or alter connection reuse.
		drain := upstreamResp.drainObserved()
		exchange.completedAt = time.Now()
		sourceCompletion := requestcapture.SourceCompletionPartial
		if capturebridge.SourceEndpointComplete(
			drain.bytesRead,
			upstreamResp.ContentLength,
			drain.reachedEOF,
			drain.readErr != nil,
		) {
			sourceCompletion = requestcapture.SourceCompletionComplete
		}
		statusFailure := capturefailure.HTTPStatus(
			requestcapture.FailureSiteResponseStatus,
			requestcapture.FailurePeerUpstream,
			upstreamResp.StatusCode,
		)
		drainFailure := capturefailure.FromError(
			requestcapture.FailureSiteResponseDrain,
			requestcapture.FailurePeerUpstream,
			requestcapture.FailureClassRead,
			requestcapture.FailureCodeDrainRead,
			drain.readErr,
		)
		pctx.queueHTTPCaptureCompletion(
			exchange,
			upstreamResp,
			sourceCompletion,
			requestcapture.TerminationReasonCredentialRefreshDrain,
			capturefailure.Observation(statusFailure, drainFailure),
		)
	} else {
		upstreamResp.Drain()
	}

	refreshedAttempt := attempt
	refreshedAttempt.provider = refreshedProvider
	retryReq, result, ok := h.prepareForwardRequest(
		ctx,
		pctx,
		refreshedAttempt,
		requestcapture.CredentialPhaseRefreshed,
	)
	if !ok {
		return nil, httpCaptureExchange{}, result, false
	}

	retryResp, retryExchange, err := h.fetchHTTPExchange(
		ctx,
		pctx,
		refreshedAttempt,
		requestcapture.CredentialPhaseRefreshed,
		retryReq,
	)
	if err != nil {
		result := h.failedUpstreamFetch(ctx, refreshedProvider.ID, err, true)
		return nil, httpCaptureExchange{}, result, false
	}
	return retryResp, retryExchange, forwardResult{}, true
}

func finishHTTPFetchFailure(ctx context.Context, pctx *proxyContext, exchange httpCaptureExchange, err error) {
	if !exchange.valid() {
		return
	}
	exchange.completedAt = time.Now()
	reason, failure := capturefailure.HTTPFetch(contextError(ctx), err)
	pctx.queueHTTPCaptureCompletion(
		exchange,
		nil,
		requestcapture.SourceCompletionPartial,
		reason,
		failure,
	)
}

func (h *Handler) fetchTrackedUpstream(ctx context.Context, pctx *proxyContext, request *http.Request) (*UpstreamResponse, error) {
	if pctx.liveBytes != nil {
		// Request bodies are replayed for provider retries, so cumulative traffic
		// reflects actual logical-request attempts instead of only client ingress.
		pctx.liveBytes.BytesSent.Add(int64(len(pctx.body)))
		pctx.liveBytes.LastActivityAt.Store(time.Now().UnixMilli())
	}
	return pctx.transport.FetchUpstream(ctx, request)
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
	exchange httpCaptureExchange,
) forwardResult {
	result := forwardResult{
		statusCode: upstreamResp.StatusCode,
		isSSE:      upstreamResp.IsSSE(),
	}
	if failoverResult, handled := h.failoverForwardResponse(ctx, pctx, provider, upstreamResp, exchange, result); handled {
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
	capturePayload := exchange.mode.CapturesPayload()
	var onWrite func(int, time.Time)
	switch {
	case capturePayload && pctx.liveBytes != nil:
		liveBytes := pctx.liveBytes
		onWrite = func(written int, writeTime time.Time) {
			liveBytes.BytesReceived.Add(int64(written))
			liveBytes.LastActivityAt.Store(writeTime.UnixMilli())
			exchange.observeClientWrite(written)
		}
	case capturePayload:
		onWrite = func(written int, _ time.Time) {
			exchange.observeClientWrite(written)
		}
	case pctx.liveBytes != nil:
		liveBytes := pctx.liveBytes
		onWrite = func(written int, writeTime time.Time) {
			liveBytes.BytesReceived.Add(int64(written))
			liveBytes.LastActivityAt.Store(writeTime.UnixMilli())
		}
	}

	wrappedWriter := &firstWriteResponseWriter{
		ResponseWriter: pctx.w,
		onFirstWrite: func() {
			if h.activeRegistry != nil {
				h.activeRegistry.MarkDataReceived(pctx.requestID)
			}
		},
		onWrite: onWrite,
	}

	writeErr := pctx.transport.WriteToClient(ctx, wrappedWriter, upstreamResp)
	upstreamResp.Close()
	exchange.completedAt = time.Now()
	result.headersWritten = true
	result.responseCommitted = wrappedWriter.committed
	// firstByteVisible surfaces `firstWriteResponseWriter.written` to the
	// transport observation layer. It is the only signal that distinguishes
	// `pre_payload_visible` from `post_payload_visible` for SSE stage
	// derivation; capture it on success and failure paths alike.
	result.firstByteVisible = wrappedWriter.written
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
	} else {
		result = h.completeWrittenResponse(ctx, provider.ID, snippetBuf, result)
	}

	// Capture completion follows behavior-owned sticky/health mutations so a
	// contended capture store cannot skew their clocks or recorded durations.
	if exchange.valid() {
		sourceCompletion := requestcapture.SourceCompletionComplete
		if writeErr != nil {
			sourceCompletion = exchange.sourceCompletionAfterError()
		}
		reason, failure := captureForwardFailure(ctx, writeErr, wrappedWriter.writeErr)
		pctx.queueHTTPCaptureCompletion(
			exchange,
			upstreamResp,
			sourceCompletion,
			reason,
			failure,
		)
	}
	return result
}

func (h *Handler) failoverForwardResponse(
	ctx context.Context,
	pctx *proxyContext,
	provider *model.Provider,
	upstreamResp *UpstreamResponse,
	exchange httpCaptureExchange,
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
	// Flag the status-classification origin explicitly so the transport
	// diagnostic layer skips this path rather than matching on the synthetic
	// error text (which is brittle under refactors or i18n).
	result.isStatusFailover = true
	var drain drainObservation
	if exchange.valid() {
		result.bodySnippet, drain = upstreamResp.drainWithSnippetObserved(0)
		exchange.completedAt = time.Now()
	} else {
		result.bodySnippet = upstreamResp.DrainWithSnippet(0)
	}
	failureObservedAt := time.Now()
	result.failureDisposition = classifyProviderFailureForProvider(
		provider,
		result.statusCode,
		upstreamResp.Header,
		result.bodySnippet,
		failureObservedAt,
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
	// Capture completion is deliberately last: provider health and suspension
	// deadlines must be derived from the wire observation, not capture latency.
	if exchange.valid() {
		sourceCompletion := requestcapture.SourceCompletionPartial
		if capturebridge.SourceEndpointComplete(
			drain.bytesRead,
			upstreamResp.ContentLength,
			drain.reachedEOF,
			drain.readErr != nil,
		) {
			sourceCompletion = requestcapture.SourceCompletionComplete
		}
		statusFailure := capturefailure.HTTPStatus(
			requestcapture.FailureSiteResponseStatus,
			requestcapture.FailurePeerUpstream,
			result.statusCode,
		)
		drainFailure := capturefailure.FromError(
			requestcapture.FailureSiteResponseDrain,
			requestcapture.FailurePeerUpstream,
			requestcapture.FailureClassRead,
			requestcapture.FailureCodeDrainRead,
			drain.readErr,
		)
		pctx.queueHTTPCaptureCompletion(
			exchange,
			upstreamResp,
			sourceCompletion,
			requestcapture.TerminationReasonStatusFailoverDrain,
			capturefailure.Observation(statusFailure, drainFailure),
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
