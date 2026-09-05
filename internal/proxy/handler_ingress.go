package proxy

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestingress"
	"github.com/doraemonkeys/switch-a/internal/requestingress/clientconnection"
	"github.com/doraemonkeys/switch-a/internal/requestingress/h2ingress"
	"github.com/doraemonkeys/switch-a/internal/requestingress/semantic"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"go.uber.org/zap"
)

var errUpstreamInputFinished = errors.New("upstream no longer consumes request input")

type requestIngressFailure struct {
	cause error
	kind  requestingress.FailureKind
}

func (e *requestIngressFailure) Error() string { return "gateway request ingress: " + e.cause.Error() }
func (e *requestIngressFailure) Unwrap() error { return e.cause }

type requestFacts struct {
	done   chan struct{}
	mu     sync.Mutex
	result semantic.Result
}

func (h *Handler) serveHTTPIngress(w http.ResponseWriter, r *http.Request, cfg *runtimeConfig, apiType, requestID string, startTime time.Time) {
	pendingReasoning := model.ReasoningObservationPending
	ctx, operation := clientconnection.Begin(r, w)
	defer operation.Close()
	originalRequest := r
	r = r.WithContext(ctx)
	pctx := &proxyContext{
		handler: h, r: r, w: w, cfg: cfg, transport: h.getTransport(cfg), apiType: apiType,
		startTime: startTime, requestID: requestID, operation: operation,
		liveBytes: &LiveBytesTracker{},
		info: RequestInfo{
			ClientIP: ExtractClientIP(r, cfg.trustProxy), UserID: ExtractUserID(r, cfg.userHeader),
			Model: requestHeadModel(r, apiType), APIType: apiType, Path: r.URL.Path, Method: r.Method,
			UserAgent: ExtractUserAgent(r), RequestID: ExtractRequestIDHeader(r),
			ContentType: ExtractContentType(r),
			Reasoning:   model.RequestedReasoningObservation{State: &pendingReasoning},
		},
	}
	pctx.capture = h.beginGatewayCapture(requestID, startTime)
	pctx.captureParticipates = pctx.capture.Valid()
	defer func() { pctx.capture.Finish(gatewayCaptureOutcome(ctx)) }()
	pctx.selectReq = &model.SelectRequest{OperationID: requestID, ClientIP: pctx.info.ClientIP, User: pctx.info.UserID,
		APIType: apiType, Model: pctx.info.Model, StickyMode: cfg.stickyMode}
	needsModel, err := selector.PrepareAdmission(ctx, h.store, pctx.selectReq)
	if err != nil {
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to prepare request admission")
		return
	}
	// Enabling duplex precedes both pump reads and response writes. Recorders used
	// by unit tests have no protocol machinery and safely report unsupported.
	if err := operation.EnableDuplex(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to enable request duplex")
		return
	}
	ingress, err := h.startIngress(ctx, originalRequest, requestingress.Options{
		MaxBodyBytes: requestBodyLimitBytes(cfg.maxBodySizeMB), OperationID: requestID,
		TrailerSnapshot: func() http.Header {
			if trailers, ok := h2ingress.Trailers(originalRequest); ok {
				return trailers
			}
			return originalRequest.Trailer.Clone()
		},
		Interrupt: operation.Interrupt, OnHead: pctx.beginCaptureIngress, OnChunk: pctx.observeCaptureIngressChunk,
		OnFinish: pctx.finishCaptureIngress,
		OnFailure: func(snapshot requestingress.Snapshot) {
			pctx.failCaptureIngress(snapshot)
			operation.Cancel(&requestIngressFailure{cause: snapshot.Err, kind: snapshot.FailureKind})
		},
		Trace: func(event requestingress.Event) {
			h.logger.Debug("request_ingress."+event.Name, zap.String("operation_id", requestID),
				zap.String("attempt_id", event.AttemptID), zap.String("source_state", string(event.State)),
				zap.Int64("received_bytes", event.ReceivedBytes), zap.Int64("memory_bytes", event.MemoryBytes),
				zap.Int64("disk_bytes", event.DiskBytes), zap.Int64("reader_bytes", event.ReaderBytes), zap.Error(event.Err))
		},
	})
	if err != nil {
		h.handleBodyError(w, err, cfg.maxBodySizeMB)
		return
	}
	pctx.ingress = ingress
	defer ingress.Close()
	pctx.upload = &ingressUpload{ingress: ingress, tracker: pctx.liveBytes}
	pctx.facts = h.projectRequest(ctx, pctx)
	needsCodexEvidence := h.codexHTTP.RequiresClientEvidence(apiType, ingress.Head().HasBody)
	if needsModel || needsCodexEvidence {
		select {
		case <-pctx.facts.done:
		case <-ctx.Done():
			h.handleIngressAdmissionError(w, pctx)
			return
		}
		if snapshot := ingress.Snapshot(); snapshot.State == requestingress.Failed {
			h.handleBodyError(w, snapshot.Err, cfg.maxBodySizeMB)
			return
		}
		pctx.applyRequestFacts()
	}
	evidence := codexheaders.ClientEvidence{}
	if needsCodexEvidence {
		evidence = pctx.facts.snapshot().Codex.Value
	}
	codexOperation, err := h.codexHTTP.Begin(ctx, r, apiType, requestID, pctx.cfg.ConversationRecoveryPolicy, evidence)
	if err != nil {
		h.handleCodexHTTPBeginError(w, requestID, err)
		return
	}
	pctx.codex = codexOperation
	defer codexOperation.Discard()
	pctx.selectReq.Model = pctx.info.Model
	pctx.selectReq.ClientScope = codexOperation.ClientScope()
	h.syncCodexSelectionConstraints(pctx)
	h.logger.Debug("request_ingress.admission-ready", zap.String("operation_id", requestID),
		zap.Bool("model_required", needsModel), zap.String("model", pctx.info.Model),
		zap.String("conversation_recovery_policy", string(pctx.cfg.ConversationRecoveryPolicy)),
		zap.String("source_state", string(ingress.Snapshot().State)))
	h.executeProxy(ctx, pctx)
}

