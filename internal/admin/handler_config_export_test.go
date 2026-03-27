package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/providerauth"

	"go.uber.org/zap"
)

// groupListErrorStore is a mock that fails only on ListGroups.
type groupListErrorStore struct {
	mockStore
}

func (s *groupListErrorStore) ListGroups(_ context.Context) ([]model.Group, error) {
	return nil, errors.New("database error")
}

func TestExportConfig(t *testing.T) {
	h, st, _ := testHandler()

	// Setup test data
	st.providers["p1"] = &model.Provider{
		ID:     "p1",
		Name:   "Provider 1",
		APIKey: "key1",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "claude",
			BaseURL:    "https://api.p1.com",
			APIKey:     "claude-key",
		}},
		AuthMode: "bearer",
		Weight:   1,
		Enabled:  true,
	}
	groupID := "g1"
	st.providers["p2"] = &model.Provider{
		ID:       "p2",
		Name:     "Provider 2",
		APIKey:   "key2",
		APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "codex", BaseURL: "https://api.p2.com"}},
		AuthMode: "auto",
		GroupID:  &groupID,
		Weight:   2,
		Enabled:  false,
	}

	st.groups["g1"] = &model.Group{
		ID:       "g1",
		Name:     "Group 1",
		Strategy: "priority",
		Priority: 10,
		Weight:   1,
		Enabled:  true,
	}

	st.config["sticky_mode"] = "model"
	st.config["global_max_attempts"] = "3"

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()

	h.ExportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var export ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if export.Version != ConfigExportVersion {
		t.Errorf("version = %q, want %q", export.Version, ConfigExportVersion)
	}

	if len(export.Providers) != 2 {
		t.Errorf("len(providers) = %d, want 2", len(export.Providers))
	}

	if len(export.Groups) != 1 {
		t.Errorf("len(groups) = %d, want 1", len(export.Groups))
	}

	if export.Settings["sticky_mode"] != "model" {
		t.Errorf("sticky_mode = %q, want %q", export.Settings["sticky_mode"], "model")
	}

	// Verify provider data is correct
	var p1 *ExportedProvider
	for i := range export.Providers {
		if export.Providers[i].ID == "p1" {
			p1 = &export.Providers[i]
			break
		}
	}
	if p1 == nil {
		t.Fatal("provider p1 not found in export")
	}
	if p1.Name != "Provider 1" {
		t.Errorf("p1.Name = %q, want %q", p1.Name, "Provider 1")
	}
	if len(p1.APITypes) != 1 || p1.APITypes[0].APIType != "claude" {
		t.Errorf("p1.APITypes = %v, want [{claude, ...}]", p1.APITypes)
	}
	if p1.APITypes[0].APIKey != "claude-key" {
		t.Errorf("p1.APITypes[0].APIKey = %q, want %q", p1.APITypes[0].APIKey, "claude-key")
	}
}

func TestExportConfig_NormalizesLegacySettings(t *testing.T) {
	h, st, _ := testHandler()

	st.config["sticky_enabled"] = "false"
	st.config["max_retries"] = "7"
	st.config["invalid_key"] = "ignored"

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()

	h.ExportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var export ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got := export.Settings["sticky_mode"]; got != "off" {
		t.Fatalf("sticky_mode = %q, want %q", got, "off")
	}
	if got := export.Settings["global_max_attempts"]; got != "7" {
		t.Fatalf("global_max_attempts = %q, want %q", got, "7")
	}
	if _, exists := export.Settings["sticky_enabled"]; exists {
		t.Fatal("sticky_enabled should not be exported once normalized")
	}
	if _, exists := export.Settings["max_retries"]; exists {
		t.Fatal("max_retries should not be exported once normalized")
	}
	if _, exists := export.Settings["invalid_key"]; exists {
		t.Fatal("invalid_key should not be exported")
	}
}

func TestExportConfig_SkipsInvalidSettingValues(t *testing.T) {
	h, st, _ := testHandler()

	st.config["global_max_attempts"] = "-1"
	st.config["max_retries"] = "-2"

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()

	h.ExportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var export ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, exists := export.Settings["global_max_attempts"]; exists {
		t.Fatal("global_max_attempts should be omitted when its value is invalid")
	}
	if _, exists := export.Settings["max_retries"]; exists {
		t.Fatal("max_retries should not be exported once normalized")
	}
}

