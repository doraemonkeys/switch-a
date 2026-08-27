package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/websocketprotocol"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

// executeProviderAttempt owns provider preparation and dial recovery so the
// pre-visible commitment boundary is resolved before relay takes ownership.
func (o *WebSocketSessionOrchestrator) executeProviderAttempt(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	provider *model.Provider,
	lease ProviderLease,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
) WebSocketAttemptResult {
	attemptStart := time.Now()

	prepared, failureCode, err := o.prepareProviderAttempt(ctx, r, provider, lease)
	if err != nil {
		attemptResult := newWebSocketProviderConfigurationAttempt(provider, o.apiType, attempt, selectionMode, selectionMetadata, err, time.Since(attemptStart))
		attemptResult.injectedCredential = prepared.injectedCredential
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
				Target:             requestcapture.WebSocketTransitionTarget(prepared.upstreamURL),
				TerminationReason:  reason,
				Failure:            failure,
				CredentialEvidence: emptyCaptureCredentialEvidence(),
			})
		}
		return attemptResult
	}
	if err := o.prepareCodexPhysicalDial(ctx, &prepared); err != nil {
		return o.newCodexBoundaryAttempt(provider, DialExchange{}, attempt, selectionMode, selectionMetadata, attemptStart, err, prepared.injectedCredential)
	}

	recoveryAttempted := false
	injectedCredential := prepared.injectedCredential
	dialExchange := o.handler.wsForwarder.dialUpstream(ctx, WebSocketDialRequest{
		URL:                 prepared.upstreamURL,
		Headers:             prepared.headers,
		Subprotocols:        o.subprotocol.DialOffer(),
		InjectedCredential:  prepared.injectedCredential,
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
	if err := o.finishCodexPhysicalDial(ctx, prepared, dialExchange); err != nil {
		return o.newCodexBoundaryAttempt(provider, dialExchange, attempt, selectionMode, selectionMetadata, attemptStart, err, prepared.injectedCredential)
	}
	o.handler.scheduleProviderUsageObservation(
		o.requestID, provider, prepared.credential, dialExchange.HandshakeHeaders, dialExchange.HandshakeObservedAt,
	)
	if !dialExchange.Accepted() {
		if protocolErr := o.rejectedUpgradeSubprotocolError(dialExchange); protocolErr != nil {
			attemptResult := o.newSubprotocolViolationAttempt(
				provider,
				dialExchange,
				attempt,
				selectionMode,
				selectionMetadata,
				attemptStart,
				protocolErr,
				prepared.injectedCredential,
			)
			o.queueRejectedDialCapture(ctx, dialExchange, attemptResult)
			return attemptResult
		}
		resolution := o.resolveRejectedProviderDial(
			ctx,
			r,
			provider,
			dialExchange,
			prepared,
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
		injectedCredential = resolution.injectedCredential
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
		injectedCredential,
	)
}

func (o *WebSocketSessionOrchestrator) rejectedUpgradeSubprotocolError(exchange DialExchange) error {
	if exchange.HandshakeStatusCode != http.StatusSwitchingProtocols {
		return nil
	}
	_, err := o.subprotocol.BindUpstream(exchange.NegotiatedSubprotocol)
	if err != nil {
		o.logSubprotocolDecision(
			"websocket.subprotocol_mismatch",
			webSocketSubprotocolPhaseUpstreamSelection,
			websocketprotocol.PeerUpstream,
			exchange.NegotiatedSubprotocol,
			err,
		)
	}
	return err
}

func (o *WebSocketSessionOrchestrator) newSubprotocolViolationAttempt(
	provider *model.Provider,
	exchange DialExchange,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
	err error,
	injectedCredential string,
) WebSocketAttemptResult {
	if o.clientConn != nil {
		closeWebSocketSubprotocolViolation(o.clientConn)
		o.clientConn = nil
	}
	result := exchange.toWebSocketResult()
	result.Err = err
	result.TerminalCause = model.TerminalInternalError
	o.applySessionLifecycleToResult(result)
	attemptResult := newWebSocketForwardAttemptResult(
		provider,
		attempt,
		selectionMode,
		selectionMetadata,
		result,
		err,
		time.Since(attemptStart),
	)
	attemptResult.injectedCredential = injectedCredential
	o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
	return attemptResult
}

type rejectedWebSocketDialResolution struct {
	exchange           DialExchange
	terminalAttempt    WebSocketAttemptResult
	recoveryAttempted  bool
	injectedCredential string
	accepted           bool
}

func (o *WebSocketSessionOrchestrator) resolveRejectedProviderDial(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
	dialExchange DialExchange,
	prepared webSocketPreparedProviderAttempt,
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
	attemptResult.injectedCredential = prepared.injectedCredential
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
		prepared,
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
			exchange:           recoveredExchange,
			recoveryAttempted:  true,
			injectedCredential: recoveredAttempt.injectedCredential,
			accepted:           true,
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

type webSocketPreparedProviderAttempt struct {
	upstreamURL        string
	finalURL           *url.URL
	headers            http.Header
	candidate          codexidentity.CandidateSnapshot
	applied            codexidentity.AppliedIdentity
	credential         credentialsession.Snapshot
	injectedCredential string
	boundaryPermit     *codexws.Permit
}

func (o *WebSocketSessionOrchestrator) prepareProviderAttempt(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
	lease ProviderLease,
) (webSocketPreparedProviderAttempt, requestcapture.FailureCode, error) {
	var candidate codexidentity.CandidateSnapshot
	if lease != nil {
		candidate, _ = lease.CandidateSnapshot()
	}
	baseURL, credential, err := o.handler.validateWebSocketProviderReady(provider, o.apiType, candidate)
	if err != nil {
		failureCode := webSocketConfigurationFailureCode(err)
		if !o.captureParticipates {
			return webSocketPreparedProviderAttempt{}, failureCode, err
		}
		// Readiness checks can reject credentials even when the transport target is
		// already determined. Retaining that known target makes a no-dial transition
		// diagnostically useful without pretending a physical exchange occurred.
		knownBaseURL := provider.BaseURLForAPIType(o.apiType)
		if knownBaseURL == "" {
			return webSocketPreparedProviderAttempt{}, failureCode, err
		}
		upstreamPath := BuildUpstreamPath(r.URL.Path, o.apiType)
		return webSocketPreparedProviderAttempt{
			upstreamURL: httpToWSURL(o.handler.buildFullURL(knownBaseURL, upstreamPath, r.URL.RawQuery)),
		}, failureCode, err
	}

	upstreamPath := BuildUpstreamPath(r.URL.Path, o.apiType)
	upstreamURL := httpToWSURL(o.handler.buildFullURL(baseURL, upstreamPath, r.URL.RawQuery))
	finalURL, err := url.Parse(upstreamURL)
	if err != nil {
		return webSocketPreparedProviderAttempt{upstreamURL: upstreamURL}, requestcapture.FailureCodeUnknown, err
	}

	dialHeaders, applied, err := o.handler.prepareWebSocketAttemptHeaders(
		ctx, r, provider, candidate, o.apiType, o.globalAuthMode, finalURL, o.codexUpstreamHeaderHygiene(),
	)
	prepared := webSocketPreparedProviderAttempt{
		upstreamURL:        upstreamURL,
		finalURL:           finalURL,
		headers:            dialHeaders,
		candidate:          candidate,
		applied:            applied,
		credential:         credential,
		injectedCredential: injectedCredentialForCapture(credential, dialHeaders),
	}
	if err != nil {
		return prepared, requestcapture.FailureCodeCredentialApply, &webSocketProviderConfigError{
			missingField: "credentials",
			err:          err,
		}
	}

	return prepared, "", nil
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
	prepared webSocketPreparedProviderAttempt,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
) (DialExchange, WebSocketAttemptResult, bool) {
	refreshed, err := o.handler.auth.RefreshCredentialSession(ctx, prepared.credential)
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

	dialHeaders, applied, err := o.handler.prepareWebSocketAttemptHeaders(
		ctx, r, provider, prepared.candidate, o.apiType, o.globalAuthMode, prepared.finalURL, o.codexUpstreamHeaderHygiene(),
	)
	refreshedInjectedCredential := injectedCredentialForCapture(prepared.credential, dialHeaders)
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
		configAttempt.injectedCredential = refreshedInjectedCredential
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
				Target:             requestcapture.WebSocketTransitionTarget(prepared.upstreamURL),
				TerminationReason:  reason,
				Failure:            failure,
				CredentialEvidence: emptyCaptureCredentialEvidence(),
			})
		}
		return DialExchange{}, configAttempt, true
	}
	prepared.applied = applied
	prepared.headers = dialHeaders
	if err := o.prepareCodexPhysicalDial(ctx, &prepared); err != nil {
		configAttempt := newWebSocketProviderConfigurationAttempt(
			provider, o.apiType, attempt, selectionMode, selectionMetadata, err, time.Since(attemptStart),
		)
		configAttempt.injectedCredential = refreshedInjectedCredential
		o.applySessionLifecycleToAttempt(&configAttempt)
		return DialExchange{}, configAttempt, true
	}

	dialExchange := o.handler.wsForwarder.dialUpstream(ctx, WebSocketDialRequest{
		URL:                 prepared.upstreamURL,
		Headers:             dialHeaders,
		Subprotocols:        o.subprotocol.DialOffer(),
		InjectedCredential:  refreshedInjectedCredential,
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
	if err := o.finishCodexPhysicalDial(ctx, prepared, dialExchange); err != nil {
		return dialExchange, o.newCodexBoundaryAttempt(
			provider, dialExchange, attempt, selectionMode, selectionMetadata,
			attemptStart, err, refreshedInjectedCredential,
		), true
	}
	o.handler.scheduleProviderUsageObservation(
		o.requestID, provider, prepared.credential, dialExchange.HandshakeHeaders, dialExchange.HandshakeObservedAt,
	)
	if dialExchange.Accepted() {
		return dialExchange, WebSocketAttemptResult{
			RecoveryAttempted:  true,
			injectedCredential: refreshedInjectedCredential,
		}, true
	}

	dialResult := dialExchange.toWebSocketResult()
	recoveredAttempt := newWebSocketForwardAttemptResult(provider, attempt, selectionMode, selectionMetadata, dialResult, nil, time.Since(attemptStart))
	recoveredAttempt.injectedCredential = refreshedInjectedCredential
	o.stampAttemptSelectionContext(&recoveredAttempt, selectionMode, selectionMetadata)
	recoveredAttempt.RecoveryAttempted = true
	o.applySessionLifecycleToAttempt(&recoveredAttempt)
	return dialExchange, recoveredAttempt, true
}

func (o *WebSocketSessionOrchestrator) prepareCodexPhysicalDial(
	ctx context.Context,
	prepared *webSocketPreparedProviderAttempt,
) error {
	if o == nil || o.codexOperation == nil || prepared == nil {
		return nil
	}
	permit, err := o.codexOperation.PrepareDial(
		ctx, prepared.headers, prepared.candidate, prepared.applied, prepared.finalURL,
	)
	if err != nil {
		return err
	}
	prepared.boundaryPermit = permit
	applyCodexWebSocketRouteConstraint(o.selectReq, o.codexOperation)
	return nil
}

func (o *WebSocketSessionOrchestrator) codexUpstreamHeaderHygiene() bool {
	return o != nil && o.apiType == APITypeCodex && o.codexOperation != nil &&
		o.codexOperation.Features().UpstreamHeaderHygiene
}

func (o *WebSocketSessionOrchestrator) finishCodexPhysicalDial(
	ctx context.Context,
	prepared webSocketPreparedProviderAttempt,
	exchange DialExchange,
) error {
	if o == nil || o.codexOperation == nil {
		return nil
	}
	if prepared.boundaryPermit != nil {
		if err := prepared.boundaryPermit.Commit(ctx); err != nil {
			return err
		}
	}
	return o.codexOperation.ApplyHandshake(prepared.finalURL, exchange.HandshakeHeaders)
}

func (o *WebSocketSessionOrchestrator) newCodexBoundaryAttempt(
	provider *model.Provider,
	exchange DialExchange,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
	err error,
	injectedCredential string,
) WebSocketAttemptResult {
	if exchange.Conn != nil {
		_ = exchange.Conn.Close(websocketCloseStatusForCodexFailure(err), "websocket state boundary rejected")
	}
	result := exchange.toWebSocketResult()
	result.Err = err
	result.TerminalCause = model.TerminalInternalError
	result.CommitSource = model.CommitUnknown
	o.applySessionLifecycleToResult(result)
	attemptResult := newWebSocketForwardAttemptResult(
		provider, attempt, selectionMode, selectionMetadata, result, err, time.Since(attemptStart),
	)
	attemptResult.injectedCredential = injectedCredential
	o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
	return attemptResult
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
