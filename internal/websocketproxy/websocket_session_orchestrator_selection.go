package websocketproxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
)

type fallbackProviderLease struct {
	provider   *model.Provider
	generation uint64
	held       atomic.Bool
}

func (h *Gateway) newFallbackProviderLease(provider *model.Provider) ProviderLease {
	lease := &fallbackProviderLease{
		provider:   provider,
		generation: h.fallbackLeaseGeneration.Add(1),
	}
	lease.held.Store(true)
	return lease
}

func (l *fallbackProviderLease) Provider() *model.Provider {
	if l == nil {
		return nil
	}
	return l.provider
}

func (l *fallbackProviderLease) ProviderID() string {
	if l == nil || l.provider == nil {
		return ""
	}
	return l.provider.ID
}

func (l *fallbackProviderLease) Generation() uint64 {
	if l == nil {
		return 0
	}
	return l.generation
}

func (l *fallbackProviderLease) CapabilityIdentity() uintptr {
	if l == nil {
		return 0
	}
	return reflect.ValueOf(l).Pointer()
}

func (l *fallbackProviderLease) Held() bool {
	return l != nil && l.held.Load()
}

func (l *fallbackProviderLease) Release() bool {
	return l != nil && l.held.CompareAndSwap(true, false)
}

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
			var lineage requestcapture.MessageLineage
			if o.captureParticipates {
				lineage = o.capture.NewMessageID()
			}
			o.replayBuffer.RecordWithLineage(
				messageType,
				data,
				false,
				lineage,
			)
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
) (ProviderSelection, providerSwitchMode, *WebSocketSessionResult) {
	selectionMode := o.switchTracker.prepareSelection()

	selection, err := o.handler.selectProviderWithTracking(ctx, o.selectReq, attempt, o.excludedProviders)
	if err == nil {
		o.switchTracker.recordSelection(selection.Provider(), selection.Metadata)
		return selection, selectionMode, nil
	}

	if errors.Is(err, internal.ErrNoProvider) {
		if len(o.attempts) > 0 {
			return ProviderSelection{}, providerSwitchModeInitial, o.finalSessionFromLastAttempt(ctx)
		}
		o.handler.logger.Warn("no providers available for websocket", zap.String("api_type", o.apiType))
		return ProviderSelection{}, providerSwitchModeInitial, newWebSocketSelectionFailureSession(
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
	return ProviderSelection{}, providerSwitchModeInitial, newWebSocketSelectionFailureSession(
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
