package providerimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

const (
	providerImportRawCredentialMarker      = "raw-credential-material-must-not-leak"
	providerImportStoredCredentialMarker   = "stored-credential-material-must-not-leak"
	providerImportCreatedCredentialMarker  = "created-credential-material-must-not-leak"
	providerImportUpdatedCredentialMarker  = "updated-credential-material-must-not-leak"
	providerImportExistingCredentialMarker = "existing-credential-material-must-not-leak"
)

type mockStore struct {
	providers map[string]*model.Provider
	listErr   error
}

func newMockStore() *mockStore {
	return &mockStore{providers: make(map[string]*model.Provider)}
}

func (m *mockStore) ListProviders(_ context.Context) ([]model.Provider, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	providers := make([]model.Provider, 0, len(m.providers))
	for _, provider := range m.providers {
		providers = append(providers, *provider)
	}
	return providers, nil
}

type fakeProviderImportStore struct {
	err              error
	leaseErr         error
	bundles          []*store.ProviderImportBundle
	leaseProviderIDs [][]string
	applyContexts    []context.Context
	releaseCalls     int
	receipts         map[string]*store.ProviderImportReceipt
	getReceiptErr    error
	receiptClock     interface{ Now() time.Time }
	events           *[]string
}

type providerImportMutationContextKey struct{}

type providerImportTestClock struct {
	now time.Time
}

func (c *providerImportTestClock) Now() time.Time {
	return c.now
}

func (f *fakeProviderImportStore) ApplyProviderImport(ctx context.Context, bundle *store.ProviderImportBundle) error {
	f.bundles = append(f.bundles, bundle)
	f.applyContexts = append(f.applyContexts, ctx)
	if f.events != nil {
		*f.events = append(*f.events, "apply")
	}
	if f.err != nil {
		return f.err
	}
	if bundle != nil && bundle.Receipt != nil {
		if f.receipts == nil {
			f.receipts = make(map[string]*store.ProviderImportReceipt)
		}
		if existing := f.receipts[bundle.Receipt.ImportID]; existing != nil {
			if existing.Fingerprint == bundle.Receipt.Fingerprint {
				return &store.ProviderImportReceiptReplayError{Receipt: *cloneTestProviderImportReceipt(existing)}
			}
			return &store.ProviderImportReceiptConflictError{ImportID: bundle.Receipt.ImportID}
		}
		f.receipts[bundle.Receipt.ImportID] = cloneTestProviderImportReceipt(bundle.Receipt)
	}
	return nil
}

func (f *fakeProviderImportStore) GetProviderImportReceipt(
	_ context.Context,
	importID string,
) (*store.ProviderImportReceipt, error) {
	if f.getReceiptErr != nil {
		return nil, f.getReceiptErr
	}
	receipt := f.receipts[importID]
	if receipt == nil {
		return nil, store.ErrProviderImportReceiptNotFound
	}
	if f.receiptClock != nil && !receipt.ExpiresAt.After(f.receiptClock.Now()) {
		delete(f.receipts, importID)
		return nil, store.ErrProviderImportReceiptNotFound
	}
	return cloneTestProviderImportReceipt(receipt), nil
}

func cloneTestProviderImportReceipt(receipt *store.ProviderImportReceipt) *store.ProviderImportReceipt {
	if receipt == nil {
		return nil
	}
	clone := *receipt
	clone.ResponsePayload = append([]byte(nil), receipt.ResponsePayload...)
	return &clone
}

func (f *fakeProviderImportStore) WithProviderCredentialMutations(
	ctx context.Context,
	providerIDs []string,
) (context.Context, func(), error) {
	f.leaseProviderIDs = append(f.leaseProviderIDs, append([]string(nil), providerIDs...))
	if f.leaseErr != nil {
		return nil, nil, f.leaseErr
	}
	ownedContext := context.WithValue(ctx, providerImportMutationContextKey{}, true)
	return ownedContext, func() {
		f.releaseCalls++
		if f.events != nil {
			*f.events = append(*f.events, "lease_release")
		}
	}, nil
}

var (
	_ DraftService = (*fakeProviderImportService)(nil)
	_ Store        = (*fakeProviderImportStore)(nil)
)

