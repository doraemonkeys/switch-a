package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"switch-a/internal/defaults"
	"switch-a/internal/model"
)

func TestImportConfig_DryRun(t *testing.T) {
	h, st, _ := testHandler()

	// Setup existing data
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Existing Provider"}
	st.groups["g1"] = &model.Group{ID: "g1", Name: "Existing Group"}
	st.config["sticky_mode"] = "api_type"

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{ID: "p1", Name: "Updated Provider", APIKey: "key", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, Enabled: true},
			{ID: "p2", Name: "New Provider", APIKey: "key2", APITypes: []ExportedAPIType{{APIType: "codex", BaseURL: "https://api2.com"}}, Enabled: true},
		},
		Groups: []ExportedGroup{
			{ID: "g1", Name: "Updated Group", Strategy: "priority", Enabled: true},
			{ID: "g2", Name: "New Group", Strategy: "weight", Enabled: true},
		},
		Settings: map[string]string{
			"sticky_enabled":                            "false",
			"global_max_attempts":                       "5",
			defaults.ConfigKeyWebSocketProbeClientModel: "false",
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

	if !resp.DryRun {
		t.Error("expected dry_run=true")
	}

	if resp.Changes.Providers.Add != 1 {
		t.Errorf("providers.add = %d, want 1", resp.Changes.Providers.Add)
	}
	if resp.Changes.Providers.Update != 1 {
		t.Errorf("providers.update = %d, want 1", resp.Changes.Providers.Update)
	}
	if resp.Changes.Groups.Add != 1 {
		t.Errorf("groups.add = %d, want 1", resp.Changes.Groups.Add)
	}
	if resp.Changes.Groups.Update != 1 {
		t.Errorf("groups.update = %d, want 1", resp.Changes.Groups.Update)
	}
	if resp.Changes.Settings.Add != 2 {
		t.Errorf("settings.add = %d, want 2", resp.Changes.Settings.Add)
	}
	if resp.Changes.Settings.Update != 1 {
		t.Errorf("settings.update = %d, want 1", resp.Changes.Settings.Update)
	}

	// Verify data was not actually changed
	if st.providers["p2"] != nil {
		t.Error("p2 should not exist after dry run")
	}
}

func TestImportConfig_ActualImport(t *testing.T) {
	h, st, _ := testHandler()

	// Setup existing data
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Existing Provider", APIKey: "old"}
	st.groups["g1"] = &model.Group{ID: "g1", Name: "Existing Group"}

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{ID: "p1", Name: "Updated Provider", APIKey: "key", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, AuthMode: "bearer", Weight: 1, Enabled: true},
			{ID: "p2", Name: "New Provider", APIKey: "key2", APITypes: []ExportedAPIType{{APIType: "codex", BaseURL: "https://api2.com"}}, AuthMode: "auto", Weight: 2, Enabled: false},
		},
		Groups: []ExportedGroup{
			{ID: "g1", Name: "Updated Group", Strategy: "priority", Weight: 1, Enabled: true},
			{ID: "g2", Name: "New Group", Strategy: "weight", Weight: 2, Enabled: false},
		},
		Settings: map[string]string{
			"global_max_attempts":                       "5",
			defaults.ConfigKeyWebSocketProbeClientModel: "false",
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

	var resp ImportResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}

	if resp.Applied.Providers.Added != 1 {
		t.Errorf("providers.added = %d, want 1", resp.Applied.Providers.Added)
	}
	if resp.Applied.Providers.Updated != 1 {
		t.Errorf("providers.updated = %d, want 1", resp.Applied.Providers.Updated)
	}
	if resp.Applied.Groups.Added != 1 {
		t.Errorf("groups.added = %d, want 1", resp.Applied.Groups.Added)
	}
	if resp.Applied.Groups.Updated != 1 {
		t.Errorf("groups.updated = %d, want 1", resp.Applied.Groups.Updated)
	}
	if resp.Applied.Settings.Added != 2 {
		t.Errorf("settings.added = %d, want 2", resp.Applied.Settings.Added)
	}

	// Verify data was actually changed
	if st.providers["p2"] == nil {
		t.Error("p2 should exist after import")
	}
	if st.providers["p1"].Name != "Updated Provider" {
		t.Errorf("p1 name = %q, want %q", st.providers["p1"].Name, "Updated Provider")
	}
	if st.groups["g2"] == nil {
		t.Error("g2 should exist after import")
	}
	if got := st.config[defaults.ConfigKeyWebSocketProbeClientModel]; got != "false" {
		t.Errorf("websocket_probe_client_model = %q, want %q", got, "false")
	}
}

