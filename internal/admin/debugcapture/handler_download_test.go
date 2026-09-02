package debugcapture

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

type panicHeaderResponseWriter struct{}

func (panicHeaderResponseWriter) Header() http.Header { panic("forced header panic") }
func (panicHeaderResponseWriter) WriteHeader(int)     { panic("unexpected WriteHeader") }
func (panicHeaderResponseWriter) Write([]byte) (int, error) {
	panic("unexpected Write")
}

func downloadRequest(method, exportID, token string) *http.Request {
	target := "/"
	if token != "" {
		target += "?download_token=" + token
	}
	request := httptest.NewRequest(method, target, nil)
	request.SetPathValue("export_id", exportID)
	return request
}

func TestDownloadExportStreamsAcceptedGETCapability(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	addAdminQueryRecord(t, manager)
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, requestcapture.ExportRequest{Scope: requestcapture.ExportScopeAll})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	var acceptedExportID, acceptedToken string
	service := &stubCaptureService{acceptDownloadFn: func(exportID, token string) (requestcapture.Download, error) {
		acceptedExportID, acceptedToken = exportID, token
		return manager.AcceptDownload(exportID, token)
	}}
	handler := NewHandler(Config{
		Exports: service,
		Streamer: downloadStreamerFunc(func(_ context.Context, _ requestcapture.Download, destination io.Writer) error {
			_, err := io.WriteString(destination, "{\"event\":\"manifest\"}\n{\"event\":\"export_end\"}\n")
			return err
		}),
	})
	recorder := httptest.NewRecorder()

	SensitiveResponses(http.HandlerFunc(handler.DownloadExport)).ServeHTTP(
		recorder,
		downloadRequest(http.MethodGet, ticket.ExportID, ticket.DownloadToken),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if acceptedExportID != ticket.ExportID || acceptedToken != ticket.DownloadToken {
		t.Fatalf("accepted = %q %q", acceptedExportID, acceptedToken)
	}
	if got := recorder.Header().Get("Content-Type"); got != contentTypeNDJSON {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.Contains(got, "switch-a-debug-capture-"+ticket.ExportID+".ndjson") {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if got := recorder.Header().Get("Accept-Ranges"); got != "none" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	if !strings.Contains(recorder.Body.String(), "export_end") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	assertSensitiveHeaders(t, recorder)
}

func TestDownloadExportHEADAdvertisesStreamWithoutConsumingCapability(t *testing.T) {
	claims := 0
	handler := NewHandler(Config{Exports: &stubCaptureService{acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
		claims++
		return requestcapture.Download{}, nil
	}}})
	recorder := httptest.NewRecorder()

	handler.DownloadExport(recorder, downloadRequest(http.MethodHead, testExportID, testDownloadToken))

	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("status/body = %d %q", recorder.Code, recorder.Body.String())
	}
	if claims != 0 {
		t.Fatalf("HEAD consumed capability: claims=%d", claims)
	}
	if recorder.Header().Get("Accept-Ranges") != "none" {
		t.Fatal("HEAD did not advertise the non-range stream boundary")
	}
}

func TestDownloadExportRejectsNonCanonicalMethodsAndQueries(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{name: "wrong method", method: http.MethodPost, target: "/?download_token=" + testDownloadToken, wantStatus: http.StatusMethodNotAllowed},
		{name: "missing token", method: http.MethodGet, target: "/", wantStatus: http.StatusBadRequest},
		{name: "empty token", method: http.MethodGet, target: "/?download_token=", wantStatus: http.StatusBadRequest},
		{name: "short token", method: http.MethodGet, target: "/?download_token=short", wantStatus: http.StatusBadRequest},
		{name: "malformed escape", method: http.MethodGet, target: "/?download_token=%zz", wantStatus: http.StatusBadRequest},
		{name: "repeated token", method: http.MethodGet, target: "/?download_token=" + testDownloadToken + "&download_token=" + testDownloadToken, wantStatus: http.StatusBadRequest},
		{name: "extra field", method: http.MethodGet, target: "/?download_token=" + testDownloadToken + "&other=value", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := 0
			handler := NewHandler(Config{Exports: &stubCaptureService{acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
				claims++
				return requestcapture.Download{}, nil
			}}})
			request := httptest.NewRequest(test.method, test.target, nil)
			request.SetPathValue("export_id", testExportID)
			recorder := httptest.NewRecorder()

			handler.DownloadExport(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if claims != 0 {
				t.Fatal("invalid request reached capability admission")
			}
			if strings.Contains(recorder.Body.String(), testDownloadToken) {
				t.Fatalf("response reflected capability: %s", recorder.Body.String())
			}
		})
	}
}

func TestDownloadExportRejectsOversizedQueryBeforeCapabilityClaim(t *testing.T) {
	claims := 0
	handler := NewHandler(Config{Exports: &stubCaptureService{acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
		claims++
		return requestcapture.Download{}, nil
	}}})
	target := "/?download_token=" + strings.Repeat("A", maxDownloadQueryBytes)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.SetPathValue("export_id", testExportID)
	recorder := httptest.NewRecorder()

	handler.DownloadExport(recorder, request)

	if recorder.Code != http.StatusBadRequest || claims != 0 {
		t.Fatalf("status/claims = %d/%d", recorder.Code, claims)
	}
}

