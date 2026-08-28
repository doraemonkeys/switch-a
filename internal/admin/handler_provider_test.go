package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestCreateProvider_RequiresCredentialSessionForEveryAPIType(t *testing.T) {
	handler, _, _ := testHandler()
	w := performProviderRequest(t, handler.CreateProvider, http.MethodPost, "/admin/api/providers", CreateProviderRequest{
		ID: "provider-1", Name: "Provider", APITypes: []APITypeInput{{APIType: "claude", BaseURL: "https://api.example.com"}},
	})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "credential_session_id") {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func TestCreateProvider_PersistsOnlySessionReferences(t *testing.T) {
	handler, store, _ := testHandler()
	w := performProviderRequest(t, handler.CreateProvider, http.MethodPost, "/admin/api/providers", CreateProviderRequest{
		ID: "provider-1", Name: "Provider", AuthMode: "bearer", Vendor: "openai",
		APITypes: []APITypeInput{{APIType: "claude", BaseURL: "https://api.example.com", CredentialSessionID: "session-1"}},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	provider := store.providers["provider-1"]
	if provider == nil {
		t.Fatal("provider was not persisted")
	}
	snapshot, ok := provider.CredentialSessionForAPIType("claude")
	if !ok || snapshot.SessionID != "session-1" {
		t.Fatalf("credential session = %#v", snapshot)
	}
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "api_key") {
		t.Fatalf("provider response leaked credential transport fields: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "credential_type") {
		t.Fatalf("provider response exposed the removed provider-level credential discriminator: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"credential_session_id":"session-1"`) ||
		!strings.Contains(w.Body.String(), `"credential_sessions"`) {
		t.Fatalf("provider response omitted the route-to-session contract: %s", w.Body.String())
	}
}

func TestUpdateProvider_ReplacesRouteSessionReference(t *testing.T) {
	handler, store, _ := testHandler()
	store.providers["provider-1"] = &model.Provider{
		ID: "provider-1", Name: "Provider", AuthMode: "bearer", Enabled: true,
		APITypes:           []model.ProviderAPIType{{ProviderID: "provider-1", APIType: "claude", BaseURL: "https://old.example.com"}},
		CredentialSessions: []credentialsession.RouteSnapshot{testConfigCredentialRoute("provider-1", "claude", "session-old", "old-secret")},
	}
	request := UpdateProviderRequest{APITypes: []APITypeInput{{
		APIType: "claude", BaseURL: "https://new.example.com", CredentialSessionID: "session-new",
	}}}
	w := performProviderRequest(t, handler.UpdateProvider, http.MethodPut, "/admin/api/providers/provider-1", request)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	updated := store.providers["provider-1"]
	snapshot, ok := updated.CredentialSessionForAPIType("claude")
	if !ok || snapshot.SessionID != "session-new" || updated.APITypes[0].BaseURL != "https://new.example.com" {
		t.Fatalf("updated provider = %#v", updated)
	}
}

func TestDeleteProvider_DoesNotDeleteCredentialSession(t *testing.T) {
	handler, store, _ := testHandler()
	session := testConfigCredentialSession(t, "session-1", "openai", "secret")
	store.credentialSessions[session.ID] = &session
	store.providers["provider-1"] = &model.Provider{
		ID: "provider-1", Name: "Provider",
		APITypes:           []model.ProviderAPIType{{ProviderID: "provider-1", APIType: "claude", BaseURL: "https://api.example.com"}},
		CredentialSessions: []credentialsession.RouteSnapshot{testConfigCredentialRoute("provider-1", "claude", session.ID, session.SecretData)},
	}
	request := httptest.NewRequest(http.MethodDelete, "/admin/api/providers/provider-1", nil)
	request.SetPathValue("id", "provider-1")
	w := httptest.NewRecorder()
	handler.DeleteProvider(w, request)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
	if store.credentialSessions[session.ID] == nil {
		t.Fatal("deleting route target deleted independently owned credential session")
	}
}

func TestCreateProvider_ValidatesIdentifiersAndURLs(t *testing.T) {
	handler, _, _ := testHandler()
	testCases := []struct {
		name    string
		request CreateProviderRequest
	}{
		{name: "missing id", request: CreateProviderRequest{Name: "Provider"}},
		{name: "missing name", request: CreateProviderRequest{ID: "provider-1"}},
		{name: "invalid URL", request: CreateProviderRequest{ID: "provider-1", Name: "Provider", APITypes: []APITypeInput{{APIType: "claude", BaseURL: "not-a-url", CredentialSessionID: "session-1"}}}},
		{name: "duplicate API type", request: CreateProviderRequest{ID: "provider-1", Name: "Provider", APITypes: []APITypeInput{
			{APIType: "claude", BaseURL: "https://one.example.com", CredentialSessionID: "session-1"},
			{APIType: "claude", BaseURL: "https://two.example.com", CredentialSessionID: "session-2"},
		}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			w := performProviderRequest(t, handler.CreateProvider, http.MethodPost, "/admin/api/providers", testCase.request)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func performProviderRequest(t *testing.T, handler http.HandlerFunc, method, target string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if id := strings.TrimPrefix(target, "/admin/api/providers/"); id != target {
		request.SetPathValue("id", id)
	}
	w := httptest.NewRecorder()
	handler(w, request)
	return w
}
