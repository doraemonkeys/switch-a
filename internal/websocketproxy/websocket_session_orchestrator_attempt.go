package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// executeProviderAttempt owns provider preparation and dial recovery so the
// pre-visible commitment boundary is resolved before relay takes ownership.
func (o *WebSocketSessionOrchestrator) executeProviderAttempt(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	provider *model.Provider,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
) WebSocketAttemptResult {
	attemptStart := time.Now()

	upstreamURL, dialHeaders, failureCode, err := o.prepareProviderAttempt(ctx, r, provider)
	if err != nil {
		attemptResult := newWebSocketProviderConfigurationAttempt(provider, o.apiType, attempt, selectionMode, selectionMetadata, err, time.Since(attemptStart))
		o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
		o.applySessionLifecycleToAttempt(&attemptResult)
		if o.captureParticipates {
			reason, failure := webSocketPreparation(contextError(ctx), err, failureCode)
			o.capture.Transition(requestcapture.TransitionStart{
				Attempt: webSocketCaptureAttemptMetadata(
					provider,
					o.apiType,
					selectionMode,
					selectionMetadata,
					requestcapture.CredentialPhaseInitial,
				),
				Target:             requestcapture.WebSocketTransitionTarget(upstreamURL),
				TerminationReason:  reason,
				Failure:            failure,
				CredentialEvidence: emptyCaptureCredentialEvidence(),
			})
		}
		return attemptResult
	}

	recoveryAttempted := false
	dialExchange := o.handler.wsForwarder.dialUpstream(ctx, WebSocketDialRequest{
		URL:                 upstreamURL,
		Headers:             dialHeaders,
		InjectedAPIKey:      injectedAPIKeyForCapture(provider, o.apiType),
		Capture:             o.capture,
		CaptureParticipates: o.captureParticipates,
		Attempt: webSocketCaptureAttemptMetadata(
			provider,
			o.apiType,
			selectionMode,
			selectionMetadata,
			requestcapture.CredentialPhaseInitial,
		),
	})
	o.handler.scheduleProviderUsageObservation(
		o.requestID, provider, dialExchange.HandshakeHeaders, dialExchange.HandshakeObservedAt,
	)
	if !dialExchange.Accepted() {
		resolution := o.resolveRejectedProviderDial(
			ctx,
			r,
			provider,
			dialExchange,
			upstreamURL,
			attempt,
			selectionMode,
			selectionMetadata,
			attemptStart,
		)
		if !resolution.accepted {
			return resolution.terminalAttempt
		}
		dialExchange = resolution.exchange
		recoveryAttempted = resolution.recoveryAttempted
	}
	return o.relayAcceptedProviderAttempt(
		ctx,
		w,
		r,
		provider,
		dialExchange,
		attempt,
		selectionMode,
		selectionMetadata,
		attemptStart,
		recoveryAttempted,
	)
}

type rejectedWebSocketDialResolution struct {
	exchange          DialExchange
	terminalAttempt   WebSocketAttemptResult
	recoveryAttempted bool
	accepted          bool
}

func (o *WebSocketSessionOrchestrator) resolveRejectedProviderDial(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
	dialExchange DialExchange,
	upstreamURL string,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
) rejectedWebSocketDialResolution {
	dialResult := dialExchange.toWebSocketResult()
	attemptResult := newWebSocketForwardAttemptResult(
		provider,
		attempt,
		selectionMode,
		selectionMetadata,
		dialResult,
		nil,
		time.Since(attemptStart),
	)
	o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
	o.applySessionLifecycleToAttempt(&attemptResult)
	if !o.shouldRecoverUnauthorized(attemptResult) {
		o.queueRejectedDialCapture(ctx, dialExchange, attemptResult)
		return rejectedWebSocketDialResolution{terminalAttempt: attemptResult}
	}

	o.queueCredentialRefreshDrainCapture(dialExchange)
	recoveredExchange, recoveredAttempt, recovered := o.recoverUnauthorizedSameProvider(
		ctx,
		r,
		provider,
		upstreamURL,
		attempt,
		selectionMode,
		selectionMetadata,
		attemptStart,
	)
	if !recovered {
		return rejectedWebSocketDialResolution{terminalAttempt: attemptResult}
	}
	if recoveredExchange.Accepted() {
		return rejectedWebSocketDialResolution{
			exchange:          recoveredExchange,
			recoveryAttempted: true,
			accepted:          true,
		}
	}

	recoveredAttempt.RecoveryAttempted = true
	o.queueRejectedDialCapture(ctx, recoveredExchange, recoveredAttempt)
	return rejectedWebSocketDialResolution{terminalAttempt: recoveredAttempt}
}