func TestDownloadExportCapabilityCanBeRetriedUntilExpiry(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	addAdminQueryRecord(t, manager)
	baselineCharge := manager.Status().ProcessMemory.ChargedBytes
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, requestcapture.ExportRequest{Scope: requestcapture.ExportScopeAll})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	handler := NewHandler(Config{Exports: manager})
	for attempt := range 2 {
		recorder := httptest.NewRecorder()
		handler.DownloadExport(recorder, downloadRequest(http.MethodGet, ticket.ExportID, ticket.DownloadToken))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "\"event\":\"export_end\"") {
			t.Fatalf("attempt %d status/body = %d/%q", attempt+1, recorder.Code, recorder.Body.String())
		}
	}
	status := manager.Status()
	if status.PendingExportCount != 1 || status.ActiveDownloadCount != 0 ||
		status.ProcessMemory.PinnedBytes == 0 || status.ProcessMemory.TemporaryBytes != 0 ||
		status.ProcessMemory.ChargedBytes <= baselineCharge {
		t.Fatalf("retryable export accounting = %#v", status)
	}
}

func TestDownloadExportKeepsEveryPreStreamFailureUniform(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "expired capability", err: requestcapture.ErrDownloadUnavailable},
		{name: "canceled export", err: requestcapture.ErrExportCanceled},
		{name: "download pressure", err: requestcapture.ErrDownloadLimitReached},
		{name: "memory pressure", err: requestcapture.ErrCapacityExceeded},
		{name: "internal fault", err: errors.New("claim invariant rejected")},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(Config{Exports: &stubCaptureService{acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
				return requestcapture.Download{}, test.err
			}}})
			recorder := httptest.NewRecorder()
			handler.DownloadExport(recorder, downloadRequest(http.MethodGet, testExportID, testDownloadToken))
			assertErrorResponse(t, recorder, http.StatusGone, errorCodeDownloadUnavailable)
		})
	}
}

func TestDownloadExportRejectsZeroClaimBeforeCommittingSuccess(t *testing.T) {
	streamed := false
	handler := NewHandler(Config{
		Exports: &stubCaptureService{acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
			return requestcapture.Download{}, nil
		}},
		Streamer: downloadStreamerFunc(func(context.Context, requestcapture.Download, io.Writer) error {
			streamed = true
			return nil
		}),
	})
	recorder := httptest.NewRecorder()
	handler.DownloadExport(recorder, downloadRequest(http.MethodGet, testExportID, testDownloadToken))

	assertErrorResponse(t, recorder, http.StatusGone, errorCodeDownloadUnavailable)
	if streamed {
		t.Fatal("invalid claimed download reached streamer")
	}
}

func TestStableDownloadFailureReasonDoesNotExposeRawErrors(t *testing.T) {
	if got := stableDownloadFailureReason(context.Canceled); got != "request_canceled" {
		t.Fatalf("reason = %q", got)
	}
	if got := stableDownloadFailureReason(errors.New("contains raw payload")); got != "stream_error" {
		t.Fatalf("reason = %q", got)
	}
}

func TestDownloadExportAlwaysReleasesAttemptWorkspace(t *testing.T) {
	for _, test := range []struct {
		name        string
		streamErr   error
		streamPanic bool
		headerPanic bool
	}{
		{name: "streamer abandons attempt"},
		{name: "streamer returns early", streamErr: errors.New("forced stream failure")},
		{name: "streamer panics", streamPanic: true},
		{name: "response header panics", headerPanic: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, session := newAdminQueryManager(t)
			addAdminQueryRecord(t, manager)
			ticket, err := manager.CreateExport(context.Background(), session.SessionID, requestcapture.ExportRequest{Scope: requestcapture.ExportScopeAll})
			if err != nil {
				t.Fatalf("CreateExport() error = %v", err)
			}
			handler := NewHandler(Config{
				Exports: manager,
				Streamer: downloadStreamerFunc(func(context.Context, requestcapture.Download, io.Writer) error {
					if test.streamPanic {
						panic("forced streamer panic")
					}
					return test.streamErr
				}),
			})
			recorder := httptest.NewRecorder()
			var destination http.ResponseWriter = recorder
			serve := http.Handler(http.HandlerFunc(handler.DownloadExport))
			if test.headerPanic {
				destination = panicHeaderResponseWriter{}
			} else {
				serve = SensitiveResponses(serve)
			}
			func() {
				defer func() {
					wantPanic := test.streamPanic || test.headerPanic
					if recovered := recover(); wantPanic != (recovered != nil) {
						t.Fatalf("panic = %v, want panic %t", recovered, wantPanic)
					}
				}()
				serve.ServeHTTP(destination, downloadRequest(http.MethodGet, ticket.ExportID, ticket.DownloadToken))
			}()
			status := manager.Status()
			if status.PendingExportCount != 1 || status.ActiveDownloadCount != 0 ||
				status.ProcessMemory.PinnedBytes == 0 || status.ProcessMemory.TemporaryBytes != 0 {
				t.Fatalf("attempt workspace was not released: %#v", status)
			}
		})
	}
}
