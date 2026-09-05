package websocketproxy

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"go.uber.org/zap"
)

func (o *WebSocketSessionOrchestrator) prepareSelectionContinuity(ctx context.Context) {
	if o.codexOperation == nil || !o.codexOperation.AllowsAccountSwitch() {
		o.handler.maybeLookupVisibleContinuityCandidate(ctx, &o.switchTracker)
		return
	}
	// A reconnect reaches the same selection boundary before relay-only model
	// observations exist. Freeze its effective key after optional probing, including
	// the API-type fallback, so later attempts cannot reindex the recovery seed.
	o.accountRecoveryKey = selector.BuildContinuityKey(o.selectReq)
	tracker := &o.switchTracker
	if tracker.seedStore == nil {
		return
	}
	// Recovery carries an explicit exclusion, so it does not wait for model-sticky
	// precision or for the failed provider to be selected like ordinary continuity.
	candidate, found := tracker.seedStore.Lookup(o.accountRecoveryKey)
	recoverySeed := found && candidate != nil && candidate.Purpose == model.VisibleContinuitySeedAccountRecovery
	o.handler.logger.Debug("websocket.account_recovery_seed_lookup",
		zap.String("operation_id", o.requestID),
		zap.String("continuity_model", o.accountRecoveryKey.Model),
		zap.String("probe_outcome", string(o.probeOutcome)),
		zap.Bool("found", found),
		zap.Bool("account_recovery", recoverySeed),
	)
	if !found || candidate == nil {
		return
	}
	if !recoverySeed {
		if o.handler.ordinaryVisibleContinuityReady(ctx, o.selectReq) {
			tracker.continuityCandidate = candidate
			tracker.syncRequest()
		}
		return
	}
	seed, ok := tracker.seedStore.CompareAndConsume(candidate.ContinuityKey, candidate.SeedID)
	if !ok {
		o.handler.logger.Debug("websocket.account_recovery_seed_consumption_lost", zap.String("operation_id", o.requestID), zap.String("seed_id", candidate.SeedID))
		return
	}
	tracker.continuityContext = seed.ProviderContinuityContext()
	for _, providerID := range seed.ExcludedProviderIDs {
		o.excludedProviders[providerID] = true
	}
	tracker.nextMode = model.SwitchModeFailover
	tracker.syncRequest()
	o.handler.logger.Debug("websocket.account_recovery_seed_consumed", zap.String("operation_id", o.requestID), zap.String("seed_id", seed.SeedID), zap.Strings("excluded_provider_ids", seed.ExcludedProviderIDs), zap.String("selection_mode", string(model.SwitchModeFailover)))
}

type webSocketClientClose struct {
	Code   websocket.StatusCode
	Reason string
	Notice []byte
}

