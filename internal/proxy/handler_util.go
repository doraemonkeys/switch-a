package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestingress"

	"go.uber.org/zap"
)

func ptr[T any](value T) *T { return &value }

// Diagnostic media values retain interoperable protocol facts while dropping
// opaque parameters that can contain provider- or client-controlled state.
func normalizedHTTPMediaTypes(values []string) string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil || mediaType == "" {
			continue
		}
		normalized = append(normalized, strings.ToLower(mediaType))
	}
	return strings.Join(normalized, ",")
}

func normalizedHTTPResponseContentType(values []string, inferred string) string {
	if declared := normalizedHTTPMediaTypes(values); declared != "" {
		return declared
	}
	return normalizedHTTPMediaTypes([]string{inferred})
}

func normalizedHTTPContentCodings(values []string) string {
	var normalized []string
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			if coding := strings.ToLower(strings.TrimSpace(part)); coding != "" {
				normalized = append(normalized, coding)
			}
		}
	}
	return strings.Join(normalized, ",")
}

// logInsertTimeout is the maximum time allowed for inserting a request log.
// This prevents goroutine accumulation if the database is slow or blocked.
const (
	logInsertTimeout            = 2 * time.Second
	codexSelectionRejectedEvent = "codex_http.selection_rejected"
)

// handleNoProvider handles the case when no provider is available.
func (h *Handler) handleNoProvider(pctx *proxyContext, selectionErr error) {
	if selectionErr == nil {
		selectionErr = internal.ErrNoProvider
	}
	if internal.IsProviderSelectionFailure(
		selectionErr,
		internal.ProviderSelectionFailureContinuityRoutingConflict,
	) {
		decision := h.logCodexHTTPRecovery(
			pctx.requestID,
			codexSelectionRejectedEvent,
			selectionErr,
		)
		h.writeGatewayError(
			pctx.w,
			decision.HTTPStatus(),
			string(decision.ErrorCode()),
			codexHTTPRecoveryMessage(decision.Condition()),
		)
		go h.logRequest(pctx, logRequestInputs{
			Facts: nonWebSocketRuntimeFacts{
				ClientTransportStatusCode: decision.HTTPStatus(),
				TerminalErr:               selectionErr,
			},
			Latency: time.Since(pctx.startTime),
		})
		return
	}
	h.logger.Warn("no providers available", zap.String("api_type", pctx.apiType))
	h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", pctx.apiType))
	go h.logRequest(pctx, logRequestInputs{
		Facts: nonWebSocketRuntimeFacts{
			ClientTransportStatusCode: http.StatusServiceUnavailable,
			TerminalErr:               selectionErr,
		},
		Latency: time.Since(pctx.startTime),
	})
}

// handleExhaustedRetries returns the downstream status so request evidence uses
// the same transport outcome that was written to the client.
func (h *Handler) handleExhaustedRetries(pctx *proxyContext, lastErr error) int {
	if errors.Is(lastErr, ErrUpstreamNoResponse) {
		h.writeGatewayError(
			pctx.w,
			http.StatusBadGateway,
			ErrCodeUpstreamNoResponse,
			"Upstream provider closed the connection without returning a response",
		)
		return http.StatusBadGateway
	}
	if lastErr != nil {
		h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderExhausted, "All providers failed")
		return http.StatusServiceUnavailable
	}

	// No lastErr means all providers were excluded/unavailable without actually failing.
	// Log this edge case for visibility, as it indicates a selection issue rather than provider failure.
	h.logger.Warn("exhausted retries with no provider errors", zap.String("api_type", pctx.apiType))
	h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", pctx.apiType))
	return http.StatusServiceUnavailable
}

// suspendProviderUntil marks a provider unavailable until the given time.
func (h *Handler) suspendProviderUntil(ctx context.Context, providerID string, disabledUntil time.Time, reason string) {
	if h.health == nil {
		return
	}
	if err := h.health.SuspendUntil(ctx, providerID, disabledUntil, reason); err != nil {
		h.logger.Warn("failed to suspend provider after upstream failure",
			zap.String("provider_id", providerID),
			zap.Time("disabled_until", disabledUntil),
			zap.Error(err),
		)
		return
	}
	if h.selector != nil {
		h.selector.EvictProviderContinuity(providerID)
		h.logger.Info(
			"evicted provider continuity after suspension",
			zap.String("provider_id", providerID),
			zap.Time("disabled_until", disabledUntil),
		)
	}
}

