package admin

import (
	"bytes"
	"encoding/json"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRetiredConversationRecoveryConfigRejected(t *testing.T) {
	key := defaults.ConfigKeyConversationRecoveryPolicy
	for _, value := range []string{"", "preserve_conversation", "switch_account_preserve_conversation", "invalid"} {
		h, st, _ := testHandler()
		body, err := json.Marshal(map[string]string{key: value})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		h.UpdateConfig(response, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("update = %d", response.Code)
		}
		for _, dryRun := range []bool{false, true} {
			result := performConfigImport(t, h, ImportConfigRequest{Version: ConfigExportVersion, ImportScope: &ConfigImportScope{Mode: ConfigImportModeSettingsOnly}, Settings: map[string]string{key: value}}, dryRun)
			if result.Code != http.StatusBadRequest {
				t.Fatalf("import = %d", result.Code)
			}
		}
		if _, exists := st.config[key]; exists {
			t.Fatal("retired setting persisted")
		}
	}
}
