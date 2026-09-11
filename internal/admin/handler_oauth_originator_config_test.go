package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
)

func TestCodexOAuthOriginatorSettingsRoundTrip(t *testing.T) {
	key := defaults.ConfigKeyCodexOAuthOriginator
	handler, repository := newCredentialSessionHandler(t)
	initial := httptest.NewRecorder()
	handler.GetConfig(initial, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	var config ConfigResponse
	if initial.Code != http.StatusOK {
		t.Fatal(initial.Code, initial.Body.String())
	}
	if err := json.NewDecoder(initial.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	if config.Defaults[key] != "codex_cli_rs" {
		t.Fatalf("OAuth originator default = %q", config.Defaults[key])
	}
	if value, err := repository.GetConfig(context.Background(), key); err != nil || value != "codex_cli_rs" {
		t.Fatal(value, err)
	}

	for _, value := range []string{"codex_vscode", "My Codex Client/1.0 + Work", ""} {
		t.Run("originator="+value, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{key: value})
			if err != nil {
				t.Fatal(err)
			}
			update := httptest.NewRecorder()
			handler.UpdateConfig(update, httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body)))
			if update.Code != http.StatusOK {
				t.Fatal(update.Code, update.Body.String())
			}
			var updated ConfigResponse
			if err := json.NewDecoder(update.Body).Decode(&updated); err != nil {
				t.Fatal(err)
			}
			if updated.Values[key] != value || updated.Defaults[key] != "codex_cli_rs" {
				t.Fatal(updated)
			}
			if stored, err := repository.GetConfig(context.Background(), key); err != nil || stored != value {
				t.Fatal(stored, err)
			}
			refreshed := httptest.NewRecorder()
			handler.GetConfig(refreshed, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
			if err := json.NewDecoder(refreshed.Body).Decode(&updated); err != nil || updated.Values[key] != value {
				t.Fatal(updated, err)
			}

			export := httptest.NewRecorder()
			handler.ExportConfig(export, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
			if export.Code != http.StatusOK {
				t.Fatal(export.Code, export.Body.String())
			}
			var exported ExportedConfig
			if err := json.NewDecoder(export.Body).Decode(&exported); err != nil {
				t.Fatal(err)
			}
			if stored, ok := exported.Settings[key]; !ok || stored != value {
				t.Fatal(exported.Settings)
			}
			target, targetStore := newCredentialSessionHandler(t)
			result := performConfigImport(t, target, ImportConfigRequest{
				Version:     ConfigExportVersion,
				ImportScope: &ConfigImportScope{Mode: ConfigImportModeSettingsOnly},
				Settings:    exported.Settings,
			}, false)
			if result.Code != http.StatusOK {
				t.Fatal(result.Code, result.Body.String())
			}
			if restored, err := targetStore.GetConfig(context.Background(), key); err != nil || restored != value {
				t.Fatal(restored, err)
			}
		})
	}
}