func (o *WebSocketSessionOrchestrator) queueRejectedDialCapture(
	ctx context.Context,
	dialExchange DialExchange,
	attempt WebSocketAttemptResult,
) {
	if !dialExchange.captureMode.Participates() {
		return
	}
	o.queueCaptureCompletion(
		dialExchange,
		webSocketDialFailureCaptureOutcome(
			ctx,
			dialExchange,
			webSocketDialFailureReason(dialExchange, o.shouldSwitchProvider(attempt)),
		),
	)
}

func (o *WebSocketSessionOrchestrator) queueCredentialRefreshDrainCapture(dialExchange DialExchange) {
	if !dialExchange.captureMode.Participates() {
		return
	}
	o.queueCaptureCompletion(dialExchange, requestcapture.Outcome{
		SourceCompletion:  webSocketDialFailureSourceCompletion(dialExchange),
		TerminationReason: requestcapture.TerminationReasonCredentialRefreshDrain,
		Failure:           webSocketDialFailureObservation(dialExchange),
	})
}

func (o *WebSocketSessionOrchestrator) prepareProviderAttempt(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
) (string, http.Header, requestcapture.FailureCode, error) {
	baseURL, err := o.handler.validateWebSocketProviderReady(provider, o.apiType)
	if err != nil {
		failureCode := webSocketConfigurationFailureCode(err)
		if !o.captureParticipates {
			return "", nil, failureCode, err
		}
		// Readiness checks can reject credentials even when the transport target is
		// already determined. Retaining that known target makes a no-dial transition
		// diagnostically useful without pretending a physical exchange occurred.
		knownBaseURL := provider.BaseURLForAPIType(o.apiType)
		if knownBaseURL == "" {
			return "", nil, failureCode, err
		}
		upstreamPath := BuildUpstreamPath(r.URL.Path, o.apiType)
		return httpToWSURL(o.handler.buildFullURL(knownBaseURL, upstreamPath, r.URL.RawQuery)), nil, failureCode, err
	}

	upstreamPath := BuildUpstreamPath(r.URL.Path, o.apiType)
	upstreamURL := httpToWSURL(o.handler.buildFullURL(baseURL, upstreamPath, r.URL.RawQuery))

	dialHeaders, err := o.handler.prepareWebSocketAttemptHeaders(ctx, r, provider, o.apiType, o.globalAuthMode)
	if err != nil {
		return upstreamURL, dialHeaders, requestcapture.FailureCodeCredentialApply, &webSocketProviderConfigError{
			missingField: "credentials",
			err:          err,
		}
	}

	return upstreamURL, dialHeaders, "", nil
}

func webSocketConfigurationFailureCode(err error) requestcapture.FailureCode {
	var configError *webSocketProviderConfigError
	if !errors.As(err, &configError) || configError == nil {
		return requestcapture.FailureCodeUnknown
	}
	switch configError.missingField {
	case "base_url":
		return requestcapture.FailureCodeMissingBaseURL
	case "api_key":
		return requestcapture.FailureCodeMissingAPIKey
	case "credentials":
		return requestcapture.FailureCodeMissingCredentials
	default:
		return requestcapture.FailureCodeUnknown
	}
}

func webSocketCaptureAttemptMetadata(
	provider *model.Provider,
	apiType string,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	credentialPhase requestcapture.CredentialPhase,
) requestcapture.AttemptMetadata {
	if provider == nil {
		return requestcapture.AttemptMetadata{}
	}
	return requestcapture.AttemptMetadata{
		Provider: requestcapture.ProviderIdentity{
			ID:   provider.ID,
			Name: provider.Name,
		},
		APIType:              apiType,
		SelectionMode:        requestcapture.SelectionMode(selectionMode),
		SelectionSource:      requestcapture.SelectionSource(selectionMetadata.Source),
		ProviderAttemptIndex: webSocketCaptureProviderAttemptIndex,
		CredentialPhase:      credentialPhase,
	}
}

