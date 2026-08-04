package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"

	"go.uber.org/zap"
)

const responseAnalysisOperationIDFormat = "%s/attempt/%d/provider/%s/retry/%d/credential/%s"

const maxAttemptSnippetBytes = 512

type attemptFailureKind string

const (
	attemptFailureNone        attemptFailureKind = ""
	attemptFailurePreparation attemptFailureKind = "preparation"
	attemptFailureTransport   attemptFailureKind = "transport"
	attemptFailureStatus      attemptFailureKind = "status"
	attemptFailureRead        attemptFailureKind = "upstream_read"
	attemptFailureWrite       attemptFailureKind = "client_write"
	attemptFailureCanceled    attemptFailureKind = "client_canceled"
	attemptFailureInternal    attemptFailureKind = "internal"
)

// semanticAttemptFacts is deliberately value-only; evidence consumers can
// retain it without extending analyzer resource or response-body lifetimes.
type semanticAttemptFacts struct {
	requestID               string
	operationID             string
	providerID              string
	logicalAttempt          uint64
	providerAttempt         uint64
	credentialPhase         attemptevidence.CredentialPhase
	matches                 []errorrule.RuleMatch
	winner                  errorrule.RuleMatch
	protocolID              apicontract.ResponseProtocolID
	revision                errorrule.Revision
	decision                errorrule.Decision
	windowState             responseanalysis.ResolutionState
	releaseCause            responseanalysis.BoundaryReason
	globalAttemptsStarted   uint64
	globalAttemptsRemaining uint64
	globalAttemptsUnlimited bool
	ruleRetriesScheduled    uint64
	ruleRetryLimit          int
	retryFactsFrozen        bool
	alternateOutcome        attemptevidence.AlternateOutcome
	alternateProviderID     *string
	alternateSwitchMode     *attemptevidence.SwitchMode
	alternateSwitchReason   *errorrule.SwitchReason
}

// forwardResult is the frozen fact handoff from one attempt. Live response,
// body, writer, pending-response, reservation, and raw error capabilities are
// intentionally absent.
type forwardResult struct {
	headersWritten      bool
	responseCommitted   bool
	clientCanceled      bool
	firstByteVisible    bool
	isStatusFailover    bool
	isClientWriteError  bool
	statusCode          int
	success             bool
	done                bool
	isSSE               bool
	bodySnippet         string
	firstTokenMs        *int64
	responseBytes       int64
	tokenUsage          *tokenusage.TokenUsage
	failureDisposition  providerFailureDisposition
	failureKind         attemptFailureKind
	failureMessage      string
	upstreamBytes       int64
	decodedBytes        int64
	peakRequestBytes    int
	peakProcessBytes    int
	elapsedMs           int64
	boundaryReason      responseanalysis.BoundaryReason
	readTermination     responseanalysis.ReadTermination
	analysisFailure     responseanalysis.BoundaryReason
	semantic            *semanticAttemptFacts
	health              errorrule.HealthAssessment
	healthAvailable     bool
	healthCircuitOpened bool
	switchReason        string
	discarded           bool
}

func (r *forwardResult) inheritHealth(source forwardResult) {
	r.health = source.health
	r.healthAvailable = source.healthAvailable
	r.healthCircuitOpened = source.healthCircuitOpened
}

func (r forwardResult) terminalError() error {
	if r.failureMessage == "" {
		return nil
	}
	if r.failureKind == attemptFailureRead {
		if r.readTermination == responseanalysis.ReadTerminationIdleTimeout {
			if r.isSSE {
				return fmt.Errorf("%w: %s", ErrSSEIdleTimeout, r.failureMessage)
			}
			return fmt.Errorf("%w: %s", ErrReadTimeout, r.failureMessage)
		}
		return &UpstreamReadError{Err: errors.New(r.failureMessage)}
	}
	return errors.New(r.failureMessage)
}

