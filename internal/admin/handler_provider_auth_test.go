package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"switch-a/internal/model"
	"switch-a/internal/providerauth"
	storepkg "switch-a/internal/store"

	"go.uber.org/zap"
)

type mockProviderAuthService struct {
	startResp        *providerauth.ChatGPTLoginStartResponse
	startErr         error
	statusResp       *providerauth.ChatGPTLoginStatusResponse
	statusErr        error
	statusLoginID    string
	appliedProvider  *model.Provider
	appliedLoginID   string
	appliedPayload   string
	applyCalls       int
	applyErr         error
	finalizedLoginID string
	finalizeCalls    int
	finalizeErr      error
}

func (m *mockProviderAuthService) StartChatGPTLogin() (*providerauth.ChatGPTLoginStartResponse, error) {
	return m.startResp, m.startErr
}

func (m *mockProviderAuthService) GetChatGPTLoginStatus(loginID string) (*providerauth.ChatGPTLoginStatusResponse, error) {
	m.statusLoginID = loginID
	return m.statusResp, m.statusErr
}

func (m *mockProviderAuthService) ApplyChatGPTLogin(provider *model.Provider, loginID string) error {
	m.appliedProvider = provider
	m.appliedLoginID = loginID
	m.applyCalls++
	if m.applyErr != nil {
		return m.applyErr
	}
	if provider != nil && m.appliedPayload != "" {
		provider.CredentialData = m.appliedPayload
	}
	return nil
}

func (m *mockProviderAuthService) FinalizeChatGPTLogin(loginID string) error {
	m.finalizedLoginID = loginID
	m.finalizeCalls++
	return m.finalizeErr
}

func (m *mockProviderAuthService) PopulateProviderAuthProfile(_ context.Context, provider *model.Provider) {
	if provider == nil {
		return
	}
	if provider.AuthProfile == nil {
		provider.AuthProfile = &model.ProviderAuthProfile{
			Type: provider.CredentialType,
		}
	}
}

type loginCommitTrackingStore struct {
	*mockStore
	auth                   *mockProviderAuthService
	commitSeenDuringCreate bool
	commitSeenDuringUpdate bool
}

func (s *loginCommitTrackingStore) CreateProvider(ctx context.Context, provider *model.Provider) error {
	if s.auth.finalizeCalls != 0 {
		s.commitSeenDuringCreate = true
	}
	return s.mockStore.CreateProvider(ctx, provider)
}

func (s *loginCommitTrackingStore) UpdateProvider(ctx context.Context, provider *model.Provider) error {
	if s.auth.finalizeCalls != 0 {
		s.commitSeenDuringUpdate = true
	}
	return s.mockStore.UpdateProvider(ctx, provider)
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
			AuthProfile: &model.ProviderAuthProfile{
				Type:      model.ProviderCredentialTypeChatGPT,
				Ready:     true,
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
	if response.AuthProfile == nil || response.AuthProfile.Email != "user@example.com" {
		t.Fatal("response should include the completed auth profile")
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