const webSocketCaptureProviderAttemptIndex = 0

func (o *WebSocketSessionOrchestrator) shouldRecoverUnauthorized(attempt WebSocketAttemptResult) bool {
	return o.handler.auth != nil &&
		attempt.ForwardErr == nil &&
		attempt.Result != nil &&
		!attempt.Result.HandshakeAccepted &&
		attempt.Result.HandshakeStatusCode == http.StatusUnauthorized
}

func (o *WebSocketSessionOrchestrator) recoverUnauthorizedSameProvider(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
	upstreamURL string,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
) (DialExchange, WebSocketAttemptResult, bool) {
	refreshed, err := o.handler.auth.RefreshProviderCredentials(ctx, provider)
	if !refreshed {
		return DialExchange{}, WebSocketAttemptResult{}, false
	}
	if err != nil {
		o.handler.logger.Warn(
			"websocket provider credential refresh failed",
			zap.String("provider_id", provider.ID),
			zap.Error(err),
		)
		return DialExchange{}, WebSocketAttemptResult{}, false
	}

	dialHeaders, err := o.handler.prepareWebSocketAttemptHeaders(ctx, r, provider, o.apiType, o.globalAuthMode)
	if err != nil {
		configErr := &webSocketProviderConfigError{
			missingField: "credentials",
			err:          err,
		}
		configAttempt := newWebSocketProviderConfigurationAttempt(
			provider,
			o.apiType,
			attempt,
			selectionMode,
			selectionMetadata,
			configErr,
			time.Since(attemptStart),
		)
		o.stampAttemptSelectionContext(&configAttempt, selectionMode, selectionMetadata)
		configAttempt.RecoveryAttempted = true
		o.applySessionLifecycleToAttempt(&configAttempt)
		if o.captureParticipates {
			reason, failure := webSocketPreparation(
				contextError(ctx),
				configErr,
				requestcapture.FailureCodeCredentialApply,
			)
			o.capture.Transition(requestcapture.TransitionStart{
				Attempt: webSocketCaptureAttemptMetadata(
					provider,
					o.apiType,
					selectionMode,
					selectionMetadata,
					requestcapture.CredentialPhaseRefreshed,
				),
				Target:             requestcapture.WebSocketTransitionTarget(upstreamURL),
				TerminationReason:  reason,
				Failure:            failure,
				CredentialEvidence: emptyCaptureCredentialEvidence(),
			})
		}
		return DialExchange{}, configAttempt, true
	}

	dialExchange := o.handler.wsForwarder.dialUpstream(ctx, WebSocketDialRequest{
		URL:                 upstreamURL,
		Headers:             dialHeaders,
		InjectedAPIKey:      injectedAPIKeyForCapture(provider, o.apiType),
		Capture:             o.capture,
		CaptureParticipates: o.captureParticipates,
		Attempt: webSocketCaptureAttemptMetadata(
			provider,
			o.apiType,
			selectionMode,
			selectionMetadata,
			requestcapture.CredentialPhaseRefreshed,
		),
	})
	o.handler.scheduleProviderUsageObservation(
		o.requestID, provider, dialExchange.HandshakeHeaders, dialExchange.HandshakeObservedAt,
	)
	if dialExchange.Accepted() {
		return dialExchange, WebSocketAttemptResult{RecoveryAttempted: true}, true
	}

	dialResult := dialExchange.toWebSocketResult()
	recoveredAttempt := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, dialResult, nil, time.Since(attemptStart))
	o.stampAttemptSelectionContext(&recoveredAttempt, selectionMode, selectionMetadata)
	recoveredAttempt.RecoveryAttempted = true
	o.applySessionLifecycleToAttempt(&recoveredAttempt)
	return dialExchange, recoveredAttempt, true
}

