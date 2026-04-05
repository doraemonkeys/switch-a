package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
	"switch-a/internal/selector"

	"go.uber.org/zap"
)

type webSocketSelectionProbeDecision struct {
	outcome     webSocketSelectionProbeOutcome
	shouldProbe bool
}

const webSocketProbeDemandResolutionFailureMessage = "Failed to resolve hidden-model demand before provider selection"

// bootstrapSelectionContext is isolated from the main attempt loop because only
// pre-selection model consumers should read the client socket before a provider
// has been chosen.
func (o *WebSocketSessionOrchestrator) bootstrapSelectionContext(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) *WebSocketSessionResult {
	decision, err := o.selectionProbeDecision(ctx)
	o.probeOutcome = decision.outcome
	if err != nil {
		if o.handler != nil && o.handler.logger != nil {
			o.handler.logger.Error(
				"failed to resolve websocket hidden-model demand",
				zap.String("api_type", o.apiType),
				zap.Error(err),
			)
		}
		return newWebSocketProbeDecisionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			decision.outcome,
			err,
		)
	}
	if !decision.shouldProbe {
		return nil
	}

	if err := o.ensureClientAccepted(w, r); err != nil {
		result := &WebSocketResult{
			Err:           err,
			TerminalCause: model.TerminalClientUpgradeRejected,
			CommitSource:  model.CommitUnknown,
		}
		return &WebSocketSessionResult{
			RequestID:      o.requestID,
			FinalResult:    result,
			FinalErr:       err,
			Attempts:       append([]WebSocketAttemptResult(nil), o.attempts...),
			IsSticky:       o.isSticky,
			ClientAccepted: false,
			ProbeOutcome:   o.probeOutcome,
		}
	}

	result, observedModel, outcome := o.probeClientSelectionContext(ctx)
	o.probeOutcome = outcome
	o.learnResolvedModel(observedModel)
	return result
}

