package providerimport

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

func TestCommitProviderImportVerificationFailureReleasesClaimWithoutWrites(t *testing.T) {
	tests := []struct {
		name            string
		verificationErr error
		wantStatus      int
		wantKind        string
		wantCandidateID string
		wantRetryAfter  string
	}{
		{
			name:            "invalid signed token identifies row",
			verificationErr: &providerauth.ChatGPTProviderImportVerificationError{CandidateID: "candidate-create"},
			wantStatus:      http.StatusUnprocessableEntity,
			wantKind:        "provider_import_token_verification_failed",
			wantCandidateID: "candidate-create",
		},
		{
			name:            "signing keys unavailable is retryable",
			verificationErr: providerauth.ErrChatGPTProviderImportJWKSUnavailable,
			wantStatus:      http.StatusServiceUnavailable,
			wantKind:        "provider_import_signing_keys_unavailable",
			wantRetryAfter:  providerImportRetryAfter,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := providerImportReadyCandidate(
				"candidate-create",
				"account-create",
				"Verification Failure",
				providerImportStoredCredentialMarker,
			)
			events := []string{}
			service := &fakeProviderImportService{
				candidates: []providerauth.ChatGPTProviderImportCandidate{candidate},
				verifyErr:  test.verificationErr,
				events:     &events,
			}
			importStore := &fakeProviderImportStore{events: &events}
			handler := newProviderImportTestHandler(newMockStore(), service, importStore)

			response := commitProviderImportRequest(t, handler, "verification-failure", `{
				"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"Verification Failure"}]
			}`)

			requireProviderImportStatus(t, response, test.wantStatus)
			var apiError model.ErrorResponse
			decodeProviderImportResponse(t, response, &apiError)
			if apiError.Details["kind"] != test.wantKind || apiError.Details["candidate_id"] != test.wantCandidateID {
				t.Fatalf("error details = %#v, want kind %q and candidate %q", apiError.Details, test.wantKind, test.wantCandidateID)
			}
			if got := response.Header().Get("Retry-After"); got != test.wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, test.wantRetryAfter)
			}
			if !reflect.DeepEqual(events, []string{"claim", "verify", "release"}) {
				t.Fatalf("workflow events = %v, want claim released immediately after verification failure", events)
			}
			if len(importStore.leaseProviderIDs) != 0 || len(importStore.bundles) != 0 ||
				len(service.invalidateCalls) != 0 || len(service.finalizeCalls) != 0 {
				t.Fatalf(
					"side effects = (%d leases, %d writes, %d invalidations, %d finalizes), want zero",
					len(importStore.leaseProviderIDs),
					len(importStore.bundles),
					len(service.invalidateCalls),
					len(service.finalizeCalls),
				)
			}
		})
	}
}
