package providerimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

func TestCommitProviderImportRejectsCredentialRotationAfterPreviewWithoutMutation(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "provider-import.db")
	persistentStore, err := store.NewSQLiteStore(databasePath, internal.RealClock{})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = persistentStore.Close() })

	const (
		providerID = "bound-provider"
		accountID  = "account-existing"
	)
	originalSecret := providerImportEncodedCredential(t, "original")
	provider := providerImportPersistedProvider(providerID, accountID, originalSecret)
	if err := persistentStore.CreateProvider(context.Background(), &provider); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	beforePreview, err := persistentStore.GetProvider(context.Background(), providerID)
	if err != nil {
		t.Fatalf("GetProvider(before preview) error = %v", err)
	}

	importedSecret := providerImportEncodedCredential(t, "imported")
	bindingAccountID := accountID
	candidate := providerauth.ChatGPTProviderImportCandidate{
		CandidateID: "candidate-existing",
		State:       providerauth.ChatGPTProviderImportCandidateStateReady,
		Name:        "Imported Account",
		Credential: &model.ProviderCredential{
			SecretData:       importedSecret,
			BindingAccountID: &bindingAccountID,
		},
		AuthState: &model.ProviderAuthState{
			Status:    model.ProviderAuthStatusActive,
			AccountID: accountID,
		},
	}
	service := &fakeProviderImportService{
		preview: &providerauth.ChatGPTProviderImportPreview{
			ImportID:  "rotate-after-preview",
			ExpiresAt: time.Now().Add(15 * time.Minute),
			Items: []providerauth.ChatGPTProviderImportPreviewItem{{
				CandidateID: "candidate-existing",
				State:       providerauth.ChatGPTProviderImportCandidateStateReady,
				Name:        "Imported Account",
				Auth: &providerauth.ProviderAuthView{
					Type:      model.ProviderCredentialTypeChatGPT,
					Status:    model.ProviderAuthStatusActive,
					AccountID: accountID,
				},
			}},
		},
		candidates:     []providerauth.ChatGPTProviderImportCandidate{candidate},
		sealCandidates: true,
	}
	handler := NewHandler(Config{
		ProviderCatalog: persistentStore,
		Drafts:          service,
		Store:           persistentStore,
		Logger:          zap.NewNop(),
	})
	handler.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error { return nil }
	previewRequest := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{}`))
	previewResponse := httptest.NewRecorder()
	handler.PreviewProviderImport(previewResponse, previewRequest)
	requireProviderImportStatus(t, previewResponse, http.StatusCreated)

	sealed := service.candidates[0].Disposition
	if sealed == nil || sealed.ExpectedCredentialVersion != beforePreview.Credential.Version ||
		!sealed.ExpectedCredentialCreatedAt.Equal(beforePreview.Credential.CreatedAt) {
		t.Fatalf("sealed disposition = %+v, want exact preview credential generation", sealed)
	}

	rotatedSecret := providerImportEncodedCredential(t, "rotated")
	rotatedCredential := beforePreview.Credential.Clone()
	rotatedCredential.SecretData = rotatedSecret
	rotatedAuthState := beforePreview.AuthState.Clone()
	if err := persistentStore.UpdateProviderCredentialState(
		context.Background(),
		providerID,
		rotatedCredential,
		rotatedAuthState,
	); err != nil {
		t.Fatalf("UpdateProviderCredentialState(rotation) error = %v", err)
	}
	rotatedVersion := rotatedCredential.Version

	commitResponse := commitProviderImportRequest(t, handler, "rotate-after-preview", `{
		"items":[{"candidate_id":"candidate-existing","action":"update","provider_id":"bound-provider"}]
	}`)
	requireProviderImportStatus(t, commitResponse, http.StatusConflict)

	afterCommit, err := persistentStore.GetProvider(context.Background(), providerID)
	if err != nil {
		t.Fatalf("GetProvider(after commit) error = %v", err)
	}
	if afterCommit.Credential.Version != rotatedVersion || afterCommit.Credential.SecretData != rotatedSecret {
		t.Fatalf("credential after stale commit = %+v, want rotated version %d unchanged", afterCommit.Credential, rotatedVersion)
	}
	if len(service.finalizeCalls) != 0 {
		t.Fatalf("finalize calls = %v, want stale draft retained for conflict recovery", service.finalizeCalls)
	}
}

func TestCommitProviderImportPersistsProductionSplitCredentialCreateAndUpdate(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "provider-import-split.db")
	persistentStore, err := store.NewSQLiteStore(databasePath, internal.RealClock{})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = persistentStore.Close() })

	const (
		providerID = "split-provider"
		accountID  = "account-split"
	)
	createService := providerImportSplitCredentialService(
		t,
		"split-create",
		"candidate-create",
		accountID,
		"created",
	)
	createHandler := NewHandler(Config{
		ProviderCatalog: persistentStore,
		Drafts:          createService,
		Store:           persistentStore,
		Logger:          zap.NewNop(),
	})
	createHandler.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error { return nil }
	previewProviderImportForTest(t, createHandler)
	createResponse := commitProviderImportRequest(t, createHandler, "split-create", `{
		"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"split-provider","name":"Split Provider","priority":3,"concurrency":8}]
	}`)
	requireProviderImportStatus(t, createResponse, http.StatusOK)
	assertPersistedProviderImportSecret(t, persistentStore, providerID, accountID, "access-created")

	updateService := providerImportSplitCredentialService(
		t,
		"split-update",
		"candidate-update",
		accountID,
		"updated",
	)
	updateHandler := NewHandler(Config{
		ProviderCatalog: persistentStore,
		Drafts:          updateService,
		Store:           persistentStore,
		Logger:          zap.NewNop(),
	})
	updateHandler.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error { return nil }
	previewProviderImportForTest(t, updateHandler)
	updateResponse := commitProviderImportRequest(t, updateHandler, "split-update", `{
		"items":[{"candidate_id":"candidate-update","action":"update","provider_id":"split-provider"}]
	}`)
	requireProviderImportStatus(t, updateResponse, http.StatusOK)
	assertPersistedProviderImportSecret(t, persistentStore, providerID, accountID, "access-updated")
}

func providerImportSplitCredentialService(
	t *testing.T,
	importID string,
	candidateID string,
	accountID string,
	tokenGeneration string,
) *fakeProviderImportService {
	t.Helper()
	bindingAccountID := accountID
	return &fakeProviderImportService{
		preview: &providerauth.ChatGPTProviderImportPreview{
			ImportID:  importID,
			ExpiresAt: time.Now().Add(15 * time.Minute),
			Items: []providerauth.ChatGPTProviderImportPreviewItem{{
				CandidateID: candidateID,
				State:       providerauth.ChatGPTProviderImportCandidateStateReady,
				Name:        "Split Account",
				Concurrency: 8,
				Priority:    3,
				Auth: &providerauth.ProviderAuthView{
					Type:      model.ProviderCredentialTypeChatGPT,
					Status:    model.ProviderAuthStatusActive,
					AccountID: accountID,
				},
			}},
		},
		candidates: []providerauth.ChatGPTProviderImportCandidate{{
			CandidateID: candidateID,
			State:       providerauth.ChatGPTProviderImportCandidateStateReady,
			Name:        "Split Account",
			Concurrency: 8,
			Priority:    3,
			Credential: &model.ProviderCredential{
				SecretData:       providerImportEncodedCredential(t, tokenGeneration),
				BindingAccountID: &bindingAccountID,
			},
			AuthState: &model.ProviderAuthState{
				Status:    model.ProviderAuthStatusActive,
				AccountID: accountID,
			},
		}},
		sealCandidates: true,
	}
}

func previewProviderImportForTest(t *testing.T, handler *Handler) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.PreviewProviderImport(response, request)
	requireProviderImportStatus(t, response, http.StatusCreated)
}

func assertPersistedProviderImportSecret(
	t *testing.T,
	persistentStore *store.SQLiteStore,
	providerID string,
	accountID string,
	wantAccessToken string,
) {
	t.Helper()
	provider, err := persistentStore.GetProvider(context.Background(), providerID)
	if err != nil {
		t.Fatalf("GetProvider(%q) error = %v", providerID, err)
	}
	if provider.Credential == nil || provider.Credential.BindingAccountID == nil ||
		*provider.Credential.BindingAccountID != accountID {
		t.Fatalf("persisted credential binding = %#v, want %q", provider.Credential, accountID)
	}
	secret, err := model.DecodeChatGPTProviderSecret(provider.Credential.SecretData)
	if err != nil {
		t.Fatalf("DecodeChatGPTProviderSecret() error = %v", err)
	}
	if secret == nil || !secret.Ready() || secret.AccessToken != wantAccessToken {
		t.Fatalf("persisted split secret is not the committed credential")
	}
	if provider.AuthState == nil || provider.AuthState.AccountID != accountID {
		t.Fatalf("persisted auth state = %#v, want account %q", provider.AuthState, accountID)
	}
}

func providerImportEncodedCredential(t *testing.T, tokenGeneration string) string {
	t.Helper()
	encoded, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:  "access-" + tokenGeneration,
		RefreshToken: "refresh-" + tokenGeneration,
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderSecret() error = %v", err)
	}
	return encoded
}

func providerImportPersistedProvider(providerID, accountID, secret string) model.Provider {
	bindingAccountID := accountID
	return model.Provider{
		ID:             providerID,
		Name:           "Existing Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Weight:         1,
		Concurrency:    4,
		Enabled:        true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: providerID,
			APIType:    "codex",
			BaseURL:    "https://chatgpt.com/backend-api/codex",
		}},
		Credential: &model.ProviderCredential{
			ProviderID:       providerID,
			SecretData:       secret,
			BindingAccountID: &bindingAccountID,
		},
		AuthState: &model.ProviderAuthState{
			ProviderID: providerID,
			Status:     model.ProviderAuthStatusActive,
			AccountID:  accountID,
		},
	}
}
