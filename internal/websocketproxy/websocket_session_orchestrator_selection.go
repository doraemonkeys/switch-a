package websocketproxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// Suppressed provider errors are selection evidence, not relay state. Keeping
// their replacement/fallback policy beside provider selection makes the
// post-visible no-handoff rule a single decision boundary.
func (o *WebSocketSessionOrchestrator) captureSuppressedAttempt(
	provider *model.Provider,
	relayResult *webSocketRelaySessionResult,
) {
	if relayResult == nil || relayResult.SuppressedUpstreamError == nil || provider == nil {
		return
	}

	messageType := relayResult.SuppressedMessageType
	if messageType == 0 {
		// Semantic failover only suppresses JSON application frames, so a missing
		// type means we are reconstructing legacy helper output rather than a real
		// binary payload.
		messageType = websocket.MessageText
	}

	payload := append([]byte(nil), relayResult.SuppressedMessageData...)
	if len(payload) == 0 && relayResult.SuppressedUpstreamError.Raw != "" {
		payload = []byte(relayResult.SuppressedUpstreamError.Raw)
	}

	o.suppressedAttempt = &webSocketSuppressedAttempt{
		provider:      provider,
		messageType:   messageType,
		payload:       payload,
		upstreamError: relayResult.SuppressedUpstreamError.Clone(),
	}
}

func (o *WebSocketSessionOrchestrator) clearSuppressedAttempt() { o.suppressedAttempt = nil }

func (o *WebSocketSessionOrchestrator) shouldSwitchProvider(attempt WebSocketAttemptResult) bool {
	if attempt.Result == nil {
		return false
	}
	if attempt.ReplayFailed {
		return false
	}
	if attempt.shouldReplaceBeforeClientVisible() {
		return true
	}
	if attempt.Result.ClientVisible {
		return false
	}
	if attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError {
		return o.suppressedAttempt != nil &&
			attempt.Result.UpstreamError != nil &&
			attempt.Result.UpstreamError.IsSwitchableProviderScoped()
	}
	return false
}

func (o *WebSocketSessionOrchestrator) shouldFallbackToSuppressedPayload(attempt WebSocketAttemptResult) bool {
	if o.suppressedAttempt == nil || attempt.Result == nil || attempt.Result.ClientVisible {
		return false
	}
	if attempt.Result.TerminalCause == model.TerminalClientDisconnect ||
		attempt.Result.TerminalCause == model.TerminalInternalError {
		return false
	}
	if attempt.ReplayFailed {
		return true
	}
	return attempt.clientAccepted() && !o.shouldSwitchProvider(attempt)
}

type fallbackProviderLease struct {
	provider   *model.Provider
	generation uint64
	held       atomic.Bool
	candidate  codexidentity.CandidateSnapshot
	resolved   bool
}

