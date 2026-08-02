package debugcapture

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

func TestUnavailableDependenciesReturnServiceUnavailable(t *testing.T) {
	handler := NewHandler(Config{})
	tests := []struct {
		name    string
		method  string
		handler http.HandlerFunc
		pathKey string
	}{
		{name: "start", method: http.MethodPost, handler: handler.StartSession},
		{name: "status", method: http.MethodGet, handler: handler.Status},
		{name: "stop", method: http.MethodDelete, handler: handler.StopSession, pathKey: "session_id"},
		{name: "list", method: http.MethodGet, handler: handler.ListRecords, pathKey: "session_id"},
		{name: "detail", method: http.MethodGet, handler: handler.GetRecord, pathKey: "session_id"},
		{name: "create export", method: http.MethodPost, handler: handler.CreateExport, pathKey: "session_id"},
		{name: "download", method: http.MethodPost, handler: handler.DownloadExport, pathKey: "export_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/", nil)
			if test.pathKey != "" {
				req.SetPathValue(test.pathKey, "resource-1")
			}
			recorder := httptest.NewRecorder()
			test.handler(recorder, req)
			assertErrorResponse(t, recorder, http.StatusServiceUnavailable, errorCodeUnavailable)
		})
	}
}

func TestHandlersValidateRequiredPathValues(t *testing.T) {
	service := &stubCaptureService{}
	handler := NewHandler(Config{Sessions: service, Queries: service, Exports: service})
	for _, test := range []struct {
		name    string
		method  string
		handler http.HandlerFunc
		body    string
	}{
		{name: "stop", method: http.MethodDelete, handler: handler.StopSession},
		{name: "list", method: http.MethodGet, handler: handler.ListRecords},
		{name: "detail", method: http.MethodGet, handler: handler.GetRecord},
		{name: "create export", method: http.MethodPost, handler: handler.CreateExport, body: `{"scope":"all"}`},
		{name: "download", method: http.MethodPost, handler: handler.DownloadExport, body: "download_token=secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/", strings.NewReader(test.body))
			if test.name == "download" {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			recorder := httptest.NewRecorder()
			test.handler(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestHandlersRejectNonCanonicalIdentifierAliases(t *testing.T) {
	service := &stubCaptureService{}
	handler := NewHandler(Config{Sessions: service, Queries: service, Exports: service})
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		pathKey   string
		pathValue string
		prepare   func(*http.Request)
	}{
		{name: "padded session", handler: handler.StopSession, pathKey: "session_id", pathValue: testSessionID + " "},
		{name: "leading-zero session generation", handler: handler.ListRecords, pathKey: "session_id", pathValue: "cs_01_000000000000000000000000"},
		{name: "padded record encoding", handler: handler.GetRecord, pathKey: "record_id", pathValue: testRecordID + "=", prepare: func(request *http.Request) {
			request.SetPathValue("session_id", testSessionID)
		}},
		{name: "padded export encoding", handler: handler.DownloadExport, pathKey: "export_id", pathValue: testExportID + "=", prepare: func(request *http.Request) {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("download_token=secret"))
			request.SetPathValue(test.pathKey, test.pathValue)
			if test.prepare != nil {
				test.prepare(request)
			}
			recorder := httptest.NewRecorder()

			test.handler(recorder, request)

			assertErrorResponse(t, recorder, http.StatusBadRequest, errorCodeValidation)
		})
	}
}

func TestQueryAndExportHandlersMapDomainErrors(t *testing.T) {
	service := &stubCaptureService{
		openPageFn: func(context.Context, string, requestcapture.ListQuery) (*requestcapture.RecordPageLease, error) {
			return nil, requestcapture.ErrInvalidCursor
		},
		openDetailFn: func(context.Context, string, string, int) (*requestcapture.RecordDetailLease, error) {
			return nil, requestcapture.ErrRecordEvicted
		},
		createExportFn: func(context.Context, string, requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
			return requestcapture.ExportTicket{}, requestcapture.ErrExportLimitReached
		},
	}
	handler := NewHandler(Config{Queries: service, Exports: service})

	listReq := httptest.NewRequest(http.MethodGet, "/", nil)
	listReq.SetPathValue("session_id", testSessionID)
	listRecorder := httptest.NewRecorder()
	handler.ListRecords(listRecorder, listReq)
	assertErrorResponse(t, listRecorder, http.StatusBadRequest, errorCodeValidation)

	detailReq := httptest.NewRequest(http.MethodGet, "/", nil)
	detailReq.SetPathValue("session_id", testSessionID)
	detailReq.SetPathValue("record_id", testRecordID)
	detailRecorder := httptest.NewRecorder()
	handler.GetRecord(detailRecorder, detailReq)
	assertErrorResponse(t, detailRecorder, http.StatusGone, errorCodeRecordEvicted)

	exportReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"scope":"all"}`))
	exportReq.SetPathValue("session_id", testSessionID)
	exportRecorder := httptest.NewRecorder()
	handler.CreateExport(exportRecorder, exportReq)
	assertErrorResponse(t, exportRecorder, http.StatusTooManyRequests, errorCodeExportLimit)
}

func TestGetRecordRejectsQueryParameters(t *testing.T) {
	handler := NewHandler(Config{Queries: &stubCaptureService{}})
	req := httptest.NewRequest(http.MethodGet, "/?preview_bytes=10", nil)
	req.SetPathValue("session_id", testSessionID)
	req.SetPathValue("record_id", testRecordID)
	recorder := httptest.NewRecorder()
	handler.GetRecord(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestStopSessionSuccess(t *testing.T) {
	handler := NewHandler(Config{Sessions: &stubCaptureService{}})
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.SetPathValue("session_id", testSessionID)
	recorder := httptest.NewRecorder()
	handler.StopSession(recorder, req)
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("status/body = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadFormParsingBoundsAndStreamingFailure(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	addAdminQueryRecord(t, manager)
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, requestcapture.ExportRequest{Scope: requestcapture.ExportScopeAll})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	service := &stubCaptureService{acceptDownloadFn: func(exportID, token string) (requestcapture.Download, error) {
		return manager.AcceptDownload(exportID, token)
	}}
	handler := NewHandler(Config{
		Exports: service,
		Streamer: downloadStreamerFunc(func(context.Context, requestcapture.Download, io.Writer) error {
			return requestcapture.ErrExportCanceled
		}),
	})

	t.Run("malformed form", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("download_token=%zz"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("export_id", ticket.ExportID)
		recorder := httptest.NewRecorder()
		handler.DownloadExport(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", recorder.Code)
		}
	})

	t.Run("oversized form", func(t *testing.T) {
		body := "download_token=" + strings.Repeat("x", int(maxDownloadFormBytes))
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("export_id", ticket.ExportID)
		recorder := httptest.NewRecorder()
		handler.DownloadExport(recorder, req)
		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d", recorder.Code)
		}
	})

	t.Run("stream failure remains a truncated 200", func(t *testing.T) {
		form := url.Values{downloadTokenField: {ticket.DownloadToken}}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("export_id", ticket.ExportID)
		recorder := httptest.NewRecorder()
		handler.DownloadExport(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d", recorder.Code)
		}
	})
}

func TestCoreDownloadStreamerAndSmallHelpers(t *testing.T) {
	if err := (coreDownloadStreamer{}).Stream(context.Background(), requestcapture.Download{}, io.Discard); !errors.Is(err, requestcapture.ErrDownloadUnavailable) {
		t.Fatalf("stream error = %v", err)
	}
	if got := safeFilenameComponent("<>\r\n"); got != "export" {
		t.Fatalf("filename component = %q", got)
	}
	if got := stableDownloadFailureReason(requestcapture.ErrExportCanceled); got != "export_canceled" {
		t.Fatalf("reason = %q", got)
	}
	if got := stableDownloadFailureReason(requestcapture.ErrDownloadUnavailable); got != "download_unavailable" {
		t.Fatalf("reason = %q", got)
	}
	decodeErr := &bodyDecodeError{message: "bad body"}
	if decodeErr.Error() != "bad body" {
		t.Fatalf("body error = %q", decodeErr.Error())
	}
}

func TestWriteBodyDecodeErrorFallback(t *testing.T) {
	handler := NewHandler(Config{})
	recorder := httptest.NewRecorder()
	handler.writeBodyDecodeError(recorder, errors.New("unexpected decoder error"), operationContext{name: "test"})
	assertErrorResponse(t, recorder, http.StatusBadRequest, errorCodeValidation)
}
