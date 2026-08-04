package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalErrorRuleDelegatesFailClosedUntilCompositionInjectsHandler(t *testing.T) {
	handler := &Handler{}
	delegates := map[string]http.HandlerFunc{
		"list":         handler.ListInternalErrorRules,
		"get":          handler.GetInternalErrorRule,
		"create":       handler.CreateInternalErrorRule,
		"update":       handler.UpdateInternalErrorRule,
		"delete":       handler.DeleteInternalErrorRule,
		"reorder":      handler.ReorderInternalErrorRules,
		"stats":        handler.GetInternalErrorRuleStats,
		"test message": handler.TestInternalErrorMessage,
	}
	for name, delegate := range delegates {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			delegate(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
			}
			var response struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Code != "INTERNAL_ERROR" || response.Details == nil {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}
