package websocketproxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/websocketprotocol"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

const (
	webSocketReconnectRequiredStatusCode = http.StatusBadGateway
	webSocketReconnectRequiredMessage    = "Upstream provider continuity failed after the session became visible; reconnect required"
	webSocketPreVisibleFailureStatusCode = http.StatusBadGateway
	webSocketPreVisibleFailureMessage    = "Upstream WebSocket session failed before becoming visible"
)

// Session finalization owns both the canonical result and its external
// consequences. Persisting logs and applying health exactly once from this
// boundary prevents the gateway bootstrap path from interpreting attempt state.
func (h *Gateway) logWebSocketSession(info RequestInfo, session *WebSocketSessionResult, latency time.Duration) {
	if session == nil {
		return
	}

	result := session.FinalResult
	attempts := session.RequestAttempts()
	assessment := assessWebSocketSession(session)
	sessionCommitted := assessment.SessionCommitted
	clientVisible := assessment.ClientVisible
	commitSource := model.CommitUnknown
	if result != nil && result.CommitSource != "" {
		commitSource = result.CommitSource
	}

	log := &model.RequestLog{
		RequestID:                     session.RequestID,
		APIType:                       info.APIType,
		Model:                         info.Model,
		ClientIP:                      info.ClientIP,
		UserID:                        info.UserID,
		SemanticsVersion:              assessment.SemanticsVersion,
		ClientTransportStatusCode:     ptr(assessment.ClientTransportStatusCode),
		CompletionState:               ptr(assessment.CompletionState),
		ServiceOutcome:                ptr(assessment.ServiceOutcome),
		TerminationActor:              assessment.TerminationActor,
		TerminationReason:             assessment.TerminationReason,
		ClientAction:                  ptr(assessment.ClientAction),
		SessionEvidenceJSON:           assessment.SessionEvidenceJSON,
		LatencyMs:                     latency.Milliseconds(),
		IsWebSocket:                   true,
		IsSticky:                      session.IsSticky,
		RetryCount:                    session.RetryCount(),
		SessionCommitted:              &sessionCommitted,
		ClientVisible:                 &clientVisible,
		CommitSource:                  &commitSource,
		CreatedAt:                     time.Now(),
		RequestPath:                   info.Path,
		RequestMethod:                 info.Method,
		UserAgent:                     info.UserAgent,
		RequestIDHeader:               info.RequestID,
		RequestedReasoningObservation: info.Reasoning,
	}
	if session.FinalProvider != nil {
		log.ProviderID = session.FinalProvider.ID
	}
	if result != nil {
		log.ResponseBytes = result.BytesUpstreamToClient
		log.RequestBytes = result.BytesClientToUpstream
	}
	if result != nil && result.TokenUsage != nil {
		log.PromptTokens, log.CompletionTokens, log.TotalTokens,
			log.ReasoningTokens, log.CacheReadInputTokens, log.CacheCreationInputTokens, log.UsageDetails = result.TokenUsage.ToModelFields()
	}

	ctx, cancel := context.WithTimeout(context.Background(), logInsertTimeout)
	defer cancel()
	if insertErr := h.store.InsertLog(ctx, log); insertErr != nil { // coverage-ignore // store error path only reachable with a failing database
		h.logger.Error("failed to insert websocket request log", zap.Error(insertErr))
		return
	}
	if len(attempts) > 0 {
		if insertErr := h.store.InsertAttempts(ctx, attempts); insertErr != nil { // coverage-ignore -- attempt insert errors are logged but don't affect response
			h.logger.Error("failed to insert websocket request attempts", zap.Error(insertErr))
		}
	}
}

func applyWebSocketSessionHealthOutcomes(ctx context.Context, h *Gateway, session *WebSocketSessionResult) {
	if session == nil {
		return
	}
	finalProviderSawSemanticOutcome := false
	for _, attempt := range session.Attempts {
		if attempt.Provider == nil {
			continue
		}
		if session.FinalProvider != nil &&
			attempt.Provider.ID == session.FinalProvider.ID &&
			attempt.Result != nil &&
			(attempt.Result.UpstreamError != nil || attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError) {
			finalProviderSawSemanticOutcome = true
		}
		applyWebSocketHealthOutcome(ctx, h, attempt.Provider, attempt.Result)
	}
	if session.FinalProvider == nil || session.FinalResult == nil || finalProviderSawSemanticOutcome {
		return
	}
	if session.FinalResult.UpstreamError != nil || session.FinalResult.TerminalCause == model.TerminalUpstreamSemanticError {
		applyWebSocketHealthOutcome(ctx, h, session.FinalProvider, session.FinalResult)
	}
}

