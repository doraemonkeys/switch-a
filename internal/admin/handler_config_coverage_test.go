package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"switch-a/internal/model"
)

func TestUpdateConfig_TooManyUpdates(t *testing.T) {
	t.Parallel()

	h, _, _ := testHandler()
	updates := make(map[string]string, MaxConfigUpdates+1)
	for i := 0; i < MaxConfigUpdates+1; i++ {
		updates["key-"+strconv.Itoa(i)] = "value"
	}
	body, err := json.Marshal(updates)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	h.UpdateConfig(resp, req)
	assertConfigErrorCode(t, resp, http.StatusBadRequest, ErrCodeValidation)
}

func TestUpdateConfig_InvalidKey(t *testing.T) {
	t.Parallel()

	h, _, _ := testHandler()
	req := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewBufferString(`{
		"invalid.key": "value"
	}`))
	resp := httptest.NewRecorder()

	h.UpdateConfig(resp, req)
	assertConfigErrorCode(t, resp, http.StatusBadRequest, ErrCodeValidation)
}

func assertConfigErrorCode(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()

	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d", w.Code, wantStatus)
	}

	var errResp model.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Code != wantCode {
		t.Fatalf("error code = %q, want %q", errResp.Code, wantCode)
	}
}
