package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responsefacts"
)

type factsPartialWriter struct{ *httptest.ResponseRecorder }

func (w *factsPartialWriter) Write([]byte) (int, error) { return 3, io.ErrClosedPipe }

func TestHTTPCompletionWriteAndDisconnectAreIndependent(t *testing.T) {
	observed := &responsefacts.CompletionObserver{}
	observed.Observe(responsefacts.ResponseCompleted)
	writer := &firstWriteResponseWriter{ResponseWriter: &factsPartialWriter{httptest.NewRecorder()}}
	if n, err := writer.Write([]byte("response.completed")); n != 3 || err != io.ErrClosedPipe {
		t.Fatalf("write=%d,%v", n, err)
	}
	facts := nonWebSocketRuntimeFacts{
		UpstreamCompletion: observed.Snapshot(), DownstreamWrite: writer.downstreamWrite,
		ClientTransportStatusCode: http.StatusOK, ResponseCommitted: true, ServiceStarted: true,
		ClientTermination: clientTerminationDisconnect, TerminalErr: io.ErrClosedPipe,
		IsSSE: true, FirstByteVisible: true, IsClientWriteError: true,
	}
	assessment := assessNonWebSocketRequest(facts)
	if assessment.ServiceOutcome != model.ServiceOutcomeCompleted || assessment.CompletionState != model.CompletionStateCompleted ||
		assessment.TerminationReason == nil || *assessment.TerminationReason != model.TerminationReasonClientDisconnect {
		t.Fatalf("disconnect erased upstream completion: %+v", assessment)
	}
	for _, encoded := range []*string{buildNonWebSocketSessionEvidence(facts), buildNonWebSocketAttemptEvidence(facts)} {
		var evidence nonWebSocketEvidence
		if encoded == nil {
			t.Fatal("missing facts")
		}
		if err := json.Unmarshal([]byte(*encoded), &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.UpstreamCompletion == nil || evidence.UpstreamCompletion.EventType != responsefacts.ResponseCompleted ||
			evidence.DownstreamWrite == nil || evidence.DownstreamWrite.FailedCalls != 1 || evidence.DownstreamWrite.ConfirmedBytes != 3 ||
			evidence.Transport == nil || evidence.Transport.Source != "client" {
			t.Fatalf("incomplete evidence: %+v", evidence)
		}
	}
}

func TestHTTPWriteSuccessDoesNotInventUpstreamCompletionEvent(t *testing.T) {
	writer := &firstWriteResponseWriter{ResponseWriter: httptest.NewRecorder()}
	_, _ = writer.Write([]byte("data: partial\n\n"))
	facts := nonWebSocketRuntimeFacts{
		DownstreamWrite: writer.downstreamWrite, ServiceStarted: true, ResponseCommitted: true,
		ClientTermination: clientTerminationDisconnect,
	}
	assessment := assessNonWebSocketRequest(facts)
	if assessment.ServiceOutcome != model.ServiceOutcomeUnknown {
		t.Fatal(assessment)
	}
	encoded := buildNonWebSocketSessionEvidence(facts)
	var evidence nonWebSocketEvidence
	if encoded == nil {
		t.Fatal("successful writes still need recorded facts")
	}
	if err := json.Unmarshal([]byte(*encoded), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.UpstreamCompletion != nil || evidence.DownstreamWrite.SuccessfulCalls != 1 {
		t.Fatal(evidence)
	}
}