func applyWebSocketHealthOutcome(
	ctx context.Context,
	h *Gateway,
	provider *model.Provider,
	result *WebSocketResult,
) {
	if provider == nil || result == nil {
		return
	}
	healthAssessment := assessWebSocketHealth(provider, result)
	if healthAssessment.markFailure {
		h.markFailure(ctx, provider.ID, result.Err)
	}
	if healthAssessment.suspendUntil != nil {
		h.suspendProviderUntil(
			ctx,
			provider.ID,
			*healthAssessment.suspendUntil,
			healthAssessment.suspendReason,
		)
		return
	}
	if healthAssessment.markSuccess {
		h.markSuccess(ctx, provider.ID)
	}
}

func (o *WebSocketSessionOrchestrator) finalSessionFromLastAttempt(ctx context.Context) *WebSocketSessionResult {
	if len(o.attempts) == 0 {
		if o.suppressedAttempt != nil {
			return o.sessionFromSuppressedPayload(ctx)
		}
		session := newWebSocketSelectionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			http.StatusServiceUnavailable,
			model.TerminalProviderUnavailable,
			ErrCodeProviderUnavailable,
			fmt.Sprintf("No available provider for api_type: %s", o.apiType),
			internal.ErrNoProvider,
		)
		return o.finalizeSelectionFailureSession(session)
	}

	lastAttempt := o.attempts[len(o.attempts)-1]
	if o.suppressedAttempt != nil && (lastAttempt.Result == nil || !lastAttempt.Result.ClientVisible) {
		return o.sessionFromSuppressedPayload(ctx)
	}
	return o.sessionFromAttempt(lastAttempt)
}

func (o *WebSocketSessionOrchestrator) finalizeSelectionFailureSession(session *WebSocketSessionResult) *WebSocketSessionResult {
	return o.finalizeTerminalSession(session)
}

func (o *WebSocketSessionOrchestrator) finalizeTerminalSession(session *WebSocketSessionResult) *WebSocketSessionResult {
	if o == nil || session == nil {
		return session
	}
	// Freeze the endpoint identity alongside each provider attempt before any
	// evidence serializer runs. The API key is then selected from the provider
	// model and this stable API type, never from observed header text.
	session.APIType = o.apiType
	for index := range session.Attempts {
		session.Attempts[index].APIType = o.apiType
	}
	if session.ProbeOutcome == webSocketSelectionProbeOutcomeUnknown {
		session.ProbeOutcome = o.probeOutcome
	}
	o.applySessionLifecycleToResult(session.FinalResult)
	if session.FinalResult != nil {
		session.ClientAccepted = session.FinalResult.ClientAccepted
		session.FinalResult.RecoveryAction = deriveWebSocketSessionRecoveryAction(session)
	}
	populateCanonicalWebSocketGatewayMetadata(session)
	o.closeAcceptedClientOnSubprotocolExhaustion(session)
	if err := o.emitTerminalGatewayErrorIfNeeded(
		session.FinalResult,
		session.GatewayStatusCode,
		session.GatewayErrorCode,
		session.GatewayMessage,
	); err != nil && session.FinalErr == nil {
		session.FinalErr = err
	}
	return session
}

func (o *WebSocketSessionOrchestrator) closeAcceptedClientOnSubprotocolExhaustion(session *WebSocketSessionResult) {
	if o == nil || o.clientConn == nil || session == nil || session.FinalResult == nil {
		return
	}
	if !errors.Is(session.FinalResult.Err, websocketprotocol.ErrSubprotocolMismatch) {
		return
	}
	// Probe commits the downstream 101 before selecting a provider. Exhausting
	// exact-match attempts therefore has only one protocol-correct terminal form:
	// close the accepted connection instead of falling back to ordinary mode or
	// fabricating an application error frame under the wrong subprotocol.
	closeWebSocketSubprotocolViolation(o.clientConn)
	o.clientConn = nil
}

