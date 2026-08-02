package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/proxy/capturebridge"
	"github.com/doraemonkeys/switch-a/internal/proxy/capturefailure"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

type httpCaptureExchange struct {
	recorder           requestcapture.Recorder
	mode               capturebridge.Mode
	responseObserver   *capturebridge.HTTPBodyObserver
	sensitiveHeaders   requestcapture.SensitiveHeaderEvidence
	credentialEvidence requestcapture.CredentialEvidence
	completedAt        time.Time
}

type httpCaptureCompletion struct {
	exchange         httpCaptureExchange
	response         *UpstreamResponse
	sourceCompletion requestcapture.SourceCompletion
	reason           requestcapture.TerminationReason
	failure          requestcapture.FailureObservation
}

func (pctx *proxyContext) queueHTTPCaptureCompletion(
	exchange httpCaptureExchange,
	response *UpstreamResponse,
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
	pctx.httpCaptureCompletions = append(pctx.httpCaptureCompletions, httpCaptureCompletion{
		exchange:         exchange,
		response:         response,
		sourceCompletion: sourceCompletion,
		reason:           reason,
		failure:          failure,
	})
}

func (pctx *proxyContext) finishHTTPCaptureCompletions() {
	if pctx == nil {
		return
	}
	for _, completion := range pctx.httpCaptureCompletions {
		completion.exchange.finish(
			completion.response,
			completion.sourceCompletion,
			completion.reason,
			completion.failure,
		)
	}
	pctx.httpCaptureCompletions = nil
}

func (e httpCaptureExchange) valid() bool {
	return e.mode.Participates()
}

func (e *httpCaptureExchange) observeResponse(resp *UpstreamResponse) {
	if !e.mode.CapturesPayload() {
		return
	}
	responseSensitiveHeaders, responseCredentialEvidence := captureCredentialMaterial(resp.Header)
	e.sensitiveHeaders.Merge(responseSensitiveHeaders)
	e.sensitiveHeaders.Seal()
	e.credentialEvidence.Merge(responseCredentialEvidence)
	e.credentialEvidence.Seal()

	e.recorder.ObserveResponse(requestcapture.HTTPResponseHead{
		StatusCode:         resp.StatusCode,
		Protocol:           resp.Protocol,
		Headers:            resp.Header,
		ContentLength:      resp.ContentLength,
		DeclaredTrailers:   resp.Trailer,
		SensitiveHeaders:   e.sensitiveHeaders,
		CredentialEvidence: e.credentialEvidence,
	})
	if resp.Body != nil {
		resp.Body, e.responseObserver = capturebridge.WrapHTTPResponseBody(resp.Body, e.recorder, resp.ContentLength)
	}
}

func (e httpCaptureExchange) sourceCompletionAfterError() requestcapture.SourceCompletion {
	if e.responseObserver != nil && e.responseObserver.SourceComplete() {
		return requestcapture.SourceCompletionComplete
	}
	return requestcapture.SourceCompletionPartial
}

func (e httpCaptureExchange) observeClientWrite(bytes int) {
	e.recorder.ObserveClientWrite(bytes)
}

func (e httpCaptureExchange) finish(
	resp *UpstreamResponse,
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
		var trailers http.Header
		if resp != nil {
			trailers = resp.Trailer
		}
		_, trailerCredentialEvidence := captureCredentialMaterial(trailers)
		e.credentialEvidence.Merge(trailerCredentialEvidence)
		e.credentialEvidence.Seal()
		outcome.ResponseTrailers = trailers
		outcome.CredentialEvidence = e.credentialEvidence
	}
	e.recorder.Finish(outcome)
}

func captureForwardFailure(
	ctx context.Context,
	err error,
	clientWriteErr error,
) (requestcapture.TerminationReason, requestcapture.FailureObservation) {
	origin := capturefailure.HTTPForwardOriginUpstreamRead
	failureError := err
	// Capture only inspects gateway-owned wrapper types directly. Widening the
	// operand prevents untrusted upstream errors from running As/Unwrap hooks.
	switch concrete := any(err).(type) {
	case *readTimeoutError, *sseIdleTimeoutError:
		origin = capturefailure.HTTPForwardOriginReadTimeout
	case *UpstreamReadError:
		if concrete != nil && concrete.Err != nil {
			failureError = concrete.Err
		}
	default:
		if clientWriteErr != nil {
			origin = capturefailure.HTTPForwardOriginClientWrite
			failureError = clientWriteErr
		}
	}
	// io.Copy does not preserve source-vs-destination error origin. The
	// downstream writer observation is therefore the bounded control-flow fact
	// that distinguishes an opaque upstream read from a client write failure.
	return capturefailure.HTTPForward(contextError(ctx), failureError, origin)
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
