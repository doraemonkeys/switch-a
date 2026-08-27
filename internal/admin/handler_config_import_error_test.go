package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImportConfig_InvalidJSON(t *testing.T) {
	handler, _, _ := testHandler()
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewBufferString("invalid"))
	request.Header.Set("Content-Type", "application/json")
	handler.ImportConfig(w, request)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestImportConfig_RejectsLegacyVersion(t *testing.T) {
	handler, _, _ := testHandler()
	body, err := json.Marshal(ImportConfigRequest{Version: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ImportConfig(w, request)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), ConfigExportVersion) {
		t.Fatalf("status = %d body = %q", w.Code, w.Body.String())
	}
}