func (o *WebSocketSessionOrchestrator) sessionFromSuppressedPayload(_ context.Context) *WebSocketSessionResult {
	finalProvider := (*model.Provider)(nil)
	finalErr := internal.ErrNoProvider
	gatewayStatusCode := http.StatusServiceUnavailable
	gatewayErrorCode := ErrCodeProviderUnavailable
	gatewayMessage := fmt.Sprintf("No available provider for api_type: %s", o.apiType)
	result := newWebSocketGatewayFailureResult(
		gatewayStatusCode,
		model.TerminalProviderUnavailable,
		finalErr,
	)

	lastAttempt, hasLastAttempt := o.lastAttempt()
	if hasLastAttempt {
		finalProvider, gatewayStatusCode, gatewayErrorCode, gatewayMessage, finalErr = applyLastAttemptToSuppressedPayload(
			lastAttempt,
			finalProvider,
			finalErr,
			result,
			gatewayStatusCode,
			gatewayErrorCode,
			gatewayMessage,
		)
	}
	if finalProvider == nil && o.suppressedAttempt != nil {
		finalProvider = o.suppressedAttempt.provider
	}
	o.clearSuppressedAttempt()

	session := &WebSocketSessionResult{
		RequestID:                           o.requestID,
		FinalProvider:                       finalProvider,
		FinalResult:                         result,
		FinalErr:                            finalErr,
		Attempts:                            append([]WebSocketAttemptResult(nil), o.attempts...),
		IsSticky:                            o.isSticky,
		ClientAccepted:                      result.ClientAccepted,
		ProbeOutcome:                        o.probeOutcome,
		GatewayStatusCode:                   gatewayStatusCode,
		GatewayErrorCode:                    gatewayErrorCode,
		GatewayMessage:                      gatewayMessage,
		syntheticFinalFromSuppressedPayload: true,
	}
	if hasLastAttempt && lastAttempt.Result != nil {
		session.ResolvedModel = lastAttempt.Result.Model
	}
	if hasLastAttempt {
		session.injectedCredential = lastAttempt.injectedCredential
	}
	return o.finalizeTerminalSession(session)
}

func (o *WebSocketSessionOrchestrator) lastAttempt() (WebSocketAttemptResult, bool) {
	if len(o.attempts) == 0 {
		return WebSocketAttemptResult{}, false
	}
	return o.attempts[len(o.attempts)-1], true
}

func applyLastAttemptToSuppressedPayload(
	lastAttempt WebSocketAttemptResult,
	finalProvider *model.Provider,
	finalErr error,
	result *WebSocketResult,
	gatewayStatusCode int,
	gatewayErrorCode string,
	gatewayMessage string,
) (*model.Provider, int, string, string, error) {
	if lastAttempt.Provider != nil {
		finalProvider = lastAttempt.Provider
	}
	if terminalErr := lastAttempt.terminalErr(); terminalErr != nil {
		finalErr = terminalErr
		result.Err = terminalErr
	}
	if lastAttempt.Result != nil {
		result.HandshakeAccepted = lastAttempt.Result.HandshakeAccepted
		result.ClientAccepted = lastAttempt.Result.ClientAccepted
		result.TerminalCause = lastAttempt.Result.TerminalCause
		if result.TerminalCause == "" {
			result.TerminalCause = model.TerminalProviderUnavailable
		}
		result.CommitSource = lastAttempt.Result.CommitSource
	}
	// Structural guard: a synthetic final session must never inherit the
	// replaced attempt's transport observation. TerminalCause inheritance is
	// semantically desirable (callers expect a cause), but CloseError /
	// FailurePeer would misattribute a transport fact produced by an attempt
	// that no longer drives the session. Zeroing the nested struct wholesale
	// (rather than zeroing individual fields) prevents silent "partial
	// inheritance" bugs if more fields are added later.
	result.TransportObservation = WebSocketTransportObservation{}
	if lastAttempt.GatewayStatusCode > 0 {
		gatewayStatusCode = lastAttempt.GatewayStatusCode
		gatewayErrorCode = lastAttempt.GatewayErrorCode
		gatewayMessage = lastAttempt.GatewayMessage
	}
	return finalProvider, gatewayStatusCode, gatewayErrorCode, gatewayMessage, finalErr
}

