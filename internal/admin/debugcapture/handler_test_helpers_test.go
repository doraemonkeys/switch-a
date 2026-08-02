package debugcapture

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

type stubProviderCatalog struct {
	mu        sync.Mutex
	providers []model.Provider
	err       error
	calls     int
}

type trackingResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
	writes int
}

func (w *trackingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *trackingResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *trackingResponseWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.writes++
	return w.body.Write(payload)
}

func (w *trackingResponseWriter) wroteResponse() bool {
	return w.status != 0 || w.writes != 0 || w.body.Len() != 0
}

func newAdminQueryManager(t *testing.T) (*requestcapture.Manager, requestcapture.SessionInfo) {
	t.Helper()
	manager, err := requestcapture.NewManager(requestcapture.Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	session, err := manager.Start(requestcapture.StartRequest{
		Providers:                   []requestcapture.ProviderIdentity{{ID: "provider-1", Name: "Provider 1"}},
		CompletedRecordsPerProvider: 10,
		AcknowledgeRawPayloadRisk:   true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return manager, session
}

func addAdminQueryRecord(t *testing.T, manager *requestcapture.Manager) string {
	t.Helper()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "gateway-1"})
	recorder := gateway.BeginHTTP(requestcapture.RawHTTPStart{
		URL: &url.URL{Scheme: "https", Host: "provider.example", Path: "/v1/messages"},
		Attempt: requestcapture.AttemptMetadata{
			Provider:             requestcapture.ProviderIdentity{ID: "provider-1", Name: "Provider 1"},
			APIType:              "claude",
			SelectionMode:        requestcapture.SelectionModeInitial,
			SelectionSource:      requestcapture.SelectionSourceStrategy,
			ProviderAttemptIndex: 0,
			CredentialPhase:      requestcapture.CredentialPhaseInitial,
		},
		Request: requestcapture.RawRequest{
			Method:  http.MethodPost,
			Headers: http.Header{"Content-Type": {"application/json"}},
			Body:    []byte(`{"prompt":"hello"}`),
		},
	})
	if !recorder.Valid() {
		t.Fatal("test capture recorder is disabled")
	}
	recorder.ObserveResponse(requestcapture.HTTPResponseHead{
		StatusCode: http.StatusOK,
		Protocol:   "HTTP/2.0",
		Headers:    http.Header{"Content-Type": {"application/json"}},
	})
	recorder.ObserveUpstream([]byte(`{"answer":"world"}`))
	recorder.Finish(requestcapture.Outcome{
		SourceCompletion:  requestcapture.SourceCompletionComplete,
		TerminationReason: requestcapture.TerminationReasonEOF,
	})
	gateway.Finish(requestcapture.GatewayOutcome{TerminationReason: requestcapture.TerminationReasonEOF})
	return recorder.ID()
}

func (s *stubProviderCatalog) ListProviders(context.Context) ([]model.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return append([]model.Provider(nil), s.providers...), s.err
}

type stubCaptureService struct {
	startFn          func(requestcapture.StartRequest) (requestcapture.SessionInfo, error)
	stopFn           func(string) error
	openStatusFn     func(context.Context) (requestcapture.StatusLease, error)
	openPageFn       func(context.Context, string, requestcapture.ListQuery) (*requestcapture.RecordPageLease, error)
	openDetailFn     func(context.Context, string, string, int) (*requestcapture.RecordDetailLease, error)
	createExportFn   func(context.Context, string, requestcapture.ExportRequest) (requestcapture.ExportTicket, error)
	acceptDownloadFn func(string, string) (requestcapture.Download, error)
}

func (s *stubCaptureService) Start(input requestcapture.StartRequest) (requestcapture.SessionInfo, error) {
	if s.startFn == nil {
		return requestcapture.SessionInfo{}, nil
	}
	return s.startFn(input)
}

func (s *stubCaptureService) Stop(sessionID string) error {
	if s.stopFn == nil {
		return nil
	}
	return s.stopFn(sessionID)
}

func (s *stubCaptureService) OpenStatus(ctx context.Context) (requestcapture.StatusLease, error) {
	if s.openStatusFn == nil {
		return requestcapture.StatusLease{}, requestcapture.ErrManagerClosed
	}
	return s.openStatusFn(ctx)
}

func (s *stubCaptureService) OpenRecordPage(ctx context.Context, sessionID string, query requestcapture.ListQuery) (*requestcapture.RecordPageLease, error) {
	if s.openPageFn == nil {
		return nil, requestcapture.ErrManagerClosed
	}
	return s.openPageFn(ctx, sessionID, query)
}

func (s *stubCaptureService) OpenRecordDetail(ctx context.Context, sessionID, recordID string, previewBytes int) (*requestcapture.RecordDetailLease, error) {
	if s.openDetailFn == nil {
		return nil, requestcapture.ErrManagerClosed
	}
	return s.openDetailFn(ctx, sessionID, recordID, previewBytes)
}

func (s *stubCaptureService) CreateExport(ctx context.Context, sessionID string, input requestcapture.ExportRequest) (requestcapture.ExportTicket, error) {
	if s.createExportFn == nil {
		return requestcapture.ExportTicket{}, nil
	}
	return s.createExportFn(ctx, sessionID, input)
}

func (s *stubCaptureService) AcceptDownload(exportID, token string) (requestcapture.Download, error) {
	if s.acceptDownloadFn == nil {
		return requestcapture.Download{}, requestcapture.ErrDownloadUnavailable
	}
	return s.acceptDownloadFn(exportID, token)
}

type downloadStreamerFunc func(context.Context, requestcapture.Download, io.Writer) error

func (f downloadStreamerFunc) Stream(ctx context.Context, download requestcapture.Download, destination io.Writer) error {
	return f(ctx, download, destination)
}
