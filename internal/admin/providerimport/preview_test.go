package providerimport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestPreviewProviderImportBuildsReviewSafePlan(t *testing.T) {
	t.Parallel()
	expiresAt := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	authExpiry := expiresAt.Add(-time.Minute)
	longWarning := strings.Repeat("界", maxProviderImportMessageCharacters+20)
	preview := &providerauth.ChatGPTProviderImportPreview{
		ImportID:  "import-preview",
		ExpiresAt: expiresAt,
		Items: []providerauth.ChatGPTProviderImportPreviewItem{
			{CandidateID: "ready-a", SourceIndex: 0, State: providerauth.ChatGPTProviderImportCandidateStateReady, Name: " Same Name ", Priority: 4, Concurrency: 5, Auth: &providerauth.ProviderAuthView{Email: "a@example.com", AccountID: "account-a", PlanType: "plus", ExpiresAt: &authExpiry}},
			{CandidateID: "ready-b", SourceIndex: 1, State: providerauth.ChatGPTProviderImportCandidateStateReady, Name: "Same Name", Auth: &providerauth.ProviderAuthView{Email: "b@example.com", AccountID: "account-a"}},
			{CandidateID: "duplicate", SourceIndex: 2, State: providerauth.ChatGPTProviderImportCandidateStateDuplicate, Name: "Same Name"},
			{CandidateID: "invalid", SourceIndex: 3, State: providerauth.ChatGPTProviderImportCandidateStateInvalid, Warnings: []providerauth.ChatGPTProviderImportWarning{{Code: strings.Repeat("c", maxProviderImportWarningCodeCharacters+1), Message: longWarning}}},
			{CandidateID: "unsupported", SourceIndex: 4, State: providerauth.ChatGPTProviderImportCandidateStateUnsupported, Auth: &providerauth.ProviderAuthView{Email: "fallback@example.com"}},
			{CandidateID: "existing", SourceIndex: 5, State: providerauth.ChatGPTProviderImportCandidateStateExisting},
		},
		Warnings: []providerauth.ChatGPTProviderImportWarning{{Code: " document ", Message: " warning "}},
	}
	catalog := &providerImportTestCatalog{providers: []model.Provider{{ID: "same-name"}, {ID: "same-name-2"}}}
	drafts := &providerImportTestDrafts{preview: preview}
	core, logs := observer.New(zap.DebugLevel)
	handler := newProviderImportTestHandler(catalog, drafts, nil, nil, zap.New(core))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{"accounts":[]}`))

	handler.PreviewProviderImport(w, r)

	requireProviderImportStatus(t, w, http.StatusCreated)
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("preview response disclosed credential material")
	}
	if string(drafts.previewRaw) != `{"accounts":[]}` {
		t.Fatalf("parser input = %q", drafts.previewRaw)
	}
	if drafts.sealedImportID != preview.ImportID || len(drafts.sealed) != len(preview.Items) {
		t.Fatalf("sealed disposition = id %q count %d", drafts.sealedImportID, len(drafts.sealed))
	}
	var response ProviderImportPreviewResponse
	decodeProviderImportTestJSON(t, w, &response)
	if response.CreateDefaults.Weight != DefaultWeight || response.CreateDefaults.MaxRetries != DefaultProviderMaxRetries {
		t.Fatalf("create defaults = %#v", response.CreateDefaults)
	}
	if response.Items[0].ProviderID != "same-name-3" || response.Items[1].ProviderID != "same-name-4" {
		t.Fatalf("allocated IDs = %q, %q", response.Items[0].ProviderID, response.Items[1].ProviderID)
	}
	if response.Items[2].ProviderID != "same-name" || response.Items[2].DefaultSelected {
		t.Fatalf("blocked row reservation = %#v", response.Items[2])
	}
	if response.Items[3].Name != "ChatGPT Account 4" || len([]rune(response.Items[3].Message)) > maxProviderImportMessageCharacters {
		t.Fatalf("bounded invalid row = %#v", response.Items[3])
	}
	if response.Items[4].Name != "fallback@example.com" || response.Items[5].Message == "" {
		t.Fatalf("fallback rows = %#v %#v", response.Items[4], response.Items[5])
	}
	if response.Items[0].ExpiresAt == &authExpiry {
		t.Fatal("preview retained caller-owned timestamp pointer")
	}
	wantSummary := providerauth.ChatGPTProviderImportSummary{Total: 6, Ready: 2, Existing: 1, Duplicate: 1, Invalid: 1, Unsupported: 1}
	if response.Summary != wantSummary {
		t.Fatalf("summary = %#v, want %#v", response.Summary, wantSummary)
	}
	if logs.FilterMessage("provider import preview created").Len() != 1 {
		t.Fatalf("preview milestone logs = %d", logs.Len())
	}
}

func TestPreviewProviderImportAdmissionAndReadFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		configure  func(*Handler, *http.Request)
		body       string
		wantStatus int
		wantKind   string
	}{
		{name: "unavailable", configure: func(h *Handler, _ *http.Request) { h.providerImports = nil }, body: `{}`, wantStatus: http.StatusNotImplemented},
		{name: "read capacity", configure: func(h *Handler, _ *http.Request) {
			h.providerImportReadSlots = make(chan struct{}, 1)
			h.providerImportReadSlots <- struct{}{}
		}, body: `{}`, wantStatus: http.StatusTooManyRequests, wantKind: "provider_import_capacity_exceeded"},
		{name: "deadline unavailable", configure: func(h *Handler, _ *http.Request) {
			h.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error { return errors.New("unsupported") }
		}, body: `{}`, wantStatus: http.StatusServiceUnavailable, wantKind: "provider_import_upload_protection_unavailable"},
		{name: "deadline timeout", configure: func(_ *Handler, r *http.Request) { r.Body = providerImportTestBody{err: os.ErrDeadlineExceeded} }, wantStatus: http.StatusRequestTimeout, wantKind: "provider_import_body_read_timeout"},
		{name: "generic read", configure: func(_ *Handler, r *http.Request) { r.Body = providerImportTestBody{err: errors.New("broken reader")} }, wantStatus: http.StatusBadRequest},
		{name: "empty", body: " \n\t ", wantStatus: http.StatusBadRequest},
		{name: "too large", body: strings.Repeat("x", MaxProviderImportBodySize+1), wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newProviderImportTestHandler(nil, &providerImportTestDrafts{}, nil, nil, nil)
			r := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(testCase.body))
			if testCase.configure != nil {
				testCase.configure(h, r)
			}
			w := httptest.NewRecorder()
			h.PreviewProviderImport(w, r)
			requireProviderImportStatus(t, w, testCase.wantStatus)
			if testCase.wantKind != "" && !strings.Contains(w.Body.String(), testCase.wantKind) {
				t.Fatalf("body = %s, want kind %q", w.Body.String(), testCase.wantKind)
			}
		})
	}
}

func TestPreviewProviderImportDraftAndEnrichmentFailures(t *testing.T) {
	t.Parallel()
	draftErrors := []struct {
		name       string
		err        error
		wantStatus int
		wantKind   string
	}{
		{"capacity", providerauth.ErrChatGPTProviderImportCapacityExceeded, http.StatusTooManyRequests, "provider_import_capacity_exceeded"},
		{"in progress", providerauth.ErrChatGPTProviderImportInProgress, http.StatusConflict, "provider_import_in_progress"},
		{"expired", providerauth.ErrChatGPTProviderImportExpired, http.StatusGone, ""},
		{"not found", providerauth.ErrChatGPTProviderImportNotFound, http.StatusGone, ""},
		{"candidate", providerauth.ErrChatGPTProviderImportCandidateNotFound, http.StatusBadRequest, ""},
		{"document", providerauth.ErrChatGPTProviderImportInvalidDocument, http.StatusBadRequest, ""},
		{"candidate invalid", providerauth.ErrChatGPTProviderImportInvalidCandidate, http.StatusBadRequest, ""},
		{"other", errors.New("opaque"), http.StatusBadRequest, ""},
	}
	for _, testCase := range draftErrors {
		t.Run(testCase.name, func(t *testing.T) {
			drafts := &providerImportTestDrafts{previewErr: testCase.err}
			h := newProviderImportTestHandler(nil, drafts, nil, nil, nil)
			w := httptest.NewRecorder()
			h.PreviewProviderImport(w, httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{}`)))
			requireProviderImportStatus(t, w, testCase.wantStatus)
			if testCase.wantKind != "" && !strings.Contains(w.Body.String(), testCase.wantKind) {
				t.Fatalf("body = %s", w.Body.String())
			}
		})
	}

	t.Run("catalog failure cancels draft", func(t *testing.T) {
		drafts := &providerImportTestDrafts{preview: &providerauth.ChatGPTProviderImportPreview{ImportID: "catalog-failure"}, cancelErr: errors.New("cleanup failed")}
		h := newProviderImportTestHandler(&providerImportTestCatalog{err: errors.New("catalog failed")}, drafts, nil, nil, zap.NewNop())
		w := httptest.NewRecorder()
		h.PreviewProviderImport(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		requireProviderImportStatus(t, w, http.StatusInternalServerError)
		if drafts.cancelledImport != "catalog-failure" {
			t.Fatalf("cancelled import = %q", drafts.cancelledImport)
		}
	})
	t.Run("seal failure cancels draft", func(t *testing.T) {
		drafts := &providerImportTestDrafts{preview: &providerauth.ChatGPTProviderImportPreview{ImportID: "seal-failure"}, sealErr: errors.New("seal failed")}
		h := newProviderImportTestHandler(nil, drafts, nil, nil, zap.NewNop())
		w := httptest.NewRecorder()
		h.PreviewProviderImport(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		requireProviderImportStatus(t, w, http.StatusInternalServerError)
		if drafts.cancelledImport != "seal-failure" {
			t.Fatalf("cancelled import = %q", drafts.cancelledImport)
		}
	})
}

func TestCancelProviderImportIsIdempotentAndReportsContention(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		importID    string
		err         error
		unavailable bool
		wantStatus  int
	}{
		{name: "success", importID: " import-1 ", wantStatus: http.StatusNoContent},
		{name: "not found", importID: "missing", err: providerauth.ErrChatGPTProviderImportNotFound, wantStatus: http.StatusNoContent},
		{name: "expired", importID: "expired", err: providerauth.ErrChatGPTProviderImportExpired, wantStatus: http.StatusNoContent},
		{name: "in progress", importID: "busy", err: providerauth.ErrChatGPTProviderImportInProgress, wantStatus: http.StatusConflict},
		{name: "failure", importID: "failed", err: errors.New("store"), wantStatus: http.StatusInternalServerError},
		{name: "missing id", importID: " ", wantStatus: http.StatusBadRequest},
		{name: "unavailable", importID: "id", unavailable: true, wantStatus: http.StatusNotImplemented},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			drafts := &providerImportTestDrafts{cancelErr: testCase.err}
			h := newProviderImportTestHandler(nil, drafts, nil, nil, zap.NewNop())
			if testCase.unavailable {
				h.providerImports = nil
			}
			w := httptest.NewRecorder()
			h.CancelProviderImport(w, providerImportTestCancelRequest(testCase.importID))
			requireProviderImportStatus(t, w, testCase.wantStatus)
		})
	}
}

func decodeProviderImportTestJSON(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}