func (o *WebSocketSessionOrchestrator) stampAttemptSelectionContext(
	attempt *WebSocketAttemptResult,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
) {
	if attempt == nil {
		return
	}
	attempt.SelectionMode = selectionMode
	attempt.SelectionMetadata = selectionMetadata
	attempt.ProviderAttempt = 1
	attempt.ProviderSwitchCount = o.switchTracker.providerSwitchCount()
}

func (o *WebSocketSessionOrchestrator) replayBufferedMessages(
	ctx context.Context,
	upstreamConn *websocket.Conn,
	observer WebSocketMessageObserver,
	captureOptions webSocketRelayOptions,
) (int64, bool, error) {
	captureOptions = captureOptions.withCaptureHooks()
	if o.replayBuffer == nil {
		return 0, false, nil
	}

	snapshot := o.replayBuffer.Snapshot()
	if !snapshot.Enabled {
		if o.lifecycle != nil && o.lifecycle.Snapshot().ClientVisible {
			// Once the session is already visible, a disabled pre-visible replay buffer
			// is expected rather than fatal. Post-visible failover reuses the live
			// downstream socket without trying to resurrect the pre-visible window.
			return 0, false, nil
		}
		return 0, false, errors.New("pre-visible replay buffer disabled")
	}
	if len(snapshot.Messages) == 0 {
		// Suppression can happen before the client sends any replayable frame. In that
		// case the replacement provider should continue with a clean socket rather than
		// treating "nothing to replay" as a synthetic transport failure.
		return 0, false, nil
	}

	var replayedBytes int64
	for index, message := range snapshot.Messages {
		source := requestcapture.MessageSourceReplay
		lineage := requestcapture.MessageLineage{}
		sourceLineage := message.Lineage
		if !message.Delivered {
			// The bootstrap selector read this frame before a provider was chosen. Its
			// first physical delivery is still the live event; only later attempts are
			// replay events linked back to that stable original message identity.
			source = requestcapture.MessageSourceLive
			lineage = message.Lineage
			sourceLineage = requestcapture.MessageLineage{}
		}
		captured := captureWebSocketMessageRead(
			captureOptions,
			requestcapture.MessageDirectionClientToUpstream,
			message.MessageType,
			message.Data,
			source,
			lineage,
			sourceLineage,
		)
		o.observeReplayClientMessage(observer, message.MessageType, message.Data)
		if err := upstreamConn.Write(ctx, message.MessageType, message.Data); err != nil {
			captureWebSocketMessageResult(
				captureOptions,
				captured,
				requestcapture.MessageDispositionWriteFailed,
				false,
				err,
			)
			return replayedBytes, true, err
		}
		if !message.Delivered {
			o.replayBuffer.MarkDelivered(index, captured.Lineage)
		}
		captureWebSocketMessageResult(
			captureOptions,
			captured,
			requestcapture.MessageDispositionForwarded,
			true,
			nil,
		)
		replayedBytes += int64(len(message.Data))
	}
	return replayedBytes, true, nil
}

func (o *WebSocketSessionOrchestrator) observeReplayClientMessage(
	observer WebSocketMessageObserver,
	messageType websocket.MessageType,
	data []byte,
) {
	switch tracked := observer.(type) {
	case *bytesTrackingObserver:
		if tracked.inner != nil {
			tracked.inner.ObserveClientMessage(messageType, data)
		}
	default:
		if observer != nil {
			observer.ObserveClientMessage(messageType, data)
		}
	}
}

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
		return o.shouldFailoverAfterClientVisible(attempt)
	}
	if attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError {
		return o.suppressedAttempt != nil &&
			attempt.Result.UpstreamError != nil &&
			attempt.Result.UpstreamError.IsSwitchableProviderScoped()
	}
	return false
}

func (o *WebSocketSessionOrchestrator) shouldFailoverAfterClientVisible(attempt WebSocketAttemptResult) bool {
	if o == nil || o.suppressedAttempt == nil || attempt.Result == nil {
		return false
	}
	if attempt.Result.UpstreamError == nil {
		return false
	}
	if !attempt.Result.UpstreamError.IsSwitchableProviderScoped() {
		return false
	}
	return o.switchTracker.continuityContext != nil
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
