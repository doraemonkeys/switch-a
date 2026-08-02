package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const serverTestExportID = "ce_AAAAAAAAAAAAAAAAAAAAAAAA"

type routeCaptureService struct {
	mu            sync.Mutex
	statusManager *requestcapture.Manager
	acceptedID    string
	acceptedToken string
	acceptErr     error
}

func (s *routeCaptureService) Start(requestcapture.StartRequest) (requestcapture.SessionInfo, error) {
	return requestcapture.SessionInfo{}, nil
}

func (s *routeCaptureService) Stop(string) error { return nil }

func (s *routeCaptureService) OpenStatus(ctx context.Context) (requestcapture.StatusLease, error) {
	if s.statusManager == nil {
		return requestcapture.StatusLease{}, requestcapture.ErrManagerClosed
	}
	return s.statusManager.OpenStatus(ctx)
}

func (s *routeCaptureService) OpenRecordPage(context.Context, string, requestcapture.ListQuery) (*requestcapture.RecordPageLease, error) {
	return nil, requestcapture.ErrManagerClosed
}

func (s *routeCaptureService) OpenRecordDetail(context.Context, string, string, int) (*requestcapture.RecordDetailLease, error) {
	return nil, requestcapture.ErrManagerClosed
}

func (s *routeCaptureService) CreateExport(context.Context, string, requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
	return requestcapture.ExportTicket{}, nil
}

func (s *routeCaptureService) AcceptDownload(exportID, token string) (requestcapture.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acceptedID = exportID
	s.acceptedToken = token
	return requestcapture.Download{}, s.acceptErr
}

func newCaptureRouteServer(t *testing.T, capture *routeCaptureService) *AdminServer {
	t.Helper()
	return newCaptureRouteServerWithLogger(t, capture, zap.NewNop())
}

func newCaptureRouteServerWithLogger(t *testing.T, capture *routeCaptureService, logger *zap.Logger) *AdminServer {
	t.Helper()
	if capture.statusManager == nil {
		manager, err := requestcapture.NewManager(requestcapture.Config{})
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		capture.statusManager = manager
		t.Cleanup(func() { _ = manager.Close() })
	}
	return NewAdmin(AdminConfig{
		Port:            "0",
		AdminToken:      "test-token",
		Logger:          logger,
		Store:           &mockStore{},
		CaptureSessions: capture,
		CaptureQueries:  capture,
		CaptureExports:  capture,
	})
}

func TestDebugCaptureProtectedRoutesRequireBearerAndSecurityHeaders(t *testing.T) {
	server := newCaptureRouteServer(t, &routeCaptureService{})
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/admin/api/debug-capture/sessions"},
		{method: http.MethodGet, path: "/admin/api/debug-capture/status"},
		{method: http.MethodDelete, path: "/admin/api/debug-capture/sessions/session-1"},
		{method: http.MethodGet, path: "/admin/api/debug-capture/sessions/session-1/records"},
		{method: http.MethodGet, path: "/admin/api/debug-capture/sessions/session-1/records/record-1"},
		{method: http.MethodPost, path: "/admin/api/debug-capture/sessions/session-1/exports"},
		{method: http.MethodGet, path: "/admin/api/debug-capture/unknown"},
		{method: http.MethodGet, path: "/admin/api/debug-capture"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
			}
			assertCaptureSecurityHeaders(t, recorder)
		})
	}
}

