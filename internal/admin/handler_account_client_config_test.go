package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/providerauth/accountclient"
)

func TestGPTAccountClientConfigRoundTrip(t *testing.T) {
	key := defaults.ConfigKeyGPTAccountFallbackClient
	for _, mode := range []accountclient.FallbackClient{accountclient.FallbackSwitchA, accountclient.FallbackOfficialStable} {
		t.Run(string(mode), func(t *testing.T) {
			h, _, _ := testHandler()
			body, err := json.Marshal(map[string]string{key: string(mode)})
			if err != nil {
				t.Fatal(err)
			}
			update := httptest.NewRecorder()
			h.UpdateConfig(update, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
			if update.Code != http.StatusOK {
				t.Fatal(update.Code, update.Body.String())
			}
			var config ConfigResponse
			if err := json.NewDecoder(update.Body).Decode(&config); err != nil {
				t.Fatal(err)
			}
			if config.Values[key] != string(mode) || config.Defaults[key] != defaults.DefaultGPTAccountFallbackClient {
				t.Fatal(config)
			}
			export := httptest.NewRecorder()
			h.ExportConfig(export, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
			var exported ExportedConfig
			if err := json.NewDecoder(export.Body).Decode(&exported); err != nil {
				t.Fatal(err)
			}
			if exported.Settings[key] != string(mode) {
				t.Fatal(exported.Settings)
			}
			target, st, _ := testHandler()
			result := performConfigImport(t, target, ImportConfigRequest{
				Version: ConfigExportVersion, ImportScope: &ConfigImportScope{Mode: ConfigImportModeSettingsOnly},
				Settings: exported.Settings,
			}, false)
			if result.Code != http.StatusOK || st.config[key] != string(mode) {
				t.Fatal(result.Code, result.Body.String(), st.config)
			}
		})
	}
}

func TestGPTAccountClientConfigRejectsInvalidMode(t *testing.T) {
	key := defaults.ConfigKeyGPTAccountFallbackClient
	for _, value := range []string{"", "true", "random", " official_stable"} {
		t.Run(value, func(t *testing.T) {
			h, st, _ := testHandler()
			body, err := json.Marshal(map[string]string{key: value})
			if err != nil {
				t.Fatal(err)
			}
			update := httptest.NewRecorder()
			h.UpdateConfig(update, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
			if update.Code != http.StatusBadRequest {
				t.Fatal(update.Code, update.Body.String())
			}
			for _, dryRun := range []bool{true, false} {
				result := performConfigImport(t, h, ImportConfigRequest{
					Version: ConfigExportVersion, ImportScope: &ConfigImportScope{Mode: ConfigImportModeSettingsOnly},
					Settings: map[string]string{key: value},
				}, dryRun)
				if result.Code != http.StatusBadRequest {
					t.Fatal(result.Code, result.Body.String())
				}
			}
			if _, exists := st.config[key]; exists {
				t.Fatal("invalid mode persisted")
			}
		})
	}
}
