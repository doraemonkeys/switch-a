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

// bootstrapSelectionContext is isolated from the main attempt loop because only
// model-sensitive routing is allowed to read the client socket before a provider
// has been chosen.
func (o *WebSocketSessionOrchestrator) bootstrapSelectionContext(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) *WebSocketSessionResult {
	if !o.shouldProbeClientSelectionContext(ctx) {
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
		}
	}

	result, observedModel := o.probeClientSelectionContext(ctx)
	if observedModel != "" {
		o.info.Model = observedModel
		o.selectReq.Model = observedModel
	}
	return result
}

func (o *WebSocketSessionOrchestrator) shouldProbeClientSelectionContext(ctx context.Context) bool {
	if o == nil || o.selectReq == nil || o.handler == nil {
		return false
	}
	if o.apiType != APITypeCodex {
		return false
	}
	if selector.BuildContinuityKey(o.selectReq).Model != "" {
		return false
	}
	return o.handler.webSocketSelectionDependsOnModel(ctx, o.selectReq, o.apiType)
}

func (o *WebSocketSessionOrchestrator) probeClientSelectionContext(ctx context.Context) (*WebSocketSessionResult, string) {
	if o.clientConn == nil {
		return nil, ""
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
			}, ""
		}

		if o.replayBuffer != nil {
			o.replayBuffer.Record(messageType, data, false)
		}

		observer := newWebSocketMessageObserver(o.apiType, o.info.Model, nil, nil, nil)
		if observer == nil {
			return nil, ""
		}

		observer.ObserveClientMessage(messageType, data)
		observation := observer.Snapshot()
		if observation.Model == "" || observation.Model == ModelUnknown {
			return nil, ""
		}
		return nil, observation.Model
	case <-timer.C:
		return nil, ""
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
		}, ""
	}
}

func (o *WebSocketSessionOrchestrator) selectProvider(ctx context.Context, attempt int) (*model.Provider, bool, *WebSocketSessionResult) {
	o.selectReq.FailoverContext = o.failoverContext
	o.selectReq.MaxProviderSwitches = o.maxAttempts

	provider, fromSticky, err := o.handler.selectProviderWithTracking(ctx, o.selectReq, attempt, o.excludedProviders)
	if err == nil {
		if o.failoverContext == nil {
			o.failoverContext = model.NewFailoverContext(provider)
		} else {
			o.failoverContext.Update(provider)
		}
		return provider, fromSticky, nil
	}

	if errors.Is(err, internal.ErrNoProvider) {
		if len(o.attempts) > 0 {
			return nil, false, o.finalSessionFromLastAttempt(ctx)
		}
		o.handler.logger.Warn("no providers available for websocket", zap.String("api_type", o.apiType))
		return nil, false, newWebSocketSelectionFailureSession(
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
	return nil, false, newWebSocketSelectionFailureSession(
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
