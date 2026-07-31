package providerimport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

type blockingProviderImportDraftService struct {
	*fakeProviderImportService
	started chan struct{}
	release chan struct{}
}

type stalledProviderImportBody struct {
	started    chan struct{}
	expired    chan struct{}
	startOnce  sync.Once
	expireOnce sync.Once
}

func newStalledProviderImportBody() *stalledProviderImportBody {
	return &stalledProviderImportBody{started: make(chan struct{}), expired: make(chan struct{})}
}

func (b *stalledProviderImportBody) Read(_ []byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.expired
	return 0, os.ErrDeadlineExceeded
}

func (b *stalledProviderImportBody) Close() error {
	b.expire()
	return nil
}

func (b *stalledProviderImportBody) expire() {
	b.expireOnce.Do(func() { close(b.expired) })
}

type deadlineProviderImportResponseWriter struct {
	*httptest.ResponseRecorder
	body            *stalledProviderImportBody
	timer           *time.Timer
	deadlineSet     bool
	deadlineCleared bool
}

func (w *deadlineProviderImportResponseWriter) SetReadDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		w.deadlineCleared = true
		if w.timer != nil {
			w.timer.Stop()
		}
		return nil
	}
	w.deadlineSet = true
	w.timer = time.AfterFunc(time.Until(deadline), w.body.expire)
	return nil
}

func (s *blockingProviderImportDraftService) PreviewSub2APIChatGPTImport(
	_ []byte,
) (*providerauth.ChatGPTProviderImportPreview, error) {
	s.started <- struct{}{}
	<-s.release
	return nil, providerauth.ErrChatGPTProviderImportInvalidDocument
}

func TestPreviewProviderImportBoundsBodiesBeforeDraftAdmission(t *testing.T) {
	service := &blockingProviderImportDraftService{
		fakeProviderImportService: &fakeProviderImportService{},
		started:                   make(chan struct{}, maxConcurrentProviderImportBodyReads+1),
		release:                   make(chan struct{}),
	}
	handler := newProviderImportTestHandler(newMockStore(), service, &fakeProviderImportStore{})
	var workers sync.WaitGroup
	workers.Add(maxConcurrentProviderImportBodyReads)
	for index := 0; index < maxConcurrentProviderImportBodyReads; index++ {
		go func() {
			defer workers.Done()
			request := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{"accounts":[]}`))
			handler.PreviewProviderImport(httptest.NewRecorder(), request)
		}()
	}
	for index := 0; index < maxConcurrentProviderImportBodyReads; index++ {
		<-service.started
	}

	blockedRequest := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{"accounts":[]}`))
	blockedResponse := httptest.NewRecorder()
	handler.PreviewProviderImport(blockedResponse, blockedRequest)

	requireProviderImportStatus(t, blockedResponse, http.StatusTooManyRequests)
	if got := blockedResponse.Header().Get("Retry-After"); got != providerImportRetryAfter {
		t.Fatalf("Retry-After = %q, want %q", got, providerImportRetryAfter)
	}
	var capacityError model.ErrorResponse
	decodeProviderImportResponse(t, blockedResponse, &capacityError)
	if capacityError.Details["kind"] != "provider_import_capacity_exceeded" {
		t.Fatalf("error details = %#v, want shared capacity kind", capacityError.Details)
	}
	select {
	case <-service.started:
		t.Fatal("request reached draft service without acquiring a bounded body-read slot")
	default:
	}
	close(service.release)
	workers.Wait()
	if got := len(handler.providerImportReadSlots); got != 0 {
		t.Fatalf("body-read slots retained after completion = %d, want 0", got)
	}
}

func TestPreviewProviderImportTimesOutStalledBodyAndReleasesSlot(t *testing.T) {
	handler := newProviderImportTestHandler(newMockStore(), &fakeProviderImportService{}, &fakeProviderImportStore{})
	handler.providerImportReadTimeout = 20 * time.Millisecond
	handler.providerImportSetReadDeadline = setHTTPResponseReadDeadline
	body := newStalledProviderImportBody()
	request := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", nil)
	request.Body = body
	response := &deadlineProviderImportResponseWriter{ResponseRecorder: httptest.NewRecorder(), body: body}

	handler.PreviewProviderImport(response, request)

	requireProviderImportStatus(t, response.ResponseRecorder, http.StatusRequestTimeout)
	var apiError model.ErrorResponse
	decodeProviderImportResponse(t, response.ResponseRecorder, &apiError)
	if apiError.Details["kind"] != "provider_import_body_read_timeout" {
		t.Fatalf("error details = %#v, want body read timeout kind", apiError.Details)
	}
	select {
	case <-body.started:
	default:
		t.Fatal("stalled body was not read")
	}
	if got := len(handler.providerImportReadSlots); got != 0 {
		t.Fatalf("body-read slots retained after timeout = %d, want 0", got)
	}
	if !response.deadlineSet || !response.deadlineCleared {
		t.Fatalf("response-controller deadlines = (set %t, cleared %t), want both", response.deadlineSet, response.deadlineCleared)
	}
}

func TestPreviewProviderImportRejectsWriterWithoutReadDeadlineSupport(t *testing.T) {
	service := &fakeProviderImportService{}
	handler := newProviderImportTestHandler(newMockStore(), service, &fakeProviderImportStore{})
	handler.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error {
		return http.ErrNotSupported
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{"accounts":[]}`))
	response := httptest.NewRecorder()

	handler.PreviewProviderImport(response, request)

	requireProviderImportStatus(t, response, http.StatusServiceUnavailable)
	var apiError model.ErrorResponse
	decodeProviderImportResponse(t, response, &apiError)
	if apiError.Details["kind"] != "provider_import_upload_protection_unavailable" {
		t.Fatalf("error details = %#v, want upload protection kind", apiError.Details)
	}
	if service.previewCalls != 0 || len(handler.providerImportReadSlots) != 0 {
		t.Fatalf("unsupported deadline side effects = (%d previews, %d slots), want zero", service.previewCalls, len(handler.providerImportReadSlots))
	}
}

func TestReadProviderImportBodyPreservesDeadlineErrorCause(t *testing.T) {
	cause := errors.New("deadline transport unavailable")
	handler := newProviderImportTestHandler(newMockStore(), &fakeProviderImportService{}, &fakeProviderImportStore{})
	handler.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error {
		return cause
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{}`))

	_, err := handler.readProviderImportBody(httptest.NewRecorder(), request)

	if !errors.Is(err, errProviderImportBodyReadDeadlineUnavailable) {
		t.Fatalf("read error = %v, want deadline-unavailable classification", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("read error = %v, want original cause", err)
	}
}