// The policy owner settles failure effects before transport exposes the reconnect
// boundary, so a client reconnecting immediately observes the published outcome.
func (o *WebSocketSessionOrchestrator) accountRecoveryBeforeClose(ctx context.Context, provider *model.Provider, observer WebSocketMessageObserver) func(*webSocketRelaySessionResult) *webSocketClientClose {
	if o.codexOperation == nil || !o.codexOperation.AllowsAccountSwitch() {
		return nil
	}
	return func(relay *webSocketRelaySessionResult) *webSocketClientClose {
		if relay == nil || !relay.ClientVisible || provider == nil {
			return nil
		}
		result := relay.toWebSocketResult()
		if observer != nil {
			o.mergeRecoveryObservation(result, observer.Snapshot())
		}
		if result.UpstreamError != nil {
			result.TerminalCause = model.TerminalUpstreamSemanticError
		}
		assessment := newWebSocketAssessment(provider, webSocketGatewayEvidenceInput{}, result, result.Err, false, false, "")
		o.handler.logger.Debug("websocket.account_recovery_terminal", zap.String("operation_id", o.requestID), zap.String("provider_id", provider.ID), zap.String("terminal_cause", string(result.TerminalCause)), zap.String("client_action", string(assessment.ClientAction)), zap.String("service_outcome", string(assessment.ServiceOutcome)))
		if assessment.ClientAction != model.ClientActionReconnectRequired {
			return nil
		}
		settleContext := context.WithoutCancel(ctx)
		applyWebSocketHealthOutcome(settleContext, o.handler, provider, result)
		relay.healthOutcomePublished = true
		o.switchTracker.markClientVisible(provider, time.Now())
		seed := o.switchTracker.visibleContinuitySeed(time.Now())
		if seed != nil {
			seed.Purpose = model.VisibleContinuitySeedAccountRecovery
			seed.ContinuityKey = o.accountRecoveryKey
			seed.ExcludedProviderIDs = []string{provider.ID}
			o.handler.storeVisibleContinuitySeedFromContext(o.selectReq, o.selectReq.ProviderContinuityContext, time.Now(), seed)
		}
		relay.accountRecoveryNotified = true
		relay.CloseCode = websocket.StatusServiceRestart
		o.handler.logger.Warn("websocket.account_recovery_reconnect", zap.String("operation_id", o.requestID), zap.String("provider_id", provider.ID), zap.String("conversation_recovery_policy", string(model.ConversationRecoverySwitchAccountPreserveConversation)), zap.Bool("client_visible", true), zap.Int("attempt_index", len(o.attempts)), zap.String("switch_reason", string(model.RecoveryActionReconnectRequired)))
		return &webSocketClientClose{Code: websocket.StatusServiceRestart, Reason: ErrCodeWebSocketReconnect, Notice: marshalWebSocketGatewayError(webSocketReconnectRequiredStatusCode, ErrCodeWebSocketReconnect, webSocketReconnectRequiredMessage)}
	}
}

func closeWebSocketWithPolicy(ctx context.Context, client *websocket.Conn, result *webSocketRelaySessionResult, options webSocketRelayOptions) bool {
	if options.BeforeClientClose == nil {
		return false
	}
	directive := options.BeforeClientClose(result)
	if directive == nil {
		return false
	}
	writeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), webSocketFallbackWriteTimeout)
	defer cancel()
	if len(directive.Notice) > 0 {
		_ = client.Write(writeContext, websocket.MessageText, directive.Notice)
	}
	_ = client.Close(directive.Code, directive.Reason)
	return true
}

func shouldWriteWebSocketSticky(session *WebSocketSessionResult, recovery bool) bool {
	if session == nil || !session.ClientAccepted || session.FinalResult == nil {
		return false
	}
	if !recovery {
		return session.FinalResult.SessionCommitted
	}
	result := session.FinalResult
	if !result.ClientVisible || result.RecoveryAction == model.RecoveryActionReconnectRequired || result.UpstreamError != nil || result.accountRecoveryNotified {
		return false
	}
	assessment := assessWebSocketSession(session)
	return assessment.ServiceOutcome == model.ServiceOutcomeCompleted
}

func (o *WebSocketSessionOrchestrator) providerScopedSuppressDecision() func(webSocketPreWriteContext) webSocketPreWriteDecision {
	decide := newAllowlistedProviderScopedSuppressDecision(o.replayBuffer)
	if o.codexOperation == nil || !o.codexOperation.AllowsAccountSwitch() {
		return decide
	}
	return func(input webSocketPreWriteContext) webSocketPreWriteDecision {
		decision := decide(input)
		if decision.Action == webSocketPreWriteActionSuppress {
			// Recovery needs the existing classifier's full evidence before closing the
			// socket, including when no optional semantic observer is configured.
			if parsed := inspectWebSocketUpstreamError(input.MessageType, input.Data, input.Observation.ParseDegraded); parsed != nil {
				decision.SuppressedUpstreamError = parsed
			}
		}
		return decision
	}
}

func (o *WebSocketSessionOrchestrator) mergeRecoveryObservation(result *WebSocketResult, observation WebSocketObservation) {
	canonical := result.UpstreamError
	mergeWebSocketObservation(result, observation)
	if o.codexOperation != nil && o.codexOperation.AllowsAccountSwitch() && result.UpstreamError == nil {
		result.UpstreamError = canonical
	}
}