func TestPreviewProviderImportEnrichesFlatReviewSafeResponse(t *testing.T) {
	now := time.Date(2026, time.July, 30, 13, 0, 0, 0, time.UTC)
	draftExpiresAt := now.Add(15 * time.Minute)
	credentialExpiresAt := now.Add(40 * time.Minute)
	existingCredentialCreatedAt := now.Add(-24 * time.Hour)
	existingAccountID := "account-existing"

	service := &fakeProviderImportService{
		preview: &providerauth.ChatGPTProviderImportPreview{
			ImportID:  "opaque-import-id",
			ExpiresAt: draftExpiresAt,
			// The handler deliberately recomputes this summary after existing-account
			// enrichment, so a parser-level summary cannot leave the UI inconsistent.
			Summary: providerauth.ChatGPTProviderImportSummary{Total: 999, Ready: 999},
			Warnings: []providerauth.ChatGPTProviderImportWarning{{
				Code:    providerauth.ChatGPTProviderImportWarningProxiesIgnored,
				Message: "Proxy definitions are not imported.",
			}},
			Items: []providerauth.ChatGPTProviderImportPreviewItem{
				{
					CandidateID: "candidate-new",
					SourceIndex: 0,
					State:       providerauth.ChatGPTProviderImportCandidateStateReady,
					Name:        "Fresh Account",
					Priority:    3,
					Concurrency: 8,
					Auth: &providerauth.ProviderAuthView{
						Type:      model.ProviderCredentialTypeChatGPT,
						Status:    model.ProviderAuthStatusActive,
						Email:     "fresh@example.test",
						AccountID: "account-new",
						PlanType:  "plus",
						ExpiresAt: &credentialExpiresAt,
					},
				},
				{
					CandidateID: "candidate-existing",
					SourceIndex: 1,
					State:       providerauth.ChatGPTProviderImportCandidateStateReady,
					Name:        "Source Name Must Be Replaced",
					Priority:    1,
					Concurrency: 2,
					Auth: &providerauth.ProviderAuthView{
						Type:      model.ProviderCredentialTypeChatGPT,
						Status:    model.ProviderAuthStatusActive,
						Email:     "existing@example.test",
						AccountID: existingAccountID,
						PlanType:  "team",
					},
				},
				{
					CandidateID: "candidate-duplicate",
					SourceIndex: 2,
					State:       providerauth.ChatGPTProviderImportCandidateStateDuplicate,
					Name:        "Repeated Account",
					Warnings: []providerauth.ChatGPTProviderImportWarning{{
						Code:    providerauth.ChatGPTProviderImportWarningDuplicateAccount,
						Message: "This account is repeated in the source file.",
					}},
				},
				{
					CandidateID: "candidate-invalid",
					SourceIndex: 3,
					State:       providerauth.ChatGPTProviderImportCandidateStateInvalid,
					Name:        "Invalid Account",
				},
				{
					CandidateID: "candidate-unsupported",
					SourceIndex: 4,
					State:       providerauth.ChatGPTProviderImportCandidateStateUnsupported,
					Name:        "Unsupported Account",
				},
			},
		},
	}
	importStore := &fakeProviderImportStore{}
	adminStore := newMockStore()
	adminStore.providers["fresh-account"] = &model.Provider{
		ID:             "fresh-account",
		Name:           "ID collision",
		CredentialType: model.ProviderCredentialTypeAPIKey,
	}
	adminStore.providers["bound-provider"] = &model.Provider{
		ID:          "bound-provider",
		Name:        "Current Provider Name",
		Priority:    41,
		Concurrency: 17,
		Credential: &model.ProviderCredential{
			ProviderID:       "bound-provider",
			SecretData:       providerImportExistingCredentialMarker,
			BindingAccountID: &existingAccountID,
			Version:          7,
			CreatedAt:        existingCredentialCreatedAt,
		},
	}
	handler := newProviderImportTestHandler(adminStore, service, importStore)
	raw := []byte(`{"accounts":[{"credential_marker":"` + providerImportRawCredentialMarker + `"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", bytes.NewReader(raw))
	responseRecorder := httptest.NewRecorder()

	handler.PreviewProviderImport(responseRecorder, req)

	requireProviderImportStatus(t, responseRecorder, http.StatusCreated)
	if service.previewCalls != 1 || !bytes.Equal(service.previewRaw, raw) {
		t.Fatalf("preview service calls/raw = (%d, %q), want (1, exact request body)", service.previewCalls, service.previewRaw)
	}
	wantDispositions := []providerauth.ChatGPTProviderImportCandidateDisposition{
		{CandidateID: "candidate-new", State: providerauth.ChatGPTProviderImportCandidateStateReady},
		{
			CandidateID:                 "candidate-existing",
			State:                       providerauth.ChatGPTProviderImportCandidateStateExisting,
			ExpectedProviderID:          "bound-provider",
			ExpectedCredentialVersion:   7,
			ExpectedCredentialCreatedAt: existingCredentialCreatedAt,
		},
		{CandidateID: "candidate-duplicate", State: providerauth.ChatGPTProviderImportCandidateStateDuplicate},
		{CandidateID: "candidate-invalid", State: providerauth.ChatGPTProviderImportCandidateStateInvalid},
		{CandidateID: "candidate-unsupported", State: providerauth.ChatGPTProviderImportCandidateStateUnsupported},
	}
	if !reflect.DeepEqual(service.sealCalls, []string{"opaque-import-id"}) ||
		!reflect.DeepEqual(service.sealDispositions, [][]providerauth.ChatGPTProviderImportCandidateDisposition{wantDispositions}) {
		t.Fatalf("sealed dispositions = (%v, %+v), want immutable preview decisions", service.sealCalls, service.sealDispositions)
	}
	var response ProviderImportPreviewResponse
	decodeProviderImportResponse(t, responseRecorder, &response)
	if response.ImportID != "opaque-import-id" || !response.ExpiresAt.Equal(draftExpiresAt) {
		t.Fatalf("preview identity = (%q, %v), want (%q, %v)", response.ImportID, response.ExpiresAt, "opaque-import-id", draftExpiresAt)
	}
	wantSummary := providerauth.ChatGPTProviderImportSummary{
		Total: 5, Ready: 1, Existing: 1, Duplicate: 1, Invalid: 1, Unsupported: 1,
	}
	if response.Summary != wantSummary {
		t.Fatalf("summary = %+v, want %+v", response.Summary, wantSummary)
	}
	if !reflect.DeepEqual(response.Warnings, service.preview.Warnings) {
		t.Fatalf("warnings = %+v, want %+v", response.Warnings, service.preview.Warnings)
	}
	if len(response.Items) != 5 {
		t.Fatalf("items length = %d, want 5", len(response.Items))
	}

	created := response.Items[0]
	if created.Status != providerauth.ChatGPTProviderImportCandidateStateReady ||
		created.ProviderID != "fresh-account-2" || created.Name != "Fresh Account" ||
		created.Email != "fresh@example.test" || created.AccountID != "account-new" ||
		created.PlanType != "plus" || created.ExpiresAt == nil || !created.ExpiresAt.Equal(credentialExpiresAt) ||
		created.Priority != 3 || created.Concurrency != 8 || !created.DefaultSelected ||
		created.ExistingProviderID != "" || created.ExistingProviderName != "" {
		t.Fatalf("ready item = %+v, want flattened source metadata and a collision-free selected provider", created)
	}

	existing := response.Items[1]
	if existing.Status != providerauth.ChatGPTProviderImportCandidateStateExisting ||
		existing.ProviderID != "bound-provider" || existing.Name != "Current Provider Name" ||
		existing.ExistingProviderID != "bound-provider" || existing.ExistingProviderName != "Current Provider Name" ||
		existing.Priority != 41 || existing.Concurrency != 17 || existing.DefaultSelected {
		t.Fatalf("existing item = %+v, want current binding identity/settings and default_selected=false", existing)
	}
	if existing.AccountID != existingAccountID || existing.Email != "existing@example.test" || existing.PlanType != "team" {
		t.Fatalf("existing auth metadata = (%q, %q, %q), want source-safe identity", existing.AccountID, existing.Email, existing.PlanType)
	}
	if !strings.Contains(existing.Message, "Current Provider Name") {
		t.Fatalf("existing message = %q, want current provider name", existing.Message)
	}

	for index := 2; index < len(response.Items); index++ {
		if response.Items[index].DefaultSelected {
			t.Fatalf("blocked item %d default_selected = true, want false", index)
		}
		if response.Items[index].ProviderID == "" || response.Items[index].Message == "" || response.Items[index].Warnings == nil {
			t.Fatalf("blocked item %d = %+v, want stable display metadata", index, response.Items[index])
		}
	}

	body := responseRecorder.Body.String()
	for _, forbidden := range []string{
		providerImportRawCredentialMarker,
		providerImportStoredCredentialMarker,
		providerImportExistingCredentialMarker,
		`"auth"`,
		`"suggested_provider"`,
		`"existing_provider"`,
		`"usage"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("preview response contains forbidden credential/nested field marker %q: %s", forbidden, body)
		}
	}
}

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