func (h *Handler) handleIngressAdmissionError(w http.ResponseWriter, pctx *proxyContext) {
	snapshot := pctx.ingress.Snapshot()
	if snapshot.State == requestingress.Failed {
		h.handleBodyError(w, snapshot.Err, pctx.cfg.maxBodySizeMB)
		return
	}
	if !classifyClientTermination(pctx.r.Context()).observed() {
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Request admission interrupted")
	}
}

func (h *Handler) projectRequest(ctx context.Context, pctx *proxyContext) *requestFacts {
	facts := &requestFacts{done: make(chan struct{})}
	reader, err := pctx.ingress.Open()
	if err != nil {
		close(facts.done)
		return facts
	}
	options := semantic.Options{ContentEncodingValues: pctx.r.Header.Values("Content-Encoding"),
		MaxDecodedBytes: requestBodyLimitBytes(pctx.cfg.maxBodySizeMB), ReasoningContract: requestReasoningContract(pctx.apiType, pctx.r.URL.Path)}
	go func() {
		stop := context.AfterFunc(ctx, func() { _ = reader.Close() })
		result := semantic.Project(ctx, reader, options)
		h.recordRequestProjection(pctx.requestID, pctx.apiType, pctx.r, result)
		stop()
		_ = reader.Close()
		facts.mu.Lock()
		facts.result = result
		facts.mu.Unlock()
		close(facts.done)
		if h.activeRegistry != nil {
			modelName := result.Model.Value
			if pctx.apiType == APITypeGemini {
				modelName = requestHeadModel(pctx.r, pctx.apiType)
			}
			h.activeRegistry.UpdateObservation(pctx.requestID, modelName, result.Reasoning.Value)
		}
	}()
	return facts
}

func (f *requestFacts) snapshot() semantic.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.result
}

func requestReasoningContract(apiType, path string) semantic.ReasoningContract {
	if namespace, contract, ok := SplitAPINamespace(path); ok && namespace == apiType {
		path = contract
	}
	if !supportsReasoningObservation(apiType, path) {
		return semantic.ReasoningUnsupported
	}
	if apiType == APITypeCodex {
		return semantic.ReasoningCodex
	}
	if isClaudeCompatibleAPIType(apiType) {
		return semantic.ReasoningClaude
	}
	return semantic.ReasoningChat
}

