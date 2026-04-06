package proxy

import (
	"net/http"
	"time"

	"switch-a/internal/model"
)

type webSocketAssessment struct {
	SemanticsVersion          model.RequestSemanticsVersion
	ClientTransportStatusCode int
	CompletionState           model.CompletionState
	ServiceOutcome            model.ServiceOutcome
	TerminationActor          *model.TerminationActor
	TerminationReason         *model.TerminationReason
	ClientAction              model.ClientAction
	SessionEvidenceJSON       *string
	SessionCommitted          bool
	ClientVisible             bool
	CommitSource              model.CommitSource
	CompletionObserved        bool
	providerFailure           providerFailureDisposition
}

type webSocketHealthAssessment struct {
	markFailure   bool
	markSuccess   bool
	suspendUntil  *time.Time
	suspendReason string
}

func assessWebSocketSession(session *WebSocketSessionResult) webSocketAssessment {
	var (
		provider         *model.Provider
		result           *WebSocketResult
		fallback         error
		gateway          webSocketGatewayEvidenceInput
		transparentRetry bool
	)

	if session != nil {
		provider = session.FinalProvider
		result = session.FinalResult
		fallback = session.FinalErr
		gateway = webSocketGatewayEvidenceInput{
			StatusCode: session.GatewayStatusCode,
			ErrorCode:  session.GatewayErrorCode,
			Message:    session.GatewayMessage,
		}
		transparentRetry = webSocketTransparentRetryCandidate(session.Attempts)
	}

	assessment := newWebSocketAssessment(provider, gateway, result, fallback, transparentRetry)
	if result == nil {
		return assessment
	}
	assessment.SessionCommitted = result.SessionCommitted
	assessment.ClientVisible = result.ClientVisible
	if result.CommitSource != "" {
		assessment.CommitSource = result.CommitSource
	} else {
		assessment.CommitSource = model.CommitUnknown
	}
	assessment.CompletionObserved = result.CompletionObserved
	return assessment
}

func newWebSocketAssessment(
	provider *model.Provider,
	gateway webSocketGatewayEvidenceInput,
	result *WebSocketResult,
	fallback error,
	transparentRetry bool,
) webSocketAssessment {
	assessment := webSocketAssessment{
		SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
		ClientTransportStatusCode: webSocketClientTransportStatusCode(gateway.StatusCode, result),
		CommitSource:              model.CommitUnknown,
	}
	if result != nil && result.CommitSource != "" {
		assessment.CommitSource = result.CommitSource
	}

	assessment.providerFailure = classifyWebSocketProviderFailure(provider, result)
	assessment.CompletionObserved = result != nil && result.CompletionObserved
	assessment.TerminationReason = deriveWebSocketTerminationReason(result, gateway)
	assessment.ServiceOutcome = deriveWebSocketServiceOutcome(assessment.TerminationReason, result, assessment.providerFailure)
	assessment.ClientAction = deriveWebSocketClientAction(
		assessment.TerminationReason,
		assessment.ServiceOutcome,
		result != nil && result.ClientVisible,
		transparentRetry,
		assessment.providerFailure,
	)
	assessment.CompletionState = deriveWebSocketCompletionState(
		assessment.TerminationReason,
		assessment.ServiceOutcome,
		assessment.CompletionObserved,
	)
	assessment.TerminationActor = deriveWebSocketTerminationActor(assessment.TerminationReason)
	assessment.TerminationReason, assessment.TerminationActor = normalizeWebSocketTerminationAttribution(
		assessment.TerminationReason,
		assessment.TerminationActor,
		assessment.ServiceOutcome,
	)
	assessment.SessionEvidenceJSON = buildWebSocketEvidence(gateway, result, fallback)
	return assessment
}