func (h *Gateway) newFallbackProviderLease(provider *model.Provider, apiType string) ProviderLease {
	lease := &fallbackProviderLease{
		provider:   provider,
		generation: h.fallbackLeaseGeneration.Add(1),
	}
	if provider != nil {
		credential, ok := provider.CredentialSessionForAPIType(apiType)
		finalURL, parseErr := url.Parse(provider.BaseURLForAPIType(apiType))
		if ok && credential != nil && parseErr == nil {
			lease.candidate, parseErr = codexidentity.NewAuthorityResolver().Resolve(
				credentialsession.RouteSnapshot{
					RouteTargetID: provider.ID,
					APIType:       apiType,
					Credential:    *credential,
				},
				apiType,
				finalURL,
			)
			lease.resolved = parseErr == nil
		}
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

func (l *fallbackProviderLease) CandidateSnapshot() (codexidentity.CandidateSnapshot, bool) {
	if l == nil || !l.resolved {
		return codexidentity.CandidateSnapshot{}, false
	}
	return l.candidate, true
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
	ownerDemand bool
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

	o.subprotocol = o.subprotocol.FixForProbe()
	o.logSubprotocolDecision("websocket.subprotocol_probe_fixed", o.subprotocol.Selected(), "")
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

	result, observedModel, outcome := o.probeClientSelectionContext(ctx, decision.ownerDemand)
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
	ownerDemand := o.codexOperation != nil && o.codexOperation.NeedsOwnerBootstrap()
	if hasUsableWebSocketSelectionModel(o.selectReq.Model) && !ownerDemand {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if !o.probeClientModel && !ownerDemand {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	consumesHiddenModel := false
	var err error
	if !hasUsableWebSocketSelectionModel(o.selectReq.Model) && o.probeClientModel {
		consumesHiddenModel, err = o.handler.webSocketSelectionConsumesHiddenModel(ctx, o.selectReq)
	}
	if err != nil {
		// Demand resolution failures stay explicit. Treating them like "no probe
		// needed" would silently collapse pre-selection back to handshake-only
		// semantics even though a model-sensitive continuity consumer was unresolved.
		return webSocketSelectionProbeDecision{
			outcome: webSocketSelectionProbeOutcomeDemandResolutionFailed,
		}, fmt.Errorf("resolve websocket selection hidden-model demand: %w", err)
	}
	if !consumesHiddenModel && !ownerDemand {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if !o.supportsReplaySafeSelectionProbe() {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeUnsupported}, nil
	}
	return webSocketSelectionProbeDecision{
		outcome:     webSocketSelectionProbeOutcomeCompletedWithoutUsableModel,
		shouldProbe: true,
		ownerDemand: ownerDemand,
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

func (o *WebSocketSessionOrchestrator) probeClientSelectionContext(
	ctx context.Context,
	ownerDemands ...bool,
) (*WebSocketSessionResult, string, webSocketSelectionProbeOutcome) {
	ownerDemand := len(ownerDemands) > 0 && ownerDemands[0]
	if o.clientConn == nil {
		return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
	}
	clientConn := o.clientConn
	if o.initialClientReadCh == nil {
		clientConn.SetReadLimit(int64(preVisibleClientReplayBufferLimitBytes))
		defer clientConn.SetReadLimit(wsReadLimit)
		o.initialClientReadCh = startWebSocketInitialRead(ctx, clientConn)
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
		if len(data) > preVisibleClientReplayBufferLimitBytes {
			err := errors.New("websocket bootstrap frame exceeds replay limit")
			_ = o.clientConn.Close(websocket.StatusMessageTooBig, "bootstrap frame too large")
			return o.bootstrapProbeFailure(err, model.TerminalClientDisconnect), "", webSocketSelectionProbeOutcomeTransportFailed
		}
		if ownerDemand && o.codexOperation != nil {
			if err := o.codexOperation.InspectBootstrapFrame(ctx, messageType == websocket.MessageText, data); err != nil {
				_ = o.clientConn.Close(websocketCloseStatusForCodexFailure(err), "websocket bootstrap rejected")
				return o.bootstrapProbeFailure(err, model.TerminalClientDisconnect), "", webSocketSelectionProbeOutcomeTransportFailed
			}
			applyCodexWebSocketRouteConstraint(o.selectReq, o.codexOperation)
		}

		if o.replayBuffer != nil {
			var lineage requestcapture.MessageLineage
			if o.captureParticipates {
				lineage = o.capture.NewMessageID()
			}
			index := o.replayBuffer.RecordWithLineage(
				messageType,
				data,
				false,
				lineage,
			)
			if index == invalidWebSocketReplayMessageIndex {
				err := errors.New("websocket bootstrap frame is not replayable")
				_ = o.clientConn.Close(websocket.StatusMessageTooBig, "bootstrap frame is not replayable")
				return o.bootstrapProbeFailure(err, model.TerminalClientDisconnect), "", webSocketSelectionProbeOutcomeTransportFailed
			}
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
		if ownerDemand {
			err := errors.New("websocket owner bootstrap timed out")
			_ = o.clientConn.Close(websocket.StatusPolicyViolation, "owner bootstrap timed out")
			return o.bootstrapProbeFailure(err, model.TerminalClientDisconnect), "", webSocketSelectionProbeOutcomeTransportFailed
		}
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

func (o *WebSocketSessionOrchestrator) bootstrapProbeFailure(
	err error,
	cause model.TerminalCause,
) *WebSocketSessionResult {
	result := &WebSocketResult{
		HandshakeAccepted: true,
		Err:               err,
		TerminalCause:     cause,
		CommitSource:      model.CommitUnknown,
	}
	o.applySessionLifecycleToResult(result)
	return &WebSocketSessionResult{
		RequestID: o.requestID, FinalResult: result, FinalErr: err,
		Attempts: append([]WebSocketAttemptResult(nil), o.attempts...),
		IsSticky: o.isSticky, ClientAccepted: result.ClientAccepted,
		ProbeOutcome: webSocketSelectionProbeOutcomeTransportFailed,
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
