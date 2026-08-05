package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturebridge"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturefailure"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type httpCaptureExchange struct {
	recorder           requestcapture.Recorder
	mode               capturebridge.Mode
	responseObserver   capturebridge.HTTPBodyObservation
	sensitiveHeaders   requestcapture.SensitiveHeaderEvidence
	credentialEvidence requestcapture.CredentialEvidence
	completedAt        time.Time
}

func captureCredentialMaterial(headers http.Header) (
	requestcapture.SensitiveHeaderEvidence,
	requestcapture.CredentialEvidence,
) {
	return capturebridge.CredentialMaterial(headers)
}

func (h *Handler) captureHTTPPreparationFailure(
	ctx context.Context,
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
	request *http.Request,
	failureCode requestcapture.FailureCode,
	err error,
) {
	if !pctx.captureParticipates {
		return
	}
	var target requestcapture.TransitionTargetInput
	var credentialEvidence requestcapture.CredentialEvidence
	if request != nil {
		target = requestcapture.HTTPTransitionTarget(request.URL)
		_, credentialEvidence = captureCredentialMaterial(request.Header)
	}
	reason, failure := capturefailure.HTTPPreparation(contextError(ctx), err, failureCode)
	pctx.capture.Transition(requestcapture.TransitionStart{
		Attempt: attempt.metadata(pctx.apiType, phase), Target: target,
		TerminationReason: reason, Failure: failure, CredentialEvidence: credentialEvidence,
	})
}

func (pctx *proxyContext) finishHTTPCaptureExchange(
	exchange httpCaptureExchange,
	trailers http.Header,
	sourceCompletion requestcapture.SourceCompletion,
	reason requestcapture.TerminationReason,
	failure requestcapture.FailureObservation,
) {
	if pctx == nil || !exchange.valid() {
		return
	}
	if exchange.completedAt.IsZero() {
		exchange.completedAt = time.Now()
	}
	exchange.finish(trailers, sourceCompletion, reason, failure)
}

func finishHTTPFetchFailure(ctx context.Context, pctx *proxyContext, exchange httpCaptureExchange, err error) {
	if !exchange.valid() {
		return
	}
	exchange.completedAt = time.Now()
	reason, failure := capturefailure.HTTPFetch(contextError(ctx), err)
	pctx.finishHTTPCaptureExchange(
		exchange, nil, requestcapture.SourceCompletionPartial, reason, failure,
	)
}

func (p *pendingHTTPResponse) finishCapture(completion responseanalysis.Completion) {
	p.exchange.completedAt = time.Now()
	sourceCompletion := requestcapture.SourceCompletionPartial
	if p.exchange.sourceCompletionAfterError() == requestcapture.SourceCompletionComplete {
		sourceCompletion = requestcapture.SourceCompletionComplete
	}
	reason := requestcapture.TerminationReasonEOF
	failure := requestcapture.FailureObservation{}
	switch {
	case completion.Termination == responseanalysis.TerminationDiscarded && completion.HasSemanticObservation:
		// A late semantic match was deliberately absorbed after the stream had
		// become visible. Marking it as committed would tell capture consumers that
		// the provider error was intentionally delivered, which is the opposite of
		// the client-retry contract.
		reason = requestcapture.TerminationReasonInternalErrorAbsorbed
		failure = semanticCaptureFailure()
	case completion.HasSemanticObservation:
		reason = requestcapture.TerminationReasonInternalErrorCommitted
		failure = semanticCaptureFailure()
	default:
		switch completion.Termination {
		case responseanalysis.TerminationClientCancelled:
			reason = requestcapture.TerminationReasonClientDisconnect
			cancelErr := contextError(p.pctx.r.Context())
			if cancelErr == nil {
				cancelErr = context.Canceled
			}
			_, failure = capturefailure.HTTPForward(
				contextError(p.pctx.r.Context()), cancelErr, capturefailure.HTTPForwardOriginClientCancel,
			)
		case responseanalysis.TerminationClientWriteFailure:
			reason = requestcapture.TerminationReasonWriteError
			_, failure = capturefailure.HTTPForward(
				contextError(p.pctx.r.Context()), errors.New("client response write failed"),
				capturefailure.HTTPForwardOriginClientWrite,
			)
		case responseanalysis.TerminationUpstreamReadFailure:
			if completion.ReadTermination == responseanalysis.ReadTerminationIdleTimeout {
				reason = requestcapture.TerminationReasonTimeout
				_, failure = capturefailure.HTTPForward(
					contextError(p.pctx.r.Context()), ErrReadTimeout,
					capturefailure.HTTPForwardOriginReadTimeout,
				)
			} else {
				reason = requestcapture.TerminationReasonReadError
				_, failure = capturefailure.HTTPForward(
					contextError(p.pctx.r.Context()), errors.New("upstream response read failed"),
					capturefailure.HTTPForwardOriginUpstreamRead,
				)
			}
		case responseanalysis.TerminationInternalFailure:
			reason = requestcapture.TerminationReasonTransportError
			failure = capturefailure.Observation(capturefailure.Fact(
				requestcapture.FailureSiteResponseRead,
				requestcapture.FailurePeerGateway,
				requestcapture.FailureClassProtocol,
				requestcapture.FailureCodeGatewayContext,
			), requestcapture.FailureFact{})
		}
	}
	p.pctx.finishHTTPCaptureExchange(
		p.exchange, p.head.Trailer, sourceCompletion, reason, failure,
	)
}

