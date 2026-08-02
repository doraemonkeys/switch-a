package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"
)

type httpAttemptContext struct {
	provider             *model.Provider
	providerAttemptIndex int
	selectionMode        requestcapture.SelectionMode
	selectionSource      requestcapture.SelectionSource
}

func (a httpAttemptContext) metadata(apiType string, phase requestcapture.CredentialPhase) requestcapture.AttemptMetadata {
	return requestcapture.AttemptMetadata{
		Provider: requestcapture.ProviderIdentity{
			ID:   a.provider.ID,
			Name: a.provider.Name,
		},
		APIType:              apiType,
		SelectionMode:        a.selectionMode,
		SelectionSource:      a.selectionSource,
		ProviderAttemptIndex: a.providerAttemptIndex,
		CredentialPhase:      phase,
	}
}

func requestAttemptSelectionMode(mode model.SwitchMode) requestcapture.SelectionMode {
	switch mode {
	case model.SwitchModeReplacement:
		return requestcapture.SelectionModeReplacement
	case model.SwitchModeFailover:
		return requestcapture.SelectionModeFailover
	default:
		return requestcapture.SelectionModeInitial
	}
}

func requestAttemptSelectionSource(source selector.SelectionSource) requestcapture.SelectionSource {
	switch source {
	case selector.SelectionSourceStickyContinuity:
		return requestcapture.SelectionSourceStickyContinuity
	case selector.SelectionSourceActiveContinuity:
		return requestcapture.SelectionSourceActiveContinuity
	default:
		return requestcapture.SelectionSourceStrategy
	}
}

// Keep the attempt cutover boundary explicit in process memory so tests, mocks,
// and alternate stores never depend on database defaults to distinguish
// normalized evidence from legacy rows.
func newNormalizedRequestAttempt(requestID, providerID string, createdAt time.Time) model.RequestAttempt {
	return model.RequestAttempt{
		RequestID:        requestID,
		ProviderID:       providerID,
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		CreatedAt:        createdAt,
	}
}

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

		result := h.forwardToProvider(ctx, pctx, httpAttemptContext{
			provider:             state.currentProvider,
			providerAttemptIndex: state.providerAttempt,
			selectionMode:        requestAttemptSelectionMode(state.selectionMode),
			selectionSource:      requestAttemptSelectionSource(state.selectionMetadata.Source),
		})
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
		state.clientCanceled = state.clientCanceled || isClientCancellation(err)
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
	state.clientCanceled = state.clientCanceled || result.clientCanceled
	if state.responseCommitted {
		state.switchTracker.markClientVisible(state.currentProvider, time.Now())
	}
	state.firstTokenMs = result.firstTokenMs
	state.responseBytes = result.responseBytes
	state.tokenUsage = result.tokenUsage
	state.failureDisposition = result.failureDisposition
	// Mirror transport observation facts so finalizeProxy can reconstruct
	// the SSE observation without re-reading the writer. Only the last
	// attempt's facts survive here (consistent with err/statusCode).
	state.firstByteVisible = result.firstByteVisible
	state.isStatusFailover = result.isStatusFailover
	state.isClientWriteError = result.isClientWriteError
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
		attachSSEAttemptEvidence(pctx, attemptFactsFromForwardResult(ctx, result))
		return executeProxyLoopActionBreak
	}

	currentProviderAttempt := state.providerAttempt
	exhausted, switchReason := h.tryIncrementAndExhaustProvider(ctx, state)
	nextProviderAttempt := state.providerAttempt

	state.providerAttempt = currentProviderAttempt
	h.recordAttempt(pctx, state, result, attempt, attemptStart, switchReason)
	attachSSEAttemptEvidence(pctx, attemptFactsFromForwardResult(ctx, result))

	if !exhausted {
		state.providerAttempt = nextProviderAttempt
		// providerAttempt is already incremented in tryIncrementAndExhaustProvider,
		// so retryIndex = providerAttempt - 1 (0 for first retry, 1 for second, etc.)
		if h.applyBackoffDelay(ctx, state.currentProvider, state.providerAttempt-1) {
			state.lastErr = ctx.Err()
			state.clientCanceled = state.clientCanceled || isClientCancellation(state.lastErr)
			return executeProxyLoopActionBreak
		}
		return executeProxyLoopActionContinue
	}

	state.providerAttempt = 0
	state.switchTracker.prepareProviderSwitch()
	h.excludeCurrentProvider(pctx, state)
	return executeProxyLoopActionContinue
}

// attemptFactsFromForwardResult projects a single attempt's forwardResult
// into the shared runtime-facts shape. Keeping this translation in one
// place prevents drift between the session-level logRequest aggregate and
// the per-attempt evidence attach path ? both sides ultimately feed the
// same derivation function.
func attemptFactsFromForwardResult(ctx context.Context, result forwardResult) nonWebSocketRuntimeFacts {
	return nonWebSocketRuntimeFacts{
		ClientTransportStatusCode: result.statusCode,
		Success:                   result.success,
		ResponseCommitted:         result.responseCommitted,
		ServiceStarted:            nonWebSocketServiceStarted(result.statusCode, result.responseCommitted),
		ClientCanceled:            result.clientCanceled,
		TerminalErr:               result.err,
		IsSSE:                     result.isSSE,
		FirstByteVisible:          result.firstByteVisible,
		CtxErr:                    ctx.Err(),
		IsStatusFailover:          result.isStatusFailover,
		IsClientWriteError:        result.isClientWriteError,
	}
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

	// Handle exhausted retries.
	if !state.success && !state.headersWritten {
		h.handleExhaustedRetries(pctx, state.lastErr)
	}
	clientTransportStatusCode := state.statusCode
	if !state.success && !state.headersWritten {
		clientTransportStatusCode = http.StatusServiceUnavailable
	}

	// Log request asynchronously.
	// Trade-off: Fire-and-forget logging may lose logs on immediate shutdown,
	// but avoids blocking the response path. For a high-throughput proxy,
	// this is an acceptable trade-off as most logs will complete.
	go h.logRequest(pctx, logRequestInputs{
		Provider: state.providerUsed,
		Facts: nonWebSocketRuntimeFacts{
			ClientTransportStatusCode: clientTransportStatusCode,
			Success:                   state.success,
			ResponseCommitted:         state.responseCommitted,
			ServiceStarted:            nonWebSocketServiceStarted(clientTransportStatusCode, state.responseCommitted),
			ClientCanceled:            state.clientCanceled,
			TerminalErr:               state.lastErr,
			// SSE observation plumbing ? only read by the evidence builder
			// when IsSSE=true, so non-SSE paths leave them zero-valued.
			IsSSE:              state.isSSE,
			FirstByteVisible:   state.firstByteVisible,
			CtxErr:             pctx.r.Context().Err(),
			IsStatusFailover:   state.isStatusFailover,
			IsClientWriteError: state.isClientWriteError,
		},
		FirstTokenMs:  state.firstTokenMs,
		ResponseBytes: state.responseBytes,
		TokenUsage:    state.tokenUsage,
		Latency:       time.Since(pctx.startTime),
	})
}
