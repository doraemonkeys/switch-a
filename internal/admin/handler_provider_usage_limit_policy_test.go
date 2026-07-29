package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestCreateProvider_WithUsageLimitPolicyPersistsAndSerializes(t *testing.T) {
	t.Parallel()

	h, st, _ := testHandler()

	body := `{
		"id": "relay-provider",
		"name": "Relay Provider",
		"api_key": "relay-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}],
		"usage_limit_policy": "suspend"
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	created := st.providers["relay-provider"]
	if created == nil {
		t.Fatal("provider was not created in store")
	}
	if created.UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf("stored UsageLimitPolicy = %q, want %q", created.UsageLimitPolicy, model.ProviderUsageLimitPolicySuspend)
	}

	var response ProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf("response UsageLimitPolicy = %q, want %q", response.UsageLimitPolicy, model.ProviderUsageLimitPolicySuspend)
	}
	if !response.UsageLimitPolicyExplicit {
		t.Fatal("response UsageLimitPolicyExplicit = false, want true")
	}
}

func TestUpdateProvider_WithUsageLimitPolicyPersistsAndSerializes(t *testing.T) {
	t.Parallel()

	h, st, _ := testHandler()
	st.providers["policy-target"] = &model.Provider{
		ID:               "policy-target",
		Name:             "Target",
		APIKey:           "key",
		APITypes:         defaultProviderAPITypes("policy-target"),
		CredentialType:   model.ProviderCredentialTypeAPIKey,
		UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
		Weight:           1,
		Priority:         0,
	}

	body := `{"usage_limit_policy": "suspend"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/policy-target", bytes.NewBufferString(body))
	setPathValue(req, "id", "policy-target")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["policy-target"]
	if updated.UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf("stored UsageLimitPolicy = %q, want %q", updated.UsageLimitPolicy, model.ProviderUsageLimitPolicySuspend)
	}

	var response ProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UsageLimitPolicy != model.ProviderUsageLimitPolicySuspend {
		t.Fatalf("response UsageLimitPolicy = %q, want %q", response.UsageLimitPolicy, model.ProviderUsageLimitPolicySuspend)
	}
	if !response.UsageLimitPolicyExplicit {
		t.Fatal("response UsageLimitPolicyExplicit = false, want true")
	}
}

func TestCreateProvider_InvalidUsageLimitPolicyRejected(t *testing.T) {
	t.Parallel()

	h, _, _ := testHandler()

	body := `{
		"id": "bad-policy",
		"name": "Bad Policy",
		"api_key": "relay-key",
		"api_types": [{"api_type": "claude", "base_url": "https://api.example.com"}],
		"usage_limit_policy": "drop"
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "usage_limit_policy") {
		t.Fatalf("body = %q, want usage_limit_policy validation error", w.Body.String())
	}
}

func TestUpdateProvider_CredentialTypeRecomputesImplicitUsageLimitPolicyDefault(t *testing.T) {
	t.Parallel()

	h, st, _ := testHandler()
	st.providers["policy-default"] = &model.Provider{
		ID:               "policy-default",
		Name:             "Policy Default",
		APIKey:           "key",
		APITypes:         defaultProviderAPITypes("policy-default"),
		CredentialType:   model.ProviderCredentialTypeChatGPT,
		UsageLimitPolicy: "",
		Weight:           1,
		Priority:         0,
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/policy-default", bytes.NewBufferString(`{"credential_type":"api_key"}`))
	setPathValue(req, "id", "policy-default")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["policy-default"]
	if updated.UsageLimitPolicy != "" {
		t.Fatalf("stored UsageLimitPolicy = %q, want empty inherit-default value", updated.UsageLimitPolicy)
	}
	if updated.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("effective UsageLimitPolicy = %q, want %q", updated.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySwitchProvider)
	}

	var response ProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UsageLimitPolicy != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("response UsageLimitPolicy = %q, want %q", response.UsageLimitPolicy, model.ProviderUsageLimitPolicySwitchProvider)
	}
	if response.UsageLimitPolicyExplicit {
		t.Fatal("response UsageLimitPolicyExplicit = true, want false")
	}
}

func TestUpdateProvider_CredentialTypePreservesExplicitUsageLimitPolicyOverride(t *testing.T) {
	t.Parallel()

	h, st, _ := testHandler()
	st.providers["policy-explicit"] = &model.Provider{
		ID:               "policy-explicit",
		Name:             "Policy Explicit",
		APIKey:           "key",
		APITypes:         defaultProviderAPITypes("policy-explicit"),
		CredentialType:   model.ProviderCredentialTypeChatGPT,
		UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
		Weight:           1,
		Priority:         0,
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/providers/policy-explicit", bytes.NewBufferString(`{"credential_type":"api_key"}`))
	setPathValue(req, "id", "policy-explicit")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.providers["policy-explicit"]
	if updated.UsageLimitPolicy != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("stored UsageLimitPolicy = %q, want %q", updated.UsageLimitPolicy, model.ProviderUsageLimitPolicySwitchProvider)
	}
	if updated.UsageLimitPolicyOrDefault() != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("effective UsageLimitPolicy = %q, want %q", updated.UsageLimitPolicyOrDefault(), model.ProviderUsageLimitPolicySwitchProvider)
	}

	var response ProviderResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UsageLimitPolicy != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("response UsageLimitPolicy = %q, want %q", response.UsageLimitPolicy, model.ProviderUsageLimitPolicySwitchProvider)
	}
	if !response.UsageLimitPolicyExplicit {
		t.Fatal("response UsageLimitPolicyExplicit = false, want true")
	}
}
