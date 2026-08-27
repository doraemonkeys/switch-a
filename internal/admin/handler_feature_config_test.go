package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/store"
)

var removedCodexRolloutConfigKeys = []string{
	"codex_upstream_header_hygiene_enabled",
	"codex_websocket_subprotocol_enabled",
	"codex_continuity_enabled",
	"codex_provider_cookie_jar_enabled",
}

func TestRemovedCodexRolloutKeysAreAbsentFromConfigContract(t *testing.T) {
	configDefaults := store.GetDefaultConfigs()
	for _, key := range removedCodexRolloutConfigKeys {
		if IsValidConfigKey(key) {
			t.Errorf("IsValidConfigKey(%q) = true", key)
		}
		if _, exists := configDefaults[key]; exists {
			t.Errorf("default contract contains removed key %q", key)
		}
	}
	if !IsValidConfigKey(defaults.ConfigKeyWebSocketProbeClientModel) {
		t.Errorf("WebSocket probe setting %q was removed with rollout controls", defaults.ConfigKeyWebSocketProbeClientModel)
	}
}

func TestGetAndExportConfigOmitStaleCodexRolloutRows(t *testing.T) {
	h, st, _ := testHandler()
	for _, key := range removedCodexRolloutConfigKeys {
		st.config[key] = "true"
	}
	st.config[defaults.ConfigKeyWebSocketProbeClientModel] = "false"

	getResponse := httptest.NewRecorder()
	h.GetConfig(getResponse, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body=%s", getResponse.Code, getResponse.Body.String())
	}
	var config ConfigResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	assertRemovedCodexSettingsAbsent(t, config.Defaults)
	assertRemovedCodexSettingsAbsent(t, config.Values)
	if config.Values[defaults.ConfigKeyWebSocketProbeClientModel] != "false" {
		t.Errorf("GET omitted WebSocket probe value: %#v", config.Values)
	}

	exportResponse := httptest.NewRecorder()
	h.ExportConfig(exportResponse, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", exportResponse.Code, exportResponse.Body.String())
	}
	var exported ExportedConfig
	if err := json.NewDecoder(exportResponse.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	assertRemovedCodexSettingsAbsent(t, exported.Settings)
	if exported.Settings[defaults.ConfigKeyWebSocketProbeClientModel] != "false" {
		t.Errorf("export omitted WebSocket probe value: %#v", exported.Settings)
	}
}

func TestDirectConfigUpdateRejectsRemovedCodexRolloutKeys(t *testing.T) {
	for _, key := range removedCodexRolloutConfigKeys {
		t.Run(key, func(t *testing.T) {
			h, st, _ := testHandler()
			body, err := json.Marshal(map[string]string{key: "true"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			h.UpdateConfig(response, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if _, persisted := st.config[key]; persisted {
				t.Fatal("rejected rollout key was persisted")
			}
		})
	}
}

func TestConfigImportRejectsRemovedCodexRolloutKeys(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		for _, key := range removedCodexRolloutConfigKeys {
			name := key + "/apply"
			if dryRun {
				name = key + "/dry-run"
			}
			t.Run(name, func(t *testing.T) {
				h, st, _ := testHandler()
				response := performConfigImport(t, h, ImportConfigRequest{
					Version: ConfigExportVersion,
					ImportScope: &ConfigImportScope{
						Mode: ConfigImportModeSettingsOnly,
					},
					Settings: map[string]string{key: "true"},
				}, dryRun)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
				}
				if !strings.Contains(response.Body.String(), "Unknown config key: "+key) {
					t.Fatalf("body does not identify rejected key: %s", response.Body.String())
				}
				if _, persisted := st.config[key]; persisted {
					t.Fatal("rejected imported rollout key was persisted")
				}
			})
		}
	}
}

func assertRemovedCodexSettingsAbsent(t *testing.T, settings map[string]string) {
	t.Helper()
	for _, key := range removedCodexRolloutConfigKeys {
		if _, exists := settings[key]; exists {
			t.Errorf("settings contain removed key %q", key)
		}
	}
}