func (o *WebSocketSessionOrchestrator) sessionFromAttempt(attempt WebSocketAttemptResult) *WebSocketSessionResult {
	session := &WebSocketSessionResult{
		RequestID:          o.requestID,
		FinalProvider:      attempt.Provider,
		FinalResult:        attempt.Result.Clone(),
		FinalErr:           attempt.terminalErr(),
		Attempts:           append([]WebSocketAttemptResult(nil), o.attempts...),
		IsSticky:           o.isSticky,
		ClientAccepted:     attempt.clientAccepted(),
		ProbeOutcome:       o.probeOutcome,
		GatewayStatusCode:  attempt.GatewayStatusCode,
		GatewayErrorCode:   attempt.GatewayErrorCode,
		GatewayMessage:     attempt.GatewayMessage,
		injectedCredential: attempt.injectedCredential,
	}
	if attempt.Result != nil {
		session.ResolvedModel = attempt.Result.Model
	}
	return o.finalizeTerminalSession(session)
}

func (o *WebSocketSessionOrchestrator) emitTerminalGatewayErrorIfNeeded(
	result *WebSocketResult,
	statusCode int,
	errorCode,
	message string,
) error {
	if o == nil || o.clientConn == nil || result == nil || statusCode <= 0 {
		return nil
	}
	if result.ClientVisible && result.RecoveryAction != model.RecoveryActionReconnectRequired {
		return nil
	}

	payload := marshalWebSocketGatewayError(statusCode, errorCode, message)
	writeCtx, cancel := context.WithTimeout(context.Background(), webSocketFallbackWriteTimeout)
	defer cancel()

	if err := o.clientConn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		_ = o.clientConn.Close(websocket.StatusInternalError, "")
		o.clientConn = nil
		return err
	}

	closeTerminalSuppressedClientConn(o.clientConn)
	o.clientConn = nil
	return nil
}

func newWebSocketSelectionFailureSession(
	requestID string,
	isSticky bool,
	attempts []WebSocketAttemptResult,
	statusCode int,
	terminalCause model.TerminalCause,
	errorCode, message string,
	err error,
) *WebSocketSessionResult {
	return &WebSocketSessionResult{
		RequestID:         requestID,
		FinalResult:       newWebSocketGatewayFailureResult(statusCode, terminalCause, err),
		FinalErr:          err,
		Attempts:          append([]WebSocketAttemptResult(nil), attempts...),
		IsSticky:          isSticky,
		ProbeOutcome:      webSocketSelectionProbeOutcomeUnknown,
		GatewayStatusCode: statusCode,
		GatewayErrorCode:  errorCode,
		GatewayMessage:    message,
	}
}

func newWebSocketProbeDecisionFailureSession(
	requestID string,
	isSticky bool,
	attempts []WebSocketAttemptResult,
	probeOutcome webSocketSelectionProbeOutcome,
	err error,
) *WebSocketSessionResult {
	session := newWebSocketSelectionFailureSession(
		requestID,
		isSticky,
		attempts,
		http.StatusInternalServerError,
		model.TerminalInternalError,
		ErrCodeInternalError,
		webSocketProbeDemandResolutionFailureMessage,
		err,
	)
	session.ProbeOutcome = probeOutcome
	return session
}

func newWebSocketForwardAttemptResult(
	provider *model.Provider,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	result *WebSocketResult,
	forwardErr error,
	latency time.Duration,
) WebSocketAttemptResult {
	attemptResult := WebSocketAttemptResult{
		Provider:          provider,
		Attempt:           attempt,
		SelectionMode:     selectionMode,
		SelectionMetadata: selectionMetadata,
		Result:            result,
		ForwardErr:        forwardErr,
		LatencyMs:         latency.Milliseconds(),
		CreatedAt:         time.Now(),
	}
	if forwardErr == nil && result != nil && !result.HandshakeAccepted {
		attemptResult.GatewayStatusCode, attemptResult.GatewayErrorCode, attemptResult.GatewayMessage = websocketGatewayFailure(result)
	}
	return attemptResult
}

