package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestConversationRecoveryConfigRoundTrip(t *testing.T) {
	key := defaults.ConfigKeyConversationRecoveryPolicy
	for _, policy := range []model.ConversationRecoveryPolicy{
		model.ConversationRecoveryPreserveConversation,
		model.ConversationRecoverySwitchAccountPreserveConversation,
	} {
		t.Run(string(policy), func(t *testing.T) {
			h, _, _ := testHandler()
			body, err := json.Marshal(map[string]string{key: string(policy)})
			if err != nil {
				t.Fatal(err)
			}
			update := httptest.NewRecorder()
			h.UpdateConfig(update, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
			if update.Code != http.StatusOK {
				t.Fatalf("update: %d %s", update.Code, update.Body.String())
			}

			get := httptest.NewRecorder()
			h.GetConfig(get, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
			var config ConfigResponse
			if err := json.NewDecoder(get.Body).Decode(&config); err != nil {
				t.Fatal(err)
			}
			if config.Values[key] != string(policy) || config.Defaults[key] != defaults.DefaultConversationRecoveryPolicy {
				t.Fatalf("GET config = %#v", config)
			}

			export := httptest.NewRecorder()
			h.ExportConfig(export, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
			var exported ExportedConfig
			if err := json.NewDecoder(export.Body).Decode(&exported); err != nil {
				t.Fatal(err)
			}
			if exported.Settings[key] != string(policy) {
				t.Fatalf("export settings = %#v", exported.Settings)
			}

			destination, st, _ := testHandler()
			result := performConfigImport(t, destination, ImportConfigRequest{
				Version:     ConfigExportVersion,
				ImportScope: &ConfigImportScope{Mode: ConfigImportModeSettingsOnly},
				Settings:    exported.Settings,
			}, false)
			if result.Code != http.StatusOK {
				t.Fatalf("import: %d %s", result.Code, result.Body.String())
			}
			if st.config[key] != string(policy) {
				t.Fatalf("import persisted %q", st.config[key])
			}
		})
	}
}

func TestConversationRecoveryConfigRejectsInvalidValues(t *testing.T) {
	key := defaults.ConfigKeyConversationRecoveryPolicy
	for _, value := range []string{"", "client_decides", "unknown", " preserve_conversation"} {
		t.Run(value, func(t *testing.T) {
			h, st, _ := testHandler()
			body, err := json.Marshal(map[string]string{key: value})
			if err != nil {
				t.Fatal(err)
			}
			update := httptest.NewRecorder()
			h.UpdateConfig(update, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
			if update.Code != http.StatusBadRequest {
				t.Fatalf("update: %d %s", update.Code, update.Body.String())
			}
			for _, dryRun := range []bool{true, false} {
				result := performConfigImport(t, h, ImportConfigRequest{
					Version:     ConfigExportVersion,
					ImportScope: &ConfigImportScope{Mode: ConfigImportModeSettingsOnly},
					Settings:    map[string]string{key: value},
				}, dryRun)
				if result.Code != http.StatusBadRequest {
					t.Fatalf("import: %d %s", result.Code, result.Body.String())
				}
			}
			if _, exists := st.config[key]; exists {
				t.Fatal("invalid policy was persisted")
			}
		})
	}
}