func deriveWebSocketTerminationReason(
	result *WebSocketResult,
	gateway webSocketGatewayEvidenceInput,
) *model.TerminationReason {
	if result != nil && result.UpstreamError != nil {
		return classifyWebSocketUpstreamTerminationReason(result.UpstreamError)
	}
	if result != nil {
		switch result.TerminalCause {
		case model.TerminalProviderUnavailable:
			return ptr(model.TerminationReasonProviderUnavailable)
		case model.TerminalProviderConfigurationError:
			return ptr(model.TerminationReasonProviderConfigurationError)
		case model.TerminalCleanClose:
			return ptr(model.TerminationReasonCleanClose)
		case model.TerminalClientDisconnect:
			return ptr(model.TerminationReasonClientDisconnect)
		case model.TerminalUpstreamTransportError:
			return ptr(model.TerminationReasonTransportError)
		case model.TerminalUpstreamSemanticError:
			return ptr(model.TerminationReasonUpstreamSemanticError)
		case model.TerminalUpstreamHandshakeRejected:
			return ptr(model.TerminationReasonUpstreamHandshakeRejected)
		case model.TerminalClientUpgradeRejected:
			return ptr(model.TerminationReasonClientUpgradeRejected)
		case model.TerminalInternalError:
			return ptr(model.TerminationReasonInternalError)
		}
	}
	if gateway.StatusCode == http.StatusServiceUnavailable && gateway.ErrorCode == ErrCodeProviderUnavailable {
		return ptr(model.TerminationReasonProviderUnavailable)
	}
	return nil
}

func classifyWebSocketUpstreamTerminationReason(upstreamErr *WebSocketUpstreamError) *model.TerminationReason {
	if upstreamErr == nil {
		return nil
	}
	for _, key := range []string{upstreamErr.SemanticErrorKey(), upstreamErr.Code} {
		switch normalizeWebSocketSemanticErrorKey(key) {
		case codexUsageLimitErrorType:
			return ptr(model.TerminationReasonUsageLimitReached)
		case webSocketConnectionLimitErrorType:
			return ptr(model.TerminationReasonWebSocketConnectionLimitReached)
		}
	}
	if classifyWebSocketUpstreamError(upstreamErr) == webSocketSemanticClassificationClientScoped {
		return ptr(model.TerminationReasonClientRequestError)
	}
	return ptr(model.TerminationReasonUpstreamSemanticError)
}

type webSocketServiceOutcomeContext struct {
	clientVisible         bool
	sessionCommitted      bool
	completionObserved    bool
	providerScopedFailure bool
}

func newWebSocketServiceOutcomeContext(
	result *WebSocketResult,
	failureDisposition providerFailureDisposition,
) webSocketServiceOutcomeContext {
	return webSocketServiceOutcomeContext{
		clientVisible:         result != nil && result.ClientVisible,
		sessionCommitted:      result != nil && result.SessionCommitted,
		completionObserved:    result != nil && result.CompletionObserved,
		providerScopedFailure: failureDisposition.isProviderScoped(),
	}
}

func (ctx webSocketServiceOutcomeContext) incompleteVisibilityOutcome() model.ServiceOutcome {
	if ctx.clientVisible || ctx.sessionCommitted {
		return model.ServiceOutcomeUnknown
	}
	return model.ServiceOutcomeNeverStarted
}

func (ctx webSocketServiceOutcomeContext) completionAwareOutcome() model.ServiceOutcome {
	if ctx.completionObserved {
		return model.ServiceOutcomeCompleted
	}
	return ctx.incompleteVisibilityOutcome()
}

func (ctx webSocketServiceOutcomeContext) providerNativeSocketTerminalOutcome() model.ServiceOutcome {
	return ctx.completionAwareOutcome()
}

func (ctx webSocketServiceOutcomeContext) usageLimitOutcome() model.ServiceOutcome {
	if ctx.clientVisible {
		return model.ServiceOutcomeInterrupted
	}
	return model.ServiceOutcomeNeverStarted
}

func (ctx webSocketServiceOutcomeContext) upstreamSemanticOutcome() model.ServiceOutcome {
	if ctx.completionObserved {
		return model.ServiceOutcomeCompleted
	}
	if ctx.clientVisible && ctx.providerScopedFailure {
		return model.ServiceOutcomeInterrupted
	}
	return ctx.incompleteVisibilityOutcome()
}