func (pctx *proxyContext) applyRequestFacts() {
	if pctx.facts == nil {
		return
	}
	result := pctx.facts.snapshot()
	if pctx.apiType != APITypeGemini && result.Model.Value != "" {
		pctx.info.Model = result.Model.Value
	}
	pctx.info.Reasoning = result.Reasoning.Value
}

func (pctx *proxyContext) publishRequestObservation() {
	if pctx.facts == nil || pctx.handler.activeRegistry == nil {
		return
	}
	select {
	case <-pctx.facts.done:
		result := pctx.facts.snapshot()
		modelName := result.Model.Value
		if pctx.apiType == APITypeGemini {
			modelName = requestHeadModel(pctx.r, pctx.apiType)
		}
		pctx.handler.activeRegistry.UpdateObservation(pctx.requestID, modelName, result.Reasoning.Value)
	default:
	}
}

func (pctx *proxyContext) closeRetryWindow() {
	pctx.responseCommitted.Store(true)
	if pctx.upload != nil {
		pctx.upload.closeRetryWindow()
	}
}

func (pctx *proxyContext) finishIngress() {
	if pctx.ingress == nil {
		return
	}
	if pctx.ingress.Snapshot().State == requestingress.Receiving {
		pctx.ingress.Abort(errUpstreamInputFinished)
	}
	if pctx.facts != nil {
		<-pctx.facts.done
		pctx.applyRequestFacts()
	}
}

func (pctx *proxyContext) receivedRequestBytes() int64 {
	if pctx.ingress == nil {
		return 0
	}
	return pctx.ingress.Snapshot().ReceivedBytes
}

func (pctx *proxyContext) requestBodySnippet() string {
	if pctx.ingress == nil {
		return ""
	}
	return GetReqBodySnippet(pctx.ingress.Prefix(MaxReqBodySnippetLength + 1))
}

func (pctx *proxyContext) ingressFailure() error {
	if pctx.ingress == nil {
		return nil
	}
	snapshot := pctx.ingress.Snapshot()
	if snapshot.State == requestingress.Failed {
		return &requestIngressFailure{cause: snapshot.Err, kind: snapshot.FailureKind}
	}
	return nil
}

func supportsReasoningObservation(apiType, path string) bool {
	if isClaudeCompatibleAPIType(apiType) && path == RouteClaudeMessages {
		return true
	}
	if apiType == APITypeCodex && (path == RouteCodexResponses ||
		path == RouteCodexResponsesV1 ||
		path == RouteCodexWebSearch ||
		path == RouteCodexWebSearchV1) {
		return true
	}
	if isOpenAIChatCompletionsAPIType(apiType) && (path == RouteGrokChatCompletions || path == RouteGrokChatCompletionsV1) {
		return true
	}
	return false
}

func (h *Handler) recordRequestProjection(requestID, apiType string, request *http.Request, result semantic.Result) {
	h.logger.Debug("request_ingress.facts-completed",
		zap.String("operation_id", requestID),
		zap.String("model_state", string(result.Model.State)), zap.String("model_reason", result.Model.Reason),
		zap.String("reasoning_state", string(result.Reasoning.State)), zap.String("reasoning_reason", result.Reasoning.Reason),
		zap.String("codex_state", string(result.Codex.State)), zap.String("codex_reason", result.Codex.Reason),
		zap.Int64("decoded_bytes", result.DecodedBytes))
	switch result.Model.Reason {
	case semantic.ReasonInvalidLimit, semantic.ReasonInvalidContentEncoding, semantic.ReasonUnsupportedContentEncoding, semantic.ReasonContentDecoding, semantic.ReasonDecodedBodyTooLarge:
		contentEncodings := request.Header.Values(contentEncodingHeader)
		h.logger.Warn("request body semantic decoding failed",
			zap.String("request_id", requestID), zap.String("operation_id", requestID),
			zap.String("api_type", apiType), zap.String("path", request.URL.Path),
			zap.String("content_encoding", normalizedHTTPContentCodings(contentEncodings)),
			zap.Int("content_encoding_value_count", len(contentEncodings)),
			zap.String("decode_failure", result.Model.Reason))
	}
}
