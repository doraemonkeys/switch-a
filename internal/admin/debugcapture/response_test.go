package debugcapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type queryJSONLeaseFunc func(context.Context, io.Writer) error

func (f queryJSONLeaseFunc) WriteJSON(ctx context.Context, destination io.Writer) error {
	return f(ctx, destination)
}

type errorResponseWriter struct {
	header http.Header
	err    error
}

func (w *errorResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*errorResponseWriter) WriteHeader(int) {}

func (w *errorResponseWriter) Write([]byte) (int, error) { return 0, w.err }

func TestServiceErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "validation", err: &requestcapture.ValidationError{Field: "quota", Reason: "too large"}, wantStatus: http.StatusBadRequest, wantCode: errorCodeValidation},
		{name: "session active", err: requestcapture.ErrSessionActive, wantStatus: http.StatusConflict, wantCode: errorCodeSessionActive},
		{name: "no active session", err: requestcapture.ErrNoActiveSession, wantStatus: http.StatusNotFound, wantCode: errorCodeSessionNotFound},
		{name: "session mismatch", err: requestcapture.ErrSessionMismatch, wantStatus: http.StatusNotFound, wantCode: errorCodeSessionNotFound},
		{name: "query canceled", err: requestcapture.ErrQueryCanceled, wantStatus: http.StatusNotFound, wantCode: errorCodeSessionNotFound},
		{name: "cursor", err: requestcapture.ErrInvalidCursor, wantStatus: http.StatusBadRequest, wantCode: errorCodeValidation},
		{name: "record missing", err: requestcapture.ErrRecordNotFound, wantStatus: http.StatusNotFound, wantCode: errorCodeRecordNotFound},
		{name: "record evicted", err: requestcapture.ErrRecordEvicted, wantStatus: http.StatusGone, wantCode: errorCodeRecordEvicted},
		{name: "snapshot", err: requestcapture.ErrSnapshotChanged, wantStatus: http.StatusConflict, wantCode: errorCodeSnapshotChanged},
		{name: "export limit", err: requestcapture.ErrExportLimitReached, wantStatus: http.StatusTooManyRequests, wantCode: errorCodeExportLimit},
		{name: "capacity", err: requestcapture.ErrCapacityExceeded, wantStatus: http.StatusInsufficientStorage, wantCode: errorCodeCapacityExceeded},
		{name: "manager closed", err: requestcapture.ErrManagerClosed, wantStatus: http.StatusServiceUnavailable, wantCode: errorCodeUnavailable},
		{name: "generation exhausted", err: requestcapture.ErrGenerationExhausted, wantStatus: http.StatusServiceUnavailable, wantCode: errorCodeUnavailable},
		{name: "unexpected", err: errors.New("unexpected"), wantStatus: http.StatusInternalServerError, wantCode: errorCodeInternal},
	}
	handler := NewHandler(Config{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.writeServiceError(recorder, test.err, operationContext{name: "test"})
			assertErrorResponse(t, recorder, test.wantStatus, test.wantCode)
		})
	}
}

func TestQueryJSONMapsCancellationBeforeWriting(t *testing.T) {
	handler := NewHandler(Config{})
	recorder := httptest.NewRecorder()
	handler.writeQueryJSON(recorder, context.Background(), queryJSONLeaseFunc(func(context.Context, io.Writer) error {
		return requestcapture.ErrQueryCanceled
	}), operationContext{name: "list_records", sessionID: "session-1"})
	assertErrorResponse(t, recorder, http.StatusNotFound, errorCodeSessionNotFound)
}