func TestDebugCaptureNotFoundNeverLogsRawPath(t *testing.T) {
	observedCore, observedLogs := observer.New(zap.DebugLevel)
	server := newCaptureRouteServerWithLogger(t, &routeCaptureService{}, zap.New(observedCore))
	const pathCanary = "raw-capability-canary-must-not-enter-logs"
	req := httptest.NewRequest(http.MethodGet, debugCaptureAPIPrefix+"/"+pathCanary, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	recorder := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	assertCaptureSecurityHeaders(t, recorder)
	if logs := fmt.Sprint(observedLogs.All()); strings.Contains(logs, pathCanary) {
		t.Fatalf("capture not-found log retained raw path: %s", logs)
	}
}

func TestDebugCaptureStatusUsesBearerAndNestedRouter(t *testing.T) {
	capture := &routeCaptureService{}
	server := newCaptureRouteServer(t, capture)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/debug-capture/status", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	assertCaptureSecurityHeaders(t, recorder)
}

func TestDebugCaptureDownloadUsesCapabilityWithoutBearer(t *testing.T) {
	capture := &routeCaptureService{}
	server := newCaptureRouteServer(t, capture)
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/api/debug-capture/exports/"+serverTestExportID+"/download",
		strings.NewReader("download_token=capability-secret"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	assertCaptureSecurityHeaders(t, recorder)
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.acceptedID != serverTestExportID || capture.acceptedToken != "capability-secret" {
		t.Fatalf("accepted capability = %q %q", capture.acceptedID, capture.acceptedToken)
	}
}

func TestDebugCaptureDownloadWrongMethodIsPublicMethodError(t *testing.T) {
	server := newCaptureRouteServer(t, &routeCaptureService{})
	const capability = "must-not-be-reflected"
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet,
		"/admin/api/debug-capture/exports/"+serverTestExportID+"/download?download_token="+capability,
		nil,
	))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusMethodNotAllowed, recorder.Body.String())
	}
	if got := recorder.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q", got)
	}
	if location := recorder.Header().Get("Location"); location != "" {
		t.Fatalf("Location = %q", location)
	}
	if strings.Contains(recorder.Body.String(), capability) {
		t.Fatalf("response reflected capability: %s", recorder.Body.String())
	}
	assertCaptureSecurityHeaders(t, recorder)
}

func TestDebugCaptureDownloadErrorsKeepSecurityBoundary(t *testing.T) {
	for _, test := range []struct {
		name        string
		capture     *routeCaptureService
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "unsupported content type", capture: &routeCaptureService{}, body: "download_token=secret", wantStatus: http.StatusUnsupportedMediaType},
		{name: "ambiguous form", capture: &routeCaptureService{}, contentType: "application/x-www-form-urlencoded", body: "download_token=secret&extra=value", wantStatus: http.StatusBadRequest},
		{name: "invalid capability", capture: &routeCaptureService{acceptErr: requestcapture.ErrDownloadUnavailable}, contentType: "application/x-www-form-urlencoded", body: "download_token=secret", wantStatus: http.StatusGone},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newCaptureRouteServer(t, test.capture)
			req := httptest.NewRequest(http.MethodPost, "/admin/api/debug-capture/exports/"+serverTestExportID+"/download", strings.NewReader(test.body))
			if test.contentType != "" {
				req.Header.Set("Content-Type", test.contentType)
			}
			recorder := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(recorder, req)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			assertCaptureSecurityHeaders(t, recorder)
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("download error reflected capability: %s", recorder.Body.String())
			}
		})
	}
}

func TestDebugCaptureBoundaryRejectsCanonicalRedirectsWithoutReflectingQuery(t *testing.T) {
	observedCore, observedLogs := observer.New(zap.DebugLevel)
	server := newCaptureRouteServerWithLogger(t, &routeCaptureService{}, zap.New(observedCore))
	const capability = "capability-must-remain-private"
	paths := []string{
		"/admin/api/debug-capture//status?download_token=" + capability,
		"/admin/api/debug-capture/./status?download_token=" + capability,
		"/admin/api/debug-capture/%2e/status?download_token=" + capability,
		"/admin/api/debug-capture/sessions/session-1/../status?download_token=" + capability,
		"/outside/../admin/api/debug-capture/status?download_token=" + capability,
		"/outside/%2e%2e/admin/api/debug-capture/status?download_token=" + capability,
	}

	for _, requestPath := range paths {
		t.Run(requestPath, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, requestPath, nil))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			assertCaptureSecurityHeaders(t, recorder)
			if location := recorder.Header().Get("Location"); location != "" {
				t.Fatalf("Location = %q", location)
			}
			if strings.Contains(recorder.Body.String(), capability) {
				t.Fatalf("response reflected capability: %s", recorder.Body.String())
			}
		})
	}

	if logs := fmt.Sprint(observedLogs.All()); strings.Contains(logs, capability) {
		t.Fatalf("logs reflected capability: %s", logs)
	}
}

func TestDebugCaptureBoundaryDoesNotModifyOtherAdminResponses(t *testing.T) {
	server := newCaptureRouteServer(t, &routeCaptureService{})
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("non-capture Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "" {
		t.Fatalf("non-capture X-Content-Type-Options = %q", got)
	}
}

func TestDebugCaptureBoundaryAppliesHeadersBeforeDownstreamPanic(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := secureDebugCaptureBoundary(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("forced downstream panic")
	}))
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("downstream panic was not observed")
			}
		}()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/debug-capture/status", nil))
	}()
	assertCaptureSecurityHeaders(t, recorder)
}

func assertCaptureSecurityHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}
