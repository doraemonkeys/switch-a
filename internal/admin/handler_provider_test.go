package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestCreateProviderRequestDefaultsPreserveExplicitZero(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		body                 string
		concurrency, retries int
		backoff              model.BackoffPolicy
	}{
		{"omitted", "{}", 100, 4, model.BackoffPolicy{InitialDelay: model.Duration(time.Second), Multiplier: 3, Jitter: true}},
		{"explicit zero", `{"concurrency":0,"max_retries":0,"backoff":{}}`, 0, 0, model.BackoffPolicy{}},
		{"custom", `{"concurrency":7,"max_retries":2,"backoff":{"initial_delay":"2s","max_delay":"8s","multiplier":2,"jitter":false}}`, 7, 2, model.BackoffPolicy{InitialDelay: model.Duration(2 * time.Second), MaxDelay: model.Duration(8 * time.Second), Multiplier: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req CreateProviderRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatal(err)
			}
			provider := req.toProvider()
			if provider.Concurrency != tc.concurrency || provider.MaxRetries != tc.retries || provider.Backoff != tc.backoff {
				t.Fatalf("concurrency=%d retries=%d backoff=%+v", provider.Concurrency, provider.MaxRetries, provider.Backoff)
			}
		})
	}
}

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
	snapshot, ok := provider.CredentialSessionForRoute("claude", "http")
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

func TestCreateProvider_MaterializesNamedCredentialAtomically(t *testing.T) {
	handler, repository := newCredentialSessionHandler(t)
	w := performProviderRequest(t, handler.CreateProvider, http.MethodPost, "/admin/api/providers", CreateProviderRequest{
		ID: "provider-atomic", Name: "Atomic Provider", AuthMode: "bearer", Vendor: "openai",
		APITypes: []APITypeInput{{APIType: "claude", BaseURL: "https://api.example.com", CredentialSessionID: "session-atomic"}},
		NewCredentialSessions: []NewProviderCredentialSessionInput{{
			ID: "session-atomic", Name: "Atomic Provider", Kind: credentialsession.KindAPIKey, SecretData: "atomic-secret",
		}},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	session, err := repository.GetCredentialSession(context.Background(), "session-atomic")
	if err != nil || session.Name != "Atomic Provider" || session.SecretData != "atomic-secret" {
		t.Fatalf("credential session = (%+v, %v)", session, err)
	}
	references, err := repository.CredentialSessionRouteReferences(context.Background(), session.ID)
	if err != nil || len(references) != 1 || references[0].ProviderName != "Atomic Provider" || references[0].APIType != "claude" {
		t.Fatalf("route references = (%+v, %v)", references, err)
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
	snapshot, ok := updated.CredentialSessionForRoute("claude", "http")
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
		{name: "URL fragment", request: CreateProviderRequest{ID: "provider-1", Name: "Provider", APITypes: []APITypeInput{{APIType: "claude", BaseURL: "https://api.example.com#ignored", CredentialSessionID: "session-1"}}}},
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
	if id, ok := strings.CutPrefix(target, "/admin/api/providers/"); ok {
		request.SetPathValue("id", id)
	}
	w := httptest.NewRecorder()
	handler(w, request)
	return w
}