func TestPreviewProviderImportCancelsDraftWhenEnrichmentFails(t *testing.T) {
	service := &fakeProviderImportService{preview: &providerauth.ChatGPTProviderImportPreview{ImportID: "draft-to-clean-up"}}
	adminStore := newMockStore()
	adminStore.listErr = errors.New("database unavailable")
	handler := newProviderImportTestHandler(adminStore, service, &fakeProviderImportStore{})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{"accounts":[]}`))
	responseRecorder := httptest.NewRecorder()

	handler.PreviewProviderImport(responseRecorder, req)

	requireProviderImportStatus(t, responseRecorder, http.StatusInternalServerError)
	assertProviderImportError(t, responseRecorder, ErrCodeInternal, "existing providers")
	if !reflect.DeepEqual(service.cancelCalls, []string{"draft-to-clean-up"}) {
		t.Fatalf("cancel calls = %v, want draft cleanup", service.cancelCalls)
	}
}

func TestPreviewProviderImportCancelsDraftWhenSealingFails(t *testing.T) {
	service := &fakeProviderImportService{
		preview: &providerauth.ChatGPTProviderImportPreview{ImportID: "unsealed-draft"},
		sealErr: errors.New("seal failed"),
	}
	handler := newProviderImportTestHandler(newMockStore(), service, &fakeProviderImportStore{})
	request := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{"accounts":[]}`))
	response := httptest.NewRecorder()

	handler.PreviewProviderImport(response, request)

	requireProviderImportStatus(t, response, http.StatusInternalServerError)
	assertProviderImportError(t, response, ErrCodeInternal, "finalize")
	if !reflect.DeepEqual(service.sealCalls, []string{"unsealed-draft"}) ||
		!reflect.DeepEqual(service.cancelCalls, []string{"unsealed-draft"}) {
		t.Fatalf("seal/cancel calls = (%v, %v), want one failed seal followed by cleanup", service.sealCalls, service.cancelCalls)
	}
}

