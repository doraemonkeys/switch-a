package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/providerauth"
	storepkg "switch-a/internal/store"

	"go.uber.org/zap"
)

type mockProviderAuthService struct {
	startResp                    *providerauth.ChatGPTLoginStartResponse
	startErr                     error
	statusResp                   *providerauth.ChatGPTLoginStatusResponse
	statusErr                    error
	statusLoginID                string
	importResp                   *providerauth.ChatGPTLoginStatusResponse
	importErr                    error
	importAuthData               string
	importCalls                  int
	appliedProvider              *model.Provider
	appliedLoginID               string
	appliedPayload               string
	applyCalls                   int
	applyErr                     error
	finalizedLoginID             string
	finalizeCalls                int
	finalizeErr                  error
	buildAuthViewResp            *providerauth.ProviderAuthView
	buildAuthViewCalls           int
	refreshCredentialCalls       int
	refreshCredentialErr         error
	refreshCredentialUnsupported bool
	refreshCredentialPayload     string
	refreshUsageCalls            int
	refreshUsageErr              error
	refreshUsageUnsupported      bool
	refreshUsagePayload          string
}

func (m *mockProviderAuthService) StartChatGPTLogin() (*providerauth.ChatGPTLoginStartResponse, error) {
	return m.startResp, m.startErr
}

func (m *mockProviderAuthService) GetChatGPTLoginStatus(loginID string) (*providerauth.ChatGPTLoginStatusResponse, error) {
	m.statusLoginID = loginID
	return m.statusResp, m.statusErr
}

func (m *mockProviderAuthService) ImportChatGPTLogin(_ context.Context, rawAuthData string) (*providerauth.ChatGPTLoginStatusResponse, error) {
	m.importCalls++
	m.importAuthData = rawAuthData
	return m.importResp, m.importErr
}

func (m *mockProviderAuthService) ApplyChatGPTLogin(provider *model.Provider, loginID string) error {
	m.appliedProvider = provider
	m.appliedLoginID = loginID
	m.applyCalls++
	if m.applyErr != nil {
		return m.applyErr
	}
	if provider != nil && m.appliedPayload != "" {
		provider.Credential = model.ProviderCredentialFromLegacy(
			provider.ID,
			model.ProviderCredentialTypeChatGPT,
			m.appliedPayload,
		)
	}
	return nil
}

func (m *mockProviderAuthService) FinalizeChatGPTLogin(loginID string) error {
	m.finalizedLoginID = loginID
	m.finalizeCalls++
	return m.finalizeErr
}

func (m *mockProviderAuthService) BuildProviderAuthView(provider *model.Provider) *providerauth.ProviderAuthView {
	m.buildAuthViewCalls++
	if m.buildAuthViewResp != nil {
		return m.buildAuthViewResp
	}
	return providerauth.BuildProviderAuthView(provider)
}

func (m *mockProviderAuthService) RefreshProviderCredentials(_ context.Context, provider *model.Provider) (bool, error) {
	m.refreshCredentialCalls++
	if m.refreshCredentialUnsupported {
		return false, nil
	}
	if provider != nil && m.refreshCredentialPayload != "" {
		provider.Credential = model.ProviderCredentialFromLegacy(
			provider.ID,
			model.ProviderCredentialTypeChatGPT,
			m.refreshCredentialPayload,
		)
	}
	return true, m.refreshCredentialErr
}

func (m *mockProviderAuthService) RefreshProviderUsage(_ context.Context, provider *model.Provider) (bool, error) {
	m.refreshUsageCalls++
	if m.refreshUsageUnsupported {
		return false, nil
	}
	if provider != nil && m.refreshUsagePayload != "" {
		provider.Credential = model.ProviderCredentialFromLegacy(
			provider.ID,
			model.ProviderCredentialTypeChatGPT,
			m.refreshUsagePayload,
		)
	}
	return true, m.refreshUsageErr
}

type loginCommitTrackingStore struct {
	*mockStore
	auth                   *mockProviderAuthService
	commitSeenDuringCreate bool
	commitSeenDuringUpdate bool
}

func (s *loginCommitTrackingStore) CreateProvider(ctx context.Context, provider *model.Provider, options ...storepkg.ProviderWriteOptions) error {
	if s.auth.finalizeCalls != 0 {
		s.commitSeenDuringCreate = true
	}
	return s.mockStore.CreateProvider(ctx, provider, options...)
}

