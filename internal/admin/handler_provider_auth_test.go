package admin

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"go.uber.org/zap"
)

type mockProviderAuthService struct {
	startResponse  *providerauth.ChatGPTLoginStartResponse
	statusResponse *providerauth.ChatGPTLoginStatusResponse
	importResponse *providerauth.ChatGPTLoginStatusResponse
	err            error
	imported       string
	targetSession  string
}

func (m *mockProviderAuthService) StartChatGPTLogin(_ context.Context, sessionID string) (*providerauth.ChatGPTLoginStartResponse, error) {
	m.targetSession = sessionID
	return m.startResponse, m.err
}

func (m *mockProviderAuthService) GetChatGPTLoginStatus(string) (*providerauth.ChatGPTLoginStatusResponse, error) {
	return m.statusResponse, m.err
}

func (m *mockProviderAuthService) ImportChatGPTLogin(_ context.Context, raw, sessionID string) (*providerauth.ChatGPTLoginStatusResponse, error) {
	m.targetSession = sessionID
	m.imported = raw
	return m.importResponse, m.err
}

func TestProviderAuthLoginEndpoints(t *testing.T) {
	auth := &mockProviderAuthService{
		startResponse:  &providerauth.ChatGPTLoginStartResponse{},
		statusResponse: &providerauth.ChatGPTLoginStatusResponse{},
		importResponse: &providerauth.ChatGPTLoginStatusResponse{},
	}
	handler := NewHandler(Config{Store: newMockStore(), Concurrency: &mockConcurrencyTracker{}, Logger: zap.NewNop(), Auth: auth})

	start := httptest.NewRecorder()
	handler.StartChatGPTProviderLogin(start, httptest.NewRequest(http.MethodPost, "/admin/api/provider-auth/chatgpt/start", nil))
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d", start.Code)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/admin/api/provider-auth/chatgpt/sessions/login-1", nil)
	statusRequest.SetPathValue("login_id", "login-1")
	status := httptest.NewRecorder()
	handler.GetChatGPTProviderLoginStatus(status, statusRequest)
	if status.Code != http.StatusOK {
		t.Fatalf("status poll = %d", status.Code)
	}

	importResponse := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/admin/api/provider-auth/chatgpt/import", bytes.NewBufferString(`{"auth_data":"token-json"}`))
	handler.ImportChatGPTProviderCredential(importResponse, importRequest)
	if importResponse.Code != http.StatusOK || auth.imported != "token-json" {
		t.Fatalf("import status = %d imported = %q", importResponse.Code, auth.imported)
	}
}

func TestProviderAuthLoginEndpoints_FailClosedWhenUnavailable(t *testing.T) {
	handler := NewHandler(Config{Store: newMockStore(), Concurrency: &mockConcurrencyTracker{}, Logger: zap.NewNop()})
	w := httptest.NewRecorder()
	handler.StartChatGPTProviderLogin(w, httptest.NewRequest(http.MethodPost, "/admin/api/provider-auth/chatgpt/start", nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestImportChatGPTProviderCredential_RejectsInvalidInput(t *testing.T) {
	auth := &mockProviderAuthService{err: errors.New("invalid auth data")}
	handler := NewHandler(Config{Store: newMockStore(), Concurrency: &mockConcurrencyTracker{}, Logger: zap.NewNop(), Auth: auth})
	for _, body := range []string{`{"auth_data":""}`, `{"auth_data":"bad"}`} {
		w := httptest.NewRecorder()
		handler.ImportChatGPTProviderCredential(w, httptest.NewRequest(http.MethodPost, "/admin/api/provider-auth/chatgpt/import", bytes.NewBufferString(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d", body, w.Code)
		}
	}
}

func TestProviderAuthLoginCarriesCredentialProfileTarget(t *testing.T) {
	auth := &mockProviderAuthService{}
	handler := NewHandler(Config{Store: newMockStore(), Concurrency: &mockConcurrencyTracker{}, Logger: zap.NewNop(), Auth: auth})
	for _, entry := range []struct {
		body    string
		handler http.HandlerFunc
	}{
		{`{"credential_session_id":" session "}`, handler.StartChatGPTProviderLogin},
		{`{"credential_session_id":" session ","auth_data":"token-json"}`, handler.ImportChatGPTProviderCredential},
	} {
		auth.targetSession = ""
		recorder := httptest.NewRecorder()
		entry.handler(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(entry.body)))
		if recorder.Code != http.StatusOK || auth.targetSession != "session" {
			t.Fatalf("status=%d profile target=%q", recorder.Code, auth.targetSession)
		}
	}
	for _, body := range []string{`{bad`, `{"credential_session_id":42}`} {
		auth.targetSession = ""
		recorder := httptest.NewRecorder()
		handler.StartChatGPTProviderLogin(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body)))
		if recorder.Code != http.StatusBadRequest || auth.targetSession != "" {
			t.Fatal(recorder.Code, auth.targetSession)
		}
	}
}