func TestCommitProviderImportBuildsOneAtomicCreateAndCredentialUpdateBundle(t *testing.T) {
	usageResetAt := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	usageFetchedAt := usageResetAt.Add(-time.Minute)
	createdCandidate := providerImportReadyCandidate(
		"candidate-create",
		"account-create",
		"Source Create",
		providerImportCreatedCredentialMarker,
	)
	createdCandidate.AuthState.UsageSnapshot = &model.ProviderUsageSnapshot{
		FetchedAt: &usageFetchedAt,
		PlanType:  "plus",
		FiveHour: &model.ProviderUsageWindow{
			UsedPercent: 37.5, WindowSeconds: 5 * 60 * 60, ResetAt: &usageResetAt,
		},
	}
	updatedCandidate := providerImportReadyCandidate(
		"candidate-update",
		"account-update",
		"Source Update",
		providerImportUpdatedCredentialMarker,
	)
	existingCredentialCreatedAt := time.Date(2026, time.July, 1, 10, 0, 0, 0, time.UTC)
	updatedCandidate.Disposition = &providerauth.ChatGPTProviderImportCandidateDisposition{
		CandidateID:                 "candidate-update",
		State:                       providerauth.ChatGPTProviderImportCandidateStateExisting,
		ExpectedProviderID:          "bound-provider",
		ExpectedCredentialVersion:   7,
		ExpectedCredentialCreatedAt: existingCredentialCreatedAt,
	}
	skippedCandidate := providerauth.ChatGPTProviderImportCandidate{
		CandidateID: "candidate-skip",
		SourceIndex: 2,
		State:       providerauth.ChatGPTProviderImportCandidateStateDuplicate,
		Name:        "Skipped Duplicate",
	}
	events := []string{}
	service := &fakeProviderImportService{
		candidates: []providerauth.ChatGPTProviderImportCandidate{createdCandidate, updatedCandidate, skippedCandidate},
		events:     &events,
	}
	importStore := &fakeProviderImportStore{events: &events}
	adminStore := newMockStore()
	accountID := "account-update"
	groupID := "original-group"
	adminStore.providers["bound-provider"] = &model.Provider{
		ID:             "bound-provider",
		Name:           "Existing Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		GroupID:        &groupID,
		Priority:       91,
		Concurrency:    23,
		Weight:         19,
		Enabled:        false,
		Credential: &model.ProviderCredential{
			ProviderID:       "bound-provider",
			SecretData:       providerImportExistingCredentialMarker,
			BindingAccountID: &accountID,
			Version:          7,
			CreatedAt:        existingCredentialCreatedAt,
		},
	}
	handler := newProviderImportTestHandler(adminStore, service, importStore)
	requestBody := `{
		"group_id":" team-a ",
		"items":[
			{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":" New Provider ","priority":4,"concurrency":12},
			{"candidate_id":"candidate-update","action":"update","provider_id":"bound-provider","name":"Must Be Ignored","priority":1,"concurrency":1}
		]
	}`
	responseRecorder := commitProviderImportRequest(t, handler, "opaque-import-id", requestBody)

	requireProviderImportStatus(t, responseRecorder, http.StatusOK)
	if !reflect.DeepEqual(events, []string{"claim", "verify", "apply", "invalidate", "lease_release", "finalize"}) {
		t.Fatalf("workflow events = %v, want claim/verify/apply/cache invalidation/finalize", events)
	}
	if !reflect.DeepEqual(service.verifyCalls, [][]string{{"candidate-create", "candidate-update"}}) {
		t.Fatalf("verified candidates = %v, want selected candidates only in request order", service.verifyCalls)
	}
	if !reflect.DeepEqual(service.invalidateCalls, [][]string{{"new-provider", "bound-provider"}}) {
		t.Fatalf("invalidated provider sessions = %v, want every committed credential target", service.invalidateCalls)
	}
	if !reflect.DeepEqual(service.finalizeCalls, []string{"opaque-import-id"}) {
		t.Fatalf("finalize calls = %v, want successful draft consumption", service.finalizeCalls)
	}
	if len(importStore.bundles) != 1 {
		t.Fatalf("apply calls = %d, want one atomic bundle", len(importStore.bundles))
	}
	if !reflect.DeepEqual(importStore.leaseProviderIDs, [][]string{{"new-provider", "bound-provider"}}) ||
		len(importStore.applyContexts) != 1 ||
		importStore.applyContexts[0].Value(providerImportMutationContextKey{}) != true ||
		importStore.releaseCalls != 1 {
		t.Fatalf(
			"mutation lease = (ids %v, apply contexts %d, releases %d), want complete create/update ownership through apply",
			importStore.leaseProviderIDs,
			len(importStore.applyContexts),
			importStore.releaseCalls,
		)
	}
	bundle := importStore.bundles[0]
	if len(bundle.Creates) != 1 || len(bundle.CredentialUpdates) != 1 {
		t.Fatalf("bundle sizes = (%d creates, %d updates), want (1, 1)", len(bundle.Creates), len(bundle.CredentialUpdates))
	}

	create := bundle.Creates[0]
	if create.CandidateID != "candidate-create" || create.Provider.ID != "new-provider" || create.Provider.Name != "New Provider" ||
		create.Provider.GroupID == nil || *create.Provider.GroupID != "team-a" ||
		create.Provider.Priority != 4 || create.Provider.Concurrency != 12 || create.Provider.Weight != DefaultWeight ||
		!create.Provider.Enabled || create.Provider.CredentialType != model.ProviderCredentialTypeChatGPT ||
		create.Provider.AuthMode != "bearer" || len(create.Provider.APITypes) != 1 || create.Provider.APITypes[0].APIType != "codex" {
		t.Fatalf("create bundle item = %+v, want normalized ChatGPT provider with reviewed routing settings", create)
	}
	if create.Provider.Credential == nil || create.Provider.Credential.ProviderID != "new-provider" ||
		create.Provider.Credential.SecretData != providerImportCreatedCredentialMarker ||
		create.Provider.AuthState == nil || create.Provider.AuthState.ProviderID != "new-provider" ||
		create.Provider.AuthState.UsageSnapshot == nil || create.Provider.AuthState.UsageSnapshot.FiveHour == nil ||
		create.Provider.AuthState.UsageSnapshot.FiveHour.UsedPercent != 37.5 {
		t.Fatalf("create auth records = (%+v, %+v), want staged credential and usage snapshot rebound to provider", create.Provider.Credential, create.Provider.AuthState)
	}

	update := bundle.CredentialUpdates[0]
	if update.CandidateID != "candidate-update" || update.ProviderID != "bound-provider" || update.ExpectedCredentialVersion != 7 ||
		!update.ExpectedCredentialCreatedAt.Equal(existingCredentialCreatedAt) ||
		update.Credential.ProviderID != "bound-provider" || update.Credential.SecretData != providerImportUpdatedCredentialMarker ||
		update.AuthState.ProviderID != "bound-provider" || update.AuthState.AccountID != accountID {
		t.Fatalf("credential update = %+v, want versioned update of the current binding", update)
	}
	existing := adminStore.providers["bound-provider"]
	if existing.Name != "Existing Provider" || existing.GroupID == nil || *existing.GroupID != "original-group" ||
		existing.Priority != 91 || existing.Concurrency != 23 || existing.Weight != 19 || existing.Enabled {
		t.Fatalf("existing provider routing config changed during bundle construction: %+v", existing)
	}

	var response ProviderImportCommitResponse
	decodeProviderImportResponse(t, responseRecorder, &response)
	if response.ImportID != "opaque-import-id" || response.Summary != (ProviderImportCommitSummary{Created: 1, Updated: 1, Skipped: 1}) {
		t.Fatalf("commit response summary = (%q, %+v), want one created/updated/skipped", response.ImportID, response.Summary)
	}
	wantOutcomes := []ProviderImportCommitOutcome{providerImportOutcomeCreated, providerImportOutcomeUpdated}
	if len(response.Items) != len(wantOutcomes) {
		t.Fatalf("commit result item count = %d, want %d", len(response.Items), len(wantOutcomes))
	}
	for index, outcome := range wantOutcomes {
		if response.Items[index].Outcome != outcome {
			t.Fatalf("item %d outcome = %q, want %q", index, response.Items[index].Outcome, outcome)
		}
	}
	responseBody := responseRecorder.Body.String()
	for _, marker := range []string{
		providerImportCreatedCredentialMarker,
		providerImportUpdatedCredentialMarker,
		providerImportExistingCredentialMarker,
	} {
		if strings.Contains(responseBody, marker) {
			t.Fatalf("commit response leaked credential marker %q: %s", marker, responseBody)
		}
	}
}