// writeGatewayError writes a gateway error response.
func (h *Handler) writeGatewayError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	err := model.NewGatewayError(code, message)
	if encErr := json.NewEncoder(w).Encode(err); encErr != nil { // coverage-ignore -- JSON encoding of simple struct rarely fails
		h.logger.Error("failed to encode error response", zap.Error(encErr))
	}
}

func (h *Handler) handleCodexHTTPBeginError(w http.ResponseWriter, requestID string, err error) {
	h.writeCodexHTTPRecovery(w, requestID, "codex_http.request_rejected", err)
}

func (h *Handler) writeCodexHTTPRecovery(
	w http.ResponseWriter,
	requestID string,
	event string,
	err error,
) {
	decision := h.logCodexHTTPRecovery(requestID, event, err)
	h.writeGatewayError(
		w,
		decision.HTTPStatus(),
		string(decision.ErrorCode()),
		codexHTTPRecoveryMessage(decision.Condition()),
	)
}

func (h *Handler) logCodexHTTPRecovery(
	requestID string,
	event string,
	err error,
) codexrecovery.Decision {
	decision := codexHTTPRecoveryDecision(err)
	stage, kind := codexHTTPErrorContext(err)
	// The cause can embed opaque state. Stable classification and stage retain
	// diagnostic value without serializing credentials or continuity payloads.
	h.logger.Warn(event,
		zap.String("request_id", requestID),
		zap.String("operation_id", requestID),
		zap.String("recovery_condition", string(decision.Condition())),
		zap.String("recovery_action", string(decision.RecoveryAction())),
		zap.String("error_code", string(decision.ErrorCode())),
		zap.String("carrier_phase", string(decision.Phase())),
		zap.Int("recovery_http_status", decision.HTTPStatus()),
		zap.String("codex_http_stage", stage),
		zap.String("codex_http_error_kind", string(kind)),
	)
	return decision
}

// Typed continuity and Cookie causes win first. The coarse HTTP kind is only
// a fallback for adapter-originated failures that have no lower-layer category.
func codexHTTPRecoveryDecision(err error) codexrecovery.Decision {
	fallback := codexrecovery.ConditionInternalFailure
	switch {
	case codexhttp.IsKind(err, codexhttp.ErrorClientInput):
		fallback = codexrecovery.ConditionProtocolInvalid
	case codexhttp.IsKind(err, codexhttp.ErrorDependencyUnavailable):
		fallback = codexrecovery.ConditionStateStoreUnavailable
	case codexhttp.IsKind(err, codexhttp.ErrorIdentityMismatch):
		fallback = codexrecovery.ConditionStateConflict
	}
	return codexrecovery.ClassifyWithFallback(err, codexrecovery.PhaseHTTP, fallback)
}

func codexHTTPErrorContext(err error) (string, codexhttp.ErrorKind) {
	var integrationError *codexhttp.Error
	if !errors.As(err, &integrationError) || integrationError == nil {
		return "", ""
	}
	return integrationError.Stage, integrationError.Kind
}

func codexHTTPRecoveryMessage(condition codexrecovery.Condition) string {
	switch condition {
	case codexrecovery.ConditionStateConflict:
		return "Codex request state conflicts with the current operation"
	case codexrecovery.ConditionContinuityRoutingConflict:
		return codexrecovery.ClientMessage(condition)
	case codexrecovery.ConditionReconnectRequired:
		return "Codex request requires a new connection"
	case codexrecovery.ConditionNewThreadRequired:
		return "Codex request state is no longer available"
	case codexrecovery.ConditionStateStoreUnavailable:
		return "Codex state is temporarily unavailable"
	case codexrecovery.ConditionProtocolInvalid:
		return "Codex request state could not be validated"
	default:
		return "Codex request processing failed"
	}
}

