package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"switch-a/internal/model"
)

// Batch Provider Tests

func TestBatchProviderAction_Reset(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1"}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2"}
	st.providers["p3"] = &model.Provider{ID: "p3", Name: "Provider 3"}

	body := `{"action": "reset", "ids": ["p1", "p2", "p3"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp BatchProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Affected != 3 {
		t.Errorf("affected = %d, want 3", resp.Affected)
	}
	if len(resp.Results) != 3 {
		t.Errorf("len(results) = %d, want 3", len(resp.Results))
	}
	for _, r := range resp.Results {
		if !r.Success {
			t.Errorf("result for %s should be success", r.ID)
		}
	}
}

func TestBatchProviderAction_Enable(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: false}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2", Enabled: false}

	body := `{"action": "enable", "ids": ["p1", "p2"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify providers are enabled
	if !st.providers["p1"].Enabled {
		t.Error("p1 should be enabled")
	}
	if !st.providers["p2"].Enabled {
		t.Error("p2 should be enabled")
	}
}

func TestBatchProviderAction_Disable(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2", Enabled: true}

	body := `{"action": "disable", "ids": ["p1", "p2"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify providers are disabled
	if st.providers["p1"].Enabled {
		t.Error("p1 should be disabled")
	}
	if st.providers["p2"].Enabled {
		t.Error("p2 should be disabled")
	}
}

func TestBatchProviderAction_Delete(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1"}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2"}
	st.providers["p3"] = &model.Provider{ID: "p3", Name: "Provider 3"}

	body := `{"action": "delete", "ids": ["p1", "p2", "p3"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify providers are deleted
	if _, ok := st.providers["p1"]; ok {
		t.Error("p1 should be deleted")
	}
	if _, ok := st.providers["p2"]; ok {
		t.Error("p2 should be deleted")
	}
	if _, ok := st.providers["p3"]; ok {
		t.Error("p3 should be deleted")
	}
}

func TestBatchProviderAction_PartialFailure(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1"}
	st.providers["p2"] = &model.Provider{ID: "p2", Name: "Provider 2"}
	// p3 does not exist - will fail

	body := `{"action": "reset", "ids": ["p1", "p2", "p3"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	// Should return 207 Multi-Status for partial failure
	if w.Code != http.StatusMultiStatus {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusMultiStatus, w.Body.String())
	}

	var resp BatchProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false for partial failure")
	}
	if resp.Affected != 2 {
		t.Errorf("affected = %d, want 2", resp.Affected)
	}

	// Check individual results
	resultMap := make(map[string]BatchProviderResult)
	for _, r := range resp.Results {
		resultMap[r.ID] = r
	}

	if !resultMap["p1"].Success {
		t.Error("p1 should succeed")
	}
	if !resultMap["p2"].Success {
		t.Error("p2 should succeed")
	}
	if resultMap["p3"].Success {
		t.Error("p3 should fail")
	}
	if resultMap["p3"].Error == "" {
		t.Error("p3 should have an error message")
	}
}

func TestBatchProviderAction_AllFail(t *testing.T) {
	h, _, _ := testHandler()

	// None of these providers exist
	body := `{"action": "reset", "ids": ["nonexistent1", "nonexistent2"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	// Should return 400 Bad Request when all operations fail
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp BatchProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false")
	}
	if resp.Affected != 0 {
		t.Errorf("affected = %d, want 0", resp.Affected)
	}
}

func TestBatchProviderAction_InvalidAction(t *testing.T) {
	h, _, _ := testHandler()

	body := `{"action": "invalid", "ids": ["p1"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBatchProviderAction_EmptyIDs(t *testing.T) {
	h, _, _ := testHandler()

	body := `{"action": "reset", "ids": []}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBatchProviderAction_InvalidJSON(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBatchProviderAction_DeleteWithUpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1"}
	st.deleteErr = errors.New("database error")

	body := `{"action": "delete", "ids": ["p1"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	// Should return 400 Bad Request when all operations fail
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp BatchProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false")
	}
	if resp.Affected != 0 {
		t.Errorf("affected = %d, want 0", resp.Affected)
	}
}

func TestBatchProviderAction_EnableWithUpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: false}
	st.updateErr = errors.New("database error")

	body := `{"action": "enable", "ids": ["p1"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	var resp BatchProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false")
	}
	if len(resp.Results) != 1 || resp.Results[0].Error != "failed to enable provider: p1" {
		t.Errorf("expected error message 'failed to enable provider: p1', got: %v", resp.Results)
	}
}

func TestBatchProviderAction_DisableWithUpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1", Enabled: true}
	st.updateErr = errors.New("database error")

	body := `{"action": "disable", "ids": ["p1"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	var resp BatchProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false")
	}
	if len(resp.Results) != 1 || resp.Results[0].Error != "failed to disable provider: p1" {
		t.Errorf("expected error message 'failed to disable provider: p1', got: %v", resp.Results)
	}
}

func TestBatchProviderAction_ResetWithHealthError(t *testing.T) {
	h, st, health := testHandler()

	st.providers["p1"] = &model.Provider{ID: "p1", Name: "Provider 1"}
	health.enableErr = errors.New("health manager error")

	body := `{"action": "reset", "ids": ["p1"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.BatchProviderAction(w, req)

	var resp BatchProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false")
	}
	if len(resp.Results) != 1 || resp.Results[0].Error != "failed to reset provider: p1" {
		t.Errorf("expected error message 'failed to reset provider: p1', got: %v", resp.Results)
	}
}