func TestCommitProviderImportRetainsDraftUntilAtomicStoreSucceeds(t *testing.T) {
	candidate := providerImportReadyCandidate(
		"candidate-create",
		"account-create",
		"Retryable Candidate",
		providerImportStoredCredentialMarker,
	)
	events := []string{}
	service := &fakeProviderImportService{
		candidates: []providerauth.ChatGPTProviderImportCandidate{candidate},
		events:     &events,
	}
	conflict := &store.ProviderImportConflictError{Conflicts: []store.ProviderImportConflict{{
		CandidateID:               "candidate-create",
		Kind:                      store.ProviderImportConflictCredentialVersionMismatch,
		ProviderID:                "new-provider",
		ExpectedCredentialVersion: 4,
		CurrentCredentialVersion:  5,
	}}}
	importStore := &fakeProviderImportStore{err: fmt.Errorf("transaction preflight: %w", conflict), events: &events}
	handler := newProviderImportTestHandler(newMockStore(), service, importStore)
	requestBody := `{"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"New Provider","priority":0,"concurrency":0}]}`

	firstResponse := commitProviderImportRequest(t, handler, "retryable-import", requestBody)

	requireProviderImportStatus(t, firstResponse, http.StatusConflict)
	if len(service.finalizeCalls) != 0 {
		t.Fatalf("finalize calls after conflict = %v, want draft retained", service.finalizeCalls)
	}
	if !reflect.DeepEqual(events, []string{"claim", "verify", "apply", "lease_release", "release"}) {
		t.Fatalf("failure workflow events = %v, want failed commit claim released without finalize", events)
	}
	var conflictResponse struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Conflicts []store.ProviderImportConflict `json:"conflicts"`
		} `json:"details"`
	}
	decodeProviderImportResponse(t, firstResponse, &conflictResponse)
	if conflictResponse.Code != ErrCodeConflict || len(conflictResponse.Details.Conflicts) != 1 ||
		conflictResponse.Details.Conflicts[0].Kind != store.ProviderImportConflictCredentialVersionMismatch ||
		conflictResponse.Details.Conflicts[0].CurrentCredentialVersion != 5 {
		t.Fatalf("conflict response = %+v, want typed, actionable transaction conflict", conflictResponse)
	}
	if strings.Contains(firstResponse.Body.String(), providerImportStoredCredentialMarker) {
		t.Fatalf("conflict response leaked credential material: %s", firstResponse.Body.String())
	}

	// A successful retry against the same import ID demonstrates that a failed
	// transaction did not consume the server-side credential draft.
	importStore.err = nil
	secondResponse := commitProviderImportRequest(t, handler, "retryable-import", requestBody)
	requireProviderImportStatus(t, secondResponse, http.StatusOK)
	if !reflect.DeepEqual(service.finalizeCalls, []string{"retryable-import"}) {
		t.Fatalf("finalize calls after retry = %v, want exactly one successful consumption", service.finalizeCalls)
	}
	if !reflect.DeepEqual(events, []string{"claim", "verify", "apply", "lease_release", "release", "claim", "verify", "apply", "invalidate", "lease_release", "finalize"}) {
		t.Fatalf("retry workflow events = %v, want finalize only after successful apply", events)
	}
}

func TestCommitProviderImportReplaysLostResponseAfterHandlerRestart(t *testing.T) {
	candidate := providerImportReadyCandidate(
		"candidate-create",
		"account-create",
		"Replay Candidate",
		providerImportStoredCredentialMarker,
	)
	events := []string{}
	service := &fakeProviderImportService{
		candidates: []providerauth.ChatGPTProviderImportCandidate{candidate},
		events:     &events,
	}
	importStore := &fakeProviderImportStore{events: &events}
	handler := newProviderImportTestHandler(newMockStore(), service, importStore)
	firstRequest := `{
		"group_id":" replay-group ",
		"items":[{"candidate_id":" candidate-create ","action":"create","provider_id":" new-provider ","name":" Replay Provider ","priority":4,"concurrency":9}]
	}`
	// The retry is semantically identical but deliberately changes JSON field
	// order and insignificant whitespace to exercise the normalized fingerprint.
	retryRequest := `{
		"items":[{"concurrency":9,"priority":4,"name":"Replay Provider","provider_id":"new-provider","action":"create","candidate_id":"candidate-create"}],
		"group_id":"replay-group"
	}`

	firstResponse := commitProviderImportRequest(t, handler, "replay-import", firstRequest)
	service.claimErr = providerauth.ErrChatGPTProviderImportNotFound
	restartedHandler := newProviderImportTestHandler(newMockStore(), service, importStore)
	retryResponse := commitProviderImportRequest(t, restartedHandler, "replay-import", retryRequest)

	requireProviderImportStatus(t, firstResponse, http.StatusOK)
	requireProviderImportStatus(t, retryResponse, http.StatusOK)
	if firstResponse.Body.String() != retryResponse.Body.String() {
		t.Fatalf("replayed response = %s, want cached original %s", retryResponse.Body.String(), firstResponse.Body.String())
	}
	if service.claimCalls != 1 || len(importStore.bundles) != 1 || len(service.finalizeCalls) != 1 {
		t.Fatalf(
			"lost-response retry calls = (%d claim, %d apply, %d finalize), want exactly (1, 1, 1)",
			service.claimCalls,
			len(importStore.bundles),
			len(service.finalizeCalls),
		)
	}
	if !reflect.DeepEqual(events, []string{"claim", "verify", "apply", "invalidate", "lease_release", "finalize"}) {
		t.Fatalf("replay workflow events = %v, want no repeated side effects", events)
	}
}

func TestCommitProviderImportRejectsDifferentRequestAfterSuccess(t *testing.T) {
	candidate := providerImportReadyCandidate(
		"candidate-create",
		"account-create",
		"Replay Candidate",
		providerImportStoredCredentialMarker,
	)
	service := &fakeProviderImportService{candidates: []providerauth.ChatGPTProviderImportCandidate{candidate}}
	importStore := &fakeProviderImportStore{}
	handler := newProviderImportTestHandler(newMockStore(), service, importStore)
	firstRequest := `{
		"group_id":"group-a",
		"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"Original Name","priority":4,"concurrency":9}]
	}`
	changedRequest := `{
		"group_id":"group-a",
		"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"Changed Name","priority":4,"concurrency":9}]
	}`

	firstResponse := commitProviderImportRequest(t, handler, "replay-conflict", firstRequest)
	changedResponse := commitProviderImportRequest(t, handler, "replay-conflict", changedRequest)

	requireProviderImportStatus(t, firstResponse, http.StatusOK)
	requireProviderImportStatus(t, changedResponse, http.StatusConflict)
	assertProviderImportError(t, changedResponse, ErrCodeConflict, "")
	if service.claimCalls != 1 || len(importStore.bundles) != 1 || len(service.finalizeCalls) != 1 {
		t.Fatalf(
			"mismatched retry calls = (%d claim, %d apply, %d finalize), want original side effects only",
			service.claimCalls,
			len(importStore.bundles),
			len(service.finalizeCalls),
		)
	}
}

func TestCommitProviderImportDoesNotFinalizeAfterInternalStoreFailure(t *testing.T) {
	service := &fakeProviderImportService{candidates: []providerauth.ChatGPTProviderImportCandidate{
		providerImportReadyCandidate("candidate-create", "account-create", "New", providerImportStoredCredentialMarker),
	}}
	importStore := &fakeProviderImportStore{err: errors.New("write failed")}
	handler := newProviderImportTestHandler(newMockStore(), service, importStore)
	responseRecorder := commitProviderImportRequest(t, handler, "retained-import", `{
		"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"New","priority":0,"concurrency":0}]
	}`)

	requireProviderImportStatus(t, responseRecorder, http.StatusInternalServerError)
	assertProviderImportError(t, responseRecorder, ErrCodeInternal, "commit")
	if len(service.finalizeCalls) != 0 {
		t.Fatalf("finalize calls = %v, want none after failed store", service.finalizeCalls)
	}
	if importStore.releaseCalls != 1 {
		t.Fatalf("mutation lease releases = %d, want one after failed store", importStore.releaseCalls)
	}
}

