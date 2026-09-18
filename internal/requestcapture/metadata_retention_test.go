package requestcapture

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

func TestFullMetadataSurvivesCaptureQueryAndExport(t *testing.T) {
	const sessionBudget = 16 << 20
	manager := newTestManager(t, func(cfg *Config) { cfg.ProcessCeilingBytes = 2 * sessionBudget })
	session := startTestSession(t, manager, 4, sessionBudget, "selected")
	token := strings.Repeat("credential-", 1024)
	headerValue := strings.Repeat("header-value-", 1024)
	diagnostic := strings.Repeat("诊断上下文 ", 2048)
	target := "https://example.test/path?context=" + strings.Repeat("q", 16<<10)
	headers := http.Header{"Authorization": {"Bearer " + token}, "X-Large": {headerValue}}
	for index := 0; index < 140; index++ {
		headers["X-Field-"+strconv.Itoa(index)] = []string{strings.Repeat("value-", 128)}
	}
	headers["X-Many"] = make([]string, 40)
	for index := range headers["X-Many"] {
		headers["X-Many"][index] = strconv.Itoa(index)
	}
	expectedHeaders := headers.Clone()
	expectedHeaders["Authorization"] = []string{"Bearer [REDACTED]"}
	requestPayload := bytes.Repeat([]byte("request-body "), 8192)
	responsePayload := bytes.Repeat([]byte("response-body "), 8192)
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "full-metadata"})
	recorder := gateway.BeginHTTP(RawHTTPStart{
		URL: testParsedURL(target),
		Attempt: AttemptMetadata{
			Provider: ProviderIdentity{ID: "selected"}, APIType: "chat",
			SelectionMode: SelectionModeInitial, SelectionSource: SelectionSourceStrategy,
			CredentialPhase: CredentialPhaseInitial,
		},
		Request: RawRequest{
			Method: http.MethodPost, Headers: headers, Body: requestPayload,
			ContentLength:      int64(len(requestPayload)),
			SensitiveHeaders:   testSensitiveHeaderEvidence(),
			CredentialEvidence: testCredentialEvidence(token),
		},
	})
	if !recorder.Valid() {
		t.Fatal("record rejected despite ample memory")
	}
	headers["X-Large"][0] = "caller mutation"
	recorder.ObserveResponse(HTTPResponseHead{
		StatusCode: http.StatusBadRequest, Protocol: "HTTP/2", Headers: http.Header{"X-Diagnostic": {diagnostic}},
		SensitiveHeaders: testSensitiveHeaderEvidence(), CredentialEvidence: testCredentialEvidence(token),
	})
	recorder.ObserveUpstream(responsePayload)
	recorder.ObserveClientWrite(len(responsePayload))
	failure := FailureObservation{Primary: FailureFact{
		Site: FailureSiteResponseStatus, Peer: FailurePeerProvider, Class: FailureClassUpstreamSemantic,
		Code: FailureCodeProviderSemantic, ProviderErrorType: strings.Repeat("type-", 100),
		ProviderErrorCode: strings.Repeat("code-", 100), Message: diagnostic + token,
	}}
	recorder.Finish(Outcome{SourceCompletion: SourceCompletionComplete, TerminationReason: TerminationReasonEOF,
		Failure: failure, ResponseTrailers: http.Header{"X-Trailer": {headerValue}}})
	gateway.Finish(GatewayOutcome{})
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.CaptureCompletion != CaptureCompletionComplete || detail.Summary.CaptureLosses != 0 ||
		detail.Summary.Failure.Truncated || detail.Summary.Failure.Primary.Message != diagnostic+"[REDACTED]" {
		t.Fatal("complete diagnostic capture was reported as incomplete")
	}
	if detail.HTTP.Request.URL != target || !reflect.DeepEqual(detail.HTTP.Request.Headers, map[string][]string(expectedHeaders)) ||
		detail.HTTP.Response.Headers["X-Diagnostic"][0] != diagnostic ||
		detail.HTTP.Response.Trailers["X-Trailer"][0] != headerValue {
		t.Fatal("query changed retained metadata")
	}
	if status := manager.Status(); status.Session.IncompleteRecordCount != 0 || status.Session.RetainedBytes >= sessionBudget {
		t.Fatal("capture exceeded available budget or reported loss")
	}
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := download.WriteTo(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	lines := decodeExportLines(t, output.Bytes(), manager.cfg.exportLineBytes)
	exported := decodeRecordMetadata(t, lines, 0)
	if !reflect.DeepEqual(exported.Request.Headers, detail.HTTP.Request.Headers) ||
		!reflect.DeepEqual(exported.HTTP.Response, detail.HTTP.Response) || !reflect.DeepEqual(exported.Summary, detail.Summary) {
		t.Fatal("export metadata differs from query")
	}
	assertExportBlob(t, lines, 0, exported, requestBodyBlobID, requestPayload)
	assertExportBlob(t, lines, 0, exported, responseBodyBlobID, responsePayload)
}

func TestCaptureLossReasonsRemainIndependentOfSourceCompletion(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 4, 1<<20, "selected")
	gateway, recorder := beginTestHTTP(manager, "loss-reasons", "selected", nil)
	recorder.ObserveResponse(HTTPResponseHead{
		StatusCode: http.StatusOK, SensitiveHeaders: testSensitiveHeaderEvidence(),
		CredentialEvidence: testCredentialEvidence(),
	})
	restore := constrainAdditionalCapacity(manager.active.Load(), 0)
	recorder.ObserveUpstream([]byte("not retained"))
	restore()
	recorder.ObserveClientWrite(len("not retained"))
	recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
	recorder.Finish(Outcome{SourceCompletion: SourceCompletionComplete, TerminationReason: TerminationReasonEOF})
	gateway.Finish(GatewayOutcome{})
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	expected := capturevalue.CaptureLossMemoryBudget | capturevalue.CaptureLossRecorderFault
	if detail.Summary.CaptureLosses != expected || detail.Summary.CaptureCompletion != CaptureCompletionIncomplete ||
		detail.Summary.SourceCompletion != SourceCompletionComplete || detail.Summary.TerminationReason != TerminationReasonEOF {
		t.Fatalf("capture/source facts: %+v", detail.Summary)
	}
	if manager.Status().Session.IncompleteRecordCount != 1 {
		t.Fatal("multiple reasons counted the same record twice")
	}
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := download.WriteTo(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	exported := decodeRecordMetadata(t, decodeExportLines(t, output.Bytes(), manager.cfg.exportLineBytes), 0)
	if exported.Summary.CaptureLosses != expected {
		t.Fatal("export lost capture failure reasons")
	}
}
