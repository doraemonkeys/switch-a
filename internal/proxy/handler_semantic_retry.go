package proxy

import (
	"context"
	"errors"

	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/selector"
)

func (h *Handler) resolveSemanticMatch(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	semantic *semanticAttemptFacts,
) (forwardResult, bool) {
	if semantic == nil {
		result, err := pending.commit(responseanalysis.TransitionPassthrough)
		if err == nil {
			h.finalizeCommittedAttempt(ctx, pctx, state, &result)
		}
		return result, false
	}
	action := semantic.winner.Rule.Action
	freezeSemanticRetryFacts(semantic, state.ledger, globalAttemptLimit(pctx.cfg.globalMaxAttempts))
	result := forwardResult{statusCode: pending.head.StatusCode, isSSE: pending.head.EventStream, semantic: semantic}
	h.assessAndApplyHealth(ctx, pctx, state.currentProvider.ID, &result, errorrule.AttemptFacts{
		SemanticMatched: true, Committable2xx: true,
	}, action)
	decision, err := errorrule.DecideRetry(errorrule.DecisionInput{
		Action: action, ProviderID: errorrule.ProviderID(state.currentProvider.ID),
		RuleID: semantic.winner.Rule.ID, Ledger: state.ledger,
		GlobalMaxAttempts: globalAttemptLimit(pctx.cfg.globalMaxAttempts),
		Provider:          errorrule.EligibleProvider(),
	})
	if err != nil {
		return h.commitSemanticResponse(ctx, pctx, state, pending, semantic, result, errorrule.Decision{})
	}
	semantic.decision = decision
	switch decision.Value {
	case errorrule.DecisionRetrySame:
		return h.retrySemanticSameProvider(ctx, pctx, state, pending, semantic, result, action)
	case errorrule.DecisionSwitchProvider:
		return h.switchSemanticProvider(ctx, pctx, state, pending, semantic, result, decision)
	default:
		return h.commitSemanticResponse(ctx, pctx, state, pending, semantic, result, decision)
	}
}

func (h *Handler) retrySemanticSameProvider(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	semantic *semanticAttemptFacts,
	result forwardResult,
	action errorrule.Action,
) (forwardResult, bool) {
	policy, _ := action.RetryPolicy()
	key := errorrule.ProviderRuleKey{
		ProviderID: errorrule.ProviderID(state.currentProvider.ID), RuleID: semantic.winner.Rule.ID,
	}
	retryIndex := int(state.ledger.RuleRetriesScheduled(key))
	if err := h.backoff.Wait(ctx, policy.Backoff.DelayForRetry(retryIndex)); err != nil {
		return h.cancelPendingResponse(pending, semantic, result, err)
	}
	if h.httpSelector == nil {
		decision, _ := errorrule.DecideRetry(errorrule.DecisionInput{
			Action: action, ProviderID: key.ProviderID, RuleID: key.RuleID, Ledger: state.ledger,
			GlobalMaxAttempts: globalAttemptLimit(pctx.cfg.globalMaxAttempts),
			Provider:          errorrule.IneligibleProvider(errorrule.ReasonProviderLookupError),
		})
		semantic.decision = decision
		if decision.Value == errorrule.DecisionSwitchProvider {
			return h.switchSemanticProvider(ctx, pctx, state, pending, semantic, result, decision)
		}
		return h.commitSemanticResponse(ctx, pctx, state, pending, semantic, result, decision)
	}
	permit, err := h.httpSelector.ReserveSameProviderRetry(ctx, sameProviderRetryReservation{
		current: state.currentLease, request: pctx.selectReq, ruleKey: key, ledger: state.ledger,
		globalMaxAttempts: globalAttemptLimit(pctx.cfg.globalMaxAttempts),
	})
	if err != nil {
		if isClientCancellation(err) {
			return h.cancelPendingResponse(pending, semantic, result, err)
		}
		reason, ok := selector.ProviderRejectionReason(err)
		if !ok {
			reason = errorrule.ReasonProviderLookupError
		}
		decision, decisionErr := errorrule.DecideRetry(errorrule.DecisionInput{
			Action: action, ProviderID: key.ProviderID, RuleID: key.RuleID, Ledger: state.ledger,
			GlobalMaxAttempts: globalAttemptLimit(pctx.cfg.globalMaxAttempts),
			Provider:          errorrule.IneligibleProvider(reason),
		})
		if decisionErr != nil {
			return h.commitSemanticResponse(ctx, pctx, state, pending, semantic, result, errorrule.Decision{})
		}
		semantic.decision = decision
		if decision.Value == errorrule.DecisionSwitchProvider {
			return h.switchSemanticProvider(ctx, pctx, state, pending, semantic, result, decision)
		}
		return h.commitSemanticResponse(ctx, pctx, state, pending, semantic, result, decision)
	}
	discarded, err := pending.discard(
		responseanalysis.TransitionSemanticDecision,
		requestcapture.TerminationReasonInternalErrorAbsorbed,
		semanticCaptureFailure(),
	)
	if err != nil {
		permit.Release()
		if discarded.discarded {
			discarded.semantic = semantic
			discarded.inheritHealth(result)
			return discarded, false
		}
		return h.cancelPendingResponse(pending, semantic, result, err)
	}
	nextLedger, err := permit.Activate()
	if err != nil {
		discarded.failureKind = attemptFailureInternal
		discarded.failureMessage = err.Error()
		discarded.semantic = semantic
		discarded.inheritHealth(result)
		return discarded, false
	}
	state.ledger = nextLedger
	state.currentProvider = permit.Provider()
	state.providerAttempt++
	discarded.semantic = semantic
	discarded.inheritHealth(result)
	discarded.done = false
	return discarded, true
}