func deriveWebSocketServiceOutcome(
	reason *model.TerminationReason,
	result *WebSocketResult,
	failureDisposition providerFailureDisposition,
) model.ServiceOutcome {
	ctx := newWebSocketServiceOutcomeContext(result, failureDisposition)

	switch deref(reason) {
	case model.TerminationReasonClientDisconnect:
		return model.ServiceOutcomeAbandonedByClient
	case model.TerminationReasonWebSocketConnectionLimitReached:
		// Connection-limit events are provider-native terminal evidence for this
		// socket only. Keep the raw upstream payload and non-failure semantics,
		// but derive completion from the runtime facts we actually observed.
		return ctx.providerNativeSocketTerminalOutcome()
	case model.TerminationReasonCleanClose:
		return model.ServiceOutcomeCompleted
	case model.TerminationReasonUsageLimitReached:
		return ctx.usageLimitOutcome()
	case model.TerminationReasonTransportError:
		return ctx.completionAwareOutcome()
	case model.TerminationReasonUpstreamSemanticError:
		return ctx.upstreamSemanticOutcome()
	case model.TerminationReasonClientRequestError:
		return ctx.incompleteVisibilityOutcome()
	case model.TerminationReasonProviderUnavailable,
		model.TerminationReasonProviderConfigurationError,
		model.TerminationReasonUpstreamHandshakeRejected,
		model.TerminationReasonClientUpgradeRejected:
		return model.ServiceOutcomeNeverStarted
	case model.TerminationReasonInternalError, model.TerminationReasonUnknown:
		return ctx.completionAwareOutcome()
	default:
		return ctx.completionAwareOutcome()
	}
}

func deriveWebSocketClientAction(
	reason *model.TerminationReason,
	serviceOutcome model.ServiceOutcome,
	clientVisible bool,
	transparentRetry bool,
	failureDisposition providerFailureDisposition,
) model.ClientAction {
	reasonValue := deref(reason)
	switch reasonValue {
	case model.TerminationReasonUsageLimitReached:
		if clientVisible {
			return model.ClientActionReconnectRequired
		}
	case model.TerminationReasonUpstreamSemanticError:
		if clientVisible && failureDisposition.isProviderScoped() {
			return model.ClientActionReconnectRequired
		}
	case model.TerminationReasonWebSocketConnectionLimitReached:
		// `none` means the gateway does not add its own reconnect contract on top
		// of the provider-native socket limit error. Clients should rely on the
		// raw upstream payload when deciding whether to open a new websocket.
		return model.ClientActionNone
	case model.TerminationReasonClientDisconnect,
		model.TerminationReasonClientRequestError,
		model.TerminationReasonTransportError,
		model.TerminationReasonCleanClose:
		return model.ClientActionNone
	}
	if transparentRetry && serviceOutcome == model.ServiceOutcomeCompleted {
		return model.ClientActionTransparentRetry
	}
	return model.ClientActionNone
}

func webSocketTransparentRetryCandidate(attempts []WebSocketAttemptResult) bool {
	if len(attempts) < 2 {
		return false
	}
	finalAttempt := attempts[len(attempts)-1]
	if model.NormalizeSwitchMode(finalAttempt.SelectionMode) != model.SwitchModeReplacement {
		return false
	}
	return webSocketTransparentRetryBasis(attempts[len(attempts)-2])
}

func webSocketTransparentRetryBasis(attempt WebSocketAttemptResult) bool {
	if attempt.Result == nil || attempt.Result.ClientVisible {
		return false
	}
	if attempt.Result.UpstreamError != nil {
		return classifyWebSocketUpstreamFailureForProvider(
			attempt.Provider,
			attempt.Result.UpstreamError,
		).isProviderScoped()
	}
	if attempt.Result.TerminalCause == model.TerminalUpstreamHandshakeRejected {
		return classifyWebSocketHandshakeFailureForProvider(attempt.Provider, attempt.Result).isProviderScoped()
	}
	return false
}

func deriveWebSocketCompletionState(
	reason *model.TerminationReason,
	serviceOutcome model.ServiceOutcome,
	completionObserved bool,
) model.CompletionState {
	if completionObserved {
		return model.CompletionStateCompleted
	}
	if serviceOutcome == model.ServiceOutcomeUnknown {
		return model.CompletionStateUnknown
	}
	switch deref(reason) {
	case model.TerminationReasonCleanClose:
		return model.CompletionStateCompleted
	default:
		return model.CompletionStateIncomplete
	}
}

func deriveWebSocketTerminationActor(reason *model.TerminationReason) *model.TerminationActor {
	switch deref(reason) {
	case model.TerminationReasonClientDisconnect, model.TerminationReasonClientRequestError:
		return ptr(model.TerminationActorClient)
	case model.TerminationReasonProviderUnavailable,
		model.TerminationReasonProviderConfigurationError,
		model.TerminationReasonClientUpgradeRejected:
		return ptr(model.TerminationActorGateway)
	case model.TerminationReasonInternalError:
		return ptr(model.TerminationActorInternal)
	case model.TerminationReasonUsageLimitReached,
		model.TerminationReasonWebSocketConnectionLimitReached,
		model.TerminationReasonTransportError,
		model.TerminationReasonUpstreamSemanticError,
		model.TerminationReasonUpstreamHandshakeRejected,
		model.TerminationReasonCleanClose:
		return ptr(model.TerminationActorUpstream)
	case model.TerminationReasonUnknown:
		return ptr(model.TerminationActorUnknown)
	default:
		return nil
	}
}

