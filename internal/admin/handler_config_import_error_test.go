package admin

import (
	"bytes"
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

// importApplyErrorStore forces the store-level config import unit-of-work to fail.
type importApplyErrorStore struct {
	mockStore
}

func (s *importApplyErrorStore) ApplyConfigImport(_ context.Context, _ *storepkg.ConfigImportBundle) error {
	return errors.New("database error")
}

func TestImportConfig_InvalidJSON(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewBufferString("invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestImportConfig_WithWarnings(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["g1"] = &model.Group{ID: "g1", Name: "Existing Group"}

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{ID: "", Name: "Empty ID Provider"},
			{ID: "p1", Name: "Invalid API Type", APITypes: []ExportedAPIType{{APIType: "invalid", BaseURL: "https://api.com"}}},
			{ID: "p2", Name: "Invalid Auth Mode", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, AuthMode: "invalid"},
			{ID: "p3", Name: "Non-existent Group", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, GroupID: strPtr("g99")},
		},
		Groups: []ExportedGroup{
			{ID: "", Name: "Empty ID Group"},
			{ID: "g2", Name: "Invalid Strategy", Strategy: "invalid"},
			{ID: "g3", Name: "Reserved Priority", Strategy: "priority", Priority: ReservedGroupPriority},
		},
		Settings: map[string]string{
			"invalid_key": "value",
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Warnings) == 0 {
		t.Error("expected warnings in response")
	}
}

func TestImportConfig_EmptyRequest(t *testing.T) {
	h, _, _ := testHandler()

	importReq := ImportConfigRequest{
		Version:   ConfigExportVersion,
		Providers: []ExportedProvider{},
		Groups:    []ExportedGroup{},
		Settings:  map[string]string{},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp ImportResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true for empty import")
	}
}

func TestImportConfig_ProviderWithGroupReference(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{
				ID:       "p1",
				Name:     "Provider with Group",
				APIKey:   "key",
				APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}},
				GroupID:  strPtr("g1"),
				Weight:   1,
				Enabled:  true,
			},
		},
		Groups: []ExportedGroup{
			{ID: "g1", Name: "New Group", Strategy: "priority", Weight: 1, Enabled: true},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if st.providers["p1"] == nil {
		t.Fatal("provider p1 not created")
	}
	if st.providers["p1"].GroupID == nil || *st.providers["p1"].GroupID != "g1" {
		t.Errorf("provider group_id = %v, want g1", st.providers["p1"].GroupID)
	}
}