func (h *Handler) switchSemanticProvider(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	semantic *semanticAttemptFacts,
	result forwardResult,
	decision errorrule.Decision,
) (forwardResult, bool) {
	switched, resolved, continueExecution := h.activateAlternate(
		ctx, pctx, state, pending, result, SwitchReasonMaxRetriesExhausted,
		responseanalysis.TransitionSemanticDecision, requestcapture.TerminationReasonInternalErrorAbsorbed,
	)
	if switched {
		return completeSemanticSwitch(state, semantic, result, resolved, decision, continueExecution)
	}
	return h.resolveSemanticAlternateFailure(
		ctx, pctx, state, pending, semantic, result, resolved, decision, continueExecution,
	)
}

func completeSemanticSwitch(
	state *retryState,
	semantic *semanticAttemptFacts,
	prior forwardResult,
	resolved forwardResult,
	decision errorrule.Decision,
	continueExecution bool,
) (forwardResult, bool) {
	semantic.decision = decision
	semantic.alternateOutcome = attemptevidence.AlternateActivated
	providerID := state.currentProvider.ID
	semantic.alternateProviderID = &providerID
	switchMode := evidenceSwitchMode(state.selectionMode)
	semantic.alternateSwitchMode = &switchMode
	if switchReason, ok := decision.SwitchReason(); ok {
		semantic.alternateSwitchReason = &switchReason
		resolved.switchReason = string(switchReason)
	}
	resolved.semantic = semantic
	resolved.inheritHealth(prior)
	return resolved, continueExecution
}

func (h *Handler) resolveSemanticAlternateFailure(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	semantic *semanticAttemptFacts,
	prior forwardResult,
	resolved forwardResult,
	decision errorrule.Decision,
	continueExecution bool,
) (forwardResult, bool) {
	outcome, evidenceOutcome := semanticAlternateFailureOutcome(resolved)
	semantic.alternateOutcome = evidenceOutcome
	if !resolved.discarded {
		if updated, err := errorrule.ResolveAlternate(decision, outcome); err == nil {
			semantic.decision = updated
		}
	}
	if pending != nil && resolved.clientCanceled && !resolved.discarded {
		cancelErr := ctx.Err()
		if cancelErr == nil {
			cancelErr = errors.New("alternate reservation canceled")
		}
		return h.cancelPendingResponse(pending, semantic, prior, cancelErr)
	}
	if pending != nil && !resolved.responseCommitted && !resolved.discarded {
		return h.commitSemanticResponse(ctx, pctx, state, pending, semantic, prior, semantic.decision)
	}
	resolved.semantic = semantic
	resolved.inheritHealth(prior)
	return resolved, continueExecution
}

func semanticAlternateFailureOutcome(result forwardResult) (errorrule.AlternateOutcome, attemptevidence.AlternateOutcome) {
	switch {
	case result.clientCanceled:
		return errorrule.AlternateCancelled, attemptevidence.AlternateReleased
	case result.failureKind == attemptFailureInternal:
		return errorrule.AlternateFailed, attemptevidence.AlternateFailed
	default:
		return errorrule.AlternateUnavailable, attemptevidence.AlternateUnavailable
	}
}

func (h *Handler) commitSemanticResponse(
	ctx context.Context,
	pctx *proxyContext,
	state *retryState,
	pending *pendingHTTPResponse,
	semantic *semanticAttemptFacts,
	prior forwardResult,
	decision errorrule.Decision,
) (forwardResult, bool) {
	semantic.decision = decision
	result, err := pending.commit(responseanalysis.TransitionSemanticDecision)
	// The decisive match owns the attempt identity and decision-time ledger snapshot.
	// Commit may re-materialize the same parser observation after forwarding, but
	// that value has no frozen retry/alternate facts and must not replace the
	// snapshot that authorized this terminal response.
	result.semantic = semantic
	result.inheritHealth(prior)
	if err != nil {
		return result, false
	}
	result.success = false
	h.finalizeCommittedResponse(pctx, state, &result)
	return result, false
}

func (h *Handler) cancelPendingResponse(
	pending *pendingHTTPResponse,
	semantic *semanticAttemptFacts,
	prior forwardResult,
	err error,
) (forwardResult, bool) {
	captureReason := requestcapture.TerminationReasonCanceled
	captureFailure := requestcapture.FailureObservation{}
	if semantic != nil {
		captureReason = requestcapture.TerminationReasonInternalErrorAbsorbed
		captureFailure = semanticCaptureFailure()
	}
	result, discardErr := pending.discard(
		responseanalysis.TransitionSemanticDecision,
		captureReason,
		captureFailure,
	)
	if discardErr != nil && err == nil {
		err = discardErr
	}
	result.clientCanceled = true
	result.failureKind = attemptFailureCanceled
	if err != nil {
		result.failureMessage = err.Error()
	}
	result.semantic = semantic
	result.inheritHealth(prior)
	return result, false
}
