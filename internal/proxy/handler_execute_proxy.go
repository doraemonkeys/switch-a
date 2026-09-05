package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestingress"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

type httpAttemptContext struct {
	provider             *model.Provider
	candidate            codexidentity.CandidateSnapshot
	logicalAttemptIndex  int
	providerAttemptIndex int
	selectionMode        requestcapture.SelectionMode
	selectionSource      requestcapture.SelectionSource
	switchMode           model.SwitchMode
	selectionMetadata    selector.SelectionMetadata
	providerSwitchCount  int
}

func (a httpAttemptContext) metadata(apiType string, phase requestcapture.CredentialPhase) requestcapture.AttemptMetadata {
	return requestcapture.AttemptMetadata{
		Provider: requestcapture.ProviderIdentity{ID: a.provider.ID, Name: a.provider.Name},
		APIType:  apiType, SelectionMode: a.selectionMode, SelectionSource: a.selectionSource,
		ProviderAttemptIndex: a.providerAttemptIndex, CredentialPhase: phase,
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

func newNormalizedRequestAttempt(requestID, providerID string, createdAt time.Time) model.RequestAttempt {
	return model.RequestAttempt{
		RequestID: requestID, ProviderID: providerID,
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1, CreatedAt: createdAt,
	}
}

func (h *Handler) executeProxy(ctx context.Context, pctx *proxyContext) {
	rules, err := errorrule.PinRuleSet(h.ruleSets)
	if err != nil {
		h.logger.Error("failed to pin internal-error rule set", zap.String("request_id", pctx.requestID), zap.Error(err))
		h.writeGatewayError(pctx.w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to initialize response processing")
		return
	}
	state := &retryState{
		excludedProviders: make(map[string]bool),
		switchTracker: newProviderSwitchTracker(
			pctx.selectReq, pctx.cfg.globalMaxAttempts, h.visibleContinuitySeedStore,
		),
	}
	h.maybeLookupVisibleContinuityCandidate(ctx, &state.switchTracker)
	if !h.startInitialSelection(ctx, pctx, state) {
		return
	}

	for {
		if err := ctx.Err(); err != nil {
			state.lastErr = err
			state.clientTermination = classifyClientTermination(ctx)
			break
		}
		attempt := state.attemptContext()
		attemptIndex := int(state.ledger.LogicalAttemptsStarted()) - 1
		attemptStart := time.Now()
		result, continueExecution := h.executeLogicalAttempt(ctx, pctx, state, attempt, rules)
		if pctx.ingress != nil {
			snapshot := pctx.ingress.Snapshot()
			if snapshot.State == requestingress.Failed {
				result.success = false
				result.failureKind = attemptFailureIngress
				result.ingressFailureKind = snapshot.FailureKind
				result.failureMessage = snapshot.Err.Error()
				continueExecution = false
			}
		}
		h.applyForwardResult(state, result)
		h.recordAttempt(pctx, attempt, result, attemptIndex, attemptStart)
		facts := attemptFactsFromForwardResult(ctx, result)
		h.attachHTTPAttemptEvidence(pctx, result, facts)
		if !continueExecution {
			break
		}
	}
	h.finalizeProxy(pctx, state)
}

func (h *Handler) startInitialSelection(ctx context.Context, pctx *proxyContext, state *retryState) bool {
	state.selectionMode = state.switchTracker.prepareSelection()
	selection, err := h.selectInitialProvider(ctx, pctx.selectReq, 0, state.excludedProviders)
	if err != nil {
		state.lastErr = err
		if errors.Is(err, internal.ErrNoProvider) {
			h.handleNoProvider(pctx, err)
		}
		return false
	}
	ledger, err := state.ledger.StartAttempt(
		errorrule.ProviderID(selection.provider.ID), globalAttemptLimit(pctx.cfg.globalMaxAttempts),
	)
	if err != nil {
		selection.lease.Release()
		state.lastErr = err
		return false
	}
	state.ledger = ledger
	state.currentProvider = selection.provider
	state.currentLease = selection.lease
	state.selectionMetadata = selection.metadata
	state.selectionMode = state.switchTracker.recordSelection(selection.provider, selection.metadata)
	state.providerAttempt = 0
	state.providerUsed = selection.provider
	pctx.isSticky = selection.metadata.UsesContinuity()
	h.registerActiveRequest(pctx, state)
	pctx.publishRequestObservation()
	return true
}

func (state *retryState) attemptContext() httpAttemptContext {
	candidate, _ := state.currentLease.CandidateSnapshot()
	return httpAttemptContext{
		provider:             state.currentProvider,
		candidate:            candidate,
		logicalAttemptIndex:  int(state.ledger.LogicalAttemptsStarted()) - 1,
		providerAttemptIndex: state.providerAttempt,
		selectionMode:        requestAttemptSelectionMode(state.selectionMode),
		selectionSource:      requestAttemptSelectionSource(state.selectionMetadata.Source),
		switchMode:           state.selectionMode, selectionMetadata: state.selectionMetadata,
		providerSwitchCount: state.switchTracker.providerSwitchCount(),
	}
}

func globalAttemptLimit(configured int) uint {
	if configured <= 0 {
		return 0
	}
	return uint(configured)
}

func (h *Handler) executeLogicalAttempt(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	attempt httpAttemptContext,
	rules *errorrule.CompiledRuleSet,
) (forwardResult, bool) {
	request, err := h.prepareForwardRequest(ctx, pctx, attempt, requestcapture.CredentialPhaseInitial)
	if err != nil {
		result := failureResult(attemptFailurePreparation, err)
		return h.resolveLegacyFailure(ctx, pctx, state, nil, result)
	}

	pending, err := h.fetchPendingHTTPResponse(
		ctx, pctx, attempt, requestcapture.CredentialPhaseInitial, request, rules,
	)
	if err != nil {
		result := failureResult(attemptFailureTransport, err)
		result.clientTermination = classifyClientTermination(ctx)
		if result.clientTermination.observed() {
			result.failureKind = attemptFailureClientTerminated
		}
		h.assessAndApplyHealth(ctx, pctx, state.currentProvider.ID, &result, errorrule.AttemptFacts{
			TransportFailure: !result.clientTermination.observed(), ClientCancelled: result.clientTermination.observed(),
		}, errorrule.Action{})
		if result.clientTermination.observed() {
			return result, false
		}
		return h.resolveLegacyFailure(ctx, pctx, state, nil, result)
	}

	pending, refreshedResult, refreshed := h.refreshUnauthorizedSubexchange(
		ctx, pctx, state, attempt, rules, pending,
	)
	if refreshedResult != nil {
		return *refreshedResult, refreshed
	}
	return h.resolvePendingResponse(ctx, pctx, state, pending)
}

func (h *Handler) refreshUnauthorizedSubexchange(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	attempt httpAttemptContext,
	rules *errorrule.CompiledRuleSet,
	pending *pendingHTTPResponse,
) (*pendingHTTPResponse, *forwardResult, bool) {
	if pending.head.StatusCode != http.StatusUnauthorized {
		return pending, nil, false
	}
	refreshed, refreshErr := h.auth.RefreshCredentialSession(ctx, attempt.candidate.Credential())
	if refreshErr != nil || !refreshed {
		if refreshErr != nil {
			h.logger.Warn("provider credential refresh failed",
				zap.String("request_id", pctx.requestID), zap.String("provider_id", state.currentProvider.ID),
				zap.Error(refreshErr),
			)
		}
		return pending, nil, false
	}
	permit, err := h.reserveSameProviderDispatch(ctx, pctx.selectReq, state.currentLease)
	if err != nil {
		return pending, nil, false
	}
	liveProvider := permit.Provider()
	if liveProvider == nil {
		permit.Release()
		return pending, nil, false
	}
	refreshedAttempt := attempt
	refreshedAttempt.provider = liveProvider
	request, err := h.prepareForwardRequest(
		ctx, pctx, refreshedAttempt, requestcapture.CredentialPhaseRefreshed,
	)
	if err != nil {
		permit.Release()
		return pending, nil, false
	}
	discarded, discardErr := pending.discard(
		responseanalysis.TransitionExecutorDecision,
		requestcapture.TerminationReasonCredentialRefreshDrain,
		statusCaptureFailure(http.StatusUnauthorized),
	)
	if discardErr != nil {
		permit.Release()
		result := failureResult(attemptFailureInternal, discardErr)
		result.clientTermination = classifyClientTermination(ctx)
		if result.clientTermination.observed() {
			result.failureKind = attemptFailureClientTerminated
		}
		return nil, &result, false
	}
	activatedProvider, activateErr := permit.Activate()
	if activateErr != nil {
		discarded.failureKind = attemptFailureInternal
		discarded.failureMessage = activateErr.Error()
		discarded.clientTermination = classifyClientTermination(ctx)
		if discarded.clientTermination.observed() {
			discarded.failureKind = attemptFailureClientTerminated
		}
		return nil, &discarded, false
	}
	state.currentProvider = activatedProvider
	refreshedAttempt.provider = activatedProvider
	refreshedPending, err := h.fetchPendingHTTPResponse(
		ctx, pctx, refreshedAttempt, requestcapture.CredentialPhaseRefreshed, request, rules,
	)
	if err != nil {
		result := failureResult(attemptFailureTransport, err)
		result.clientTermination = classifyClientTermination(ctx)
		if result.clientTermination.observed() {
			result.failureKind = attemptFailureClientTerminated
		}
		h.assessAndApplyHealth(ctx, pctx, state.currentProvider.ID, &result, errorrule.AttemptFacts{
			TransportFailure: !result.clientTermination.observed(), ClientCancelled: result.clientTermination.observed(),
		}, errorrule.Action{})
		if result.clientTermination.observed() {
			return nil, &result, false
		}
		resolved, again := h.resolveLegacyFailure(ctx, pctx, state, nil, result)
		return nil, &resolved, again
	}
	return refreshedPending, nil, false
}

func (h *Handler) resolvePendingResponse(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
) (forwardResult, bool) {
	statusCode := pending.head.StatusCode
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		if shouldFailover(statusCode) {
			return h.resolveStatusFailure(ctx, pctx, state, pending)
		}
		result, err := pending.commit(responseanalysis.TransitionExecutorDecision)
		if err != nil {
			return result, false
		}
		h.finalizeCommittedAttempt(ctx, pctx, state, &result)
		return result, false
	}

	boundary := pending.awaitBoundary()
	if boundary.State == responseanalysis.StateProbing && boundary.Reason == responseanalysis.BoundarySemanticMatch {
		semantic := pending.semanticFacts(boundary.Observation, boundary.State, boundary.Reason)
		boundary.Observation.Release()
		pending.recordWinningRule(semantic)
		return h.resolveSemanticMatch(ctx, pctx, state, pending, semantic)
	}
	if boundary.Forwarding == nil {
		result := pending.internalFailure(errors.New("response resolved without forwarding capability"))
		if boundary.Reason == responseanalysis.BoundaryClientCancelled {
			result.clientTermination = classifyClientTermination(ctx)
			if !result.clientTermination.observed() {
				result.clientTermination = clientTerminationDisconnect
			}
			result.failureKind = attemptFailureClientTerminated
		}
		return result, false
	}
	result := pending.finishForwarding(boundary.Forwarding)
	h.finalizeCommittedAttempt(ctx, pctx, state, &result)
	return result, false
}

func (h *Handler) resolveStatusFailure(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
) (forwardResult, bool) {
	statusCode := pending.head.StatusCode
	result := forwardResult{
		statusCode: statusCode, isSSE: pending.media.IsEventStream(), isStatusFailover: true,
		failureKind:    attemptFailureStatus,
		failureMessage: fmt.Sprintf("upstream returned status %d", statusCode),
		failureDisposition: classifyProviderFailureForProvider(
			state.currentProvider, statusCode, pending.head.SourceHeader, "", time.Now(),
		),
	}
	h.assessAndApplyHealth(ctx, pctx, state.currentProvider.ID, &result, errorrule.AttemptFacts{
		HTTPStatusFailure: true,
	}, errorrule.Action{})
	if result.failureDisposition.autoDisableUntil != nil {
		h.suspendProviderUntil(
			ctx, state.currentProvider.ID, *result.failureDisposition.autoDisableUntil,
			result.failureDisposition.autoDisableReason,
		)
	}
	return h.resolveLegacyFailure(ctx, pctx, state, pending, result)
}

func failureResult(kind attemptFailureKind, err error) forwardResult {
	if kind == attemptFailureTransport && errors.Is(err, io.EOF) {
		kind = attemptFailureUpstreamNoResponse
	}
	result := forwardResult{failureKind: kind}
	if err != nil {
		result.failureMessage = err.Error()
	}
	return result
}

func (h *Handler) finalizeCommittedAttempt(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	result *forwardResult,
) {
	action := errorrule.Action{}
	semanticMatched := result.semantic != nil
	if semanticMatched {
		action = result.semantic.winner.Rule.Action
		freezeSemanticRetryFacts(result.semantic, state.ledger, globalAttemptLimit(pctx.cfg.globalMaxAttempts))
		if result.semantic.decision.Value == "" {
			result.semantic.decision = visibleSemanticDecision(result.semantic.releaseCause)
		}
		result.success = false
	}
	facts := errorrule.AttemptFacts{
		TransportFailure: result.failureKind == attemptFailureRead,
		SemanticMatched:  semanticMatched, Committable2xx: result.statusCode >= http.StatusOK && result.statusCode < http.StatusMultipleChoices,
		Completed:       result.failureKind == attemptFailureNone,
		ClientCancelled: result.clientTermination.observed(),
	}
	h.assessAndApplyHealth(ctx, pctx, state.currentProvider.ID, result, facts, action)
	h.finalizeCommittedResponse(pctx, state, result)
}

func (h *Handler) finalizeCommittedResponse(pctx *proxyContext, state *retryState, result *forwardResult) {
	if result.responseCommitted {
		state.switchTracker.markClientVisible(state.currentProvider, time.Now())
	}
	if result.responseCommitted && pctx.cfg.stickyMode != model.StickyModeOff && h.selector != nil {
		h.selector.UpdateStickyWithTTL(pctx.selectReq, state.currentProvider.ID, pctx.cfg.stickyTTL)
	}
}

func (h *Handler) assessAndApplyHealth(
	ctx context.Context,
	pctx *proxyContext,
	providerID string,
	result *forwardResult,
	facts errorrule.AttemptFacts,
	action errorrule.Action,
) {
	if pctx.ingressFailure() != nil {
		result.health = errorrule.HealthAssessment{Verdict: errorrule.HealthNeutral, Cause: errorrule.HealthCauseIncomplete}
		result.healthAvailable = true
		return
	}
	class := errorrule.ClassifyAttempt(facts)
	assessment, available, err := errorrule.AssessHealth(class, action)
	if err != nil {
		h.logger.Warn("failed to assess provider health", zap.String("request_id", pctx.requestID), zap.Error(err))
		return
	}
	result.health = assessment
	result.healthAvailable = available
	if !available || h.health == nil {
		return
	}
	switch assessment.Verdict {
	case errorrule.HealthSuccess:
		h.health.MarkSuccess(ctx, providerID)
	case errorrule.HealthFailure:
		result.healthCircuitOpened = h.health.MarkFailure(ctx, providerID, fmt.Errorf("provider attempt failed: %s", assessment.Cause))
	}
}

func freezeSemanticRetryFacts(semantic *semanticAttemptFacts, ledger errorrule.RetryLedger, globalLimit uint) {
	if semantic == nil || semantic.retryFactsFrozen {
		return
	}
	remaining, unlimited := ledger.GlobalRemaining(globalLimit)
	key := errorrule.ProviderRuleKey{
		ProviderID: errorrule.ProviderID(semantic.providerID), RuleID: semantic.winner.Rule.ID,
	}
	policy, _ := semantic.winner.Rule.Action.RetryPolicy()
	semantic.globalAttemptsStarted = uint64(ledger.LogicalAttemptsStarted())
	semantic.globalAttemptsRemaining = uint64(remaining)
	semantic.globalAttemptsUnlimited = unlimited
	semantic.ruleRetriesScheduled = uint64(ledger.RuleRetriesScheduled(key))
	semantic.ruleRetryLimit = policy.MaxRetries
	semantic.retryFactsFrozen = true
	if semantic.alternateOutcome == "" {
		semantic.alternateOutcome = attemptevidence.AlternateNotRequested
	}
}

func evidenceSwitchMode(mode model.SwitchMode) attemptevidence.SwitchMode {
	if mode == model.SwitchModeFailover {
		return attemptevidence.SwitchModeFailover
	}
	return attemptevidence.SwitchModeReplacement
}

func visibleSemanticDecision(reason responseanalysis.BoundaryReason) errorrule.Decision {
	switch reason {
	case responseanalysis.BoundaryPassthroughOnly:
		return errorrule.Decision{Value: errorrule.DecisionPassthrough, Reason: errorrule.ReasonActionPassthrough}
	case responseanalysis.BoundaryNoRetryCandidate:
		return errorrule.Decision{Value: errorrule.DecisionObserveOnly, Reason: errorrule.ReasonObserverOnly}
	default:
		return errorrule.Decision{Value: errorrule.DecisionObserveOnly, Reason: errorrule.ReasonResponseAlreadyVisible}
	}
}

func (h *Handler) applyForwardResult(state *retryState, result forwardResult) {
	state.headersWritten = result.headersWritten
	state.statusCode = result.statusCode
	state.lastErr = result.terminalError()
	state.success = result.success
	state.isSSE = result.isSSE
	state.responseCommitted = result.responseCommitted
	state.clientTermination = mergeClientTermination(state.clientTermination, result.clientTermination)
	state.semanticError = result.semantic != nil
	state.firstTokenMs = result.firstTokenMs
	state.responseBytes = result.responseBytes
	state.tokenUsage = cloneTokenUsage(result.tokenUsage)
	state.failureDisposition = result.failureDisposition
	state.firstByteVisible = result.firstByteVisible
	state.isStatusFailover = result.isStatusFailover
	state.isClientWriteError = result.isClientWriteError
	state.injectedCredential = result.injectedCredential
}

func attemptFactsFromForwardResult(ctx context.Context, result forwardResult) nonWebSocketRuntimeFacts {
	return nonWebSocketRuntimeFacts{
		ClientTransportStatusCode: result.statusCode, Success: result.success,
		ResponseCommitted: result.responseCommitted,
		ServiceStarted:    nonWebSocketServiceStarted(result.statusCode, result.responseCommitted),
		ClientTermination: result.clientTermination, TerminalErr: result.terminalError(),
		IsSSE: result.isSSE, FirstByteVisible: result.firstByteVisible, CtxErr: ctx.Err(),
		IsStatusFailover: result.isStatusFailover, IsClientWriteError: result.isClientWriteError,
		SemanticError: result.semantic != nil, InjectedCredential: result.injectedCredential,
	}
}

func (h *Handler) finalizeProxy(pctx *proxyContext, state *retryState) {
	pctx.finishIngress()
	if sourceErr := pctx.ingressFailure(); sourceErr != nil {
		state.lastErr = sourceErr
		state.success = false
	}
	if state.activeRegistered && h.activeRegistry != nil {
		h.activeRegistry.Unregister(pctx.requestID)
	} else if state.currentLease != nil {
		state.currentLease.Release()
	}
	if shouldStoreHTTPVisibleContinuitySeed(state) {
		h.storeVisibleContinuitySeed(&state.switchTracker, time.Now())
	}
	clientStatus := state.statusCode
	if !state.success && !state.headersWritten {
		if sourceErr := pctx.ingressFailure(); sourceErr != nil {
			h.handleBodyError(pctx.w, sourceErr, pctx.cfg.maxBodySizeMB)
			clientStatus = http.StatusInternalServerError
			if errors.Is(sourceErr, requestingress.ErrBodyTooLarge) {
				clientStatus = http.StatusRequestEntityTooLarge
			}
		} else {
			clientStatus = h.handleExhaustedRetries(pctx, state.lastErr)
		}
	}
	h.scheduleProviderUsagePersistence(pctx)
	go h.logRequest(pctx, logRequestInputs{
		Provider: state.providerUsed,
		Facts: nonWebSocketRuntimeFacts{
			ClientTransportStatusCode: clientStatus, Success: state.success,
			ResponseCommitted: state.responseCommitted,
			ServiceStarted:    nonWebSocketServiceStarted(clientStatus, state.responseCommitted),
			ClientTermination: state.clientTermination, TerminalErr: state.lastErr,
			IsSSE: state.isSSE, FirstByteVisible: state.firstByteVisible,
			CtxErr: pctx.r.Context().Err(), IsStatusFailover: state.isStatusFailover,
			IsClientWriteError: state.isClientWriteError, SemanticError: state.semanticError,
			InjectedCredential: state.injectedCredential,
		},
		FirstTokenMs: state.firstTokenMs, ResponseBytes: state.responseBytes,
		TokenUsage: state.tokenUsage, Latency: time.Since(pctx.startTime),
	})
	if state.responseCommitted && pctx.ingressFailure() != nil {
		panic(http.ErrAbortHandler)
	}
}
