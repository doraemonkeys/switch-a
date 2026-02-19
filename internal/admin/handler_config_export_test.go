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

	"go.uber.org/zap"
)

// groupListErrorStore is a mock that fails only on ListGroups.
type groupListErrorStore struct {
	mockStore
}

func (s *groupListErrorStore) ListGroups(_ context.Context) ([]model.Group, error) {
	return nil, errors.New("database error")
}

// providerCreateErrorStore is a mock that fails only on CreateProvider.
type providerCreateErrorStore struct {
	mockStore
}

func (s *providerCreateErrorStore) CreateProvider(_ context.Context, _ *model.Provider) error {
	return errors.New("database error")
}

func TestExportConfig(t *testing.T) {
	h, st, _ := testHandler()

	// Setup test data
	st.providers["p1"] = &model.Provider{
		ID:       "p1",
		Name:     "Provider 1",
		APIKey:   "key1",
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.p1.com"}},
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

func TestImportConfig_DryRun(t *testing.T) {
	h, st, _ := testHandler()

	// Setup existing data
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Existing Provider"}
	st.groups["g1"] = &model.Group{ID: "g1", Name: "Existing Group"}
	st.config["sticky_mode"] = "api_type"

	importReq := ImportConfigRequest{
		Version: "1.0",
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
		Version: "1.0",
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
		Version: "1.0",
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
		Version: "1.0",
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
		Version:   "1.0",
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
		Version: "1.0",
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

	importReq := ImportConfigRequest{Version: "1.0"}
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

	importReq := ImportConfigRequest{Version: "1.0"}
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

	importReq := ImportConfigRequest{Version: "1.0"}
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
		Version: "1.0",
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
		Version: "1.0",
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
		Version: "1.0",
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
		Version: "1.0",
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
		Version: "1.0",
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

func TestExportConfig_BackoffRoundtrip(t *testing.T) {
	h, st, _ := testHandler()

	// Setup provider with backoff settings
	st.providers["p1"] = &model.Provider{
		ID:       "p1",
		Name:     "Backoff Provider",
		APIKey:   "key",
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.example.com"}},
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

func TestValidateExportedProvider_MalformedURL(t *testing.T) {
	p := &ExportedProvider{
		ID:       "p1",
		Name:     "Test",
		APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "not-a-url"}},
	}

	warnings := validateExportedProvider(p)
	found := false
	for _, w := range warnings {
		if w == "Provider 'p1' has malformed base_url for api_type: claude" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected malformed base_url warning, got: %v", warnings)
	}
}

func TestMigrateImportKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		wantKey   string
		wantValue string
	}{
		{"legacy true", "sticky_enabled", "true", "sticky_mode", "api_type"},
		{"legacy one", "sticky_enabled", "1", "sticky_mode", "api_type"},
		{"legacy false", "sticky_enabled", "false", "sticky_mode", "off"},
		{"legacy zero", "sticky_enabled", "0", "sticky_mode", "off"},
		{"legacy uppercase", "sticky_enabled", "TRUE", "sticky_mode", "api_type"},
		{"legacy invalid", "sticky_enabled", "maybe", "sticky_enabled", "maybe"},
		{"other key", "sticky_ttl", "300", "sticky_ttl", "300"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, gotValue := migrateImportKey(tt.key, tt.value)
			if gotKey != tt.wantKey || gotValue != tt.wantValue {
				t.Errorf("migrateImportKey(%q, %q) = (%q, %q), want (%q, %q)", tt.key, tt.value, gotKey, gotValue, tt.wantKey, tt.wantValue)
			}
		})
	}
}
