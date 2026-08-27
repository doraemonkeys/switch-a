package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
)

func TestImportConfig_DryRunUsesCredentialSessionContract(t *testing.T) {
	handler, store, _ := testHandler()
	requestBody := ImportConfigRequest{
		Version:            ConfigExportVersion,
		CredentialSessions: []ExportedCredentialSession{importedTestSession("session-1", "secret-1")},
		Providers: []ExportedProvider{{
			ID: "provider-1", Name: "Provider", AuthMode: "bearer", Weight: 1, Enabled: true,
			APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.example.com", CredentialSessionID: "session-1"}},
		}},
	}
	w := performConfigImport(t, handler, requestBody, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var response ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.DryRun || response.Changes.Providers.Add != 1 || response.Changes.CredentialSessions.Add != 1 {
		t.Fatalf("preview = %#v", response)
	}
	if store.providers["provider-1"] != nil || store.credentialSessions["session-1"] != nil {
		t.Fatal("dry run mutated store")
	}
}

func TestImportConfig_DryRunReturnsEmptyWarningsArray(t *testing.T) {
	handler, _, _ := testHandler()
	w := performConfigImport(t, handler, ImportConfigRequest{
		Version: ConfigExportVersion, Providers: []ExportedProvider{}, CredentialSessions: []ExportedCredentialSession{},
		Groups: []ExportedGroup{}, RoutingPolicies: []ExportedRoutingPolicy{}, Settings: map[string]string{},
	}, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"warnings":[]`) {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func TestImportConfig_AppliesProviderAndCredentialSessionAtomically(t *testing.T) {
	handler, store, _ := testHandler()
	requestBody := ImportConfigRequest{
		Version:            ConfigExportVersion,
		CredentialSessions: []ExportedCredentialSession{importedTestSession("session-1", "secret-1")},
		Providers: []ExportedProvider{{
			ID: "provider-1", Name: "Provider", AuthMode: "bearer", Weight: 1, Enabled: true,
			APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://api.example.com", CredentialSessionID: "session-1"}},
		}},
		Groups: []ExportedGroup{{ID: "group-1", Name: "Group", Strategy: "priority", Weight: 1, Enabled: true}},
		Settings: map[string]string{
			"global_max_attempts":                       "5",
			defaults.ConfigKeyWebSocketProbeClientModel: "false",
		},
	}
	w := performConfigImport(t, handler, requestBody, false)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	provider := store.providers["provider-1"]
	if provider == nil {
		t.Fatal("provider was not imported")
	}
	snapshot, ok := provider.CredentialSessionForAPIType("claude")
	if !ok || snapshot.SessionID != "session-1" || store.credentialSessions["session-1"] == nil {
		t.Fatalf("provider/session state = provider %#v sessions %#v", provider, store.credentialSessions)
	}
}

func TestImportConfig_MigratesLegacySettingKeys(t *testing.T) {
	handler, store, _ := testHandler()
	w := performConfigImport(t, handler, ImportConfigRequest{
		Version:  ConfigExportVersion,
		Settings: map[string]string{"sticky_enabled": "false", "max_retries": "6"},
	}, false)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if store.config["sticky_mode"] != "off" || store.config["global_max_attempts"] != "6" {
		t.Fatalf("migrated settings = %#v", store.config)
	}
}

func TestImportConfig_RejectsInvalidSettingValuesOnApply(t *testing.T) {
	handler, store, _ := testHandler()
	w := performConfigImport(t, handler, ImportConfigRequest{
		Version:  ConfigExportVersion,
		Settings: map[string]string{"global_max_attempts": "-1"},
	}, false)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if _, exists := store.config["global_max_attempts"]; exists {
		t.Fatal("invalid setting was persisted")
	}
}

func performConfigImport(t *testing.T, handler *Handler, payload ImportConfigRequest, dryRun bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	target := "/admin/api/config/import"
	if dryRun {
		target += "?dry_run=true"
	}
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ImportConfig(w, request)
	return w
}
