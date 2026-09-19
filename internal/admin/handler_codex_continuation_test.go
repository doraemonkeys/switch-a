package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuation"
)

func TestProviderCodexContinuationAPI(t *testing.T) {
	h, store, _ := testHandler()
	input := CreateProviderRequest{ID: "codex-policy", Name: "Codex", AuthMode: "bearer", Vendor: "openai",
		APITypes: []APITypeInput{{APIType: "codex", BaseURL: "https://api.example.test", CredentialSessionID: "session-1"}}}
	w := performProviderRequest(t, h.CreateProvider, http.MethodPost, "/admin/api/providers", input)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	want := continuation.Policy{Outbound: continuation.Any, Inbound: continuation.None}
	if store.providers[input.ID].CodexContinuation != want {
		t.Fatal("new provider defaults not persisted")
	}
	var payload ProviderPayload
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CodexContinuation != want {
		t.Fatalf("response = %s", w.Body.String())
	}
	policy := continuation.Policy{Outbound: continuation.None, Inbound: continuation.SameIdentity}
	w = performProviderRequest(t, h.UpdateProvider, http.MethodPut, "/admin/api/providers/"+input.ID, UpdateProviderRequest{CodexContinuation: &policy})
	if w.Code != http.StatusOK || store.providers[input.ID].CodexContinuation != policy {
		t.Fatalf("update = %d %s", w.Code, w.Body.String())
	}
	name := "Renamed"
	w = performProviderRequest(t, h.UpdateProvider, http.MethodPut, "/admin/api/providers/"+input.ID, UpdateProviderRequest{Name: &name})
	if w.Code != http.StatusOK || store.providers[input.ID].CodexContinuation != policy {
		t.Fatal("omitted policy reset existing settings")
	}
	exported := buildExportedProvider(store.providers[input.ID])
	restored, ok := buildProviderFromExport(&exported, map[string]bool{})
	if !ok || restored.CodexContinuation != policy {
		t.Fatal("configuration transfer lost continuation policy")
	}
	policy.Outbound = "invalid"
	w = performProviderRequest(t, h.UpdateProvider, http.MethodPut, "/admin/api/providers/"+input.ID, UpdateProviderRequest{CodexContinuation: &policy})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid update = %d", w.Code)
	}
	input.ID = "invalid"
	input.CodexContinuation = policy
	w = performProviderRequest(t, h.CreateProvider, http.MethodPost, "/admin/api/providers", input)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid create = %d", w.Code)
	}
}