func TestCommitProviderImportStopsWhenCredentialMutationLeaseFails(t *testing.T) {
	candidate := providerImportReadyCandidate(
		"candidate-create",
		"account-create",
		"New",
		providerImportStoredCredentialMarker,
	)
	service := &fakeProviderImportService{candidates: []providerauth.ChatGPTProviderImportCandidate{candidate}}
	importStore := &fakeProviderImportStore{leaseErr: context.DeadlineExceeded}
	handler := newProviderImportTestHandler(newMockStore(), service, importStore)
	response := commitProviderImportRequest(t, handler, "lease-timeout", `{
		"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"New"}]
	}`)

	requireProviderImportStatus(t, response, http.StatusRequestTimeout)
	if len(importStore.bundles) != 0 || importStore.releaseCalls != 0 || len(service.finalizeCalls) != 0 {
		t.Fatalf(
			"calls after lease failure = (%d apply, %d release, %d finalize), want no side effects",
			len(importStore.bundles),
			importStore.releaseCalls,
			len(service.finalizeCalls),
		)
	}
}

func TestCommitProviderImportRequiresUpdateTargetToMatchCurrentBinding(t *testing.T) {
	accountID := "bound-account"
	candidate := providerImportReadyCandidate("candidate-update", accountID, "Existing", providerImportUpdatedCredentialMarker)
	candidate.Disposition = &providerauth.ChatGPTProviderImportCandidateDisposition{
		CandidateID:                 "candidate-update",
		State:                       providerauth.ChatGPTProviderImportCandidateStateExisting,
		ExpectedProviderID:          "actual-provider",
		ExpectedCredentialVersion:   3,
		ExpectedCredentialCreatedAt: time.Date(2026, time.July, 1, 10, 0, 0, 0, time.UTC),
	}
	service := &fakeProviderImportService{candidates: []providerauth.ChatGPTProviderImportCandidate{candidate}}
	adminStore := newMockStore()
	adminStore.providers["actual-provider"] = &model.Provider{
		ID:   "actual-provider",
		Name: "Actual Provider",
		Credential: &model.ProviderCredential{
			ProviderID:       "actual-provider",
			BindingAccountID: &accountID,
			Version:          3,
		},
	}
	importStore := &fakeProviderImportStore{}
	handler := newProviderImportTestHandler(adminStore, service, importStore)
	responseRecorder := commitProviderImportRequest(t, handler, "binding-check", `{
		"items":[{"candidate_id":"candidate-update","action":"update","provider_id":"different-provider","name":"Ignored","priority":0,"concurrency":0}]
	}`)

	requireProviderImportStatus(t, responseRecorder, http.StatusConflict)
	if len(importStore.bundles) != 0 || len(service.finalizeCalls) != 0 {
		t.Fatalf("calls after target mismatch = (%d apply, %d finalize), want none", len(importStore.bundles), len(service.finalizeCalls))
	}
	var response struct {
		Details struct {
			Conflicts []store.ProviderImportConflict `json:"conflicts"`
		} `json:"details"`
	}
	decodeProviderImportResponse(t, responseRecorder, &response)
	if len(response.Details.Conflicts) != 1 ||
		response.Details.Conflicts[0].Kind != store.ProviderImportConflictAccountBindingMismatch ||
		response.Details.Conflicts[0].ProviderID != "different-provider" ||
		response.Details.Conflicts[0].ConflictingProviderID != "actual-provider" {
		t.Fatalf("binding conflict = %+v, want explicit requested/current providers", response.Details.Conflicts)
	}
}

func TestCommitProviderImportAllowsExactLegacyProviderIDForCredentialUpdate(t *testing.T) {
	const legacyProviderID = "Legacy_Provider.V1"
	accountID := "legacy-bound-account"
	candidate := providerImportReadyCandidate("candidate-update", accountID, "Legacy", providerImportUpdatedCredentialMarker)
	candidate.Disposition = &providerauth.ChatGPTProviderImportCandidateDisposition{
		CandidateID:                 "candidate-update",
		State:                       providerauth.ChatGPTProviderImportCandidateStateExisting,
		ExpectedProviderID:          legacyProviderID,
		ExpectedCredentialVersion:   12,
		ExpectedCredentialCreatedAt: time.Date(2026, time.July, 1, 10, 0, 0, 0, time.UTC),
	}
	service := &fakeProviderImportService{candidates: []providerauth.ChatGPTProviderImportCandidate{candidate}}
	adminStore := newMockStore()
	adminStore.providers[legacyProviderID] = &model.Provider{
		ID:   legacyProviderID,
		Name: "Legacy Provider",
		Credential: &model.ProviderCredential{
			ProviderID:       legacyProviderID,
			BindingAccountID: &accountID,
			Version:          12,
		},
	}
	importStore := &fakeProviderImportStore{}
	handler := newProviderImportTestHandler(adminStore, service, importStore)
	responseRecorder := commitProviderImportRequest(t, handler, "legacy-update", `{
		"items":[{"candidate_id":"candidate-update","action":"update","provider_id":" Legacy_Provider.V1 ","name":"Ignored","priority":0,"concurrency":0}]
	}`)

	requireProviderImportStatus(t, responseRecorder, http.StatusOK)
	if len(importStore.bundles) != 1 || len(importStore.bundles[0].CredentialUpdates) != 1 {
		t.Fatalf("apply bundles = %+v, want one credential update", importStore.bundles)
	}
	update := importStore.bundles[0].CredentialUpdates[0]
	if update.ProviderID != legacyProviderID || update.ExpectedCredentialVersion != 12 {
		t.Fatalf("legacy update target = (%q, %d), want (%q, 12)", update.ProviderID, update.ExpectedCredentialVersion, legacyProviderID)
	}
	if !reflect.DeepEqual(service.finalizeCalls, []string{"legacy-update"}) {
		t.Fatalf("finalize calls = %v, want successful legacy update consumption", service.finalizeCalls)
	}
}