func (s *loginCommitTrackingStore) UpdateProvider(ctx context.Context, provider *model.Provider, options ...storepkg.ProviderWriteOptions) error {
	if s.auth.finalizeCalls != 0 {
		s.commitSeenDuringUpdate = true
	}
	return s.mockStore.UpdateProvider(ctx, provider, options...)
}

func mustMarshalChatGPTCredentialData(t *testing.T) string {
	t.Helper()
	payload, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("marshal credential payload: %v", err)
	}
	return string(payload)
}

func TestStartChatGPTProviderLogin(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		startResp: &providerauth.ChatGPTLoginStartResponse{
			LoginID: "login-123",
			AuthURL: "https://chatgpt.com/oauth/authorize?state=test",
		},
	}
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-auth/chatgpt/start", nil)
	w := httptest.NewRecorder()

	handler.StartChatGPTProviderLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var response providerauth.ChatGPTLoginStartResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.LoginID != "login-123" {
		t.Fatalf("login id = %q, want %q", response.LoginID, "login-123")
	}
	if response.AuthURL == "" {
		t.Fatal("auth url should not be empty")
	}
}

func TestStartChatGPTProviderLogin_Error(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	handler := NewHandler(Config{
		Store: newMockStore(),
		Auth: &mockProviderAuthService{
			startErr: errors.New("boom"),
		},
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-auth/chatgpt/start", nil)
	w := httptest.NewRecorder()

	handler.StartChatGPTProviderLogin(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestStartChatGPTProviderLogin_WithoutAuthService(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-auth/chatgpt/start", nil)
	w := httptest.NewRecorder()

	handler.StartChatGPTProviderLogin(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestGetChatGPTProviderLoginStatus(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		statusResp: &providerauth.ChatGPTLoginStatusResponse{
			LoginID: "login-123",
			Status:  providerauth.ChatGPTLoginStatusCompleted,
			Auth: &providerauth.ProviderAuthView{
				Type:      model.ProviderCredentialTypeChatGPT,
				Status:    providerauth.ProviderAuthStatusActive,
				Email:     "user@example.com",
				AccountID: "acct_test",
			},
		},
	}
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/provider-auth/chatgpt/sessions/login-123", nil)
	setPathValue(req, "login_id", "login-123")
	w := httptest.NewRecorder()

	handler.GetChatGPTProviderLoginStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusOK)
	}
	if auth.statusLoginID != "login-123" {
		t.Fatalf("status login id = %q, want %q", auth.statusLoginID, "login-123")
	}

	var response providerauth.ChatGPTLoginStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != providerauth.ChatGPTLoginStatusCompleted {
		t.Fatalf("response status = %q, want %q", response.Status, providerauth.ChatGPTLoginStatusCompleted)
	}
	if response.Auth == nil || response.Auth.Email != "user@example.com" {
		t.Fatal("response should include the completed auth view")
	}
}

func TestGetChatGPTProviderLoginStatus_MissingLoginID(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Auth:   &mockProviderAuthService{},
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/provider-auth/chatgpt/sessions/", nil)
	w := httptest.NewRecorder()

	handler.GetChatGPTProviderLoginStatus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetChatGPTProviderLoginStatus_WithoutAuthService(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/provider-auth/chatgpt/sessions/login-123", nil)
	setPathValue(req, "login_id", "login-123")
	w := httptest.NewRecorder()

	handler.GetChatGPTProviderLoginStatus(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestImportChatGPTProviderCredential(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		importResp: &providerauth.ChatGPTLoginStatusResponse{
			LoginID: "login-import",
			Status:  providerauth.ChatGPTLoginStatusCompleted,
			Auth: &providerauth.ProviderAuthView{
				Type:      model.ProviderCredentialTypeChatGPT,
				Status:    providerauth.ProviderAuthStatusActive,
				Email:     "import@example.com",
				AccountID: "acct_import",
			},
		},
	}
	handler := NewHandler(Config{Store: newMockStore(), Auth: auth, Logger: logger})

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/api/provider-auth/chatgpt/import",
		strings.NewReader(`{"auth_data":"{\"access_token\":\"acc\"}"}`),
	)
	w := httptest.NewRecorder()

	handler.ImportChatGPTProviderCredential(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusOK)
	}
	if auth.importCalls != 1 {
		t.Fatalf("ImportChatGPTLogin calls = %d, want 1", auth.importCalls)
	}
	if auth.importAuthData != `{"access_token":"acc"}` {
		t.Fatalf("import auth data = %q, want raw pasted blob", auth.importAuthData)
	}

	var response providerauth.ChatGPTLoginStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != providerauth.ChatGPTLoginStatusCompleted {
		t.Fatalf("response status = %q, want %q", response.Status, providerauth.ChatGPTLoginStatusCompleted)
	}
	if response.Auth == nil || response.Auth.AccountID != "acct_import" {
		t.Fatal("response should include the imported auth view")
	}
}

func TestImportChatGPTProviderCredential_EmptyAuthData(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{}
	handler := NewHandler(Config{Store: newMockStore(), Auth: auth, Logger: logger})

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/api/provider-auth/chatgpt/import",
		strings.NewReader(`{"auth_data":"   "}`),
	)
	w := httptest.NewRecorder()

	handler.ImportChatGPTProviderCredential(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if auth.importCalls != 0 {
		t.Fatalf("ImportChatGPTLogin calls = %d, want 0 for empty auth_data", auth.importCalls)
	}
}

func TestImportChatGPTProviderCredential_InvalidBody(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Auth:   &mockProviderAuthService{},
		Logger: logger,
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/api/provider-auth/chatgpt/import",
		strings.NewReader("not json"),
	)
	w := httptest.NewRecorder()

	handler.ImportChatGPTProviderCredential(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestImportChatGPTProviderCredential_ImportError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	handler := NewHandler(Config{
		Store: newMockStore(),
		Auth: &mockProviderAuthService{
			importErr: errors.New("auth data is missing a refresh token"),
		},
		Logger: logger,
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/api/provider-auth/chatgpt/import",
		strings.NewReader(`{"auth_data":"{\"access_token\":\"acc\"}"}`),
	)
	w := httptest.NewRecorder()

	handler.ImportChatGPTProviderCredential(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestImportChatGPTProviderCredential_WithoutAuthService(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	handler := NewHandler(Config{Store: newMockStore(), Logger: logger})

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/api/provider-auth/chatgpt/import",
		strings.NewReader(`{"auth_data":"{}"}`),
	)
	w := httptest.NewRecorder()

	handler.ImportChatGPTProviderCredential(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestRefreshProviderCredential(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	payload, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
	})
	if err != nil {
		t.Fatalf("marshal credential payload: %v", err)
	}
	auth := &mockProviderAuthService{
		refreshCredentialPayload: string(payload),
	}
	store := newMockStore()
	store.providers["gpt-provider"] = &model.Provider{
		ID:             "gpt-provider",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	handler := NewHandler(Config{
		Store:  store,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/gpt-provider/refresh-credential", nil)
	setPathValue(req, "id", "gpt-provider")
	w := httptest.NewRecorder()

	handler.RefreshProviderCredential(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if auth.refreshCredentialCalls != 1 {
		t.Fatalf("RefreshProviderCredentials calls = %d, want 1", auth.refreshCredentialCalls)
	}
	if auth.refreshUsageCalls != 0 {
		t.Fatalf("RefreshProviderUsage calls = %d, want 0", auth.refreshUsageCalls)
	}

	var response ProviderPayload
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Auth == nil || response.Auth.Status != providerauth.ProviderAuthStatusActive {
		t.Fatalf("Auth = %#v, want active auth view", response.Auth)
	}
}

func TestRefreshProviderUsage(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "team",
		Usage: &model.ProviderUsageSnapshot{
			FetchedAt: &now,
			PlanType:  "team",
		},
	})
	if err != nil {
		t.Fatalf("marshal credential payload: %v", err)
	}
	auth := &mockProviderAuthService{
		refreshUsagePayload: string(payload),
	}
	store := newMockStore()
	store.providers["gpt-provider"] = &model.Provider{
		ID:             "gpt-provider",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	handler := NewHandler(Config{
		Store:  store,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/gpt-provider/refresh-usage", nil)
	setPathValue(req, "id", "gpt-provider")
	w := httptest.NewRecorder()

	handler.RefreshProviderUsage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if auth.refreshUsageCalls != 1 {
		t.Fatalf("RefreshProviderUsage calls = %d, want 1", auth.refreshUsageCalls)
	}
	if auth.refreshCredentialCalls != 0 {
		t.Fatalf("RefreshProviderCredentials calls = %d, want 0", auth.refreshCredentialCalls)
	}

	var response ProviderPayload
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Auth == nil || response.Auth.PlanType != "team" {
		t.Fatalf("Auth = %#v, want refreshed usage auth view", response.Auth)
	}
}

func TestRefreshProviderCredential_AuthStateConflict(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		refreshCredentialErr: &providerauth.ProviderAuthStateError{
			ProviderID: "gpt-provider",
			Status:     providerauth.ProviderAuthStatusReauthRequired,
			Reason:     providerauth.ProviderAuthReasonInvalidGrant,
		},
	}
	store := newMockStore()
	store.providers["gpt-provider"] = &model.Provider{
		ID:             "gpt-provider",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}
	handler := NewHandler(Config{
		Store:  store,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/gpt-provider/refresh-credential", nil)
	setPathValue(req, "id", "gpt-provider")
	w := httptest.NewRecorder()

	handler.RefreshProviderCredential(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status code = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestCreateProvider_ChatGPTLoginCommitsAfterPersistence(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		appliedPayload: mustMarshalChatGPTCredentialData(t),
	}
	store := &loginCommitTrackingStore{
		mockStore: newMockStore(),
		auth:      auth,
	}
	handler := NewHandler(Config{
		Store:  store,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", strings.NewReader(`{
		"id": "gpt-provider",
		"name": "GPT Provider",
		"credential_type": "chatgpt",
		"credential_login_id": "login-123"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	if auth.applyCalls != 1 {
		t.Fatalf("ApplyChatGPTLogin calls = %d, want 1", auth.applyCalls)
	}
	if auth.appliedLoginID != "login-123" {
		t.Fatalf("applied login id = %q, want %q", auth.appliedLoginID, "login-123")
	}
	if auth.finalizeCalls != 1 {
		t.Fatalf("FinalizeChatGPTLogin calls = %d, want 1", auth.finalizeCalls)
	}
	if auth.finalizedLoginID != "login-123" {
		t.Fatalf("finalized login id = %q, want %q", auth.finalizedLoginID, "login-123")
	}
	if store.commitSeenDuringCreate {
		t.Fatal("chatgpt login should not be committed before provider persistence succeeds")
	}
}

func TestCreateProvider_ChatGPTLoginRemainsReusableWhenPersistenceFails(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		appliedPayload: mustMarshalChatGPTCredentialData(t),
	}
	store := &loginCommitTrackingStore{
		mockStore: newMockStore(),
		auth:      auth,
	}
	store.createErr = errors.New("db unavailable")
	handler := NewHandler(Config{
		Store:  store,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", strings.NewReader(`{
		"id": "gpt-provider",
		"name": "GPT Provider",
		"credential_type": "chatgpt",
		"credential_login_id": "login-123"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if auth.applyCalls != 1 {
		t.Fatalf("ApplyChatGPTLogin calls = %d, want 1", auth.applyCalls)
	}
	if auth.finalizeCalls != 0 {
		t.Fatalf("FinalizeChatGPTLogin calls = %d, want 0", auth.finalizeCalls)
	}
}

func TestCreateProvider_ChatGPTLoginConflictReturnsConflictAndDoesNotFinalize(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		appliedPayload: mustMarshalChatGPTCredentialData(t),
	}
	trackingStore := &loginCommitTrackingStore{
		mockStore: newMockStore(),
		auth:      auth,
	}
	trackingStore.createErr = &storepkg.CredentialBindingConflictError{
		AccountID:  "acct_test",
		ProviderID: "existing-provider",
	}
	handler := NewHandler(Config{
		Store:  trackingStore,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", strings.NewReader(`{
		"id": "gpt-provider",
		"name": "GPT Provider",
		"credential_type": "chatgpt",
		"credential_login_id": "login-123"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateProvider(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status code = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `existing-provider`) {
		t.Fatalf("response body = %q, want conflicting provider id", w.Body.String())
	}
	var conflictResponse struct {
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conflictResponse); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	if conflictResponse.Details["kind"] != "credential_binding" ||
		conflictResponse.Details["account_id"] != "acct_test" ||
		conflictResponse.Details["provider_id"] != "existing-provider" {
		t.Fatalf("conflict details = %#v, want credential binding metadata", conflictResponse.Details)
	}
	if auth.applyCalls != 1 {
		t.Fatalf("ApplyChatGPTLogin calls = %d, want 1", auth.applyCalls)
	}
	if auth.finalizeCalls != 0 {
		t.Fatalf("FinalizeChatGPTLogin calls = %d, want 0", auth.finalizeCalls)
	}
}

func TestUpdateProvider_ChatGPTLoginCommitsAfterPersistence(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		appliedPayload: mustMarshalChatGPTCredentialData(t),
	}
	store := &loginCommitTrackingStore{
		mockStore: newMockStore(),
		auth:      auth,
	}
	store.providers["gpt-provider"] = &model.Provider{
		ID:             "gpt-provider",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		APIKey:         "legacy-key",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "gpt-provider",
			APIType:    "responses",
			BaseURL:    "https://api.example.com",
		}},
	}
	handler := NewHandler(Config{
		Store:  store,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/gpt-provider", strings.NewReader(`{
		"name": "GPT Provider",
		"credential_type": "chatgpt",
		"credential_login_id": "login-456"
	}`))
	req.Header.Set("Content-Type", "application/json")
	setPathValue(req, "id", "gpt-provider")
	w := httptest.NewRecorder()

	handler.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if auth.applyCalls != 1 {
		t.Fatalf("ApplyChatGPTLogin calls = %d, want 1", auth.applyCalls)
	}
	if auth.finalizeCalls != 1 {
		t.Fatalf("FinalizeChatGPTLogin calls = %d, want 1", auth.finalizeCalls)
	}
	if auth.finalizedLoginID != "login-456" {
		t.Fatalf("finalized login id = %q, want %q", auth.finalizedLoginID, "login-456")
	}
	if store.commitSeenDuringUpdate {
		t.Fatal("chatgpt login should not be committed before provider persistence succeeds")
	}
}

func TestUpdateProvider_ChatGPTLoginRemainsReusableWhenPersistenceFails(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		appliedPayload: mustMarshalChatGPTCredentialData(t),
	}
	store := &loginCommitTrackingStore{
		mockStore: newMockStore(),
		auth:      auth,
	}
	store.updateErr = errors.New("db unavailable")
	store.providers["gpt-provider"] = &model.Provider{
		ID:             "gpt-provider",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		APIKey:         "legacy-key",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "gpt-provider",
			APIType:    "responses",
			BaseURL:    "https://api.example.com",
		}},
	}
	handler := NewHandler(Config{
		Store:  store,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/gpt-provider", strings.NewReader(`{
		"name": "GPT Provider",
		"credential_type": "chatgpt",
		"credential_login_id": "login-456"
	}`))
	req.Header.Set("Content-Type", "application/json")
	setPathValue(req, "id", "gpt-provider")
	w := httptest.NewRecorder()

	handler.UpdateProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if auth.applyCalls != 1 {
		t.Fatalf("ApplyChatGPTLogin calls = %d, want 1", auth.applyCalls)
	}
	if auth.finalizeCalls != 0 {
		t.Fatalf("FinalizeChatGPTLogin calls = %d, want 0", auth.finalizeCalls)
	}
}

func TestUpdateProvider_ChatGPTLoginConflictReturnsConflictAndDoesNotFinalize(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	auth := &mockProviderAuthService{
		appliedPayload: mustMarshalChatGPTCredentialData(t),
	}
	trackingStore := &loginCommitTrackingStore{
		mockStore: newMockStore(),
		auth:      auth,
	}
	trackingStore.updateErr = &storepkg.CredentialBindingConflictError{
		AccountID:  "acct_test",
		ProviderID: "existing-provider",
	}
	trackingStore.providers["gpt-provider"] = &model.Provider{
		ID:             "gpt-provider",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		APIKey:         "legacy-key",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "gpt-provider",
			APIType:    "responses",
			BaseURL:    "https://api.example.com",
		}},
	}
	handler := NewHandler(Config{
		Store:  trackingStore,
		Auth:   auth,
		Logger: logger,
	})

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/gpt-provider", strings.NewReader(`{
		"name": "GPT Provider",
		"credential_type": "chatgpt",
		"credential_login_id": "login-456"
	}`))
	req.Header.Set("Content-Type", "application/json")
	setPathValue(req, "id", "gpt-provider")
	w := httptest.NewRecorder()

	handler.UpdateProvider(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status code = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `existing-provider`) {
		t.Fatalf("response body = %q, want conflicting provider id", w.Body.String())
	}
	if auth.applyCalls != 1 {
		t.Fatalf("ApplyChatGPTLogin calls = %d, want 1", auth.applyCalls)
	}
	if auth.finalizeCalls != 0 {
		t.Fatalf("FinalizeChatGPTLogin calls = %d, want 0", auth.finalizeCalls)
	}
}
