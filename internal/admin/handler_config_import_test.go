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

	"go.uber.org/zap"
)

// providerCreateErrorStore is a mock that fails only on CreateProvider.
type providerCreateErrorStore struct {
	mockStore
}

func (s *providerCreateErrorStore) CreateProvider(_ context.Context, _ *model.Provider) error {
	return errors.New("database error")
}

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
			"sticky_enabled":      "false",
			"global_max_attempts": "5",
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
	if resp.Changes.Settings.Add != 1 {
		t.Errorf("settings.add = %d, want 1", resp.Changes.Settings.Add)
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
			"global_max_attempts": "5",
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

func TestImportConfig_WarnsAndSkipsInvalidSettingValues(t *testing.T) {
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

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var result ImportResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if result.Applied.Settings != (AppliedCount{}) {
		t.Fatalf("settings applied = %+v, want zero", result.Applied.Settings)
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
			{ID: "", Name: "Empty ID Provider"}, // Empty ID
			{ID: "p1", Name: "Invalid API Type", APITypes: []ExportedAPIType{{APIType: "invalid", BaseURL: "https://api.com"}}},                          // Invalid API type
			{ID: "p2", Name: "Invalid Auth Mode", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, AuthMode: "invalid"},     // Invalid auth mode
			{ID: "p3", Name: "Non-existent Group", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, GroupID: strPtr("g99")}, // Group doesn't exist
		},
		Groups: []ExportedGroup{
			{ID: "", Name: "Empty ID Group"},                                                             // Empty ID
			{ID: "g2", Name: "Invalid Strategy", Strategy: "invalid"},                                    // Invalid strategy
			{ID: "g3", Name: "Reserved Priority", Strategy: "priority", Priority: ReservedGroupPriority}, // Reserved priority
		},
		Settings: map[string]string{
			"invalid_key": "value", // Unknown key
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

	// Import a provider that references a group in the same import
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

	// Verify the provider has the correct group reference
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
	// Use a custom store that fails only on group list
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
	h, st, _ := testHandler()
	st.createErr = errors.New("database error")

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
	// Use a custom store that fails only on provider create
	logger, _ := zap.NewDevelopment()
	st := &providerCreateErrorStore{mockStore: *newMockStore()}

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

func TestImportConfig_SkipsInvalidProviders(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Providers: []ExportedProvider{
			{ID: "", Name: "Empty ID"},                      // Skipped: empty ID
			{ID: "p1", Name: ""},                            // Skipped: empty name
			{ID: "p2", Name: "No URL", APIKey: "key"},       // Skipped: no api_types with base_url
			{ID: "p3", Name: "No Key"},                      // Skipped: empty api_key
			{ID: "p4", Name: "No API Types", APIKey: "key"}, // Skipped: no API types
			{ID: "p5", Name: "Valid", APIKey: "key", APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.com"}}, Weight: 1, Enabled: true}, // Valid
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

	// Only p5 should be created
	if resp.Applied.Providers.Added != 1 {
		t.Errorf("providers.added = %d, want 1", resp.Applied.Providers.Added)
	}
	if st.providers["p5"] == nil {
		t.Error("p5 should exist")
	}
}

func TestImportConfig_SkipsInvalidGroups(t *testing.T) {
	h, st, _ := testHandler()

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		Groups: []ExportedGroup{
			{ID: "", Name: "Empty ID"},                                // Skipped: empty ID
			{ID: "g1", Name: ""},                                      // Skipped: empty name
			{ID: "g2", Name: "Invalid Strategy", Strategy: "invalid"}, // Skipped: invalid strategy
			{ID: "g3", Name: "Reserved Priority", Strategy: "priority", Priority: ReservedGroupPriority}, // Skipped: reserved priority
			{ID: "g4", Name: "Valid", Strategy: "priority", Weight: 1, Enabled: true},                    // Valid
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

	// Only g4 should be created
	if resp.Applied.Groups.Added != 1 {
		t.Errorf("groups.added = %d, want 1", resp.Applied.Groups.Added)
	}
	if st.groups["g4"] == nil {
		t.Error("g4 should exist")
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
				// AuthMode, Weight not set
			},
		},
		Groups: []ExportedGroup{
			{
				ID:   "g1",
				Name: "Group without defaults",
				// Strategy, Weight not set
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

	// Verify defaults were applied
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

// Helper function for creating string pointers
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