func TestExportConfig_ProviderListError(t *testing.T) {
	h, st, _ := testHandler()
	st.listErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()

	h.ExportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestExportConfig_GroupListError(t *testing.T) {
	// Use a custom store that fails only on group list
	logger, _ := zap.NewDevelopment()
	st := &groupListErrorStore{mockStore: *newMockStore()}

	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()

	h.ExportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestExportConfig_ConfigError(t *testing.T) {
	h, st, _ := testHandler()
	st.configErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()

	h.ExportConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestExportConfig_BackoffRoundtrip(t *testing.T) {
	h, st, _ := testHandler()

	// Setup provider with backoff settings and an API-type-only credential to
	// ensure export/import round-trips the split-key model.
	st.providers["p1"] = &model.Provider{
		ID:     "p1",
		Name:   "Backoff Provider",
		APIKey: "",
		APITypes: []model.ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "claude",
			BaseURL:    "https://api.example.com",
			APIKey:     "claude-key",
		}},
		AuthMode: "bearer",
		Weight:   1,
		Enabled:  true,
		Backoff: model.BackoffPolicy{
			InitialDelay: model.Duration(500 * time.Millisecond),
			MaxDelay:     model.Duration(5 * time.Second),
			Multiplier:   2.5,
			Jitter:       true,
		},
	}

	// Export
	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()
	h.ExportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d", w.Code, http.StatusOK)
	}

	var export ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatalf("failed to decode export: %v", err)
	}

	if len(export.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(export.Providers))
	}

	ep := export.Providers[0]
	if ep.APIKey != "" {
		t.Errorf("exported APIKey = %q, want empty default key", ep.APIKey)
	}
	if len(ep.APITypes) != 1 || ep.APITypes[0].APIKey != "claude-key" {
		t.Fatalf("exported APITypes = %+v, want API-type override to survive export", ep.APITypes)
	}
	if ep.Backoff.InitialDelay != model.Duration(500*time.Millisecond) {
		t.Errorf("exported InitialDelay = %v, want 500ms", time.Duration(ep.Backoff.InitialDelay))
	}
	if ep.Backoff.MaxDelay != model.Duration(5*time.Second) {
		t.Errorf("exported MaxDelay = %v, want 5s", time.Duration(ep.Backoff.MaxDelay))
	}
	if ep.Backoff.Multiplier != 2.5 {
		t.Errorf("exported Multiplier = %v, want 2.5", ep.Backoff.Multiplier)
	}
	if !ep.Backoff.Jitter {
		t.Error("exported Jitter = false, want true")
	}

	// Import the exported data into a fresh store
	h2, st2, _ := testHandler()

	importReq := ImportConfigRequest{
		Version:   export.Version,
		Providers: export.Providers,
	}
	body, _ := json.Marshal(importReq)
	req2 := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	h2.ImportConfig(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body: %s", w2.Code, http.StatusOK, w2.Body.String())
	}

	// Verify backoff settings survived the roundtrip
	imported := st2.providers["p1"]
	if imported == nil {
		t.Fatal("provider p1 not found after import")
	}
	if imported.APIKey != "" {
		t.Errorf("imported APIKey = %q, want empty default key", imported.APIKey)
	}
	if len(imported.APITypes) != 1 || imported.APITypes[0].APIKey != "claude-key" {
		t.Fatalf("imported APITypes = %+v, want API-type override to survive import", imported.APITypes)
	}
	if imported.Backoff.InitialDelay != model.Duration(500*time.Millisecond) {
		t.Errorf("imported InitialDelay = %v, want 500ms", time.Duration(imported.Backoff.InitialDelay))
	}
	if imported.Backoff.MaxDelay != model.Duration(5*time.Second) {
		t.Errorf("imported MaxDelay = %v, want 5s", time.Duration(imported.Backoff.MaxDelay))
	}
	if imported.Backoff.Multiplier != 2.5 {
		t.Errorf("imported Multiplier = %v, want 2.5", imported.Backoff.Multiplier)
	}
	if !imported.Backoff.Jitter {
		t.Error("imported Jitter = false, want true")
	}
}

