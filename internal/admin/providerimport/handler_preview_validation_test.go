package providerimport

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

func TestPreviewProviderImportRejectsUnsafeOrInvalidBodies(t *testing.T) {
	tests := []struct {
		name        string
		body        []byte
		previewErr  error
		wantStatus  int
		wantCalls   int
		wantMessage string
		wantCode    string
		wantKind    string
		wantRetry   string
	}{
		{
			name:        "empty document",
			body:        []byte("  \n\t"),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "empty",
		},
		{
			name:        "dedicated five megabyte limit",
			body:        bytes.Repeat([]byte{'x'}, MaxProviderImportBodySize+1),
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantMessage: "5 MB",
		},
		{
			name:        "parser validation error",
			body:        []byte(`{"accounts":"not-an-array"}`),
			previewErr:  fmt.Errorf("%w: accounts must be an array", providerauth.ErrChatGPTProviderImportInvalidDocument),
			wantStatus:  http.StatusBadRequest,
			wantCalls:   1,
			wantMessage: "accounts must be an array",
		},
		{
			name:        "draft capacity is retryable",
			body:        []byte(`{"accounts":[]}`),
			previewErr:  providerauth.ErrChatGPTProviderImportCapacityExceeded,
			wantStatus:  http.StatusTooManyRequests,
			wantCalls:   1,
			wantMessage: "retry",
			wantCode:    ErrCodeConflict,
			wantKind:    "provider_import_capacity_exceeded",
			wantRetry:   providerImportRetryAfter,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeProviderImportService{previewErr: test.previewErr}
			handler := newProviderImportTestHandler(newMockStore(), service, &fakeProviderImportStore{})
			req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", bytes.NewReader(test.body))
			responseRecorder := httptest.NewRecorder()

			handler.PreviewProviderImport(responseRecorder, req)

			requireProviderImportStatus(t, responseRecorder, test.wantStatus)
			wantCode := test.wantCode
			if wantCode == "" {
				wantCode = ErrCodeValidation
			}
			assertProviderImportError(t, responseRecorder, wantCode, test.wantMessage)
			if test.wantKind != "" {
				var response model.ErrorResponse
				decodeProviderImportResponse(t, responseRecorder, &response)
				if response.Details["kind"] != test.wantKind {
					t.Fatalf("error details = %#v, want kind %q", response.Details, test.wantKind)
				}
			}
			if got := responseRecorder.Header().Get("Retry-After"); got != test.wantRetry {
				t.Fatalf("Retry-After = %q, want %q", got, test.wantRetry)
			}
			if service.previewCalls != test.wantCalls {
				t.Fatalf("preview calls = %d, want %d", service.previewCalls, test.wantCalls)
			}
		})
	}
}
