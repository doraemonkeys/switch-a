package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Provider Tests

func defaultProviderAPITypes(providerID string) []model.ProviderAPIType {
	return []model.ProviderAPIType{{
		ProviderID: providerID,
		APIType:    "claude",
		BaseURL:    "https://api.example.com",
	}}
}

func TestListProviders(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1"}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2"}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/providers", nil)
	w := httptest.NewRecorder()

	h.ListProviders(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var providers []model.Provider
	if err := json.NewDecoder(w.Body).Decode(&providers); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(providers) != 2 {
		t.Errorf("len(providers) = %d, want 2", len(providers))
	}
}

func TestListProviders_Error(t *testing.T) {
	h, st, _ := testHandler()
	st.listErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/providers", nil)
	w := httptest.NewRecorder()

	h.ListProviders(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{
		ID:   "test-provider",
		Name: "Test Provider",
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/providers/test-provider", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.GetProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var provider model.Provider
	if err := json.NewDecoder(w.Body).Decode(&provider); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if provider.ID != "test-provider" {
		t.Errorf("ID = %q, want %q", provider.ID, "test-provider")
	}
}

func TestGetProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/providers/non-existent", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.GetProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/providers/", nil)
	w := httptest.NewRecorder()

	h.GetProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetProvider_InternalError(t *testing.T) {
	h, st, _ := testHandler()
	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/providers/test", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.GetProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestCreateProvider(t *testing.T) {
	h, st, _ := testHandler()

	body := `{
		"id": "new-provider",
		"name": "New Provider",
		"api_key": "sk-test-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	if _, ok := st.providers["new-provider"]; !ok {
		t.Error("provider was not created in store")
	}
}

func TestCreateProvider_WithAPITypeKeyOverride(t *testing.T) {
	h, st, _ := testHandler()

	body := `{
		"id": "split-creds",
		"name": "Split Credentials",
		"api_key": "",
		"api_types": [{
			"api_type": "claude",
			"base_url": "https://api.example.com",
			"api_key": "type-only-key"
		}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	created := st.providers["split-creds"]
	if created == nil {
		t.Fatal("provider was not created in store")
	}
	if created.APIKey != "" {
		t.Errorf("APIKey = %q, want empty default key", created.APIKey)
	}
	if len(created.APITypes) != 1 || created.APITypes[0].APIKey != "type-only-key" {
		t.Fatalf("APITypes = %+v, want api_type override key to persist", created.APITypes)
	}
}

func TestCreateProvider_WhitespaceOverrideFallsBackToDefaultKey(t *testing.T) {
	h, st, _ := testHandler()

	body := `{
		"id": "trimmed-default",
		"name": "Trimmed Default",
		"api_key": "  default-key  ",
		"api_types": [{
			"api_type": "claude",
			"base_url": "https://api.example.com",
			"api_key": "   "
		}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	created := st.providers["trimmed-default"]
	if created == nil {
		t.Fatal("provider was not created in store")
	}
	if created.APIKey != "default-key" {
		t.Errorf("APIKey = %q, want %q", created.APIKey, "default-key")
	}
	if len(created.APITypes) != 1 || created.APITypes[0].APIKey != "" {
		t.Fatalf("APITypes = %+v, want whitespace override to normalize to empty", created.APITypes)
	}
}

func TestCreateProvider_ValidationErrors(t *testing.T) {
	h, _, _ := testHandler()

	tests := []struct {
		name string
		body string
	}{
		{"missing id", `{"name": "Test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "https://api.com"}]}`},
		{"missing name", `{"id": "test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "https://api.com"}]}`},
		{"missing api_key", `{"id": "test", "name": "Test", "api_types": [{"api_type": "claude", "base_url": "https://api.com"}]}`},
		{"missing api_types", `{"id": "test", "name": "Test", "api_key": "key"}`},
		{"empty api_types", `{"id": "test", "name": "Test", "api_key": "key", "api_types": []}`},
		{"missing base_url in api_type", `{"id": "test", "name": "Test", "api_key": "key", "api_types": [{"api_type": "claude"}]}`},
		{"invalid base_url (no scheme)", `{"id": "test", "name": "Test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "not-a-url"}]}`},
		{"invalid base_url (no host)", `{"id": "test", "name": "Test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "http://"}]}`},
		{"negative max_retries", `{"id": "test", "name": "Test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "https://api.com"}], "max_retries": -2}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.CreateProvider(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestCreateProvider_InvalidJSON(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateProvider_Conflict(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["existing"] = &model.Provider{ID: "existing", Name: "Existing"}

	body := `{
		"id": "existing",
		"name": "New Provider",
		"api_key": "sk-test-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestCreateProvider_CreateError(t *testing.T) {
	h, st, _ := testHandler()
	st.createErr = errors.New("database error")

	body := `{
		"id": "new-provider",
		"name": "New Provider",
		"api_key": "sk-test-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestCreateProvider_GetCheckError(t *testing.T) {
	h, st, _ := testHandler()
	st.getErr = errors.New("database error")

	body := `{
		"id": "new-provider",
		"name": "New Provider",
		"api_key": "sk-test-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestCreateProvider_WithEnabled(t *testing.T) {
	h, st, _ := testHandler()

	body := `{
		"id": "new-provider",
		"name": "New Provider",
		"api_key": "sk-test-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}],
		"enabled": false
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	if st.providers["new-provider"].Enabled {
		t.Error("provider should be disabled")
	}
}

func TestCreateProvider_WithBackoff(t *testing.T) {
	h, st, _ := testHandler()

	body := `{
		"id": "new-provider",
		"name": "New Provider",
		"api_key": "sk-test-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}],
		"backoff": {
			"initial_delay": "100ms",
			"max_delay": "5s",
			"multiplier": 2.0,
			"jitter": true
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	provider := st.providers["new-provider"]
	if provider.Backoff.InitialDelay != model.Duration(100*time.Millisecond) {
		t.Errorf("InitialDelay = %v, want 100ms", provider.Backoff.InitialDelay)
	}
	if provider.Backoff.MaxDelay != model.Duration(5*time.Second) {
		t.Errorf("MaxDelay = %v, want 5s", provider.Backoff.MaxDelay)
	}
	if provider.Backoff.Multiplier != 2.0 {
		t.Errorf("Multiplier = %v, want 2.0", provider.Backoff.Multiplier)
	}
	if !provider.Backoff.Jitter {
		t.Error("Jitter should be true")
	}
}

func TestCreateProvider_BackoffValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "negative initial_delay",
			body: `{"id": "test", "name": "Test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "https://api.com"}], "backoff": {"initial_delay": "-1s"}}`,
		},
		{
			name: "initial_delay exceeds max_delay",
			body: `{"id": "test", "name": "Test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "https://api.com"}], "backoff": {"initial_delay": "10s", "max_delay": "1s"}}`,
		},
		{
			name: "multiplier less than 1",
			body: `{"id": "test", "name": "Test", "api_key": "key", "api_types": [{"api_type": "claude", "base_url": "https://api.com"}], "backoff": {"initial_delay": "100ms", "multiplier": 0.5}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, _ := testHandler()

			req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.CreateProvider(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func TestUpdateProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{
		ID:       "test-provider",
		Name:     "Old Name",
		APIKey:   "old-key",
		APITypes: defaultProviderAPITypes("test-provider"),
		Weight:   1,
		Priority: 0,
	}

	body := `{"name": "New Name", "weight": 5}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test-provider", bytes.NewBufferString(body))
	setPathValue(req, "id", "test-provider")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["test-provider"]
	if updated.Name != "New Name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New Name")
	}
	if updated.Weight != 5 {
		t.Errorf("Weight = %d, want 5", updated.Weight)
	}
}

func TestUpdateProvider_AllowsAPITypeKeyOverrideToReplaceDefault(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{
		ID:       "test-provider",
		Name:     "Old Name",
		APIKey:   "old-key",
		APITypes: defaultProviderAPITypes("test-provider"),
	}

	body := `{
		"api_key": "",
		"api_types": [{
			"api_type": "claude",
			"base_url": "https://api.example.com",
			"api_key": "claude-override-key"
		}]
	}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test-provider", bytes.NewBufferString(body))
	setPathValue(req, "id", "test-provider")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["test-provider"]
	if updated.APIKey != "" {
		t.Errorf("APIKey = %q, want cleared default key", updated.APIKey)
	}
	if len(updated.APITypes) != 1 || updated.APITypes[0].APIKey != "claude-override-key" {
		t.Fatalf("APITypes = %+v, want override key to persist", updated.APITypes)
	}
}

func TestUpdateProvider_WhitespaceOverrideFallsBackToDefaultKey(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{
		ID:       "test-provider",
		Name:     "Old Name",
		APIKey:   "old-key",
		APITypes: defaultProviderAPITypes("test-provider"),
	}

	body := `{
		"api_key": "  fresh-default  ",
		"api_types": [{
			"api_type": "claude",
			"base_url": "https://api.example.com",
			"api_key": "   "
		}]
	}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test-provider", bytes.NewBufferString(body))
	setPathValue(req, "id", "test-provider")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["test-provider"]
	if updated.APIKey != "fresh-default" {
		t.Errorf("APIKey = %q, want %q", updated.APIKey, "fresh-default")
	}
	if len(updated.APITypes) != 1 || updated.APITypes[0].APIKey != "" {
		t.Fatalf("APITypes = %+v, want whitespace override to normalize to empty", updated.APITypes)
	}
}

func TestUpdateProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/non-existent", bytes.NewBufferString(body))
	setPathValue(req, "id", "non-existent")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUpdateProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateProvider_InvalidJSON(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test"] = &model.Provider{
		ID:       "test",
		Name:     "Test",
		APITypes: defaultProviderAPITypes("test"),
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString("invalid"))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateProvider_ClearGroupID(t *testing.T) {
	h, st, _ := testHandler()

	groupID := "group-1"
	st.providers["test"] = &model.Provider{
		ID:       "test",
		Name:     "Test",
		APIKey:   "key",
		GroupID:  &groupID,
		APITypes: defaultProviderAPITypes("test"),
	}

	body := `{"group_id": ""}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if st.providers["test"].GroupID != nil {
		t.Error("GroupID should be nil after clearing")
	}
}

func TestUpdateProvider_UpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test"] = &model.Provider{
		ID:       "test",
		Name:     "Test",
		APIKey:   "key",
		APITypes: defaultProviderAPITypes("test"),
	}
	st.updateErr = errors.New("database error")

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdateProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test-id", bytes.NewBufferString(body))
	setPathValue(req, "id", "test-id")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdateProvider_AllFields(t *testing.T) {
	h, st, _ := testHandler()

	groupID := "group-1"
	// Add the group so that GroupID validation passes
	st.groups[groupID] = &model.Group{ID: groupID, Name: "Test Group"}
	st.providers["test"] = &model.Provider{
		ID:          "test",
		Name:        "Old Name",
		APIKey:      "old-key",
		APITypes:    defaultProviderAPITypes("test"),
		AuthMode:    "bearer",
		GroupID:     nil,
		Weight:      1,
		Priority:    0,
		Concurrency: 0,
		MaxRetries:  0,
		Enabled:     true,
	}

	body := `{
		"name": "New Name",
		"api_key": "new-key",
		"api_types": [{"api_type": "claude", "base_url": "https://new.api.com"}, {"api_type": "codex", "base_url": "https://new.api.com"}],
		"auth_mode": "x-api-key",
		"group_id": "group-1",
		"weight": 5,
		"priority": 10,
		"concurrency": 3,
		"max_retries": 2,
		"enabled": false
	}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["test"]
	if updated.Name != "New Name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New Name")
	}
	if updated.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want %q", updated.APIKey, "new-key")
	}
	if len(updated.APITypes) != 2 {
		t.Errorf("len(APITypes) = %d, want 2", len(updated.APITypes))
	}
	if updated.AuthMode != "x-api-key" {
		t.Errorf("AuthMode = %q, want %q", updated.AuthMode, "x-api-key")
	}
	if updated.GroupID == nil || *updated.GroupID != groupID {
		t.Errorf("GroupID = %v, want %q", updated.GroupID, groupID)
	}
	if updated.Weight != 5 {
		t.Errorf("Weight = %d, want 5", updated.Weight)
	}
	if updated.Priority != 10 {
		t.Errorf("Priority = %d, want 10", updated.Priority)
	}
	if updated.Concurrency != 3 {
		t.Errorf("Concurrency = %d, want 3", updated.Concurrency)
	}
	if updated.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want 2", updated.MaxRetries)
	}
	if updated.Enabled {
		t.Error("Enabled should be false")
	}
}

func TestUpdateProvider_WithBackoff(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test"] = &model.Provider{
		ID:       "test",
		Name:     "Test",
		APIKey:   "key",
		APITypes: defaultProviderAPITypes("test"),
		Backoff:  model.BackoffPolicy{},
	}

	body := `{
		"backoff": {
			"initial_delay": "200ms",
			"max_delay": "10s",
			"multiplier": 1.5,
			"jitter": true
		}
	}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["test"]
	if updated.Backoff.InitialDelay != model.Duration(200*time.Millisecond) {
		t.Errorf("InitialDelay = %v, want 200ms", updated.Backoff.InitialDelay)
	}
	if updated.Backoff.MaxDelay != model.Duration(10*time.Second) {
		t.Errorf("MaxDelay = %v, want 10s", updated.Backoff.MaxDelay)
	}
	if updated.Backoff.Multiplier != 1.5 {
		t.Errorf("Multiplier = %v, want 1.5", updated.Backoff.Multiplier)
	}
	if !updated.Backoff.Jitter {
		t.Error("Jitter should be true")
	}
}

func TestUpdateProvider_BackoffValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "negative initial_delay",
			body:    `{"backoff": {"initial_delay": "-1s"}}`,
			wantMsg: "Invalid backoff",
		},
		{
			name:    "initial_delay exceeds max_delay",
			body:    `{"backoff": {"initial_delay": "10s", "max_delay": "1s"}}`,
			wantMsg: "Invalid backoff",
		},
		{
			name:    "multiplier less than 1",
			body:    `{"backoff": {"initial_delay": "100ms", "multiplier": 0.5}}`,
			wantMsg: "Invalid backoff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := testHandler()
			st.providers["test"] = &model.Provider{
				ID:       "test",
				Name:     "Test",
				APIKey:   "key",
				APITypes: defaultProviderAPITypes("test"),
			}

			req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(tt.body))
			setPathValue(req, "id", "test")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.UpdateProvider(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.wantMsg)) {
				t.Errorf("body = %s, want to contain %q", w.Body.String(), tt.wantMsg)
			}
		})
	}
}

func TestUpdateProvider_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "empty name",
			body:    `{"name": ""}`,
			wantMsg: "Name cannot be empty",
		},
		{
			name:    "empty api_key",
			body:    `{"api_key": ""}`,
			wantMsg: "api_key is required for api_type: claude",
		},
		{
			name:    "zero weight",
			body:    `{"weight": 0}`,
			wantMsg: "Weight must be positive",
		},
		{
			name:    "negative weight",
			body:    `{"weight": -1}`,
			wantMsg: "Weight must be positive",
		},
		{
			name:    "negative concurrency",
			body:    `{"concurrency": -1}`,
			wantMsg: "Concurrency cannot be negative",
		},
		{
			name:    "negative max_retries",
			body:    `{"max_retries": -2}`,
			wantMsg: "MaxRetries must be non-negative",
		},
		{
			name:    "max_retries boundary minus one",
			body:    `{"max_retries": -1}`,
			wantMsg: "MaxRetries must be non-negative",
		},
		{
			name:    "invalid base_url in api_type (no scheme)",
			body:    `{"api_types": [{"api_type": "claude", "base_url": "not-a-url"}]}`,
			wantMsg: "Invalid base_url for api_type",
		},
		{
			name:    "invalid base_url in api_type (no host)",
			body:    `{"api_types": [{"api_type": "claude", "base_url": "http://"}]}`,
			wantMsg: "Invalid base_url for api_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := testHandler()
			st.providers["test"] = &model.Provider{
				ID:       "test",
				Name:     "Test",
				APIKey:   "key",
				APITypes: defaultProviderAPITypes("test"),
			}

			req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(tt.body))
			setPathValue(req, "id", "test")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.UpdateProvider(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.wantMsg)) {
				t.Errorf("body = %s, want to contain %q", w.Body.String(), tt.wantMsg)
			}
		})
	}
}

func TestUpdateProvider_SyncsHealthManager(t *testing.T) {
	h, st, health := testHandler()

	st.providers["test"] = &model.Provider{
		ID:       "test",
		Name:     "Test",
		APIKey:   "key",
		Enabled:  true,
		APITypes: defaultProviderAPITypes("test"),
	}

	// Disable the provider
	body := `{"enabled": false}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify provider is disabled
	if st.providers["test"].Enabled {
		t.Error("Provider should be disabled")
	}

	// Re-enable the provider
	body = `{"enabled": true}`
	req = httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify provider is enabled
	if !st.providers["test"].Enabled {
		t.Error("Provider should be enabled")
	}

	// Test that health manager error is logged but doesn't fail the request
	health.disableErr = errors.New("health manager error")
	st.providers["test"].Enabled = true

	body = `{"enabled": false}`
	req = httptest.NewRequest(http.MethodPut, "/admin/api/providers/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	h.UpdateProvider(w, req)

	// Request should still succeed even if health manager fails
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestDeleteProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test"}

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/providers/test-provider", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.DeleteProvider(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if _, ok := st.providers["test-provider"]; ok {
		t.Error("provider was not deleted")
	}
}

func TestDeleteProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/providers/non-existent", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.DeleteProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/providers/", nil)
	w := httptest.NewRecorder()

	h.DeleteProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDeleteProvider_DeleteError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test"] = &model.Provider{ID: "test", Name: "Test"}
	st.deleteErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/providers/test", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DeleteProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDeleteProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/providers/test", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DeleteProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestEnableProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test", Enabled: false}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/enable", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if !st.providers["test-provider"].Enabled {
		t.Error("provider should be enabled")
	}
}

func TestEnableProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/non-existent/enable", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestEnableProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers//enable", nil)
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestEnableProvider_UpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test"] = &model.Provider{ID: "test", Name: "Test", Enabled: false}
	st.updateErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/enable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestEnableProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/enable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDisableProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test", Enabled: true}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/disable", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if st.providers["test-provider"].Enabled {
		t.Error("provider should be disabled")
	}
}

func TestDisableProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/non-existent/disable", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDisableProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers//disable", nil)
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDisableProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/disable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDisableProvider_UpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test"] = &model.Provider{ID: "test", Name: "Test", Enabled: true}
	st.updateErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/disable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestResetProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test"}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/reset", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestResetProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/non-existent/reset", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestResetProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers//reset", nil)
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestResetProvider_HealthEnableError(t *testing.T) {
	h, st, health := testHandler()

	st.providers["test"] = &model.Provider{ID: "test", Name: "Test"}
	health.enableErr = errors.New("health manager error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/reset", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestResetProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/reset", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestResetProvider_NoHealthManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()

	// Create handler without health manager
	h := NewHandler(Config{
		Store:       st,
		Health:      nil,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test"}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/reset", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