func TestQueryJSONSuppressesErrorsAfterBodyCommit(t *testing.T) {
	const sensitiveErrorText = "socket failure containing sensitive upstream text"
	observedCore, observedLogs := observer.New(zap.WarnLevel)
	handler := NewHandler(Config{Logger: zap.New(observedCore)})
	recorder := httptest.NewRecorder()
	handler.writeQueryJSON(recorder, context.Background(), queryJSONLeaseFunc(func(_ context.Context, destination io.Writer) error {
		_, _ = destination.Write([]byte(`{"partial":`))
		return errors.New(sensitiveErrorText)
	}), operationContext{name: "get_record", sessionID: "session-1", recordID: "record-1"})

	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"partial":` {
		t.Fatalf("status/body = %d %q", recorder.Code, recorder.Body.String())
	}
	logs := observedLogs.All()
	if len(logs) != 1 || logs[0].ContextMap()["failure_reason"] != "response_write_failed" {
		t.Fatalf("query failure logs = %#v", logs)
	}
	if strings.Contains(fmt.Sprint(logs), sensitiveErrorText) {
		t.Fatal("query writer error text crossed the observability boundary")
	}
}

func TestQueryJSONDoesNotRespondAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &trackingResponseWriter{}
	NewHandler(Config{}).writeQueryJSON(w, ctx, queryJSONLeaseFunc(func(context.Context, io.Writer) error {
		return requestcapture.ErrQueryCanceled
	}), operationContext{name: "list_records"})
	if w.wroteResponse() {
		t.Fatalf("canceled request committed a response: status=%d body=%q", w.status, w.body.String())
	}
}

func TestJSONResponseLogsOnlyStableWriterFailure(t *testing.T) {
	const sensitiveErrorText = "connection error containing sensitive request data"
	observedCore, observedLogs := observer.New(zap.ErrorLevel)
	handler := NewHandler(Config{Logger: zap.New(observedCore)})
	handler.writeJSON(
		&errorResponseWriter{err: errors.New(sensitiveErrorText)},
		http.StatusOK,
		map[string]string{"state": "active"},
		operationContext{name: "status"},
	)
	logs := observedLogs.All()
	if len(logs) != 1 || logs[0].ContextMap()["failure_reason"] != "response_write_failed" {
		t.Fatalf("response failure logs = %#v", logs)
	}
	if strings.Contains(fmt.Sprint(logs), sensitiveErrorText) {
		t.Fatal("response writer error text crossed the observability boundary")
	}
}

func TestDownloadExportDoesNotLogUnvalidatedPathIdentifier(t *testing.T) {
	const credentialCanary = "sk-live-secret-canary"
	observedCore, observedLogs := observer.New(zap.ErrorLevel)
	handler := NewHandler(Config{
		Logger:  zap.New(observedCore),
		Exports: &stubCaptureService{},
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("export_id", credentialCanary)

	handler.DownloadExport(&errorResponseWriter{err: errors.New("forced response failure")}, request)

	logs := observedLogs.All()
	if len(logs) != 1 || logs[0].ContextMap()["failure_reason"] != failureReasonResponseWrite {
		t.Fatalf("response failure logs = %#v", logs)
	}
	if strings.Contains(fmt.Sprint(logs), credentialCanary) {
		t.Fatal("unvalidated path identifier crossed the observability boundary")
	}
}

func TestSensitiveResponsesCoversDownstreamErrors(t *testing.T) {
	recorder := httptest.NewRecorder()
	SensitiveResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusUnauthorized)
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assertSensitiveHeaders(t, recorder)
}

func TestDecodeJSONBodyEnforcesBoundAndSingleValue(t *testing.T) {
	handler := NewHandler(Config{Providers: &stubProviderCatalog{}, Sessions: &stubCaptureService{}})
	for _, body := range []string{
		`{"provider_ids":["` + strings.Repeat("x", int(maxJSONBodyBytes)) + `"],"acknowledge_raw_payload_risk":true}`,
		`{"provider_ids":["provider-a"],"acknowledge_raw_payload_risk":true}` + strings.Repeat(" ", int(maxJSONBodyBytes)),
	} {
		recorder := httptest.NewRecorder()
		handler.StartSession(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
		}
	}
}

func assertSensitiveHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var response model.ErrorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != wantCode {
		t.Fatalf("code = %q, want %q", response.Code, wantCode)
	}
}