type semanticMatchTracker struct {
	mu      sync.Mutex
	rules   *errorrule.CompiledRuleSet
	scope   errorrule.RequestScope
	result  errorrule.MatchResult
	matched bool
}

func newSemanticMatchTracker(rules *errorrule.CompiledRuleSet, scope errorrule.RequestScope) *semanticMatchTracker {
	return &semanticMatchTracker{rules: rules, scope: scope}
}

func (m *semanticMatchTracker) Match(fields responseanalysis.SemanticFields) bool {
	if m == nil || m.rules == nil {
		return false
	}
	normalized := errorrule.SemanticFields{
		Type: fields.Type, Code: fields.Code, Message: fields.Message, Reason: fields.Reason,
	}
	result := m.rules.Match(m.scope, normalized)
	if result.Winner == nil {
		return false
	}
	m.mu.Lock()
	if !m.matched {
		m.result = result
		m.matched = true
	}
	m.mu.Unlock()
	return true
}

func (m *semanticMatchTracker) Facts() (errorrule.MatchResult, bool) {
	if m == nil {
		return errorrule.MatchResult{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result, m.matched
}

type pendingHTTPResponse struct {
	head              upstreamtransport.ResponseHead
	pending           *responseanalysis.PendingResponse
	writer            *firstWriteResponseWriter
	exchange          httpCaptureExchange
	matcher           *semanticMatchTracker
	rules             *errorrule.CompiledRuleSet
	statsOnce         sync.Once
	snippet           *boundedSnippet
	pctx              *proxyContext
	operationID       string
	providerID        string
	logicalAttempt    uint64
	providerAttempt   uint64
	credentialPhase   attemptevidence.CredentialPhase
	analysisStartedAt time.Time
}

type boundedSnippet struct {
	bytes []byte
}

func (b *boundedSnippet) Observe(payload []byte) {
	if b == nil || len(b.bytes) >= maxAttemptSnippetBytes || len(payload) == 0 {
		return
	}
	remaining := maxAttemptSnippetBytes - len(b.bytes)
	if len(payload) > remaining {
		payload = payload[:remaining]
	}
	b.bytes = append(b.bytes, payload...)
}

func (b *boundedSnippet) String() string {
	if b == nil {
		return ""
	}
	return string(b.bytes)
}

func (h *Handler) prepareForwardRequest(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
) (*http.Request, error) {
	request, failureCode, err := h.buildProviderRequest(ctx, pctx, attempt.provider)
	if err != nil {
		h.captureHTTPPreparationFailure(ctx, pctx, attempt, phase, nil, failureCode, err)
		return nil, err
	}
	if err := h.applyForwardCredentials(ctx, request.Header, attempt.provider, pctx); err != nil {
		h.captureHTTPPreparationFailure(
			ctx, pctx, attempt, phase, request, requestcapture.FailureCodeCredentialApply, err,
		)
		return nil, err
	}
	return request, nil
}

func (h *Handler) applyForwardCredentials(
	ctx context.Context,
	headers http.Header,
	provider *model.Provider,
	pctx *proxyContext,
) error {
	if h.auth != nil {
		return h.auth.ApplyProviderCredentials(ctx, headers, provider, pctx.apiType, pctx.cfg.globalAuthMode, pctx.r)
	}
	SetAuthHeader(headers, provider.APIKeyForAPIType(pctx.apiType), provider.AuthMode, pctx.cfg.globalAuthMode, pctx.r)
	return nil
}

func (h *Handler) fetchPendingHTTPResponse(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
	request *http.Request,
	rules *errorrule.CompiledRuleSet,
) (*pendingHTTPResponse, error) {
	exchange := h.beginHTTPExchange(pctx, attempt, phase, request)
	if pctx.liveBytes != nil {
		pctx.liveBytes.BytesSent.Add(int64(len(pctx.body)))
		pctx.liveBytes.LastActivityAt.Store(time.Now().UnixMilli())
	}
	response, err := pctx.transport.FetchUpstream(ctx, request)
	if err != nil {
		finishHTTPFetchFailure(ctx, pctx, exchange, err)
		return nil, err
	}
	head, body, err := response.Take()
	if err != nil {
		finishHTTPFetchFailure(ctx, pctx, exchange, err)
		return nil, err
	}
	body = exchange.observeResponse(head, body)

	scope := errorrule.RequestScope{
		ProviderID: errorrule.ProviderID(attempt.provider.ID),
		APIType:    apicontract.APIType(pctx.apiType),
	}
	matcher := newSemanticMatchTracker(rules, scope)
	mode, modeErr := responseAnalysisMode(head.StatusCode, rules.DetectionPlan(scope))
	if modeErr != nil {
		_ = body.Close()
		return nil, modeErr
	}

	snippet := &boundedSnippet{}
	writer := h.newAttemptResponseWriter(pctx, &exchange, snippet)
	idleDuration := pctx.cfg.readTimeout
	if head.EventStream {
		idleDuration = pctx.cfg.sseIdleTimeout
		if h.activeRegistry != nil {
			h.activeRegistry.UpdateSSE(pctx.requestID, true)
		}
	}
	operationID := fmt.Sprintf(
		responseAnalysisOperationIDFormat,
		pctx.requestID,
		attempt.logicalAttemptIndex,
		attempt.provider.ID,
		attempt.providerAttemptIndex,
		phase,
	)
	analysisStartedAt := time.Now()
	pending := h.analyzer.Start(ctx, responseanalysis.StartInput{
		OperationID:     operationID,
		Mode:            mode,
		APIType:         pctx.apiType,
		ContentType:     head.SourceHeader.Get("Content-Type"),
		ContentEncoding: head.SourceHeader.Get("Content-Encoding"),
		StatusCode:      head.StatusCode,
		Header:          head.Header,
		Trailer:         head.Trailer,
		Body:            body,
		Writer:          writer,
		IdleDuration:    idleDuration,
		Match:           matcher.Match,
	})
	return &pendingHTTPResponse{
		head: head, pending: pending, writer: writer, exchange: exchange,
		matcher: matcher, rules: rules, snippet: snippet, pctx: pctx,
		operationID: operationID, providerID: attempt.provider.ID,
		logicalAttempt:  uint64(attempt.logicalAttemptIndex + 1),
		providerAttempt: uint64(attempt.providerAttemptIndex + 1),
		credentialPhase: evidenceCredentialPhase(phase), analysisStartedAt: analysisStartedAt,
	}, nil
}

func evidenceCredentialPhase(phase requestcapture.CredentialPhase) attemptevidence.CredentialPhase {
	if phase == requestcapture.CredentialPhaseRefreshed {
		return attemptevidence.CredentialPhaseRefreshed
	}
	return attemptevidence.CredentialPhasePrimary
}

func responseAnalysisMode(statusCode int, plan errorrule.DetectionPlan) (responseanalysis.AnalysisMode, error) {
	if statusCode < defaults.StatusSuccessMin || statusCode >= defaults.StatusSuccessMax {
		return responseanalysis.HoldMode(), nil
	}
	switch plan {
	case errorrule.DetectionProbe:
		return responseanalysis.ProbeMode(), nil
	case errorrule.DetectionObserveOnly:
		return responseanalysis.ObserveMode(responseanalysis.BoundaryPassthroughOnly)
	default:
		return responseanalysis.ObserveMode(responseanalysis.BoundaryNoRetryCandidate)
	}
}

func (h *Handler) newAttemptResponseWriter(
	pctx *proxyContext,
	exchange *httpCaptureExchange,
	snippet *boundedSnippet,
) *firstWriteResponseWriter {
	return &firstWriteResponseWriter{
		ResponseWriter: pctx.w,
		onFirstWrite: func() {
			if h.activeRegistry != nil {
				h.activeRegistry.MarkDataReceived(pctx.requestID)
			}
		},
		onWrite: func(written int, at time.Time) {
			if pctx.liveBytes != nil {
				pctx.liveBytes.BytesReceived.Add(int64(written))
				pctx.liveBytes.LastActivityAt.Store(at.UnixMilli())
			}
			if exchange != nil && exchange.mode.CapturesPayload() {
				exchange.observeClientWrite(written)
			}
		},
		onPayload: snippet.Observe,
	}
}

func (p *pendingHTTPResponse) awaitBoundary() responseanalysis.Boundary {
	return p.pending.AwaitBoundary()
}

func (p *pendingHTTPResponse) commit(cause responseanalysis.TransitionCause) (forwardResult, error) {
	forwarding, err := p.pending.Commit(cause)
	if err != nil {
		return p.internalFailure(err), err
	}
	return p.finishForwarding(forwarding), nil
}

func (p *pendingHTTPResponse) finishForwarding(forwarding *responseanalysis.ForwardingResponse) forwardResult {
	if forwarding == nil {
		return p.internalFailure(errors.New("forwarding response is required"))
	}
	milestone := forwarding.AwaitSemanticOrCompletion()
	if milestone.Matched {
		milestone.Observation.Release()
	}
	completion := forwarding.Wait()
	result := p.resultFromCompletion(completion)
	if completion.HasSemanticObservation {
		result.semantic = p.semanticFacts(completion.SemanticObservation, completion.State, completion.BoundaryReason)
		completion.SemanticObservation.Release()
	}
	if completion.HasUsageObservation && completion.UsageObservation.Usage != nil {
		usage := *completion.UsageObservation.Usage
		result.tokenUsage = &usage
		completion.UsageObservation.Release()
	}
	p.recordWinningRule(result.semantic)
	p.finishCapture(completion)
	return result
}

func (p *pendingHTTPResponse) discard(
	cause responseanalysis.TransitionCause,
	reason requestcapture.TerminationReason,
	failure requestcapture.FailureObservation,
) (forwardResult, error) {
	receipt, err := p.pending.Discard(cause)
	result := forwardResult{
		statusCode: p.head.StatusCode, isSSE: p.head.EventStream,
		upstreamBytes: receipt.UpstreamBytesRead, decodedBytes: receipt.DecodedBytesAnalyzed,
		responseBytes:    receipt.ClientBodyBytesWritten,
		peakRequestBytes: receipt.PeakRequestBytes, peakProcessBytes: receipt.PeakProcessBytes,
		headersWritten: receipt.HeadersCommitted, responseCommitted: receipt.HeadersCommitted,
		firstByteVisible: receipt.ClientBodyBytesWritten > 0,
		boundaryReason:   receipt.BoundaryReason, analysisFailure: receipt.AnalysisFailure,
		elapsedMs: time.Since(p.analysisStartedAt).Milliseconds(), discarded: receipt.Cause != "",
	}
	if err != nil {
		result.failureKind = attemptFailureInternal
		result.failureMessage = err.Error()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.clientCanceled = true
			result.failureKind = attemptFailureCanceled
		}
	}
	p.exchange.completedAt = time.Now()
	sourceCompletion := requestcapture.SourceCompletionPartial
	if p.exchange.sourceCompletionAfterError() == requestcapture.SourceCompletionComplete {
		sourceCompletion = requestcapture.SourceCompletionComplete
	}
	p.pctx.finishHTTPCaptureExchange(p.exchange, p.head.Trailer, sourceCompletion, reason, failure)
	return result, err
}

func (p *pendingHTTPResponse) resultFromCompletion(completion responseanalysis.Completion) forwardResult {
	result := forwardResult{
		headersWritten: completion.HeadersCommitted, responseCommitted: completion.HeadersCommitted,
		firstByteVisible: completion.ClientBodyBytesWritten > 0,
		statusCode:       p.head.StatusCode, isSSE: p.head.EventStream,
		responseBytes: completion.ClientBodyBytesWritten, upstreamBytes: completion.UpstreamBytesRead,
		decodedBytes:     completion.DecodedBytesAnalyzed,
		peakRequestBytes: completion.PeakRequestBytes, peakProcessBytes: completion.PeakProcessBytes,
		elapsedMs: time.Since(p.analysisStartedAt).Milliseconds(), boundaryReason: completion.BoundaryReason,
		readTermination: completion.ReadTermination, analysisFailure: completion.AnalysisFailure,
		bodySnippet: p.snippet.String(), done: true,
	}
	if p.head.EventStream && p.writer.written && !p.writer.firstWriteTime.IsZero() {
		value := p.writer.firstWriteTime.Sub(p.pctx.startTime).Milliseconds()
		result.firstTokenMs = &value
	}
	switch completion.Termination {
	case responseanalysis.TerminationCompleted:
		result.success = p.head.StatusCode < defaults.StatusClientError
	case responseanalysis.TerminationClientCancelled:
		result.clientCanceled = true
		result.failureKind = attemptFailureCanceled
		result.failureMessage = "client canceled response forwarding"
	case responseanalysis.TerminationClientWriteFailure:
		result.failureKind = attemptFailureWrite
		result.failureMessage = "client response write failed"
		result.isClientWriteError = true
	case responseanalysis.TerminationUpstreamReadFailure:
		result.failureKind = attemptFailureRead
		if completion.ReadTermination == responseanalysis.ReadTerminationIdleTimeout {
			result.failureMessage = "upstream response idle timeout"
		} else {
			result.failureMessage = "upstream response read failed"
		}
	default:
		result.failureKind = attemptFailureInternal
		result.failureMessage = "response forwarding failed internally"
	}
	return result
}

func (p *pendingHTTPResponse) semanticFacts(
	observation responseanalysis.Observation,
	state responseanalysis.ResolutionState,
	reason responseanalysis.BoundaryReason,
) *semanticAttemptFacts {
	matches, matched := p.matcher.Facts()
	if !matched || matches.Winner == nil {
		return nil
	}
	return &semanticAttemptFacts{
		requestID: p.pctx.requestID, operationID: p.operationID, providerID: p.providerID,
		logicalAttempt: p.logicalAttempt, providerAttempt: p.providerAttempt,
		credentialPhase: p.credentialPhase,
		matches:         append([]errorrule.RuleMatch(nil), matches.All...), winner: *matches.Winner,
		protocolID: observation.ProtocolID, revision: p.rules.Revision(), windowState: state, releaseCause: reason,
		alternateOutcome: attemptevidence.AlternateNotRequested,
	}
}

func (p *pendingHTTPResponse) recordWinningRule(facts *semanticAttemptFacts) {
	if facts == nil || p == nil || p.pctx == nil {
		return
	}
	p.statsOnce.Do(func() {
		handler := p.pctx.handler
		if handler == nil || handler.ruleStats == nil {
			return
		}
		if err := handler.ruleStats.Hit(statistics.HandleFor(facts.winner.Rule), time.Now()); err != nil {
			handler.logger.Warn("failed to record internal-error rule hit",
				zap.String("request_id", p.pctx.requestID),
				zap.String("rule_id", string(facts.winner.Rule.ID)),
				zap.Error(err),
			)
		}
	})
}

func (p *pendingHTTPResponse) internalFailure(err error) forwardResult {
	message := "pending response failed"
	if err != nil {
		message = err.Error()
	}
	return forwardResult{
		statusCode: p.head.StatusCode, isSSE: p.head.EventStream,
		failureKind: attemptFailureInternal, failureMessage: message,
	}
}

func cloneTokenUsage(source *tokenusage.TokenUsage) *tokenusage.TokenUsage {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}