func statusCaptureFailure(statusCode int) requestcapture.FailureObservation {
	return capturefailure.Observation(capturefailure.HTTPStatus(
		requestcapture.FailureSiteResponseStatus,
		requestcapture.FailurePeerUpstream,
		statusCode,
	), requestcapture.FailureFact{})
}

func semanticCaptureFailure() requestcapture.FailureObservation {
	return capturefailure.Observation(capturefailure.Fact(
		requestcapture.FailureSiteResponseRead,
		requestcapture.FailurePeerProvider,
		requestcapture.FailureClassUpstreamSemantic,
		requestcapture.FailureCodeProviderSemantic,
	), requestcapture.FailureFact{})
}

func (e httpCaptureExchange) valid() bool {
	return e.mode.Participates()
}

func (e *httpCaptureExchange) observeResponse(
	head upstreamtransport.ResponseHead,
	body io.ReadCloser,
) io.ReadCloser {
	if !e.mode.CapturesPayload() {
		return body
	}
	responseSensitiveHeaders, responseCredentialEvidence := captureCredentialMaterial(head.SourceHeader)
	e.sensitiveHeaders.Merge(responseSensitiveHeaders)
	e.sensitiveHeaders.Seal()
	e.credentialEvidence.Merge(responseCredentialEvidence)
	e.credentialEvidence.Seal()

	e.recorder.ObserveResponse(requestcapture.HTTPResponseHead{
		StatusCode:         head.StatusCode,
		Protocol:           head.Protocol,
		Headers:            head.SourceHeader,
		ContentLength:      head.ContentLength,
		DeclaredTrailers:   head.Trailer,
		SensitiveHeaders:   e.sensitiveHeaders,
		CredentialEvidence: e.credentialEvidence,
	})
	if body != nil {
		body, e.responseObserver = capturebridge.WrapHTTPResponseBody(body, e.recorder, head.ContentLength)
	}
	return body
}

func (e httpCaptureExchange) sourceCompletionAfterError() requestcapture.SourceCompletion {
	if e.responseObserver.SourceComplete() {
		return requestcapture.SourceCompletionComplete
	}
	return requestcapture.SourceCompletionPartial
}

func (e httpCaptureExchange) observeClientWrite(bytes int) {
	e.recorder.ObserveClientWrite(bytes)
}

func (e httpCaptureExchange) finish(
	trailers http.Header,
	sourceCompletion requestcapture.SourceCompletion,
	reason requestcapture.TerminationReason,
	failure requestcapture.FailureObservation,
) {
	if !e.valid() {
		return
	}

	outcome := requestcapture.Outcome{
		SourceCompletion:  sourceCompletion,
		TerminationReason: reason,
		Failure:           failure,
		CompletedAt:       e.completedAt,
	}
	if e.mode.CapturesPayload() {
		_, trailerCredentialEvidence := captureCredentialMaterial(trailers)
		e.credentialEvidence.Merge(trailerCredentialEvidence)
		e.credentialEvidence.Seal()
		outcome.ResponseTrailers = trailers
		outcome.CredentialEvidence = e.credentialEvidence
	}
	e.recorder.Finish(outcome)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (h *Handler) beginHTTPExchange(
	pctx *proxyContext,
	attempt httpAttemptContext,
	phase requestcapture.CredentialPhase,
	request *http.Request,
) httpCaptureExchange {
	if !pctx.captureParticipates {
		return httpCaptureExchange{}
	}

	sensitiveHeaders, credentialEvidence := captureCredentialMaterial(request.Header)
	recorder := pctx.capture.BeginHTTP(requestcapture.RawHTTPStart{
		Attempt: attempt.metadata(pctx.apiType, phase),
		URL:     request.URL,
		Request: requestcapture.RawRequest{
			Method:             request.Method,
			Headers:            request.Header,
			ContentLength:      request.ContentLength,
			Trailers:           request.Trailer,
			Body:               pctx.body,
			SensitiveHeaders:   sensitiveHeaders,
			CredentialEvidence: credentialEvidence,
		},
	})
	return httpCaptureExchange{
		recorder:           recorder,
		mode:               capturebridge.ModeForRecorder(recorder),
		sensitiveHeaders:   sensitiveHeaders,
		credentialEvidence: credentialEvidence,
	}
}