// logRequestInputs bundles the runtime inputs needed to build a request
// log and its evidence. Centralizing them here stops the logRequest
// signature from drifting every time a new observation field lands; the
// caller feeds a single aggregate in, and callers that don't observe a
// field (e.g., handleNoProvider) can leave the zero value.
type logRequestInputs struct {
	Provider *model.Provider
	Facts    nonWebSocketRuntimeFacts
	// Reporting facts unrelated to assessment/evidence derivation, kept
	// outside `Facts` to prevent accidental coupling between transport
	// observability and transfer statistics.
	FirstTokenMs  *int64
	ResponseBytes int64
	TokenUsage    *TokenUsage
	Latency       time.Duration
}

// logRequest logs the request asynchronously.
// Note: Uses context.Background() with timeout because this runs after the HTTP response
// completes and the request context may already be cancelled.
func (h *Handler) logRequest(pctx *proxyContext, inputs logRequestInputs) {
	assessment := assessNonWebSocketRequest(inputs.Facts)

	log := &model.RequestLog{
		RequestID:                 pctx.requestID,
		APIType:                   pctx.info.APIType,
		Model:                     pctx.info.Model,
		ClientIP:                  pctx.info.ClientIP,
		UserID:                    pctx.info.UserID,
		SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
		ClientTransportStatusCode: ptr(inputs.Facts.ClientTransportStatusCode),
		CompletionState:           ptr(assessment.CompletionState),
		ServiceOutcome:            ptr(assessment.ServiceOutcome),
		TerminationActor:          assessment.TerminationActor,
		TerminationReason:         assessment.TerminationReason,
		ClientAction:              ptr(assessment.ClientAction),
		SessionEvidenceJSON:       assessment.SessionEvidenceJSON,
		LatencyMs:                 inputs.Latency.Milliseconds(),
		IsSSE:                     inputs.Facts.IsSSE,
		RetryCount:                max(0, len(pctx.attempts)-1),
		IsSticky:                  pctx.isSticky,
		CreatedAt:                 time.Now(),
		RequestPath:               pctx.info.Path,
		RequestMethod:             pctx.info.Method,
		UserAgent:                 pctx.info.UserAgent,
		RequestIDHeader:           pctx.info.RequestID,
		FirstTokenMs:              inputs.FirstTokenMs,
		// Phase 3 transfer statistics
		RequestBytes:                  pctx.receivedRequestBytes(),
		ResponseBytes:                 inputs.ResponseBytes,
		ContentType:                   pctx.info.ContentType,
		RequestedReasoningObservation: pctx.info.Reasoning,
	}

	if inputs.Provider != nil {
		log.ProviderID = inputs.Provider.ID
	}

	// Persist token usage to database (Phase 4a-4).
	if inputs.TokenUsage != nil {
		h.logger.Debug("token usage captured",
			zap.String("request_id", pctx.requestID),
			zap.Int64("prompt_tokens", inputs.TokenUsage.PromptTokens.Value),
			zap.Int64("completion_tokens", inputs.TokenUsage.CompletionTokens.Value),
			zap.Int64("total_tokens", inputs.TokenUsage.TotalTokens.Value),
			zap.Int64("reasoning_tokens", inputs.TokenUsage.ReasoningTokens.Value),
			zap.Int64("cache_read_tokens", inputs.TokenUsage.CacheReadInputTokens.Value),
		)
		// Convert TokenUsage to model fields for database storage
		log.PromptTokens, log.CompletionTokens, log.TotalTokens,
			log.ReasoningTokens, log.CacheReadInputTokens, log.CacheCreationInputTokens, log.UsageDetails = inputs.TokenUsage.ToModelFields()
	}

	// Use timeout to prevent goroutine accumulation if database is slow or blocked
	ctx, cancel := context.WithTimeout(context.Background(), h.requestLogInsertTimeout)
	defer cancel()

	if insertErr := h.store.InsertLog(ctx, log); insertErr != nil { // coverage-ignore -- log insert errors are logged but don't affect response
		h.logger.Error("failed to insert request log", zap.Error(insertErr))
	}

	// Store attempts if there are any (for retry tracking)
	if len(pctx.attempts) > 0 {
		if insertErr := h.store.InsertAttempts(ctx, pctx.attempts); insertErr != nil { // coverage-ignore -- attempt insert errors are logged but don't affect response
			h.logger.Error("failed to insert request attempts", zap.Error(insertErr))
		}
	}
}