func TestExportConfig_ChatGPTProviderCanonicalizesTransportFields(t *testing.T) {
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

	st.providers["gpt"] = &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		APIKey:         "stale-key",
		APITypes:       []model.ProviderAPIType{{ProviderID: "gpt", APIType: "claude", BaseURL: "https://stale.example.com", APIKey: "stale-override"}},
		AuthMode:       "auto",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &model.ProviderCredential{
			ProviderID:       "gpt",
			SecretData:       string(credentialData),
			BindingAccountID: strPtr("acct_test"),
			Version:          3,
		},
		AuthState: &model.ProviderAuthState{
			ProviderID: "gpt",
			Status:     model.ProviderAuthStatusActive,
			AccountID:  "acct_test",
		},
		Enabled: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()

	h.ExportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var export ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatalf("decode export: %v", err)
	}

	if len(export.Providers) != 1 {
		t.Fatalf("len(export.Providers) = %d, want 1", len(export.Providers))
	}

	provider := export.Providers[0]
	if provider.CredentialType != model.ProviderCredentialTypeChatGPT {
		t.Fatalf("CredentialType = %q, want %q", provider.CredentialType, model.ProviderCredentialTypeChatGPT)
	}
	if provider.Credential == nil {
		t.Fatal("Credential = nil, want exported credential payload")
	}
	if provider.Credential.SecretData != string(credentialData) {
		t.Fatalf("Credential.SecretData = %q, want original credential payload", provider.Credential.SecretData)
	}
	if provider.Credential.BindingAccountID == nil || *provider.Credential.BindingAccountID != "acct_test" {
		t.Fatalf("Credential.BindingAccountID = %v, want acct_test", provider.Credential.BindingAccountID)
	}
	if provider.Credential.Version != 3 {
		t.Fatalf("Credential.Version = %d, want 3", provider.Credential.Version)
	}
	if provider.AuthState == nil {
		t.Fatal("AuthState = nil, want exported auth state")
	}
	if provider.AuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("AuthState.Status = %q, want %q", provider.AuthState.Status, model.ProviderAuthStatusActive)
	}
	if provider.AuthMode != "bearer" {
		t.Fatalf("AuthMode = %q, want %q", provider.AuthMode, "bearer")
	}
	if provider.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", provider.APIKey)
	}
	if len(provider.APITypes) != 1 {
		t.Fatalf("len(provider.APITypes) = %d, want 1", len(provider.APITypes))
	}
	if provider.APITypes[0].APIType != "codex" {
		t.Fatalf("APIType = %q, want %q", provider.APITypes[0].APIType, "codex")
	}
	if provider.APITypes[0].BaseURL != providerauth.ChatGPTCodexBaseURL() {
		t.Fatalf("BaseURL = %q, want %q", provider.APITypes[0].BaseURL, providerauth.ChatGPTCodexBaseURL())
	}
	if provider.APITypes[0].APIKey != "" {
		t.Fatalf("APITypes[0].APIKey = %q, want empty", provider.APITypes[0].APIKey)
	}
}

func TestExportConfig_ChatGPTProviderRoundTripPreservesCredentialAndAuthState(t *testing.T) {
	h, st, _ := testHandler()

	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("encode credentialData: %v", err)
	}

	now := time.Date(2026, time.March, 27, 4, 0, 0, 0, time.UTC)
	bindingAccountID := "acct_test"
	st.providers["gpt"] = &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		APIKey:         "stale-key",
		APITypes:       []model.ProviderAPIType{{ProviderID: "gpt", APIType: "claude", BaseURL: "https://stale.example.com", APIKey: "stale-override"}},
		AuthMode:       "auto",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential: &model.ProviderCredential{
			ProviderID:       "gpt",
			SecretData:       credentialData,
			BindingAccountID: &bindingAccountID,
			Version:          5,
		},
		AuthState: &model.ProviderAuthState{
			ProviderID:       "gpt",
			Status:           model.ProviderAuthStatusReauthRequired,
			StatusReason:     "invalid_grant",
			LastError:        "refresh_token_reused",
			LastTransitionAt: &now,
			AccountID:        "acct_test",
			Email:            "user@example.com",
		},
		Enabled: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	w := httptest.NewRecorder()
	h.ExportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d", w.Code, http.StatusOK)
	}

	var export ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatalf("decode export: %v", err)
	}

	h2, st2, _ := testHandler()
	importReq := ImportConfigRequest{
		Version:   export.Version,
		Providers: export.Providers,
	}
	body, err := json.Marshal(importReq)
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h2.ImportConfig(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body: %s", w2.Code, http.StatusOK, w2.Body.String())
	}

	imported := st2.providers["gpt"]
	if imported == nil {
		t.Fatal("provider gpt not found after roundtrip import")
	}
	if imported.Credential == nil {
		t.Fatal("Credential = nil, want imported credential payload")
	}
	if imported.Credential.SecretData != credentialData {
		t.Fatalf("Credential.SecretData = %q, want round-tripped payload", imported.Credential.SecretData)
	}
	if imported.Credential.Version != 5 {
		t.Fatalf("Credential.Version = %d, want 5", imported.Credential.Version)
	}
	if imported.AuthState == nil {
		t.Fatal("AuthState = nil, want imported auth state")
	}
	if imported.AuthState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("AuthState.Status = %q, want %q", imported.AuthState.Status, model.ProviderAuthStatusReauthRequired)
	}
	if imported.AuthState.StatusReason != "invalid_grant" {
		t.Fatalf("AuthState.StatusReason = %q, want invalid_grant", imported.AuthState.StatusReason)
	}
	if imported.Credential == nil || imported.Credential.SecretData != credentialData {
		t.Fatalf("Credential = %#v, want secret payload %q", imported.Credential, credentialData)
	}
}