func TestCommitProviderImportValidatesRequestBeforePersistence(t *testing.T) {
	validItem := `{"candidate_id":"candidate-ready","action":"create","provider_id":"new-provider","name":"New","priority":0,"concurrency":0}`
	tests := []struct {
		name       string
		importID   string
		body       string
		candidates []providerauth.ChatGPTProviderImportCandidate
		wantCode   string
	}{
		{name: "missing import ID", body: `{"items":[` + validItem + `]}`, wantCode: ErrCodeValidation},
		{name: "malformed JSON", importID: "draft", body: `{`, wantCode: ErrCodeValidation},
		{name: "unknown field", importID: "draft", body: `{"items":[` + validItem + `],"surprise":true}`, wantCode: ErrCodeValidation},
		{name: "trailing document", importID: "draft", body: `{"items":[` + validItem + `]} {}`, wantCode: ErrCodeValidation},
		{name: "no selected items", importID: "draft", body: `{"items":[]}`, wantCode: ErrCodeValidation},
		{name: "blank candidate ID", importID: "draft", body: `{"items":[{"candidate_id":" ","action":"create","provider_id":"new-provider","name":"New"}]}`, wantCode: ErrCodeValidation},
		{name: "duplicate candidate", importID: "draft", body: `{"items":[` + validItem + `,` + validItem + `]}`, wantCode: ErrCodeValidation},
		{name: "invalid action", importID: "draft", body: `{"items":[{"candidate_id":"candidate-ready","action":"replace","provider_id":"new-provider","name":"New"}]}`, wantCode: ErrCodeValidation},
		{name: "invalid create provider ID", importID: "draft", body: `{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"Not_Valid","name":"New"}]}`, wantCode: ErrCodeValidation},
		{name: "missing create name", importID: "draft", body: `{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"new-provider","name":" "}]}`, wantCode: ErrCodeValidation},
		{name: "candidate ID too long", importID: "draft", body: fmt.Sprintf(`{"items":[{"candidate_id":"%s","action":"create","provider_id":"new-provider","name":"New"}]}`, strings.Repeat("c", maxProviderImportCandidateIDCharacters+1)), wantCode: ErrCodeValidation},
		{name: "provider ID too long", importID: "draft", body: fmt.Sprintf(`{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"%s","name":"New"}]}`, strings.Repeat("p", maxProviderImportIdentifierCharacters+1)), wantCode: ErrCodeValidation},
		{name: "provider name too long", importID: "draft", body: fmt.Sprintf(`{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"new-provider","name":"%s"}]}`, strings.Repeat("n", maxProviderImportNameCharacters+1)), wantCode: ErrCodeValidation},
		{name: "negative priority", importID: "draft", body: `{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"new-provider","name":"New","priority":-1}]}`, wantCode: ErrCodeValidation},
		{name: "priority too large", importID: "draft", body: fmt.Sprintf(`{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"new-provider","name":"New","priority":%d}]}`, maxProviderImportRoutingValue+1), wantCode: ErrCodeValidation},
		{name: "negative concurrency", importID: "draft", body: `{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"new-provider","name":"New","concurrency":-1}]}`, wantCode: ErrCodeValidation},
		{name: "concurrency too large", importID: "draft", body: fmt.Sprintf(`{"items":[{"candidate_id":"candidate-ready","action":"create","provider_id":"new-provider","name":"New","concurrency":%d}]}`, maxProviderImportRoutingValue+1), wantCode: ErrCodeValidation},
		{name: "update requires provider ID", importID: "draft", body: `{"items":[{"candidate_id":"candidate-ready","action":"update","provider_id":""}]}`, wantCode: ErrCodeValidation},
		{
			name:       "unknown candidate",
			importID:   "draft",
			body:       `{"items":[` + validItem + `]}`,
			candidates: []providerauth.ChatGPTProviderImportCandidate{providerImportReadyCandidate("another-candidate", "account", "Other", providerImportStoredCredentialMarker)},
			wantCode:   ErrCodeValidation,
		},
		{
			name:     "blocked candidate",
			importID: "draft",
			body:     `{"items":[` + validItem + `]}`,
			candidates: []providerauth.ChatGPTProviderImportCandidate{{
				CandidateID: "candidate-ready", State: providerauth.ChatGPTProviderImportCandidateStateInvalid,
			}},
			wantCode: ErrCodeValidation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeProviderImportService{candidates: test.candidates}
			importStore := &fakeProviderImportStore{}
			handler := newProviderImportTestHandler(newMockStore(), service, importStore)
			responseRecorder := commitProviderImportRequest(t, handler, test.importID, test.body)

			requireProviderImportStatus(t, responseRecorder, http.StatusBadRequest)
			assertProviderImportError(t, responseRecorder, test.wantCode, "")
			if len(importStore.bundles) != 0 || len(service.finalizeCalls) != 0 {
				t.Fatalf("invalid request calls = (%d apply, %d finalize), want none", len(importStore.bundles), len(service.finalizeCalls))
			}
		})
	}
}

func TestCommitProviderImportMapsDraftErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "expired",
			err:        fmt.Errorf("%w: expired-draft", providerauth.ErrChatGPTProviderImportExpired),
			wantStatus: http.StatusGone,
			wantCode:   ErrCodeConflict,
		},
		{
			name:       "not found",
			err:        fmt.Errorf("%w: missing-draft", providerauth.ErrChatGPTProviderImportNotFound),
			wantStatus: http.StatusGone,
			wantCode:   ErrCodeConflict,
		},
		{
			name:       "candidate not found",
			err:        fmt.Errorf("%w: candidate", providerauth.ErrChatGPTProviderImportCandidateNotFound),
			wantStatus: http.StatusBadRequest,
			wantCode:   ErrCodeValidation,
		},
		{
			name:       "already claimed",
			err:        providerauth.ErrChatGPTProviderImportInProgress,
			wantStatus: http.StatusConflict,
			wantCode:   ErrCodeConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeProviderImportService{claimErr: test.err}
			importStore := &fakeProviderImportStore{}
			handler := newProviderImportTestHandler(newMockStore(), service, importStore)
			responseRecorder := commitProviderImportRequest(t, handler, "draft", `{
				"items":[{"candidate_id":"candidate","action":"create","provider_id":"provider","name":"Provider"}]
			}`)

			requireProviderImportStatus(t, responseRecorder, test.wantStatus)
			assertProviderImportError(t, responseRecorder, test.wantCode, "")
			if len(importStore.bundles) != 0 || len(service.finalizeCalls) != 0 {
				t.Fatalf("draft error calls = (%d apply, %d finalize), want none", len(importStore.bundles), len(service.finalizeCalls))
			}
		})
	}
}

