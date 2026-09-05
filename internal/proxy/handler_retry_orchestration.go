package proxy

import (
	"context"
	"maps"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

type legacyRetryDisposition uint8

const (
	legacyRetryRejected legacyRetryDisposition = iota
	legacyRetryActivated
	legacyRetryTerminal
)

type legacyRetryResolution struct {
	disposition     legacyRetryDisposition
	result          forwardResult
	rejectionReason errorrule.DecisionReason
}

func (h *Handler) resolveLegacyFailure(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	result forwardResult,
) (forwardResult, bool) {
	if sourceErr := pctx.ingressFailure(); sourceErr != nil {
		if pending != nil {
			_, _ = pending.discard(responseanalysis.TransitionExecutorDecision, requestcapture.TerminationReasonCanceled, requestcapture.FailureObservation{})
		}
		return failureResult(attemptFailureInternal, sourceErr), false
	}
	retryRejectionReason := errorrule.DecisionReason("")
	if h.canAttemptLegacyRetry(ctx, pctx, state, result) {
		retry := h.attemptLegacyRetry(ctx, pctx, state, pending, result)
		switch retry.disposition {
		case legacyRetryActivated:
			return retry.result, true
		case legacyRetryTerminal:
			return retry.result, false
		default:
			result = retry.result
			retryRejectionReason = retry.rejectionReason
		}
	}
	h.logLegacyRetryRejection(pctx, state, retryRejectionReason)
	// A headerless EOF is attributable to the selected provider but does not prove
	// whether that provider received the request. After its local retry budget is
	// exhausted, changing providers would hide that ambiguity behind a new route.
	if result.failureKind == attemptFailureUpstreamNoResponse {
		h.logger.Info("proxy.provider_switch_suppressed",
			zap.String("request_id", pctx.requestID),
			zap.String("provider_id", state.currentProvider.ID),
			zap.Int("provider_attempt", state.providerAttempt+1),
			zap.String("failure_kind", string(result.failureKind)),
			zap.String("decision", "return_upstream_error"),
		)
		return result, false
	}

	switched, resolved, continueExecution := h.activateAlternate(
		ctx, pctx, state, pending, result,
		h.legacySwitchReason(ctx, state, result, retryRejectionReason),
		responseanalysis.TransitionExecutorDecision,
		requestcapture.TerminationReasonStatusFailoverDrain,
	)
	if switched {
		return resolved, continueExecution
	}
	return h.commitLegacyFailure(pctx, state, pending, result, resolved)
}

func (h *Handler) canAttemptLegacyRetry(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	result forwardResult,
) bool {
	if state.providerAttempt >= max(0, state.currentProvider.MaxRetries) ||
		shouldForceProviderSwitch(result.statusCode) || result.failureDisposition.forcesProviderSwitch() {
		return false
	}
	if h.health != nil && !h.health.IsAvailable(ctx, state.currentProvider.ID) {
		return false
	}
	remaining, unlimited := state.ledger.GlobalRemaining(globalAttemptLimit(pctx.cfg.globalMaxAttempts))
	return unlimited || remaining > 0
}

func (h *Handler) attemptLegacyRetry(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	result forwardResult,
) legacyRetryResolution {
	delay := state.currentProvider.Backoff.DelayForRetry(state.providerAttempt)
	if err := h.backoff.Wait(ctx, delay); err != nil {
		return legacyRetryResolution{disposition: legacyRetryTerminal, result: h.cancelLegacyRetry(ctx, pending, result, err)}
	}
	if h.health != nil && !h.health.IsAvailable(ctx, state.currentProvider.ID) {
		return legacyRetryResolution{result: result}
	}
	permit, err := h.reserveSameProviderDispatch(ctx, pctx.selectReq, state.currentLease)
	if cancelErr := legacyRetryCancellation(ctx, err); cancelErr != nil {
		return legacyRetryResolution{
			disposition: legacyRetryTerminal,
			result:      h.cancelLegacyRetry(ctx, pending, result, cancelErr),
		}
	}
	if err != nil || permit == nil {
		return legacyRetryResolution{result: result, rejectionReason: providerRetryRejectionReason(err)}
	}
	live := permit.Provider()
	if live == nil || live.ID != state.currentProvider.ID || live.ID != state.currentLease.ProviderID() {
		permit.Release()
		return legacyRetryResolution{result: result, rejectionReason: errorrule.ReasonProviderLookupError}
	}
	return h.activateLegacyRetry(ctx, pctx, state, pending, result, permit, live.ID)
}

func legacyRetryCancellation(ctx context.Context, err error) error {
	if !classifyClientTermination(ctx).observed() {
		return nil
	}
	if err != nil {
		return err
	}
	return ctx.Err()
}

func providerRetryRejectionReason(err error) errorrule.DecisionReason {
	reason, ok := selector.ProviderRejectionReason(err)
	if !ok || reason == "" {
		return errorrule.ReasonProviderLookupError
	}
	return reason
}

func (h *Handler) cancelLegacyRetry(
	ctx context.Context,
	pending *pendingHTTPResponse,
	result forwardResult,
	err error,
) forwardResult {
	if pending != nil {
		canceled, _ := h.cancelPendingResponse(pending, nil, result, err)
		return canceled
	}
	result.clientTermination = classifyClientTermination(ctx)
	result.failureKind = attemptFailureInternal
	if result.clientTermination.observed() {
		result.failureKind = attemptFailureClientTerminated
	}
	if err != nil {
		result.failureMessage = err.Error()
	}
	return result
}

func (h *Handler) activateLegacyRetry(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	result forwardResult,
	permit sameProviderDispatchPermit,
	providerID string,
) legacyRetryResolution {
	nextLedger, err := state.ledger.StartLegacyRetry(
		errorrule.ProviderID(providerID), globalAttemptLimit(pctx.cfg.globalMaxAttempts),
	)
	if err != nil {
		permit.Release()
		return legacyRetryResolution{result: result}
	}
	if pending != nil {
		discarded, discardErr := pending.discard(
			responseanalysis.TransitionExecutorDecision,
			requestcapture.TerminationReasonStatusFailoverDrain,
			statusCaptureFailure(result.statusCode),
		)
		if discardErr != nil {
			permit.Release()
			return legacyRetryResolution{
				disposition: legacyRetryTerminal,
				result:      h.cancelLegacyRetry(ctx, pending, result, discardErr),
			}
		}
		discarded.failureKind = result.failureKind
		discarded.failureMessage = result.failureMessage
		discarded.inheritHealth(result)
		result = discarded
	}
	activatedProvider, err := permit.Activate()
	if err != nil {
		result.failureKind = attemptFailureInternal
		result.failureMessage = err.Error()
		result.clientTermination = classifyClientTermination(ctx)
		if result.clientTermination.observed() {
			result.failureKind = attemptFailureClientTerminated
		}
		return legacyRetryResolution{disposition: legacyRetryTerminal, result: result}
	}
	state.ledger = nextLedger
	state.currentProvider = activatedProvider
	state.providerAttempt++
	result.done = false
	return legacyRetryResolution{disposition: legacyRetryActivated, result: result}
}

func (h *Handler) logLegacyRetryRejection(
	pctx *proxyContext,
	state *retryState,
	reason errorrule.DecisionReason,
) {
	if reason == "" {
		return
	}
	h.logger.Debug("proxy.same_provider_retry_rejected",
		zap.String("request_id", pctx.requestID),
		zap.String("provider_id", state.currentProvider.ID),
		zap.String("reason", string(reason)),
	)
}

func (h *Handler) legacySwitchReason(
	ctx context.Context,
	state *retryState,
	result forwardResult,
	retryRejectionReason errorrule.DecisionReason,
) string {
	switch {
	case retryRejectionReason != "":
		return string(errorrule.SwitchReasonProviderUnavailable)
	case result.failureDisposition.forcesProviderSwitch():
		return result.failureDisposition.switchReason
	case shouldForceProviderSwitch(result.statusCode):
		return formatPermanentErrorReason(result.statusCode)
	case h.health != nil && !h.health.IsAvailable(ctx, state.currentProvider.ID):
		return SwitchReasonCircuitBreakerTriggered
	default:
		return SwitchReasonMaxRetriesExhausted
	}
}

func (h *Handler) commitLegacyFailure(
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	result forwardResult,
	resolved forwardResult,
) (forwardResult, bool) {
	if pending == nil || resolved.clientTermination.observed() || resolved.discarded {
		return resolved, false
	}
	committed, err := pending.commit(responseanalysis.TransitionExecutorDecision)
	if err != nil {
		return committed, false
	}
	committed.failureKind = result.failureKind
	committed.failureMessage = result.failureMessage
	committed.isStatusFailover = result.isStatusFailover
	committed.failureDisposition = result.failureDisposition
	committed.inheritHealth(result)
	committed.success = false
	h.finalizeCommittedResponse(pctx, state, &committed)
	return committed, false
}

func (h *Handler) activateAlternate(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	result forwardResult,
	switchReason string,
	discardCause responseanalysis.TransitionCause,
	captureReason requestcapture.TerminationReason,
) (bool, forwardResult, bool) {
	remaining, unlimited := state.ledger.GlobalRemaining(globalAttemptLimit(pctx.cfg.globalMaxAttempts))
	if !unlimited && remaining == 0 {
		return false, result, false
	}
	preview := state.switchTracker.previewProviderSwitch()
	excluded := cloneProviderExclusions(state.excludedProviders)
	excluded[state.currentProvider.ID] = true
	reservation, err := h.reserveAlternateProvider(ctx, preview.request(), excluded)
	if err != nil {
		result.clientTermination = classifyClientTermination(ctx)
		if result.clientTermination.observed() {
			result.failureKind = attemptFailureClientTerminated
		}
		return false, result, false
	}
	releaseReservation := true
	defer func() {
		if releaseReservation {
			reservation.Release()
		}
	}()
	if err := reservation.PrepareActivation(ctx); err != nil {
		result.clientTermination = classifyClientTermination(ctx)
		if result.clientTermination.observed() {
			result.failureKind = attemptFailureClientTerminated
		} else {
			result.failureKind = attemptFailureInternal
			result.failureMessage = err.Error()
		}
		return false, result, false
	}
	alternate := reservation.Provider()
	if alternate == nil {
		result.failureKind = attemptFailureInternal
		result.failureMessage = "alternate reservation returned no provider"
		return false, result, false
	}
	selectionMode := preview.recordSelection(alternate, reservation.Metadata())
	nextLedger, err := state.ledger.StartAttempt(
		errorrule.ProviderID(alternate.ID), globalAttemptLimit(pctx.cfg.globalMaxAttempts),
	)
	if err != nil {
		result.failureKind = attemptFailureInternal
		result.failureMessage = err.Error()
		return false, result, false
	}
	result, discardFailed := discardPendingForAlternateActivation(
		ctx, pending, result, discardCause, captureReason,
	)
	if discardFailed {
		return false, result, false
	}
	if err := state.switchTracker.commitProviderSwitch(preview); err != nil {
		result.failureKind = attemptFailureInternal
		result.failureMessage = err.Error()
		return false, result, false
	}
	newLease := reservation.Activate()
	if newLease == nil || !newLease.Held() {
		result.failureKind = attemptFailureInternal
		result.failureMessage = "alternate reservation activation failed"
		return false, result, false
	}
	releaseReservation = false
	oldLease := state.currentLease
	oldProviderID := state.currentProvider.ID
	state.excludedProviders[oldProviderID] = true
	state.currentProvider = newLease.Provider()
	state.currentLease = newLease
	state.selectionMetadata = reservation.Metadata()
	state.selectionMode = selectionMode
	state.providerAttempt = 0
	state.providerUsed = state.currentProvider
	if h.activeRegistry != nil {
		h.registerActiveRequest(pctx, state)
	} else if oldLease != nil {
		oldLease.Release()
	}
	// The copy-on-write value was validated before discard but is adopted only at
	// the dispatch boundary after both capability and continuity transfers.
	state.ledger = nextLedger
	result.switchReason = switchReason
	result.done = false
	return true, result, true
}

func discardPendingForAlternateActivation(
	ctx context.Context,
	pending *pendingHTTPResponse,
	result forwardResult,
	discardCause responseanalysis.TransitionCause,
	captureReason requestcapture.TerminationReason,
) (forwardResult, bool) {
	if pending == nil {
		return result, false
	}
	captureFailure := statusCaptureFailure(result.statusCode)
	if discardCause == responseanalysis.TransitionSemanticDecision {
		captureFailure = semanticCaptureFailure()
	}
	discarded, discardErr := pending.discard(discardCause, captureReason, captureFailure)
	if discardErr == nil {
		discarded.failureKind = result.failureKind
		discarded.failureMessage = result.failureMessage
		discarded.inheritHealth(result)
		return discarded, false
	}
	if discarded.discarded {
		discarded.inheritHealth(result)
		return discarded, true
	}
	result.clientTermination = classifyClientTermination(ctx)
	result.failureKind = attemptFailureInternal
	if result.clientTermination.observed() {
		result.failureKind = attemptFailureClientTerminated
	}
	result.failureMessage = discardErr.Error()
	return result, true
}

func cloneProviderExclusions(source map[string]bool) map[string]bool {
	clone := make(map[string]bool, len(source)+1)
	maps.Copy(clone, source)
	return clone
}
