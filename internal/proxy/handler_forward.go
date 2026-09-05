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
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestingress"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"

	"go.uber.org/zap"
)

const responseAnalysisOperationIDFormat = "%s/attempt/%d/provider/%s/retry/%d/credential/%s"

const maxAttemptSnippetBytes = 512

type attemptFailureKind string

const (
	attemptFailureNone               attemptFailureKind = ""
	attemptFailurePreparation        attemptFailureKind = "preparation"
	attemptFailureTransport          attemptFailureKind = "transport"
	attemptFailureUpstreamNoResponse attemptFailureKind = "upstream_no_response"
	attemptFailureStatus             attemptFailureKind = "status"
	attemptFailureRead               attemptFailureKind = "upstream_read"
	attemptFailureWrite              attemptFailureKind = "client_write"
	attemptFailureClientTerminated   attemptFailureKind = "client_terminated"
	attemptFailureInternal           attemptFailureKind = "internal"
	attemptFailureIngress            attemptFailureKind = "request_ingress"
	attemptFailureDisguise           attemptFailureKind = "client_disguise"
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
	headersWritten        bool
	responseCommitted     bool
	clientTermination     clientTermination
	firstByteVisible      bool
	isStatusFailover      bool
	isClientWriteError    bool
	statusCode            int
	success               bool
	upstreamErrorObserved bool
	done                  bool
	isSSE                 bool
	bodySnippet           string
	firstTokenMs          *int64
	responseBytes         int64
	tokenUsage            *tokenusage.TokenUsage
	failureDisposition    providerFailureDisposition
	failureKind           attemptFailureKind
	ingressFailureKind    requestingress.FailureKind
	failureMessage        string
	upstreamBytes         int64
	decodedBytes          int64
	peakRequestBytes      int
	peakProcessBytes      int
	elapsedMs             int64
	boundaryReason        responseanalysis.BoundaryReason
	readTermination       responseanalysis.ReadTermination
	analysisFailure       responseanalysis.BoundaryReason
	semantic              *semanticAttemptFacts
	health                errorrule.HealthAssessment
	healthAvailable       bool
	healthCircuitOpened   bool
	switchReason          string
	discarded             bool
	injectedCredential    string
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
	if r.failureKind == attemptFailureIngress {
		return &requestIngressFailure{cause: errors.New(r.failureMessage), kind: r.ingressFailureKind}
	}
	if r.failureKind == attemptFailureDisguise {
		return fmt.Errorf("%w: %s", errClientDisguiseFailed, r.failureMessage)
	}
	if r.failureKind == attemptFailureUpstreamNoResponse {
		return fmt.Errorf("%w: %s", ErrUpstreamNoResponse, r.failureMessage)
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
	mu            sync.Mutex
	rules         *errorrule.CompiledRuleSet
	scope         errorrule.RequestScope
	result        errorrule.MatchResult
	matched       bool
	observedError bool
}

func newSemanticMatchTracker(rules *errorrule.CompiledRuleSet, scope errorrule.RequestScope) *semanticMatchTracker {
	return &semanticMatchTracker{rules: rules, scope: scope}
}

func (m *semanticMatchTracker) Match(fields responseanalysis.SemanticFields) bool {
	if m != nil {
		// The analyzer invokes this callback only for classified upstream error
		// events. A missing matching rule does not turn that event into success.
		m.mu.Lock()
		m.observedError = true
		m.mu.Unlock()
	}
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

func (m *semanticMatchTracker) ObservedError() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.observedError
}

func (m *semanticMatchTracker) Facts() (errorrule.MatchResult, bool) {
	if m == nil {
		return errorrule.MatchResult{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result, m.matched
}

func (h *Handler) prepareForwardRequest(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
) (*http.Request, error) {
	request, failureCode, err := h.buildProviderRequest(ctx, pctx, attempt.provider)
	if err != nil {
		h.captureHTTPPreparationFailure(ctx, pctx, attempt, phase, nil, injectedCredentialForCapture(attempt.candidate, nil), failureCode, err)
		return nil, err
	}
	defer func() {
		if err != nil && request.Body != nil {
			_ = request.Body.Close()
		}
	}()
	applied, err := h.applyForwardCredentials(ctx, request, attempt, pctx)
	if err != nil {
		h.captureHTTPPreparationFailure(
			ctx, pctx, attempt, phase, request, injectedCredentialForCapture(attempt.candidate, request.Header), requestcapture.FailureCodeCredentialApply, err,
		)
		return nil, err
	}
	codexAttempt, err := pctx.codex.PrepareAttempt(ctx, request, attempt.candidate, applied)
	if err != nil {
		h.captureHTTPPreparationFailure(
			ctx, pctx, attempt, phase, request, injectedCredentialForCapture(attempt.candidate, request.Header), requestcapture.FailureCodeCredentialApply, err,
		)
		return nil, err
	}
	h.syncCodexSelectionConstraints(pctx)
	request = request.WithContext(context.WithValue(request.Context(), codexAttemptContextKey{}, codexAttempt))
	err = h.prepareHTTPDisguise(ctx, pctx, attempt.provider, request)
	if err != nil {
		if abandonErr := codexAttempt.AbandonBeforeDisclosure(ctx); abandonErr != nil {
			h.logger.Warn("client_disguise.http_undisclosed_abandon_failed", zap.String("operation_id", pctx.requestID), zap.Error(abandonErr))
		}
		return nil, err
	}
	return request, nil
}

func (h *Handler) syncCodexSelectionConstraints(pctx *proxyContext) {
	if pctx == nil || pctx.codex == nil || pctx.selectReq == nil {
		return
	}
	pctx.selectReq.RequiredAuthority, pctx.selectReq.PreferredRouteTargetID = pctx.codex.RequiredAuthority()
}

type codexAttemptContextKey struct{}

func codexAttemptFromRequest(request *http.Request) *codexhttp.Attempt {
	if request == nil {
		return nil
	}
	attempt, _ := request.Context().Value(codexAttemptContextKey{}).(*codexhttp.Attempt)
	return attempt
}

func redirectExecutionPolicy(
	apiType string,
	requestPolicy upstreamtransport.RequestPolicy,
) upstreamtransport.ExecutionOptions {
	if apiType == APITypeCodex && requestPolicy.Cookies == upstreamtransport.ServerManagedCookies {
		return upstreamtransport.ExecutionOptions{Redirects: upstreamtransport.ExposeRedirects}
	}
	return upstreamtransport.ExecutionOptions{Redirects: upstreamtransport.FollowRedirects}
}

func (h *Handler) applyForwardCredentials(
	ctx context.Context,
	request *http.Request,
	attempt httpAttemptContext,
	pctx *proxyContext,
) (codexidentity.AppliedIdentity, error) {
	return h.auth.ApplyProviderCredentials(
		ctx,
		request.Header,
		attempt.candidate,
		attempt.provider.AuthMode,
		pctx.cfg.globalAuthMode,
		pctx.r,
		request.URL,
	)
}

func (h *Handler) fetchPendingHTTPResponse(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
	request *http.Request,
	rules *errorrule.CompiledRuleSet,
) (*pendingHTTPResponse, error) {
	injectedCredential := injectedCredentialForCapture(attempt.candidate, request.Header)
	exchange := h.beginHTTPExchange(pctx, attempt, phase, request, injectedCredential)
	transportOptions := redirectExecutionPolicy(pctx.apiType, pctx.codex.RequestPolicy())
	transportOptions.Observe = func(event upstreamtransport.TransmissionEvent) {
		h.logger.Debug("request_ingress.transmission", zap.String("operation_id", pctx.requestID),
			zap.String("attempt_id", attempt.responseOperationID(pctx.requestID, phase)),
			zap.String("event", string(event.Kind)), zap.Int64("transmission_index", event.TransmissionIndex), zap.Int64("hop_index", event.HopIndex),
			zap.String("reopen_reason", string(event.ReopenReason)), zap.Bool("retry_eligible", event.RetryEligible),
			zap.Int("previous_reopens", event.PreviousReopens), zap.Int64("upstream_body_read_bytes", event.BodyReadBytes),
			zap.String("disclosure", event.Disclosure.String()), zap.Bool("response_committed", pctx.responseCommitted.Load()), zap.Error(event.Err))
	}
	response, disclosure, err := pctx.transport.FetchUpstream(ctx, request, transportOptions)
	if result, failed := h.disguiseFailure(pctx, err); failed {
		_ = h.settleCodexHTTPAttempt(ctx, pctx, attempt, codexAttemptFromRequest(request), nil, disclosure)
		closeUpstreamResponse(response)
		finishHTTPFetchFailure(ctx, pctx, exchange, result.terminalError())
		return nil, pctx.disguise.failure
	}
	if response != nil {
		disclosure = upstreamtransport.RequestDisclosureConfirmed
	}
	codexAttempt := codexAttemptFromRequest(request)
	if settlementErr := h.settleCodexHTTPAttempt(ctx, pctx, attempt, codexAttempt, response, disclosure); settlementErr != nil {
		finishHTTPFetchFailure(ctx, pctx, exchange, settlementErr)
		return nil, settlementErr
	}
	if err != nil {
		finishHTTPFetchFailure(ctx, pctx, exchange, err)
		return nil, err
	}
	head, body, err := response.Take()
	if err != nil {
		finishHTTPFetchFailure(ctx, pctx, exchange, err)
		return nil, err
	}
	if err := codexAttempt.ObserveResponse(&head); err != nil {
		_ = body.Close()
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
	requestAccept := request.Header.Values("Accept")
	media := resolveHTTPResponseMedia(&head, requestAccept)
	hasResponseBody := httpResponseAllowsBody(request.Method, head.StatusCode)
	var wireBytesRead func() int64
	operationID := attempt.responseOperationID(pctx.requestID, phase)
	if pctx.apiType == APITypeCodex && hasResponseBody && media.IsEventStream() {
		normalized, normalizeErr := upstreamtransport.NormalizeEventStream(head, body)
		if normalizeErr != nil {
			_ = body.Close()
			finishHTTPFetchFailure(ctx, pctx, exchange, normalizeErr)
			h.logger.Warn("proxy.codex_sse_response_normalization_failed",
				zap.String("request_id", pctx.requestID),
				zap.String("operation_id", operationID),
				zap.String("provider_id", attempt.provider.ID),
				zap.String("response_content_encoding", normalizedHTTPContentCodings(head.SourceHeader.Values("Content-Encoding"))),
				zap.Error(normalizeErr),
			)
			return nil, normalizeErr
		}
		body = normalized.Body
		head = normalized.Head
		if normalized.Transformed {
			wireBytesRead = normalized.WireBytesRead
			h.logger.Debug("proxy.codex_sse_response_normalized",
				zap.String("request_id", pctx.requestID),
				zap.String("operation_id", operationID),
				zap.String("provider_id", attempt.provider.ID),
				zap.String("source_content_encoding", normalized.SourceEncoding),
				zap.String("downstream_content_encoding", "identity"),
			)
		}
	}
	writer := h.newAttemptResponseWriter(
		pctx,
		&exchange,
		snippet,
		codexAttempt,
		pctx.apiType == APITypeCodex && hasResponseBody && media.IsEventStream(),
	)
	if err := pctx.disguise.prepareResponse(ctx, writer, head, media, hasResponseBody); err != nil {
		_ = body.Close()
		return nil, err
	}
	idleDuration := pctx.cfg.readTimeout
	if media.IsEventStream() {
		idleDuration = pctx.cfg.sseIdleTimeout
		if h.activeRegistry != nil {
			h.activeRegistry.UpdateSSE(pctx.requestID, true)
		}
	}
	h.captureProviderUsageObservation(pctx, attempt, head.SourceHeader, time.Now(), operationID)
	h.logHTTPResponseMediaDecision(media, httpResponseMediaLogContext{
		requestID: pctx.requestID, operationID: operationID, providerID: attempt.provider.ID, apiType: pctx.apiType,
		logicalAttempt: attempt.logicalAttemptIndex + 1, providerAttempt: attempt.providerAttemptIndex + 1,
		statusCode: head.StatusCode, acceptValueCount: len(requestAccept),
		contentType:           normalizedHTTPResponseContentType(head.SourceHeader.Values("Content-Type"), media.ContentType()),
		contentTypeValueCount: len(head.SourceHeader.Values("Content-Type")),
		contentEncoding:       normalizedHTTPContentCodings(head.SourceHeader.Values("Content-Encoding")),
	})
	analysisStartedAt := time.Now()
	pending := h.analyzer.Start(ctx, responseanalysis.StartInput{
		OperationID:     operationID,
		Mode:            mode,
		APIType:         pctx.apiType,
		ContentType:     media.ContentType(),
		ContentEncoding: head.Header.Get("Content-Encoding"),
		StatusCode:      head.StatusCode,
		Header:          head.Header,
		Trailer:         head.Trailer,
		Body:            body,
		Writer:          writer,
		IdleDuration:    idleDuration,
		Match:           matcher.Match,
	})
	return &pendingHTTPResponse{
		head: head, media: media, pending: pending, writer: writer, exchange: exchange,
		matcher: matcher, rules: rules, snippet: snippet, pctx: pctx,
		operationID: operationID, providerID: attempt.provider.ID,
		logicalAttempt:  uint64(attempt.logicalAttemptIndex + 1),
		providerAttempt: uint64(attempt.providerAttemptIndex + 1),
		credentialPhase: evidenceCredentialPhase(phase), analysisStartedAt: analysisStartedAt,
		injectedCredential: injectedCredential,
		codexAttempt:       codexAttempt,
		wireBytesRead:      wireBytesRead,
		closeUpload:        request.Body.Close,
	}, nil
}

func (h *Handler) settleCodexHTTPAttempt(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	codexAttempt *codexhttp.Attempt,
	response *upstreamtransport.Response,
	disclosure upstreamtransport.RequestDisclosure,
) error {
	if disclosure.DefinitelyNotDisclosed() {
		return h.abandonUndisclosedCodexHTTPAttempt(ctx, pctx, attempt, codexAttempt, disclosure)
	}
	if err := codexAttempt.MarkDisclosed(ctx); err != nil {
		closeUpstreamResponse(response)
		return err
	}
	h.syncCodexSelectionConstraints(pctx)
	return nil
}

func (h *Handler) abandonUndisclosedCodexHTTPAttempt(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	codexAttempt *codexhttp.Attempt,
	disclosure upstreamtransport.RequestDisclosure,
) error {
	if err := codexAttempt.AbandonBeforeDisclosure(ctx); err != nil {
		h.logger.Warn("proxy.codex_http_attempt_abandon_failed",
			zap.String("request_id", pctx.requestID),
			zap.String("provider_id", attempt.provider.ID),
			zap.String("disclosure", disclosure.String()),
			zap.Error(err),
		)
		return err
	}
	if pctx.apiType == APITypeCodex {
		h.logger.Debug("proxy.codex_http_attempt_abandoned_before_disclosure",
			zap.String("request_id", pctx.requestID),
			zap.String("provider_id", attempt.provider.ID),
			zap.String("disclosure", disclosure.String()),
		)
	}
	return nil
}

func closeUpstreamResponse(response *upstreamtransport.Response) {
	if response == nil {
		return
	}
	body, err := response.TakeBody()
	if err == nil {
		_ = body.Close()
	}
}

type httpResponseMediaLogContext struct {
	requestID             string
	operationID           string
	providerID            string
	apiType               string
	logicalAttempt        int
	providerAttempt       int
	statusCode            int
	acceptValueCount      int
	contentType           string
	contentTypeValueCount int
	contentEncoding       string
}

func (h *Handler) logHTTPResponseMediaDecision(
	media responseanalysis.ResponseMedia,
	context httpResponseMediaLogContext,
) {
	h.logger.Debug("http response media decision",
		zap.String("request_id", context.requestID),
		zap.String("operation_id", context.operationID),
		zap.String("provider_id", context.providerID),
		zap.String("api_type", context.apiType),
		zap.Int("logical_attempt", context.logicalAttempt),
		zap.Int("provider_attempt", context.providerAttempt),
		zap.Int("http_status", context.statusCode),
		zap.String("response_content_type", context.contentType),
		zap.String("response_content_encoding", context.contentEncoding),
		zap.String("media_source", string(media.Source())),
		zap.String("media_decision", string(media.Decision())),
		zap.String("media_reason", string(media.Reason())),
		zap.Int("accept_value_count", context.acceptValueCount),
		zap.Int("response_content_type_value_count", context.contentTypeValueCount),
	)
}

func resolveHTTPResponseMedia(head *upstreamtransport.ResponseHead, requestAccept []string) responseanalysis.ResponseMedia {
	if head == nil {
		return responseanalysis.ResponseMedia{}
	}
	media := responseanalysis.ResolveResponseMedia(head.SourceHeader.Get("Content-Type"), requestAccept)
	if media.Source() == responseanalysis.ResponseMediaFromRequestAccept {
		if head.Header == nil {
			head.Header = make(http.Header)
		}
		head.Header.Set("Content-Type", media.ContentType())
	}
	return media
}

func evidenceCredentialPhase(phase requestcapture.CredentialPhase) attemptevidence.CredentialPhase {
	if phase == requestcapture.CredentialPhaseRefreshed {
		return attemptevidence.CredentialPhaseRefreshed
	}
	return attemptevidence.CredentialPhasePrimary
}

func (h *Handler) newAttemptResponseWriter(
	pctx *proxyContext,
	exchange *httpCaptureExchange,
	snippet *boundedSnippet,
	codexAttempt *codexhttp.Attempt,
	scanServerSSE bool,
) *firstWriteResponseWriter {
	writer := &firstWriteResponseWriter{
		ResponseWriter: pctx.w,
		onCommit:       pctx.closeRetryWindow,
		prepareVisible: func(header http.Header) (*codexhttp.Visibility, error) {
			return codexAttempt.PrepareVisible(pctx.r.Context(), header)
		},
		commitVisible: func(visibility *codexhttp.Visibility) error {
			return visibility.Commit(context.WithoutCancel(pctx.r.Context()))
		},
		onGateFailure: func(err error) {
			for name := range pctx.w.Header() {
				pctx.w.Header().Del(name)
			}
			h.writeCodexHTTPRecovery(
				pctx.w,
				pctx.requestID,
				"codex_http.pre_commit_failed",
				err,
			)
		},
		onStreamGateFailure: func(err error) {
			h.logCodexHTTPRecovery(
				pctx.requestID,
				"codex_http.sse_committed_state_failed",
				err,
			)
		},
		onUncertain: func() {
			h.logger.Warn("codex_http.response_write_uncertain",
				zap.String("request_id", pctx.requestID),
				zap.String("decision", "pending_retained"),
			)
		},
		onCommitError: func(err error) {
			h.logCodexHTTPRecovery(
				pctx.requestID,
				"codex_http.response_commit_failed",
				err,
			)
		},
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
	if scanServerSSE {
		writer.sseGate = codexAttempt.NewSSEGate()
		writer.sseContext = pctx.r.Context()
	}
	return writer
}

func cloneTokenUsage(source *tokenusage.TokenUsage) *tokenusage.TokenUsage {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}
