package providerimport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

type blockingProviderImportResponseWriter struct {
	header       http.Header
	writeStarted chan struct{}
	releaseWrite chan struct{}
	once         sync.Once
}

func newBlockingProviderImportResponseWriter() *blockingProviderImportResponseWriter {
	return &blockingProviderImportResponseWriter{
		header:       make(http.Header),
		writeStarted: make(chan struct{}),
		releaseWrite: make(chan struct{}),
	}
}

func (w *blockingProviderImportResponseWriter) Header() http.Header { return w.header }

func (w *blockingProviderImportResponseWriter) WriteHeader(_ int) {}

func (w *blockingProviderImportResponseWriter) Write(payload []byte) (int, error) {
	w.once.Do(func() { close(w.writeStarted) })
	<-w.releaseWrite
	return len(payload), nil
}

func TestCommitProviderImportReleasesCredentialLeaseBeforeResponseWrite(t *testing.T) {
	candidate := providerImportReadyCandidate(
		"candidate-create",
		"account-create",
		"Lease Lifetime",
		providerImportStoredCredentialMarker,
	)
	service := &fakeProviderImportService{candidates: []providerauth.ChatGPTProviderImportCandidate{candidate}}
	importStore := &fakeProviderImportStore{}
	handler := newProviderImportTestHandler(newMockStore(), service, importStore)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports/import/commit", strings.NewReader(`{
		"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"Lease Lifetime"}]
	}`))
	request.SetPathValue("import_id", "import")
	response := newBlockingProviderImportResponseWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.CommitProviderImport(response, request)
	}()

	<-response.writeStarted
	if importStore.releaseCalls != 1 {
		t.Fatalf("credential lease releases before blocked response write = %d, want 1", importStore.releaseCalls)
	}
	close(response.releaseWrite)
	<-done
}
