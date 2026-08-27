package admin

import (
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderCRUD_PreservesExplicitUsageLimitPolicy(t *testing.T) {
	handler, store, _ := testHandler()
	w := performProviderRequest(t, handler.CreateProvider, http.MethodPost, "/admin/api/providers", CreateProviderRequest{
		ID: "provider-1", Name: "Provider", UsageLimitPolicy: model.ProviderUsageLimitPolicySuspend,
		APITypes: []APITypeInput{{APIType: "codex", BaseURL: "https://api.example.com", CredentialSessionID: "session-1"}},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", w.Code, w.Body.String())
	}
	if store.providers["provider-1"].UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf("stored policy = %q", store.providers["provider-1"].UsageLimitPolicy)
	}

	policy := model.ProviderUsageLimitPolicySwitchProvider
	w = performProviderRequest(t, handler.UpdateProvider, http.MethodPut, "/admin/api/providers/provider-1", UpdateProviderRequest{UsageLimitPolicy: &policy})
	if w.Code != http.StatusOK || store.providers["provider-1"].UsageLimitPolicy != policy {
		t.Fatalf("update status = %d policy = %q", w.Code, store.providers["provider-1"].UsageLimitPolicy)
	}
}

func TestProviderCRUD_RejectsInvalidUsageLimitPolicy(t *testing.T) {
	handler, store, _ := testHandler()
	store.providers["provider-1"] = &model.Provider{
		ID: "provider-1", Name: "Provider",
		APITypes:           []model.ProviderAPIType{{ProviderID: "provider-1", APIType: "codex", BaseURL: "https://api.example.com"}},
		CredentialSessions: []credentialsession.RouteSnapshot{testConfigCredentialRoute("provider-1", "codex", "session-1", "secret")},
	}
	invalid := model.ProviderUsageLimitPolicy("invalid")
	w := performProviderRequest(t, handler.UpdateProvider, http.MethodPut, "/admin/api/providers/provider-1", UpdateProviderRequest{UsageLimitPolicy: &invalid})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