func newWebSocketProviderConfigurationAttempt(
	provider *model.Provider,
	apiType string,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	err error,
	latency time.Duration,
) WebSocketAttemptResult {
	missingField := "configuration"
	var configErr *webSocketProviderConfigError
	if errors.As(err, &configErr) && configErr.missingField != "" {
		missingField = configErr.missingField
	}

	message := fmt.Sprintf(
		"Provider %q is not ready for websocket %q: %s",
		provider.ID,
		apiType,
		missingField,
	)
	return WebSocketAttemptResult{
		Provider:          provider,
		Attempt:           attempt,
		SelectionMode:     selectionMode,
		SelectionMetadata: selectionMetadata,
		Result:            newWebSocketGatewayFailureResult(http.StatusBadGateway, model.TerminalProviderConfigurationError, err),
		ForwardErr:        err,
		LatencyMs:         latency.Milliseconds(),
		CreatedAt:         time.Now(),
		GatewayStatusCode: http.StatusBadGateway,
		GatewayErrorCode:  ErrCodeWebSocketUpgrade,
		GatewayMessage:    message,
	}
}

func websocketSwitchReason(attempt WebSocketAttemptResult) string {
	if attempt.Result != nil &&
		!attempt.Result.ClientVisible &&
		attempt.Result.UpstreamError != nil {
		if disposition := classifyWebSocketUpstreamFailureForProvider(attempt.Provider, attempt.Result.UpstreamError); disposition.forcesProviderSwitch() {
			return disposition.switchReason
		}
	}
	if failureDisposition := classifyWebSocketHandshakeFailureForProvider(attempt.Provider, attempt.Result); failureDisposition.forcesProviderSwitch() {
		return failureDisposition.switchReason
	}
	if attempt.Result != nil && attempt.Result.TerminalCause != "" {
		return string(attempt.Result.TerminalCause)
	}
	return ""
}

func deriveWebSocketSessionRecoveryAction(session *WebSocketSessionResult) model.RecoveryAction {
	if session == nil {
		return model.RecoveryActionNone
	}
	assessment := assessWebSocketSession(session)
	switch assessment.ClientAction {
	case model.ClientActionReconnectRequired:
		return model.RecoveryActionReconnectRequired
	case model.ClientActionTransparentRetry:
		return model.RecoveryActionTransparentRetry
	default:
		return model.RecoveryActionNone
	}
}

func populateCanonicalWebSocketGatewayMetadata(session *WebSocketSessionResult) {
	if session == nil || session.FinalResult == nil {
		return
	}
	if session.FinalResult.RecoveryAction == model.RecoveryActionReconnectRequired {
		session.GatewayStatusCode = webSocketReconnectRequiredStatusCode
		session.GatewayErrorCode = ErrCodeWebSocketReconnect
		session.GatewayMessage = webSocketReconnectRequiredMessage
		return
	}
	if session.GatewayStatusCode > 0 {
		if session.GatewayErrorCode == "" {
			session.GatewayErrorCode = ErrCodeWebSocketUpgrade
		}
		if session.GatewayMessage == "" {
			session.GatewayMessage = canonicalWebSocketGatewayMessage(session.FinalResult)
		}
		return
	}
	if session.FinalResult.ClientVisible {
		return
	}
	session.GatewayStatusCode = webSocketPreVisibleFailureStatusCode
	session.GatewayErrorCode = ErrCodeWebSocketUpgrade
	session.GatewayMessage = canonicalWebSocketGatewayMessage(session.FinalResult)
}

func canonicalWebSocketGatewayMessage(result *WebSocketResult) string {
	if result == nil {
		return webSocketPreVisibleFailureMessage
	}
	if result.TerminalCause == model.TerminalProviderConfigurationError && result.Err != nil {
		return result.Err.Error()
	}
	return webSocketPreVisibleFailureMessage
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