func normalizeWebSocketTerminationAttribution(
	reason *model.TerminationReason,
	actor *model.TerminationActor,
	serviceOutcome model.ServiceOutcome,
) (*model.TerminationReason, *model.TerminationActor) {
	switch {
	case reason == nil && serviceOutcome == model.ServiceOutcomeUnknown:
		return ptr(model.TerminationReasonUnknown), ptr(model.TerminationActorUnknown)
	case reason == nil:
		return nil, nil
	// The normalized end-state contract treats nominal clean closes as completions,
	// not as diagnostic terminations, regardless of whether completion was explicit
	// or inferred from terminal clean-close evidence.
	case serviceOutcome == model.ServiceOutcomeCompleted && *reason == model.TerminationReasonCleanClose:
		return nil, nil
	default:
		return reason, actor
	}
}

func classifyWebSocketProviderFailure(provider *model.Provider, result *WebSocketResult) providerFailureDisposition {
	if result == nil {
		return providerFailureDisposition{}
	}
	if result.UpstreamError != nil {
		return classifyWebSocketUpstreamFailureForProvider(provider, result.UpstreamError)
	}
	return classifyWebSocketHandshakeFailureForProvider(provider, result)
}

func assessWebSocketHealth(provider *model.Provider, result *WebSocketResult) webSocketHealthAssessment {
	assessment := newWebSocketAssessment(provider, webSocketGatewayEvidenceInput{}, result, resultError(result), false)
	if assessment.providerFailure.autoDisableUntil != nil {
		return webSocketHealthAssessment{
			markFailure:   shouldTrackWebSocketFailureInHealth(result),
			suspendUntil:  assessment.providerFailure.autoDisableUntil,
			suspendReason: assessment.providerFailure.autoDisableReason,
		}
	}

	switch deref(assessment.TerminationReason) {
	case model.TerminationReasonClientDisconnect:
		if result != nil && result.SessionCommitted {
			return webSocketHealthAssessment{markSuccess: true}
		}
		return webSocketHealthAssessment{}
	case model.TerminationReasonClientRequestError, model.TerminationReasonWebSocketConnectionLimitReached:
		if assessment.ServiceOutcome == model.ServiceOutcomeCompleted {
			return webSocketHealthAssessment{markSuccess: true}
		}
		return webSocketHealthAssessment{}
	case model.TerminationReasonUsageLimitReached:
		return webSocketHealthAssessment{markFailure: shouldTrackWebSocketFailureInHealth(result)}
	case model.TerminationReasonTransportError:
		if result != nil && result.ClientVisible && !result.CompletionObserved {
			return webSocketHealthAssessment{}
		}
		if result != nil && result.SessionCommitted {
			return webSocketHealthAssessment{markSuccess: true}
		}
	}

	if assessment.ServiceOutcome == model.ServiceOutcomeCompleted {
		return webSocketHealthAssessment{markSuccess: true}
	}
	if result != nil && result.UpstreamError != nil {
		return webSocketHealthAssessment{
			markFailure: assessment.providerFailure.isProviderScoped() && shouldTrackWebSocketFailureInHealth(result),
		}
	}
	switch {
	case result == nil:
		return webSocketHealthAssessment{}
	case result.TerminalCause == model.TerminalProviderConfigurationError:
		return webSocketHealthAssessment{}
	case result.TerminalCause == model.TerminalUpstreamHandshakeRejected,
		result.TerminalCause == model.TerminalUpstreamTransportError,
		result.TerminalCause == model.TerminalUpstreamSemanticError:
		return webSocketHealthAssessment{markFailure: shouldTrackWebSocketFailureInHealth(result)}
	default:
		return webSocketHealthAssessment{}
	}
}

func resultError(result *WebSocketResult) error {
	if result == nil {
		return nil
	}
	return result.Err
}

func deref[T comparable](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

func ptr[T any](value T) *T {
	return &value
}