func TestImportConfig_MigratesLegacyStickyEnabled(t *testing.T) {
	h, st, _ := testHandler()
	st.config["sticky_mode"] = "model"

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Settings: map[string]string{
			"sticky_enabled": "1",
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if got := st.config["sticky_mode"]; got != "api_type" {
		t.Errorf("sticky_mode = %q, want %q", got, "api_type")
	}
	if _, exists := st.config["sticky_enabled"]; exists {
		t.Error("sticky_enabled should not be persisted after import migration")
	}
}

func TestImportConfig_MigratesLegacyMaxRetriesKey(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Settings: map[string]string{
			"max_retries": "5",
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(preview.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", preview.Warnings)
	}
	if preview.Changes.Settings.Add != 1 {
		t.Fatalf("settings.add = %d, want 1", preview.Changes.Settings.Add)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if got := st.config["global_max_attempts"]; got != "5" {
		t.Fatalf("global_max_attempts = %q, want %q", got, "5")
	}
	if _, exists := st.config["max_retries"]; exists {
		t.Fatal("max_retries should not be written back during import")
	}
}

func TestImportConfig_RejectsInvalidSettingValuesOnApply(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Settings: map[string]string{
			"global_max_attempts": "-1",
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(preview.Warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 invalid-value warning", preview.Warnings)
	}
	if preview.Warnings[0] != "Invalid config value will be skipped for global_max_attempts: must be a non-negative integer" {
		t.Fatalf("warning = %q", preview.Warnings[0])
	}
	if preview.Changes.Settings != (ChangeCount{}) {
		t.Fatalf("settings changes = %+v, want zero", preview.Changes.Settings)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if _, exists := st.config["global_max_attempts"]; exists {
		t.Fatal("invalid global_max_attempts should not be persisted")
	}
}

func TestImportConfig_NoChanges(t *testing.T) {
	h, st, _ := testHandler()

	groupID := "g1"
	st.providers["p1"] = &model.Provider{
		ID:          "p1",
		Name:        "Provider 1",
		APIKey:      "key",
		APITypes:    []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.com"}},
		AuthMode:    "bearer",
		GroupID:     &groupID,
		Weight:      2,
		Priority:    1,
		Concurrency: 10,
		MaxRetries:  3,
		Enabled:     true,
	}
	st.groups["g1"] = &model.Group{
		ID:       "g1",
		Name:     "Group 1",
		Strategy: "priority",
		Priority: 1,
		Weight:   2,
		Enabled:  true,
	}
	st.config["global_max_attempts"] = "5"

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{
				ID:          "p1",
				Name:        "Provider 1",
				APIKey:      "key",
				APITypes:    []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}},
				AuthMode:    "bearer",
				GroupID:     &groupID,
				Weight:      2,
				Priority:    1,
				Concurrency: 10,
				MaxRetries:  3,
				Enabled:     true,
			},
		},
		Groups: []ExportedGroup{
			{
				ID:       "g1",
				Name:     "Group 1",
				Strategy: "priority",
				Priority: 1,
				Weight:   2,
				Enabled:  true,
			},
		},
		Settings: map[string]string{
			"global_max_attempts": "5",
		},
	}

	body, _ := json.Marshal(importReq)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode preview: %v", err)
	}

	if preview.Changes.Providers != (ChangeCount{}) {
		t.Fatalf("provider changes = %+v, want zero", preview.Changes.Providers)
	}
	if preview.Changes.Groups != (ChangeCount{}) {
		t.Fatalf("group changes = %+v, want zero", preview.Changes.Groups)
	}
	if preview.Changes.Settings != (ChangeCount{}) {
		t.Fatalf("settings changes = %+v, want zero", preview.Changes.Settings)
	}
	if len(preview.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", preview.Warnings)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var result ImportResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if result.Applied.Providers != (AppliedCount{}) {
		t.Fatalf("provider applied = %+v, want zero", result.Applied.Providers)
	}
	if result.Applied.Groups != (AppliedCount{}) {
		t.Fatalf("group applied = %+v, want zero", result.Applied.Groups)
	}
	if result.Applied.Settings != (AppliedCount{}) {
		t.Fatalf("settings applied = %+v, want zero", result.Applied.Settings)
	}
}