type nonWebSocketAssessment struct {
	CompletionState   model.CompletionState
	ServiceOutcome    model.ServiceOutcome
	TerminationActor  *model.TerminationActor
	TerminationReason *model.TerminationReason
	ClientAction      model.ClientAction
	// SessionEvidenceJSON is populated by the evidence builder from the
	// runtime facts themselves; keeping it on the assessment keeps the
	// logRequest surface narrow ? the caller feeds facts in, the assessment
	// carries both classification and evidence out.
	SessionEvidenceJSON *string
}

// nonWebSocketRuntimeFacts aggregates SSE/non-WS observation inputs so the
// assessment and evidence layers share one shape. The struct deliberately
// splits transport observation (IsSSE / FirstByteVisible / CtxErr /
// IsStatusFailover / IsClientWriteError) from the request-level fields
// (ClientTransportStatusCode / Success / ResponseCommitted / ServiceStarted /
// ClientTermination / TerminalErr); this keeps the evidence
// derivation pure while the termination/outcome logic stays isolated on
// request-level axes.
type nonWebSocketRuntimeFacts struct {
	ClientTransportStatusCode int
	Success                   bool
	ResponseCommitted         bool
	ServiceStarted            bool
	ClientTermination         clientTermination
	TerminalErr               error
	// Transport observation inputs ? only meaningful when IsSSE is true.
	// Keeping them on the same struct avoids a second parameter pack; when
	// IsSSE=false the evidence builder short-circuits without reading them.
	IsSSE              bool
	FirstByteVisible   bool
	CtxErr             error
	IsStatusFailover   bool
	IsClientWriteError bool
	SemanticError      bool
	// InjectedCredential is explicit switch-a credential evidence. It is never
	// inferred from headers, URLs, cookies, or provider error text.
	InjectedCredential string
}

func assessNonWebSocketRequest(facts nonWebSocketRuntimeFacts) nonWebSocketAssessment {
	serviceOutcome := deriveNonWebSocketServiceOutcome(facts)
	assessment := nonWebSocketAssessment{
		CompletionState: deriveNonWebSocketCompletionState(serviceOutcome),
		ServiceOutcome:  serviceOutcome,
		ClientAction:    model.ClientActionNone,
	}
	assessment.TerminationActor, assessment.TerminationReason = deriveNonWebSocketTermination(facts, serviceOutcome)
	assessment.SessionEvidenceJSON = buildNonWebSocketSessionEvidence(facts)
	return assessment
}

func deriveNonWebSocketServiceOutcome(facts nonWebSocketRuntimeFacts) model.ServiceOutcome {
	var ingressFailure *requestIngressFailure
	if errors.As(facts.TerminalErr, &ingressFailure) && facts.ServiceStarted {
		return model.ServiceOutcomeInterrupted
	}
	switch {
	case facts.ClientTermination.observed():
		return model.ServiceOutcomeAbandonedByClient
	case !facts.ServiceStarted:
		return model.ServiceOutcomeNeverStarted
	case facts.TerminalErr != nil:
		return model.ServiceOutcomeUnknown
	case facts.SemanticError && facts.ResponseCommitted:
		return model.ServiceOutcomeInterrupted
	case facts.Success:
		return model.ServiceOutcomeCompleted
	default:
		return model.ServiceOutcomeCompleted
	}
}

func deriveNonWebSocketCompletionState(serviceOutcome model.ServiceOutcome) model.CompletionState {
	switch serviceOutcome {
	case model.ServiceOutcomeCompleted:
		return model.CompletionStateCompleted
	case model.ServiceOutcomeUnknown:
		return model.CompletionStateUnknown
	default:
		return model.CompletionStateIncomplete
	}
}

