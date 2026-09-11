package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
)

func TestWebSocketMessageSizeConfig(t *testing.T) {
	key := defaults.ConfigKeyWebSocketMaxMessageSizeMiB
	h, st, _ := testHandler()
	get := httptest.NewRecorder()
	h.GetConfig(get, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	var initial ConfigResponse
	if err := json.Unmarshal(get.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Defaults[key] != "128" {
		t.Fatalf("default = %q, want 128", initial.Defaults[key])
	}

	for _, value := range []string{"1", "128", "256", strconv.FormatInt(defaults.MaxWebSocketMessageSizeMiB, 10)} {
		body, err := json.Marshal(map[string]string{key: value})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		h.UpdateConfig(response, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
		if response.Code != http.StatusOK || st.config[key] != value {
			t.Fatalf("update %s: status=%d, persisted=%q, body=%s", value, response.Code, st.config[key], response.Body.String())
		}
	}
	saved := st.config[key]
	for _, value := range []string{"", "0", "-1", "1.5", "128M", "invalid", "9223372036854775808", strconv.FormatInt(defaults.MaxWebSocketMessageSizeMiB+1, 10)} {
		t.Run(value, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{key: value})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			h.UpdateConfig(response, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
			if response.Code != http.StatusBadRequest || st.config[key] != saved {
				t.Fatalf("invalid update: status=%d, persisted=%q", response.Code, st.config[key])
			}
		})
	}
}

func TestWebSocketMessageSizeConfigTransfer(t *testing.T) {
	key := defaults.ConfigKeyWebSocketMaxMessageSizeMiB
	h, st, _ := testHandler()
	st.config[key] = "256"
	response := httptest.NewRecorder()
	h.ExportConfig(response, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	var exported ExportedConfig
	if err := json.Unmarshal(response.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || exported.Settings[key] != "256" {
		t.Fatalf("export: status=%d, settings=%v", response.Code, exported.Settings)
	}
	for _, value := range []string{"64", "0"} {
		for _, dryRun := range []bool{true, false} {
			response := performConfigImport(t, h, ImportConfigRequest{
				Version:     ConfigExportVersion,
				ImportScope: &ConfigImportScope{Mode: ConfigImportModeSettingsOnly},
				Settings:    map[string]string{key: value},
			}, dryRun)
			want := http.StatusOK
			if value == "0" {
				want = http.StatusBadRequest
			}
			if response.Code != want {
				t.Fatalf("import %s dryRun=%v: status=%d, body=%s", value, dryRun, response.Code, response.Body.String())
			}
		}
	}
	if st.config[key] != "64" {
		t.Fatalf("imported setting = %q, want 64", st.config[key])
	}
}
