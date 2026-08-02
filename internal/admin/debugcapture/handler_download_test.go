package debugcapture

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

type panicHeaderResponseWriter struct{}

func (panicHeaderResponseWriter) Header() http.Header { panic("forced response header panic") }

func (panicHeaderResponseWriter) WriteHeader(int) { panic("unexpected WriteHeader") }

func (panicHeaderResponseWriter) Write([]byte) (int, error) { panic("unexpected Write") }

type failingDownloadFormBody struct{}

func (failingDownloadFormBody) Read([]byte) (int, error) {
	return 0, errors.New("forced form read failure")
}

func (failingDownloadFormBody) Close() error { return nil }

func TestDownloadExportStreamsAcceptedCapability(t *testing.T) {
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
	form := url.Values{downloadTokenField: {ticket.DownloadToken}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("export_id", ticket.ExportID)
	recorder := httptest.NewRecorder()

	SensitiveResponses(http.HandlerFunc(handler.DownloadExport)).ServeHTTP(recorder, req)

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
	if !strings.Contains(recorder.Body.String(), "export_end") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	assertSensitiveHeaders(t, recorder)
}

func TestDownloadExportRejectsTokenOutsideExactForm(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		target      string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "wrong method", method: http.MethodGet, target: "/", wantStatus: http.StatusMethodNotAllowed},
		{name: "query token", method: http.MethodPost, target: "/?download_token=secret", contentType: "application/x-www-form-urlencoded", wantStatus: http.StatusBadRequest},
		{name: "wrong content type", method: http.MethodPost, target: "/", contentType: "application/json", body: `{"download_token":"secret"}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "missing token", method: http.MethodPost, target: "/", contentType: "application/x-www-form-urlencoded", body: "", wantStatus: http.StatusBadRequest},
		{name: "repeated token", method: http.MethodPost, target: "/", contentType: "application/x-www-form-urlencoded", body: "download_token=a&download_token=b", wantStatus: http.StatusBadRequest},
		{name: "extra field", method: http.MethodPost, target: "/", contentType: "application/x-www-form-urlencoded", body: "download_token=a&other=b", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accepted := false
			service := &stubCaptureService{acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
				accepted = true
				return requestcapture.Download{}, nil
			}}
			handler := NewHandler(Config{Exports: service})
			req := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.contentType != "" {
				req.Header.Set("Content-Type", test.contentType)
			}
			req.SetPathValue("export_id", testExportID)
			recorder := httptest.NewRecorder()
			handler.DownloadExport(recorder, req)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if accepted {
				t.Fatal("capability accepted for invalid request")
			}
		})
	}
}

func TestDownloadExportRejectsAmbiguousTransportFormsBeforeCapabilityClaim(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*http.Request)
	}{
		{
			name: "empty query delimiter",
			configure: func(request *http.Request) {
				request.URL.ForceQuery = true
			},
		},
		{
			name: "content encoding",
			configure: func(request *http.Request) {
				request.Header.Set("Content-Encoding", "gzip")
			},
		},
		{
			name: "repeated content type",
			configure: func(request *http.Request) {
				request.Header.Add("Content-Type", "application/x-www-form-urlencoded")
			},
		},
		{
			name: "conflicting content type",
			configure: func(request *http.Request) {
				request.Header.Add("Content-Type", "application/json")
			},
		},
		{
			name: "parameterized content type alias",
			configure: func(request *http.Request) {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
			},
		},
		{
			name: "malformed escape",
			configure: func(request *http.Request) {
				request.Body = io.NopCloser(strings.NewReader("download_token=%zz"))
			},
		},
		{
			name: "body read failure",
			configure: func(request *http.Request) {
				request.Body = failingDownloadFormBody{}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := 0
			handler := NewHandler(Config{Exports: &stubCaptureService{
				acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
					claims++
					return requestcapture.Download{}, nil
				},
			}})
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("download_token=secret"))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetPathValue("export_id", testExportID)
			test.configure(request)
			recorder := httptest.NewRecorder()

			handler.DownloadExport(recorder, request)

			if recorder.Code < http.StatusBadRequest || recorder.Code >= http.StatusInternalServerError {
				t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
			}
			if claims != 0 {
				t.Fatalf("AcceptDownload calls = %d, want 0", claims)
			}
		})
	}
}

func TestDownloadExportConcurrentConsumptionHasOneWinner(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	addAdminQueryRecord(t, manager)
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, requestcapture.ExportRequest{Scope: requestcapture.ExportScopeAll})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	handler := NewHandler(Config{
		Exports: manager,
		Streamer: downloadStreamerFunc(func(context.Context, requestcapture.Download, io.Writer) error {
			return nil
		}),
	})

	const contenders = 16
	statuses := make(chan int, contenders)
	var waitGroup sync.WaitGroup
	for range contenders {
		waitGroup.Go(func() {
			form := url.Values{downloadTokenField: {ticket.DownloadToken}}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("export_id", ticket.ExportID)
			recorder := httptest.NewRecorder()
			handler.DownloadExport(recorder, req)
			statuses <- recorder.Code
		})
	}
	waitGroup.Wait()
	close(statuses)

	successes := 0
	for status := range statuses {
		switch status {
		case http.StatusOK:
			successes++
		case http.StatusGone:
		default:
			t.Fatalf("unexpected status %d", status)
		}
	}
	if successes != 1 {
		t.Fatalf("successful downloads = %d, want 1", successes)
	}
}

func TestDownloadExportKeepsEveryPreStreamFailureUniform(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "invalid expired or replayed capability", err: requestcapture.ErrDownloadUnavailable},
		{name: "canceled export", err: requestcapture.ErrExportCanceled},
		{name: "valid capability under download pressure", err: requestcapture.ErrDownloadLimitReached},
		{name: "valid capability under memory pressure", err: requestcapture.ErrCapacityExceeded},
		{name: "valid capability with an internal claim fault", err: errors.New("claim invariant rejected")},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(Config{Exports: &stubCaptureService{acceptDownloadFn: func(string, string) (requestcapture.Download, error) {
				return requestcapture.Download{}, test.err
			}}})
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("download_token=secret"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("export_id", testExportID)
			recorder := httptest.NewRecorder()
			handler.DownloadExport(recorder, req)
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
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("download_token=secret"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("export_id", testExportID)
	recorder := httptest.NewRecorder()

	handler.DownloadExport(recorder, request)

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

func TestDownloadExportAlwaysReleasesClaimedCoreLease(t *testing.T) {
	for _, test := range []struct {
		name        string
		streamErr   error
		streamPanic bool
		headerPanic bool
	}{
		{name: "streamer abandons lease"},
		{name: "streamer returns early", streamErr: errors.New("forced stream failure")},
		{name: "streamer panics", streamPanic: true},
		{name: "response header panics", headerPanic: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, session := newAdminQueryManager(t)
			addAdminQueryRecord(t, manager)
			baselineCharge := manager.Status().ProcessMemory.ChargedBytes
			ticket, err := manager.CreateExport(context.Background(), session.SessionID, requestcapture.ExportRequest{Scope: requestcapture.ExportScopeAll})
			if err != nil {
				t.Fatalf("CreateExport() error = %v", err)
			}
			if manager.Status().ProcessMemory.PinnedBytes == 0 {
				t.Fatal("test export did not pin its snapshot")
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
			form := url.Values{downloadTokenField: {ticket.DownloadToken}}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("export_id", ticket.ExportID)
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
				serve.ServeHTTP(destination, req)
			}()
			if !test.headerPanic {
				assertSensitiveHeaders(t, recorder)
			}

			status := manager.Status()
			if status.PendingExportCount != 0 || status.ActiveDownloadCount != 0 ||
				status.ProcessMemory.PinnedBytes != 0 || status.ProcessMemory.TemporaryBytes != 0 ||
				status.ProcessMemory.ChargedBytes != baselineCharge {
				t.Fatalf("claimed download lease was not released: %#v", status)
			}
		})
	}
}
