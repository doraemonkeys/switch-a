package proxy

import (
	"context"
	"time"
)

type executeProxyLoopAction int

const (
	executeProxyLoopActionReady executeProxyLoopAction = iota
	executeProxyLoopActionContinue
	executeProxyLoopActionBreak
	executeProxyLoopActionReturn
)

// executeProxy keeps the retry loop orchestration together so handler.go can stay
// focused on request entry and response plumbing.
func (h *Handler) executeProxy(ctx context.Context, pctx *proxyContext) {
	state := &retryState{
		excludedProviders: make(map[string]bool),
		switchTracker:     newProviderSwitchTracker(pctx.selectReq, pctx.cfg.globalMaxAttempts, h.visibleContinuitySeedStore),
	}
	h.maybeLookupVisibleContinuityCandidate(ctx, &state.switchTracker)

	attempt := 0
	for !state.headersWritten {
		attemptStart, action := h.prepareExecuteProxyAttempt(ctx, pctx, state, attempt)
		if action == executeProxyLoopActionReturn {
			return
		}
		if action == executeProxyLoopActionBreak {
			break
		}
		if action == executeProxyLoopActionContinue {
			continue
		}

		result := h.forwardToProvider(ctx, pctx, state.currentProvider)
		h.applyForwardResult(state, result)
		action = h.advanceExecuteProxyAttempt(ctx, pctx, state, result, attempt, attemptStart)
		if action == executeProxyLoopActionBreak {
			break
		}
		if action == executeProxyLoopActionContinue {
			attempt++
			continue
		}

		attempt++
	}

	h.finalizeProxy(pctx, state)
}

func (h *Handler) prepareExecuteProxyAttempt(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	attempt int,
) (time.Time, executeProxyLoopAction) {
	if pctx.cfg.globalMaxAttempts > 0 && attempt >= pctx.cfg.globalMaxAttempts {
		return time.Time{}, executeProxyLoopActionBreak
	}

	// The retry loop exits before selection so cancellation never burns another
	// provider attempt or mutates continuity state after the client is gone.
	if err := ctx.Err(); err != nil {
		state.lastErr = err
		return time.Time{}, executeProxyLoopActionBreak
	}

	if state.currentProvider == nil {
		continueLoop, earlyReturn := h.selectAndRegisterProvider(ctx, pctx, state, attempt)
		switch {
		case earlyReturn:
			return time.Time{}, executeProxyLoopActionReturn
		case !continueLoop:
			return time.Time{}, executeProxyLoopActionBreak
		}
	}

	if action := h.refreshRetryProviderIfNeeded(ctx, pctx, state); action != executeProxyLoopActionReady {
		return time.Time{}, action
	}

	state.providerUsed = state.currentProvider
	return time.Now(), executeProxyLoopActionReady
}

func (h *Handler) refreshRetryProviderIfNeeded(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
) executeProxyLoopAction {
	if state.providerAttempt <= 0 {
		return executeProxyLoopActionReady
	}

	refreshedProvider, err := h.eligibleProviderByID(ctx, pctx.selectReq, state.currentProvider.ID)
	if err != nil {
		state.lastErr = err
		return executeProxyLoopActionBreak
	}
	if refreshedProvider == nil {
		h.excludeCurrentProvider(pctx, state)
		return executeProxyLoopActionContinue
	}

	state.currentProvider = refreshedProvider
	return executeProxyLoopActionReady
}

func (h *Handler) applyForwardResult(state *retryState, result forwardResult) {
	state.headersWritten = result.headersWritten
	state.statusCode = result.statusCode
	state.lastErr = result.err
	state.success = result.success
	state.isSSE = result.isSSE
	state.responseCommitted = result.responseCommitted
	if state.responseCommitted {
		state.switchTracker.markClientVisible(state.currentProvider, time.Now())
	}
	state.firstTokenMs = result.firstTokenMs
	state.responseBytes = result.responseBytes
	state.tokenUsage = result.tokenUsage
	state.failureDisposition = result.failureDisposition
}

func (h *Handler) advanceExecuteProxyAttempt(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	result forwardResult,
	attempt int,
	attemptStart time.Time,
) executeProxyLoopAction {
	// SSE status is updated inside forwardToProvider before streaming starts so
	// monitor state can reflect a long-lived stream even when the write blocks.
	if result.done {
		h.recordAttempt(pctx, state, result, attempt, attemptStart, "")
		return executeProxyLoopActionBreak
	}

	currentProviderAttempt := state.providerAttempt
	exhausted, switchReason := h.tryIncrementAndExhaustProvider(ctx, state)
	nextProviderAttempt := state.providerAttempt

	state.providerAttempt = currentProviderAttempt
	h.recordAttempt(pctx, state, result, attempt, attemptStart, switchReason)

	if !exhausted {
		state.providerAttempt = nextProviderAttempt
		// providerAttempt is already incremented in tryIncrementAndExhaustProvider,
		// so retryIndex = providerAttempt - 1 (0 for first retry, 1 for second, etc.)
		if h.applyBackoffDelay(ctx, state.currentProvider, state.providerAttempt-1) {
			state.lastErr = ctx.Err()
			return executeProxyLoopActionBreak
		}
		return executeProxyLoopActionContinue
	}

	state.providerAttempt = 0
	state.switchTracker.prepareProviderSwitch()
	h.excludeCurrentProvider(pctx, state)
	return executeProxyLoopActionContinue
}

// finalizeProxy performs cleanup and logging after the retry loop completes.
func (h *Handler) finalizeProxy(pctx *proxyContext, state *retryState) {
	// Active registry teardown owns provider-slot release for tracked requests so
	// background stale cleanup and explicit request completion share one contract.
	if state.activeRegistered && h.activeRegistry != nil {
		h.activeRegistry.Unregister(pctx.requestID)
	} else if state.currentProvider != nil {
		h.releaseConcurrency(state.currentProvider.ID)
	}

	if shouldStoreHTTPVisibleContinuitySeed(state) {
		h.storeVisibleContinuitySeed(&state.switchTracker, time.Now())
	}

	// Log request asynchronously.
	// Trade-off: Fire-and-forget logging may lose logs on immediate shutdown,
	// but avoids blocking the response path. For a high-throughput proxy,
	// this is an acceptable trade-off as most logs will complete.
	go h.logRequest(
		pctx,
		state.providerUsed,
		state.statusCode,
		state.success,
		state.isSSE,
		state.firstTokenMs,
		state.responseBytes,
		state.tokenUsage,
		state.lastErr,
		time.Since(pctx.startTime),
	)

	// Handle exhausted retries.
	if !state.success && !state.headersWritten {
		h.handleExhaustedRetries(pctx, state.lastErr)
	}
}
