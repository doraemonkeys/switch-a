package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
		BaseURL:  "https://api.p1.com",
		APIKey:   "key1",
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}},
		AuthMode: "bearer",
		Weight:   1,
		Enabled:  true,
	}
	groupID := "g1"
	st.providers["p2"] = &model.Provider{
		ID:       "p2",
		Name:     "Provider 2",
		BaseURL:  "https://api.p2.com",
		APIKey:   "key2",
		APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "codex"}},
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

	st.config["sticky_enabled"] = "true"
	st.config["max_retries"] = "3"

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

	if export.Settings["sticky_enabled"] != "true" {
		t.Errorf("sticky_enabled = %q, want %q", export.Settings["sticky_enabled"], "true")
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
	if len(p1.APITypes) != 1 || p1.APITypes[0] != "claude" {
		t.Errorf("p1.APITypes = %v, want [claude]", p1.APITypes)
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
	st.config["sticky_enabled"] = "true"

	importReq := ImportConfigRequest{
		Version: "1.0",
		Providers: []ExportedProvider{
			{ID: "p1", Name: "Updated Provider", BaseURL: "https://api.com", APIKey: "key", APITypes: []string{"claude"}, Enabled: true},
			{ID: "p2", Name: "New Provider", BaseURL: "https://api2.com", APIKey: "key2", APITypes: []string{"codex"}, Enabled: true},
		},
		Groups: []ExportedGroup{
			{ID: "g1", Name: "Updated Group", Strategy: "priority", Enabled: true},
			{ID: "g2", Name: "New Group", Strategy: "weight", Enabled: true},
		},
		Settings: map[string]string{
			"sticky_enabled": "false",
			"max_retries":    "5",
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

	// Verify data was not actually changed
	if st.providers["p2"] != nil {
		t.Error("p2 should not exist after dry run")
	}
}

func TestImportConfig_ActualImport(t *testing.T) {
	h, st, _ := testHandler()

	// Setup existing data
	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Existing Provider", BaseURL: "old", APIKey: "old"}
	st.groups["g1"] = &model.Group{ID: "g1", Name: "Existing Group"}

	importReq := ImportConfigRequest{
		Version: "1.0",
		Providers: []ExportedProvider{
			{ID: "p1", Name: "Updated Provider", BaseURL: "https://api.com", APIKey: "key", APITypes: []string{"claude"}, AuthMode: "bearer", Weight: 1, Enabled: true},
			{ID: "p2", Name: "New Provider", BaseURL: "https://api2.com", APIKey: "key2", APITypes: []string{"codex"}, AuthMode: "auto", Weight: 2, Enabled: false},
		},
		Groups: []ExportedGroup{
			{ID: "g1", Name: "Updated Group", Strategy: "priority", Weight: 1, Enabled: true},
			{ID: "g2", Name: "New Group", Strategy: "weight", Weight: 2, Enabled: false},
		},
		Settings: map[string]string{
			"max_retries": "5",
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
			{ID: "", Name: "Empty ID Provider"},                                                          // Empty ID
			{ID: "p1", Name: "Invalid API Type", APITypes: []string{"invalid"}},                          // Invalid API type
			{ID: "p2", Name: "Invalid Auth Mode", APITypes: []string{"claude"}, AuthMode: "invalid"},     // Invalid auth mode
			{ID: "p3", Name: "Non-existent Group", APITypes: []string{"claude"}, GroupID: strPtr("g99")}, // Group doesn't exist
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
				BaseURL:  "https://api.com",
				APIKey:   "key",
				APITypes: []string{"claude"},
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
			{ID: "p1", Name: "New Provider", BaseURL: "https://api.com", APIKey: "key", APITypes: []string{"claude"}, Weight: 1, Enabled: true},
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
			{ID: "", Name: "Empty ID"},                                                  // Skipped: empty ID
			{ID: "p1", Name: ""},                                                        // Skipped: empty name
			{ID: "p2", Name: "No URL", APIKey: "key"},                                   // Skipped: empty base_url
			{ID: "p3", Name: "No Key", BaseURL: "https://api.com"},                      // Skipped: empty api_key
			{ID: "p4", Name: "No API Types", BaseURL: "https://api.com", APIKey: "key"}, // Skipped: no API types
			{ID: "p5", Name: "Valid", BaseURL: "https://api.com", APIKey: "key", APITypes: []string{"claude"}, Weight: 1, Enabled: true}, // Valid
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
				BaseURL:  "https://api.com",
				APIKey:   "key",
				APITypes: []string{"claude"},
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