func TestCancelProviderImportMapsDraftLifecycleStates(t *testing.T) {
	tests := []struct {
		name       string
		importID   string
		cancelErr  error
		wantStatus int
		wantCode   string
		wantKind   string
	}{
		{name: "active draft", importID: "active", wantStatus: http.StatusNoContent},
		{name: "already missing", importID: "missing", cancelErr: providerauth.ErrChatGPTProviderImportNotFound, wantStatus: http.StatusNoContent},
		{name: "already expired", importID: "expired", cancelErr: fmt.Errorf("%w: expired", providerauth.ErrChatGPTProviderImportExpired), wantStatus: http.StatusNoContent},
		{name: "commit in progress", importID: "claimed", cancelErr: providerauth.ErrChatGPTProviderImportInProgress, wantStatus: http.StatusConflict, wantCode: ErrCodeConflict, wantKind: "provider_import_in_progress"},
		{name: "missing import ID", importID: "", wantStatus: http.StatusBadRequest, wantCode: ErrCodeValidation},
		{name: "service failure", importID: "active", cancelErr: errors.New("cleanup failed"), wantStatus: http.StatusInternalServerError, wantCode: ErrCodeInternal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeProviderImportService{cancelErr: test.cancelErr}
			handler := newProviderImportTestHandler(newMockStore(), service, &fakeProviderImportStore{})
			req := httptest.NewRequest(http.MethodDelete, "/admin/api/provider-imports/"+test.importID, nil)
			req.SetPathValue("import_id", test.importID)
			responseRecorder := httptest.NewRecorder()

			handler.CancelProviderImport(responseRecorder, req)

			requireProviderImportStatus(t, responseRecorder, test.wantStatus)
			if test.wantCode != "" {
				assertProviderImportError(t, responseRecorder, test.wantCode, "")
			}
			if test.wantKind != "" {
				var response model.ErrorResponse
				decodeProviderImportResponse(t, responseRecorder, &response)
				if response.Details["kind"] != test.wantKind {
					t.Fatalf("error details = %#v, want kind %q", response.Details, test.wantKind)
				}
			}
			wantCalls := 1
			if test.importID == "" {
				wantCalls = 0
			}
			if len(service.cancelCalls) != wantCalls {
				t.Fatalf("cancel calls = %v, want count %d", service.cancelCalls, wantCalls)
			}
		})
	}
}

func TestProviderImportHandlersReportUnavailableDependencies(t *testing.T) {
	handler := NewHandler(Config{ProviderCatalog: newMockStore(), Logger: zap.NewNop()})

	previewRequest := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{"accounts":[]}`))
	previewResponse := httptest.NewRecorder()
	handler.PreviewProviderImport(previewResponse, previewRequest)
	requireProviderImportStatus(t, previewResponse, http.StatusNotImplemented)

	commitRequest := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports/draft/commit", strings.NewReader(`{"items":[]}`))
	commitRequest.SetPathValue("import_id", "draft")
	commitResponse := httptest.NewRecorder()
	handler.CommitProviderImport(commitResponse, commitRequest)
	requireProviderImportStatus(t, commitResponse, http.StatusNotImplemented)

	cancelRequest := httptest.NewRequest(http.MethodDelete, "/admin/api/provider-imports/draft", nil)
	cancelRequest.SetPathValue("import_id", "draft")
	cancelResponse := httptest.NewRecorder()
	handler.CancelProviderImport(cancelResponse, cancelRequest)
	requireProviderImportStatus(t, cancelResponse, http.StatusNotImplemented)
}

func providerImportReadyCandidate(candidateID, accountID, name, secretMarker string) providerauth.ChatGPTProviderImportCandidate {
	bindingAccountID := accountID
	return providerauth.ChatGPTProviderImportCandidate{
		CandidateID: candidateID,
		State:       providerauth.ChatGPTProviderImportCandidateStateReady,
		Name:        name,
		Credential: &model.ProviderCredential{
			ProviderID:       "staged-provider",
			SecretData:       secretMarker,
			BindingAccountID: &bindingAccountID,
			Version:          1,
		},
		AuthState: &model.ProviderAuthState{
			ProviderID: "staged-provider",
			Status:     model.ProviderAuthStatusActive,
			AccountID:  accountID,
			Email:      candidateID + "@example.test",
			PlanType:   "plus",
		},
		Disposition: &providerauth.ChatGPTProviderImportCandidateDisposition{
			CandidateID: candidateID,
			State:       providerauth.ChatGPTProviderImportCandidateStateReady,
		},
	}
}

func newProviderImportTestHandler(
	adminStore *mockStore,
	service DraftService,
	importStore Store,
) *Handler {
	handler := NewHandler(Config{
		ProviderCatalog: adminStore,
		Drafts:          service,
		Store:           importStore,
		Logger:          zap.NewNop(),
	})
	handler.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error { return nil }
	return handler
}

func commitProviderImportRequest(t *testing.T, handler *Handler, importID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/api/provider-imports/"+importID+"/commit",
		strings.NewReader(body),
	)
	req.SetPathValue("import_id", importID)
	responseRecorder := httptest.NewRecorder()
	handler.CommitProviderImport(responseRecorder, req)
	return responseRecorder
}

func requireProviderImportStatus(t *testing.T, responseRecorder *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if responseRecorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", responseRecorder.Code, wantStatus, responseRecorder.Body.String())
	}
	if got := responseRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func assertProviderImportError(
	t *testing.T,
	responseRecorder *httptest.ResponseRecorder,
	wantCode string,
	wantMessageFragment string,
) {
	t.Helper()
	var response model.ErrorResponse
	decodeProviderImportResponse(t, responseRecorder, &response)
	if response.Code != wantCode {
		t.Fatalf("error code = %q, want %q; response = %+v", response.Code, wantCode, response)
	}
	if wantMessageFragment != "" && !strings.Contains(response.Message, wantMessageFragment) {
		t.Fatalf("error message = %q, want fragment %q", response.Message, wantMessageFragment)
	}
}

func decodeProviderImportResponse(t *testing.T, responseRecorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", responseRecorder.Body.String(), err)
	}
}