func TestImportConfig_ListProvidersError(t *testing.T) {
	h, st, _ := testHandler()
	st.listErr = errors.New("database error")

	importReq := ImportConfigRequest{Version: ConfigExportVersion}
	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestImportConfig_ListGroupsError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := &groupListErrorStore{mockStore: *newMockStore()}

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	importReq := ImportConfigRequest{Version: ConfigExportVersion}
	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestImportConfig_GetConfigError(t *testing.T) {
	h, st, _ := testHandler()
	st.configErr = errors.New("database error")

	importReq := ImportConfigRequest{Version: ConfigExportVersion}
	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestImportConfig_CreateGroupError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := &importApplyErrorStore{mockStore: *newMockStore()}

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Groups: []ExportedGroup{
			{ID: "g1", Name: "New Group", Strategy: "priority", Weight: 1, Enabled: true},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestImportConfig_CreateProviderError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := &importApplyErrorStore{mockStore: *newMockStore()}

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{ID: "p1", Name: "New Provider", APIKey: "key", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, Weight: 1, Enabled: true},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestImportConfig_RejectsInvalidProvidersAtomically(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{ID: "", Name: "Empty ID"},
			{ID: "p1", Name: ""},
			{ID: "p2", Name: "No URL", APIKey: "key"},
			{ID: "p3", Name: "No Key"},
			{ID: "p4", Name: "No API Types", APIKey: "key"},
			{ID: "p5", Name: "Valid", APIKey: "key", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, Weight: 1, Enabled: true},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	if len(st.providers) != 0 {
		t.Fatalf("providers = %#v, want no persisted providers after validation failure", st.providers)
	}
}

func TestImportConfig_RejectsInvalidGroupsAtomically(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Groups: []ExportedGroup{
			{ID: "", Name: "Empty ID"},
			{ID: "g1", Name: ""},
			{ID: "g2", Name: "Invalid Strategy", Strategy: "invalid"},
			{ID: "g3", Name: "Reserved Priority", Strategy: "priority", Priority: ReservedGroupPriority},
			{ID: "g4", Name: "Valid", Strategy: "priority", Weight: 1, Enabled: true},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	if len(st.groups) != 0 {
		t.Fatalf("groups = %#v, want no persisted groups after validation failure", st.groups)
	}
}

func TestImportConfig_DefaultValues(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{
				ID:       "p1",
				Name:     "Provider without defaults",
				APIKey:   "key",
				APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}},
			},
		},
		Groups: []ExportedGroup{
			{
				ID:   "g1",
				Name: "Group without defaults",
			},
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	p1 := st.providers["p1"]
	if p1 == nil {
		t.Fatal("p1 not created")
	}
	if p1.AuthMode != DefaultAuthMode {
		t.Errorf("p1.AuthMode = %q, want %q", p1.AuthMode, DefaultAuthMode)
	}
	if p1.Weight != DefaultWeight {
		t.Errorf("p1.Weight = %d, want %d", p1.Weight, DefaultWeight)
	}

	g1 := st.groups["g1"]
	if g1 == nil {
		t.Fatal("g1 not created")
	}
	if g1.Strategy != DefaultStrategy {
		t.Errorf("g1.Strategy = %q, want %q", g1.Strategy, DefaultStrategy)
	}
	if g1.Weight != DefaultWeight {
		t.Errorf("g1.Weight = %d, want %d", g1.Weight, DefaultWeight)
	}
}

func strPtr(s string) *string {
	return &s
}

func TestImportConfig_ChatGPTProviderDerivesTransportFields(t *testing.T) {
	h, st, _ := testHandler()

	credentialData, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("marshal credentialData: %v", err)
	}

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{{
			ID:             "gpt",
			Name:           "GPT Provider",
			APIKey:         "stale-key",
			AuthMode:       "auto",
			CredentialType: model.ProviderCredentialTypeChatGPT,
			Credential: &ExportedProviderCredential{
				SecretData:       string(credentialData),
				BindingAccountID: strPtr("acct_test"),
				Version:          3,
			},
			AuthState: &ExportedProviderAuthState{
				Status:    model.ProviderAuthStatusActive,
				AccountID: "acct_test",
			},
			Enabled: true,
		}},
	}

	body, err := json.Marshal(importReq)
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	imported := st.providers["gpt"]
	if imported == nil {
		t.Fatal("provider gpt not found after import")
	}
	if imported.CredentialType != model.ProviderCredentialTypeChatGPT {
		t.Fatalf("CredentialType = %q, want %q", imported.CredentialType, model.ProviderCredentialTypeChatGPT)
	}
	if imported.Credential == nil {
		t.Fatal("Credential = nil, want imported credential payload")
	}
	if imported.Credential.SecretData != string(credentialData) {
		t.Fatalf("Credential.SecretData = %q, want original credential payload", imported.Credential.SecretData)
	}
	if imported.Credential.Version != 3 {
		t.Fatalf("Credential.Version = %d, want 3", imported.Credential.Version)
	}
	if imported.AuthState == nil {
		t.Fatal("AuthState = nil, want imported auth state")
	}
	if imported.AuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("AuthState.Status = %q, want %q", imported.AuthState.Status, model.ProviderAuthStatusActive)
	}
	if imported.Credential == nil || imported.Credential.SecretData != string(credentialData) {
		t.Fatalf("Credential = %#v, want secret payload %q", imported.Credential, string(credentialData))
	}
	if imported.AuthMode != "bearer" {
		t.Fatalf("AuthMode = %q, want %q", imported.AuthMode, "bearer")
	}
	if imported.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", imported.APIKey)
	}
	if len(imported.APITypes) != 1 {
		t.Fatalf("len(imported.APITypes) = %d, want 1", len(imported.APITypes))
	}
	if imported.APITypes[0].APIType != "codex" {
		t.Fatalf("APIType = %q, want %q", imported.APITypes[0].APIType, "codex")
	}
	if imported.APITypes[0].BaseURL != providerauth.ChatGPTCodexBaseURL() {
		t.Fatalf("BaseURL = %q, want %q", imported.APITypes[0].BaseURL, providerauth.ChatGPTCodexBaseURL())
	}
	if imported.APITypes[0].APIKey != "" {
		t.Fatalf("APITypes[0].APIKey = %q, want empty", imported.APITypes[0].APIKey)
	}
}

func TestImportConfig_RejectsLegacyVersion(t *testing.T) {
	h, _, _ := testHandler()

	body, err := json.Marshal(ImportConfigRequest{Version: "1.0"})
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ConfigExportVersion) {
		t.Fatalf("response body = %q, want expected version %q", w.Body.String(), ConfigExportVersion)
	}
}
