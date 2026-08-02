package debugcapture

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

type failingResponseWriter struct {
	header http.Header
	status int
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(status int) { w.status = status }

func (w *failingResponseWriter) Write([]byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return 0, errors.New("forced response writer failure")
}

func TestListRecordsParsesStablePagination(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	var captured requestcapture.ListQuery
	service := &stubCaptureService{openPageFn: func(ctx context.Context, sessionID string, query requestcapture.ListQuery) (*requestcapture.RecordPageLease, error) {
		if sessionID != testSessionID {
			t.Fatalf("sessionID = %q", sessionID)
		}
		captured = query
		return manager.OpenRecordPage(ctx, session.SessionID, requestcapture.ListQuery{Limit: query.Limit})
	}}
	handler := NewHandler(Config{Queries: service})
	req := httptest.NewRequest(http.MethodGet, "/?limit=12&cursor=cursor-1&snapshot_watermark=watermark-1", nil)
	req.SetPathValue("session_id", testSessionID)
	recorder := httptest.NewRecorder()
	handler.ListRecords(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if captured != (requestcapture.ListQuery{Limit: 12, Cursor: "cursor-1", SnapshotWatermark: "watermark-1"}) {
		t.Fatalf("query = %#v", captured)
	}
}

func TestListRecordsRejectsAmbiguousQueries(t *testing.T) {
	for _, rawQuery := range []string{
		"limit=0",
		"limit=201",
		"limit=abc",
		"limit=",
		"cursor=cursor-only",
		"snapshot_watermark=watermark-only",
		"cursor=&snapshot_watermark=",
		"limit=1&limit=2",
		"cursor=%zz&snapshot_watermark=watermark",
		"unknown=value",
	} {
		t.Run(rawQuery, func(t *testing.T) {
			handler := NewHandler(Config{Queries: &stubCaptureService{}})
			req := httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
			req.SetPathValue("session_id", testSessionID)
			recorder := httptest.NewRecorder()
			handler.ListRecords(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestListRecordsRejectsQueryBeforeUnboundedParsing(t *testing.T) {
	handler := NewHandler(Config{Queries: &stubCaptureService{}})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.RawQuery = "cursor=" + strings.Repeat("x", maxRecordListQueryBytes)
	req.SetPathValue("session_id", testSessionID)
	recorder := httptest.NewRecorder()

	handler.ListRecords(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetRecordReturnsBoundedCoreDetail(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	storedRecordID := addAdminQueryRecord(t, manager)
	service := &stubCaptureService{openDetailFn: func(ctx context.Context, sessionID, recordID string, previewBytes int) (*requestcapture.RecordDetailLease, error) {
		if sessionID != testSessionID || recordID != testRecordID || previewBytes != 0 {
			t.Fatalf("arguments = %q %q %d", sessionID, recordID, previewBytes)
		}
		return manager.OpenRecordDetail(ctx, session.SessionID, storedRecordID, previewBytes)
	}}
	handler := NewHandler(Config{Queries: service})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("session_id", testSessionID)
	req.SetPathValue("record_id", testRecordID)
	recorder := httptest.NewRecorder()
	handler.GetRecord(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var detail requestcapture.RecordDetail
	if err := json.NewDecoder(recorder.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Summary.RecordID != storedRecordID {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestQueryHandlersReleaseTemporaryMemoryAfterWriterFailure(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	handler := NewHandler(Config{Queries: manager})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("session_id", session.SessionID)
	w := &failingResponseWriter{}

	handler.ListRecords(w, req)

	if w.status != http.StatusOK {
		t.Fatalf("status = %d", w.status)
	}
	if temporary := manager.Status().ProcessMemory.TemporaryBytes; temporary != 0 {
		t.Fatalf("temporary bytes after failed query response = %d", temporary)
	}
}

func TestQueryHandlersRejectMissingLease(t *testing.T) {
	handler := NewHandler(Config{Queries: &stubCaptureService{
		openPageFn: func(context.Context, string, requestcapture.ListQuery) (*requestcapture.RecordPageLease, error) {
			return nil, nil
		},
	}})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("session_id", testSessionID)
	recorder := httptest.NewRecorder()
	handler.ListRecords(recorder, req)
	assertErrorResponse(t, recorder, http.StatusInternalServerError, errorCodeInternal)
}

func TestQueryHandlersDoNotWriteAfterRequestCancellation(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req.SetPathValue("session_id", session.SessionID)
	w := &trackingResponseWriter{}
	NewHandler(Config{Queries: manager}).ListRecords(w, req)
	if w.wroteResponse() {
		t.Fatalf("canceled query committed a response: status=%d body=%q", w.status, w.body.String())
	}
}

func TestCreateExportUsesExplicitSelectionScope(t *testing.T) {
	expiresAt := time.Date(2026, 8, 1, 2, 3, 4, 0, time.UTC)
	var captured requestcapture.ExportRequest
	service := &stubCaptureService{createExportFn: func(_ context.Context, sessionID string, input requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
		captured = input
		return requestcapture.ExportTicket{
			ExportID: testExportID, SessionID: sessionID, RecordCount: 2, ExpiresAt: expiresAt,
			DownloadToken: "secret-token",
		}, nil
	}}
	handler := NewHandler(Config{Exports: service})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"scope":"records","record_ids":["`+testRecordID+`","`+testOtherRecordID+`"]}`))
	req.SetPathValue("session_id", testSessionID)
	recorder := httptest.NewRecorder()
	handler.CreateExport(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if captured.Scope != requestcapture.ExportScopeRecords || len(captured.RecordIDs) != 2 {
		t.Fatalf("selection = %#v", captured)
	}
	if captured.RecordIDs[0] != testRecordID {
		t.Fatalf("record IDs changed: %#v", captured.RecordIDs)
	}
	expectedDownloadPath := exportDownloadPathPrefix + testExportID + "/download"
	if got := recorder.Header().Get("Location"); got != expectedDownloadPath {
		t.Fatalf("Location = %q", got)
	}
	var grant ExportDownloadGrant
	if err := json.NewDecoder(recorder.Body).Decode(&grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if grant.DownloadPath != expectedDownloadPath || grant.ExportID != testExportID || grant.SessionID != testSessionID {
		t.Fatalf("grant = %#v", grant)
	}
}

func TestCreateExportRejectsInvalidSuccessfulTicket(t *testing.T) {
	expiresAt := time.Date(2099, 8, 1, 2, 3, 4, 0, time.UTC)
	validTicket := requestcapture.ExportTicket{
		ExportID:      testExportID,
		SessionID:     testSessionID,
		RecordCount:   1,
		ExpiresAt:     expiresAt,
		DownloadToken: "secret-token",
	}
	tests := []struct {
		name   string
		mutate func(*requestcapture.ExportTicket)
	}{
		{name: "non-canonical export ID", mutate: func(ticket *requestcapture.ExportTicket) { ticket.ExportID = "export-1" }},
		{name: "wrong session binding", mutate: func(ticket *requestcapture.ExportTicket) { ticket.SessionID = "cs_2_000000000000000000000000" }},
		{name: "empty snapshot", mutate: func(ticket *requestcapture.ExportTicket) { ticket.RecordCount = 0 }},
		{name: "missing expiry", mutate: func(ticket *requestcapture.ExportTicket) { ticket.ExpiresAt = time.Time{} }},
		{name: "missing token", mutate: func(ticket *requestcapture.ExportTicket) { ticket.DownloadToken = "" }},
		{name: "ambiguous token whitespace", mutate: func(ticket *requestcapture.ExportTicket) { ticket.DownloadToken = " token " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ticket := validTicket
			test.mutate(&ticket)
			handler := NewHandler(Config{Exports: &stubCaptureService{
				createExportFn: func(context.Context, string, requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
					return ticket, nil
				},
			}})
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"scope":"all"}`))
			request.SetPathValue("session_id", testSessionID)
			recorder := httptest.NewRecorder()

			handler.CreateExport(recorder, request)

			assertErrorResponse(t, recorder, http.StatusInternalServerError, errorCodeInternal)
			if location := recorder.Header().Get("Location"); location != "" {
				t.Fatalf("invalid ticket published Location %q", location)
			}
		})
	}
}

func TestCreateExportPropagatesRequestCancellationWithoutPublishingTicket(t *testing.T) {
	called := false
	service := &stubCaptureService{createExportFn: func(ctx context.Context, _ string, _ requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
		called = true
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("export context error = %v", ctx.Err())
		}
		return requestcapture.ExportTicket{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"scope":"all"}`)).WithContext(ctx)
	req.SetPathValue("session_id", testSessionID)
	w := &trackingResponseWriter{}

	NewHandler(Config{Exports: service}).CreateExport(w, req)

	if !called {
		t.Fatal("export service was not called with the request context")
	}
	if w.wroteResponse() || w.Header().Get("Location") != "" {
		t.Fatalf("canceled export committed a response: status=%d body=%q location=%q", w.status, w.body.String(), w.Header().Get("Location"))
	}
}

func TestCreateExportDoesNotPublishSuccessfulTicketAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	service := &stubCaptureService{createExportFn: func(received context.Context, _ string, _ requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
		if received != ctx {
			t.Fatal("export service did not receive the request context")
		}
		cancel()
		return requestcapture.ExportTicket{
			ExportID:  testExportID,
			SessionID: testSessionID,
		}, nil
	}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"scope":"all"}`)).WithContext(ctx)
	req.SetPathValue("session_id", testSessionID)
	w := &trackingResponseWriter{}

	NewHandler(Config{Exports: service}).CreateExport(w, req)

	if w.wroteResponse() || w.Header().Get("Location") != "" {
		t.Fatalf("canceled export committed a successful response: status=%d body=%q location=%q", w.status, w.body.String(), w.Header().Get("Location"))
	}
}

func TestCreateExportMapsLifecycleCancellation(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "session stopped", err: requestcapture.ErrNoActiveSession, wantStatus: http.StatusNotFound, wantCode: errorCodeSessionNotFound},
		{name: "manager closed", err: requestcapture.ErrManagerClosed, wantStatus: http.StatusServiceUnavailable, wantCode: errorCodeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(Config{Exports: &stubCaptureService{
				createExportFn: func(context.Context, string, requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
					return requestcapture.ExportTicket{}, test.err
				},
			}})
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"scope":"all"}`))
			req.SetPathValue("session_id", testSessionID)
			recorder := httptest.NewRecorder()
			handler.CreateExport(recorder, req)
			assertErrorResponse(t, recorder, test.wantStatus, test.wantCode)
		})
	}
}

func TestCreateExportRejectsAmbiguousSelections(t *testing.T) {
	for _, body := range []string{
		`{"scope":"all","record_ids":["` + testRecordID + `"]}`,
		`{"scope":"records"}`,
		`{"scope":"records","record_ids":["` + testRecordID + `","` + testRecordID + `"]}`,
		`{"scope":"records","record_ids":[""]}`,
		`{"scope":"records","record_ids":[" ` + testRecordID + ` "]}`,
		`{"scope":"future"}`,
		`{"scope":"all","unknown":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			handler := NewHandler(Config{Exports: &stubCaptureService{}})
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.SetPathValue("session_id", testSessionID)
			recorder := httptest.NewRecorder()
			handler.CreateExport(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