func TestImportConfig_ExportRoundTripPreservesLegacyProviderDefaultsAsNoOp(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{
		ID:       "p1",
		Name:     "Provider 1",
		APIKey:   "key",
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.com"}},
		Enabled:  true,
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	exportW := httptest.NewRecorder()
	h.ExportConfig(exportW, exportReq)

	if exportW.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d; body: %s", exportW.Code, http.StatusOK, exportW.Body.String())
	}

	var exported ExportedConfig
	if err := json.NewDecoder(exportW.Body).Decode(&exported); err != nil {
		t.Fatalf("failed to decode export: %v", err)
	}

	body, _ := json.Marshal(exported)

	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	previewReq.Header.Set("Content-Type", "application/json")
	previewW := httptest.NewRecorder()
	h.ImportConfig(previewW, previewReq)

	if previewW.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d; body: %s", previewW.Code, http.StatusOK, previewW.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(previewW.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode preview: %v", err)
	}

	if preview.Changes.Providers != (ChangeCount{}) {
		t.Fatalf("provider changes = %+v, want zero", preview.Changes.Providers)
	}

	importReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	importReq.Header.Set("Content-Type", "application/json")
	importW := httptest.NewRecorder()
	h.ImportConfig(importW, importReq)

	if importW.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body: %s", importW.Code, http.StatusOK, importW.Body.String())
	}

	var result ImportResult
	if err := json.NewDecoder(importW.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if result.Applied.Providers != (AppliedCount{}) {
		t.Fatalf("provider applied = %+v, want zero", result.Applied.Providers)
	}
}

func TestImportConfig_ExportRoundTripPreservesChatGPTAuthTimestampsAsNoOp(t *testing.T) {
	h, st, _ := testHandler()

	sourceTZ := time.FixedZone("SourceTZ", 8*60*60)
	lastTransitionAt := time.Date(2026, time.March, 27, 15, 4, 5, 0, sourceTZ)
	lastRefreshAt := time.Date(2026, time.March, 27, 15, 9, 5, 0, sourceTZ)
	expiresAt := time.Date(2026, time.March, 28, 15, 4, 5, 0, sourceTZ)
	lastRefreshFailureAt := time.Date(2026, time.March, 27, 14, 54, 5, 0, sourceTZ)
	fetchedAt := time.Date(2026, time.March, 27, 15, 14, 5, 0, sourceTZ)
	resetAt := time.Date(2026, time.March, 27, 20, 0, 0, 0, sourceTZ)
	bindingAccountID := "acct_test"
	usageSnapshot := &model.ProviderUsageSnapshot{
		FetchedAt: &fetchedAt,
		PlanType:  "plus",
		FiveHour: &model.ProviderUsageWindow{
			UsedPercent:   12.5,
			WindowSeconds: 5 * 60 * 60,
			ResetAt:       &resetAt,
		},
	}
	credentialData, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    bindingAccountID,
		Email:        "user@example.com",
		PlanType:     "plus",
		LastRefresh:  lastRefreshAt,
		ExpiresAt:    expiresAt,
		Usage:        usageSnapshot,
	})
	if err != nil {
		t.Fatalf("marshal credentialData: %v", err)
	}

	st.providers["gpt"] = &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
		Credential: &model.ProviderCredential{
			ProviderID:       "gpt",
			SecretData:       string(credentialData),
			BindingAccountID: &bindingAccountID,
			Version:          3,
		},
		AuthState: &model.ProviderAuthState{
			ProviderID:           "gpt",
			Status:               model.ProviderAuthStatusActive,
			StatusReason:         "healthy",
			LastTransitionAt:     &lastTransitionAt,
			Email:                "user@example.com",
			AccountID:            bindingAccountID,
			PlanType:             "plus",
			ExpiresAt:            &expiresAt,
			LastRefreshAt:        &lastRefreshAt,
			UsageSnapshot:        model.CloneProviderUsageSnapshot(usageSnapshot),
			RefreshFailCount:     1,
			LastRefreshFailureAt: &lastRefreshFailureAt,
		},
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
	exportW := httptest.NewRecorder()
	h.ExportConfig(exportW, exportReq)

	if exportW.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d; body: %s", exportW.Code, http.StatusOK, exportW.Body.String())
	}

	var exported ExportedConfig
	if err := json.NewDecoder(exportW.Body).Decode(&exported); err != nil {
		t.Fatalf("failed to decode export: %v", err)
	}

	body, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}

	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	previewReq.Header.Set("Content-Type", "application/json")
	previewW := httptest.NewRecorder()
	h.ImportConfig(previewW, previewReq)

	if previewW.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d; body: %s", previewW.Code, http.StatusOK, previewW.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(previewW.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode preview: %v", err)
	}

	if preview.Changes.Providers != (ChangeCount{}) {
		t.Fatalf("provider changes = %+v, want zero", preview.Changes.Providers)
	}

	importReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	importReq.Header.Set("Content-Type", "application/json")
	importW := httptest.NewRecorder()
	h.ImportConfig(importW, importReq)

	if importW.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body: %s", importW.Code, http.StatusOK, importW.Body.String())
	}

	var result ImportResult
	if err := json.NewDecoder(importW.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if result.Applied.Providers != (AppliedCount{}) {
		t.Fatalf("provider applied = %+v, want zero", result.Applied.Providers)
	}
}