func deriveNonWebSocketTermination(
	facts nonWebSocketRuntimeFacts,
	serviceOutcome model.ServiceOutcome,
) (*model.TerminationActor, *model.TerminationReason) {
	var ingressFailure *requestIngressFailure
	if errors.As(facts.TerminalErr, &ingressFailure) {
		if ingressFailure.kind == requestingress.FailureRead {
			return ptr(model.TerminationActorClient), ptr(model.TerminationReasonClientDisconnect)
		}
		if ingressFailure.kind == requestingress.FailureLimit || ingressFailure.kind == requestingress.FailureLength {
			return ptr(model.TerminationActorClient), ptr(model.TerminationReasonClientRequestError)
		}
		return ptr(model.TerminationActorGateway), ptr(model.TerminationReasonTransportError)
	}
	switch facts.ClientTermination {
	case clientTerminationDisconnect:
		return ptr(model.TerminationActorClient), ptr(model.TerminationReasonClientDisconnect)
	case clientTerminationTimeout:
		return ptr(model.TerminationActorClient), ptr(model.TerminationReasonTimeout)
	}

	switch {
	case serviceOutcome == model.ServiceOutcomeUnknown:
		return ptr(model.TerminationActorUpstream), ptr(model.TerminationReasonTransportError)
	case facts.SemanticError && facts.ResponseCommitted:
		return ptr(model.TerminationActorUpstream), ptr(model.TerminationReasonUpstreamSemanticError)
	case facts.ClientTransportStatusCode == StatusCodeNoResponse:
		return ptr(model.TerminationActorUnknown), ptr(model.TerminationReasonUnknown)
	case !facts.ResponseCommitted && internal.IsProviderSelectionFailure(
		facts.TerminalErr,
		internal.ProviderSelectionFailureContinuityRoutingConflict,
	):
		return ptr(model.TerminationActorGateway), ptr(model.TerminationReasonProviderConfigurationError)
	case !facts.ResponseCommitted && errors.Is(facts.TerminalErr, internal.ErrNoProvider):
		return ptr(model.TerminationActorGateway), ptr(model.TerminationReasonProviderUnavailable)
	case facts.ClientTransportStatusCode >= http.StatusBadRequest && facts.ClientTransportStatusCode < http.StatusInternalServerError:
		return ptr(model.TerminationActorClient), ptr(model.TerminationReasonClientRequestError)
	case facts.ClientTransportStatusCode >= http.StatusInternalServerError:
		if !facts.ResponseCommitted {
			return ptr(model.TerminationActorGateway), ptr(model.TerminationReasonProviderUnavailable)
		}
		return ptr(model.TerminationActorUpstream), ptr(model.TerminationReasonTransportError)
	default:
		return nil, nil
	}
}

func nonWebSocketServiceStarted(statusCode int, responseCommitted bool) bool {
	if !responseCommitted {
		return false
	}
	return statusCode > 0 && statusCode < http.StatusBadRequest
}

// shouldFailover determines if we should try another provider instead of
// forwarding this response to the client. Returns true for provider-side issues
// that may be resolved by switching to a different provider:
//   - Server errors (5xx): temporary upstream issues
//   - Payment/quota errors (402): provider billing limits or quota exhausted
//   - Rate limiting (429): provider overloaded
//   - Auth errors (401, 403): provider misconfiguration (wrong/expired API key)
func shouldFailover(statusCode int) bool {
	return statusCode >= defaults.StatusServerError ||
		statusCode == defaults.StatusPaymentRequired ||
		statusCode == defaults.StatusTooManyRequests ||
		statusCode == defaults.StatusUnauthorized ||
		statusCode == defaults.StatusForbidden
}

// shouldForceProviderSwitch determines if the status code indicates a permanent
// failure that won't be resolved by retrying the same provider. For these codes,
// we should immediately switch to a different provider instead of wasting retries:
//   - 402 Payment Required: billing/quota issue, won't change with retries
//   - 401 Unauthorized: authentication failure, won't change with retries
//   - 403 Forbidden: access denied, won't change with retries
//
// Note: 429 (Too Many Requests) and 5xx errors are NOT included because they may
// be transient and could succeed on retry with the same provider.
func shouldForceProviderSwitch(statusCode int) bool {
	return statusCode == defaults.StatusPaymentRequired ||
		statusCode == defaults.StatusUnauthorized ||
		statusCode == defaults.StatusForbidden
}

const headerUserAgent = "User-Agent"

// EnsureExplicitUserAgentHeader preserves the caller's omission of User-Agent.
// Go injects its own default when the field is absent, so the proxy must carry
// an explicit empty value through both HTTP and WebSocket transports.
func EnsureExplicitUserAgentHeader(headers http.Header) {
	if len(headers.Values(headerUserAgent)) > 0 {
		return
	}
	headers.Set(headerUserAgent, "")
}
