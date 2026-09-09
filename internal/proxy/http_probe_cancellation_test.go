package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/responsefacts"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"go.uber.org/zap"
)

type probeCancellationBody struct {
	reader    *strings.Reader
	blocked   chan struct{}
	closed    chan struct{}
	blockOnce sync.Once
	closeOnce sync.Once
}

func (b *probeCancellationBody) Read(p []byte) (int, error) {
	if b.reader.Len() > 0 {
		return b.reader.Read(p)
	}
	b.blockOnce.Do(func() { close(b.blocked) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *probeCancellationBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestCancelledProbeSettlesActualResponseFacts(t *testing.T) {
	for _, test := range []struct {
		name       string
		deadline   bool
		forwarding bool
	}{
		{name: "probe/disconnect"},
		{name: "probe/deadline", deadline: true},
		{name: "forwarding/disconnect", forwarding: true},
		{name: "forwarding/deadline", forwarding: true, deadline: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := captureTestProvider("https://provider.invalid")
			manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{ID: provider.ID, Name: provider.Name}})
			defer manager.Close()
			gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: test.name})
			budget, err := responseanalysis.NewDefaultProcessMemoryBudget()
			if err != nil {
				t.Fatal(err)
			}
			analyzer, err := responseanalysis.NewAnalyzer(responseanalysis.NewRegistry(), budget, responseanalysis.AnalyzerOptions{ProbeDuration: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			mode := responseanalysis.ProbeMode()
			if test.forwarding {
				mode, err = responseanalysis.ObserveMode(responseanalysis.BoundaryNoRetryCandidate)
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			if test.deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			defer cancel()
			// Control events fill the probe without crossing its visibility or memory boundary.
			payload := strings.Repeat(": ping\n\n", 1024)
			body := &probeCancellationBody{reader: strings.NewReader(payload), blocked: make(chan struct{}), closed: make(chan struct{})}
			defer body.Close()
			recorder := httptest.NewRecorder()
			writer := &firstWriteResponseWriter{ResponseWriter: recorder}
			completion := new(responsefacts.CompletionObserver)
			handler := &Handler{logger: zap.NewNop()}
			pctx := &proxyContext{
				cfg: &runtimeConfig{}, apiType: APITypeCodex,
				r:       httptest.NewRequest(http.MethodPost, "/responses", nil).WithContext(ctx),
				handler: handler, startTime: time.Now(), capture: gateway, captureParticipates: true,
			}
			initializeTestCaptureIngress(t, pctx, []byte("{}"))
			exchange := handler.beginHTTPExchange(pctx, httpAttemptContext{
				provider: &provider, selectionMode: requestcapture.SelectionModeInitial,
				selectionSource: requestcapture.SelectionSourceStrategy,
			}, requestcapture.CredentialPhaseInitial, pctx.r, "")
			uploadClosed := false
			pending := &pendingHTTPResponse{
				head:   upstreamtransport.ResponseHead{StatusCode: http.StatusOK},
				media:  responseanalysis.ResolveResponseMedia("text/event-stream", nil),
				writer: writer, upstreamCompletion: completion, pctx: pctx, exchange: exchange, analysisStartedAt: time.Now(),
				closeUpload: func() error { uploadClosed = true; return nil },
				pending: analyzer.Start(ctx, responseanalysis.StartInput{
					Mode: mode, APIType: APITypeCodex, ContentType: "text/event-stream", StatusCode: http.StatusOK,
					Body: body, Writer: writer, ObserveCompletion: completion.Observe,
				}),
			}
			select {
			case <-body.blocked:
			case <-time.After(5 * time.Second):
				t.Fatal("response body was not consumed")
			}
			if !test.deadline {
				cancel()
			}
			result, again := handler.resolvePendingResponse(ctx, pctx, &retryState{currentProvider: &provider}, pending)
			wantTermination, wantErr := clientTerminationDisconnect, context.Canceled
			wantKind, wantSignal := transportKindDisconnect, transportSignalCanceled
			wantCaptureReason := requestcapture.TerminationReasonClientDisconnect
			if test.deadline {
				wantTermination, wantErr = clientTerminationTimeout, context.DeadlineExceeded
				wantKind, wantSignal = transportKindTimeout, transportSignalTimeout
				wantCaptureReason = requestcapture.TerminationReasonTimeout
			}
			if again || result.failureKind != attemptFailureClientTerminated || result.clientTermination != wantTermination {
				t.Fatalf("incorrect termination: again=%v result=%+v", again, result)
			}
			if strings.Contains(result.failureMessage, "forwarding capability") || !errors.Is(result.terminalError(), wantErr) {
				t.Errorf("cancellation became an internal error: %v", result.terminalError())
			}
			wantWritten, wantStage := int64(0), transportStagePreConnectionVisible
			if test.forwarding {
				wantWritten, wantStage = int64(len(payload)), transportStagePostPayloadVisible
			}
			if result.upstreamBytes != int64(len(payload)) || result.responseBytes != wantWritten ||
				result.responseCommitted != test.forwarding || !result.done {
				t.Errorf("settled response facts lost: upstream=%d downstream=%d committed=%v done=%v",
					result.upstreamBytes, result.responseBytes, result.responseCommitted, result.done)
			}
			if int64(recorder.Body.Len()) != wantWritten || result.DownstreamWrite.ConfirmedBytes != wantWritten ||
				result.UpstreamCompletion.EventType != "" {
				t.Error("incorrect response write or completion facts")
			}
			if !uploadClosed || budget.Used() != 0 {
				t.Errorf("resources still held: uploadClosed=%v memory=%d", uploadClosed, budget.Used())
			}
			if !result.healthAvailable || result.health.Verdict != errorrule.HealthNeutral ||
				result.health.Cause != errorrule.HealthCauseClientCancelled {
				t.Errorf("client cancellation affected provider health: %+v", result.health)
			}
			encoded := buildNonWebSocketAttemptEvidence(attemptFactsFromForwardResult(ctx, result))
			var evidence nonWebSocketEvidence
			if encoded == nil {
				t.Fatal("missing cancellation evidence")
			}
			if err := json.Unmarshal([]byte(*encoded), &evidence); err != nil {
				t.Fatal(err)
			}
			if evidence.Transport == nil || evidence.Transport.Source != transportSourceClient ||
				evidence.Transport.Kind != wantKind || evidence.Transport.Signal != wantSignal || evidence.Transport.Stage != wantStage {
				t.Errorf("incorrect transport attribution: %+v", evidence.Transport)
			}
			gateway.Finish(requestcapture.GatewayOutcome{})
			page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 1 {
				t.Fatalf("capture did not finish the response: %+v", page.Records)
			}
			record := page.Records[0]
			if !record.HasFailure || record.Failure.Primary.Peer != requestcapture.FailurePeerClient ||
				record.TerminationReason != wantCaptureReason || record.SourceCompletion != requestcapture.SourceCompletionPartial {
				t.Errorf("incorrect capture cancellation facts: %+v", record)
			}
		})
	}
}

func TestResponseTerminationEvidenceKeepsObservedOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name   string
		result forwardResult
		source string
		signal string
	}{
		{name: "upstream read", result: forwardResult{failureKind: attemptFailureRead},
			source: transportSourceUpstream, signal: transportSignalUpstreamReadError},
		{name: "upstream idle", result: forwardResult{failureKind: attemptFailureRead, readTermination: responseanalysis.ReadTerminationIdleTimeout},
			source: transportSourceUpstream, signal: transportSignalSSEIdleTimeout},
		{name: "client write", result: forwardResult{failureKind: attemptFailureWrite, isClientWriteError: true},
			source: transportSourceClient, signal: transportSignalClientWriteError},
		{name: "internal failure", result: forwardResult{failureKind: attemptFailureInternal},
			source: transportSourceUpstream, signal: transportSignalUnknownTransport},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := test.result
			result.isSSE, result.failureMessage, result.clientTermination = true, "observed failure", clientTerminationDisconnect
			diagnostic := deriveNonWebSocketTransportDiagnostic(attemptFactsFromForwardResult(ctx, result))
			if diagnostic == nil || diagnostic.Source != test.source || diagnostic.Signal != test.signal {
				t.Fatalf("later cancellation replaced the observed error: %+v", diagnostic)
			}
			if errors.Is(result.terminalError(), context.Canceled) {
				t.Fatal("non-cancellation failure acquired cancellation type")
			}
		})
	}
}