func (o *WebSocketSessionOrchestrator) selectionProbeDecision(
	ctx context.Context,
) (webSocketSelectionProbeDecision, error) {
	if o == nil || o.selectReq == nil || o.handler == nil {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if hasUsableWebSocketSelectionModel(o.selectReq.Model) {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if !o.probeClientModel {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	consumesHiddenModel, err := o.handler.webSocketSelectionConsumesHiddenModel(ctx, o.selectReq)
	if err != nil {
		// Demand resolution failures stay explicit. Treating them like "no probe
		// needed" would silently collapse pre-selection back to handshake-only
		// semantics even though a model-sensitive continuity consumer was unresolved.
		return webSocketSelectionProbeDecision{
			outcome: webSocketSelectionProbeOutcomeDemandResolutionFailed,
		}, fmt.Errorf("resolve websocket selection hidden-model demand: %w", err)
	}
	if !consumesHiddenModel {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if !o.supportsReplaySafeSelectionProbe() {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeUnsupported}, nil
	}
	return webSocketSelectionProbeDecision{
		outcome:     webSocketSelectionProbeOutcomeCompletedWithoutUsableModel,
		shouldProbe: true,
	}, nil
}

func (o *WebSocketSessionOrchestrator) supportsReplaySafeSelectionProbe() bool {
	if o == nil || o.replayBuffer == nil {
		return false
	}
	return o.selectionProbeObserver(ModelUnknown) != nil
}

func (o *WebSocketSessionOrchestrator) selectionProbeObserver(initialModel string) WebSocketMessageObserver {
	if o == nil {
		return nil
	}
	if o.newSelectionProbeObserver != nil {
		return o.newSelectionProbeObserver(o.apiType, initialModel)
	}
	return newWebSocketMessageObserver(o.apiType, initialModel, nil, nil, nil)
}

func (o *WebSocketSessionOrchestrator) probeClientSelectionContext(ctx context.Context) (*WebSocketSessionResult, string, webSocketSelectionProbeOutcome) {
	if o.clientConn == nil {
		return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
	}
	if o.initialClientReadCh == nil {
		o.initialClientReadCh = startWebSocketInitialRead(ctx, o.clientConn)
	}

	timer := time.NewTimer(webSocketPreVisibleClientReadWindow)
	defer timer.Stop()

	select {
	case initialClientRead := <-o.initialClientReadCh:
		o.initialClientReadCh = nil
		messageType, data, err := initialClientRead.messageType, initialClientRead.data, initialClientRead.err
		if err != nil {
			result := &WebSocketResult{
				HandshakeAccepted: true,
				Err:               err,
				TerminalCause:     classifyRelayTerminalCause(err, webSocketPeerClient),
				CommitSource:      model.CommitUnknown,
			}
			o.applySessionLifecycleToResult(result)
			return &WebSocketSessionResult{
				RequestID:      o.requestID,
				FinalResult:    result,
				FinalErr:       err,
				Attempts:       append([]WebSocketAttemptResult(nil), o.attempts...),
				IsSticky:       o.isSticky,
				ClientAccepted: result.ClientAccepted,
				ProbeOutcome:   webSocketSelectionProbeOutcomeTransportFailed,
			}, "", webSocketSelectionProbeOutcomeTransportFailed
		}

		if o.replayBuffer != nil {
			o.replayBuffer.Record(messageType, data, false)
		}

		observer := o.selectionProbeObserver(o.info.Model)
		if observer == nil {
			return nil, "", webSocketSelectionProbeOutcomeUnsupported
		}

		observer.ObserveClientMessage(messageType, data)
		observation := observer.Snapshot()
		if observation.Model == "" || observation.Model == ModelUnknown {
			return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
		}
		return nil, observation.Model, webSocketSelectionProbeOutcomeObservedUsableModel
	case <-timer.C:
		return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
	case <-ctx.Done():
		result := &WebSocketResult{
			HandshakeAccepted: true,
			Err:               ctx.Err(),
			TerminalCause:     model.TerminalInternalError,
			CommitSource:      model.CommitUnknown,
		}
		o.applySessionLifecycleToResult(result)
		return &WebSocketSessionResult{
			RequestID:      o.requestID,
			FinalResult:    result,
			FinalErr:       ctx.Err(),
			Attempts:       append([]WebSocketAttemptResult(nil), o.attempts...),
			IsSticky:       o.isSticky,
			ClientAccepted: result.ClientAccepted,
			ProbeOutcome:   webSocketSelectionProbeOutcomeTransportFailed,
		}, "", webSocketSelectionProbeOutcomeTransportFailed
	}
}

func (o *WebSocketSessionOrchestrator) selectProvider(
	ctx context.Context,
	attempt int,
) (*model.Provider, providerSwitchMode, selector.SelectionMetadata, *WebSocketSessionResult) {
	selectionMode := o.switchTracker.prepareSelection()

	provider, selectionMetadata, err := o.handler.selectProviderWithTracking(ctx, o.selectReq, attempt, o.excludedProviders)
	if err == nil {
		o.switchTracker.recordSelection(provider, selectionMetadata)
		return provider, selectionMode, selectionMetadata, nil
	}

	if errors.Is(err, internal.ErrNoProvider) {
		if len(o.attempts) > 0 {
			return nil, providerSwitchModeInitial, selector.SelectionMetadata{}, o.finalSessionFromLastAttempt(ctx)
		}
		o.handler.logger.Warn("no providers available for websocket", zap.String("api_type", o.apiType))
		return nil, providerSwitchModeInitial, selector.SelectionMetadata{}, newWebSocketSelectionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			http.StatusServiceUnavailable,
			model.TerminalProviderUnavailable,
			ErrCodeProviderUnavailable,
			fmt.Sprintf("No available provider for api_type: %s", o.apiType),
			err,
		)
	}

	o.handler.logger.Error("provider selection failed for websocket", zap.Error(err))
	return nil, providerSwitchModeInitial, selector.SelectionMetadata{}, newWebSocketSelectionFailureSession(
		o.requestID,
		o.isSticky,
		o.attempts,
		http.StatusInternalServerError,
		model.TerminalInternalError,
		ErrCodeInternalError,
		"Provider selection failed",
		err,
	)
}
