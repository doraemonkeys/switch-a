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

func TestImportConfig_SelectionScopeImportsExactProvidersAndPreservesRoutingPolicies(t *testing.T) {
	h, st, _ := testHandler()

	selectedGroupID := "group-selected"
	unselectedGroupID := "group-unselected"
	exactProviderID := "provider-exact"
	st.groups[selectedGroupID] = &model.Group{
		ID:       selectedGroupID,
		Name:     "Old Group",
		Strategy: DefaultStrategy,
		Weight:   DefaultWeight,
		Enabled:  true,
	}
	st.providers["provider-selected"] = storedTestProviderWithGroup(
		"provider-selected",
		"Selected Provider",
		"codex",
		"https://selected.old.example",
		selectedGroupID,
	)
	st.providers["provider-sibling"] = storedTestProviderWithGroup(
		"provider-sibling",
		"Sibling Provider",
		"claude",
		"https://sibling.old.example",
		selectedGroupID,
	)
	st.providers[exactProviderID] = storedTestProvider(
		exactProviderID,
		"Exact Provider",
		"claude",
		"https://exact.old.example",
	)
	st.routingPolicies[1] = &model.RoutingPolicy{
		ID:               1,
		APIType:          "claude",
		Enabled:          true,
		TargetProviderID: &exactProviderID,
	}
	st.config[configStickyModeKey] = "off"

	importReq := ImportConfigRequest{
		Version:            ConfigExportVersion,
		ImportScope:        selectionConfigImportScope(nil, []string{"provider-selected", "provider-selected"}),
		CredentialSessions: []ExportedCredentialSession{importedTestSession("provider-selected-session", "selected"), importedTestSession("provider-sibling-session", "sibling"), importedTestSession("provider-unselected-session", "unselected")},
		Groups: []ExportedGroup{
			{
				ID:       selectedGroupID,
				Name:     "New Group",
				Strategy: DefaultStrategy,
				Weight:   DefaultWeight,
				Enabled:  true,
			},
			{
				ID:       unselectedGroupID,
				Name:     "Ignored Group",
				Strategy: DefaultStrategy,
				Weight:   DefaultWeight,
				Enabled:  true,
			},
		},
		Providers: []ExportedProvider{
			scopeTestProvider("provider-selected", "Selected Provider", "codex", "https://selected.new.example", &selectedGroupID),
			scopeTestProvider("provider-sibling", "Sibling Provider", "claude", "https://sibling.new.example", &selectedGroupID),
			scopeTestProvider("provider-unselected", "Ignored Provider", "codex", "https://ignored.example", &unselectedGroupID),
		},
		RoutingPolicies: []ExportedRoutingPolicy{
			{
				APIType:        "codex",
				Enabled:        true,
				AllowedVendors: []string{"openai"},
			},
		},
		Settings: map[string]string{
			configStickyModeKey: "api_type",
		},
	}

	preview := previewConfigImportRequest(t, h, importReq)
	if len(preview.Warnings) != 0 {
		t.Fatalf("preview warnings = %v, want none", preview.Warnings)
	}
	if preview.Changes.Groups.Update != 1 {
		t.Fatalf("preview groups.update = %d, want 1", preview.Changes.Groups.Update)
	}
	if preview.Changes.Providers.Update != 1 {
		t.Fatalf("preview providers.update = %d, want 1", preview.Changes.Providers.Update)
	}
	if preview.Changes.RoutingPolicies != (ChangeCount{}) {
		t.Fatalf("preview routing_policies = %+v, want zero", preview.Changes.RoutingPolicies)
	}
	if preview.Changes.Settings != (ChangeCount{}) {
		t.Fatalf("preview settings = %+v, want zero", preview.Changes.Settings)
	}

	result, statusCode, body := applyConfigImportRequest(t, h, importReq)
	if statusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", statusCode, http.StatusOK, body)
	}
	if result.Applied.Groups.Updated != 1 {
		t.Fatalf("applied groups.updated = %d, want 1", result.Applied.Groups.Updated)
	}
	if result.Applied.Providers.Updated != 1 {
		t.Fatalf("applied providers.updated = %d, want 1", result.Applied.Providers.Updated)
	}
	if result.Applied.RoutingPolicies != (AppliedCount{}) {
		t.Fatalf("applied routing_policies = %+v, want zero", result.Applied.RoutingPolicies)
	}
	if result.Applied.Settings != (AppliedCount{}) {
		t.Fatalf("applied settings = %+v, want zero", result.Applied.Settings)
	}

	if got := st.groups[selectedGroupID].Name; got != "New Group" {
		t.Fatalf("group name = %q, want New Group", got)
	}
	if got := st.providers["provider-selected"].APITypes[0].BaseURL; got != "https://selected.new.example" {
		t.Fatalf("selected provider base_url = %q, want new URL", got)
	}
	if got := st.providers["provider-sibling"].APITypes[0].BaseURL; got != "https://sibling.old.example" {
		t.Fatalf("sibling provider base_url = %q, want unchanged old URL", got)
	}
	if _, exists := st.providers["provider-unselected"]; exists {
		t.Fatal("provider-unselected was imported, want it excluded")
	}
	if got := st.config[configStickyModeKey]; got != "off" {
		t.Fatalf("sticky_mode = %q, want off", got)
	}
	if len(st.routingPolicies) != 1 {
		t.Fatalf("len(routingPolicies) = %d, want 1", len(st.routingPolicies))
	}
	if policy := st.routingPolicies[1]; policy == nil || policy.TargetProviderID == nil || *policy.TargetProviderID != exactProviderID {
		t.Fatalf("routing policy = %#v, want preserved exact provider rule", st.routingPolicies[1])
	}
}

func TestImportConfig_SelectionScopeGroupImportsProvidersInThatGroup(t *testing.T) {
	h, st, _ := testHandler()

	selectedGroupID := "group-selected"
	unselectedGroupID := "group-unselected"
	st.groups[selectedGroupID] = &model.Group{
		ID:       selectedGroupID,
		Name:     "Old Group",
		Strategy: DefaultStrategy,
		Weight:   DefaultWeight,
		Enabled:  true,
	}
	st.providers["provider-selected"] = storedTestProviderWithGroup(
		"provider-selected",
		"Selected Provider",
		"codex",
		"https://selected.old.example",
		selectedGroupID,
	)
	st.providers["provider-sibling"] = storedTestProviderWithGroup(
		"provider-sibling",
		"Sibling Provider",
		"claude",
		"https://sibling.old.example",
		selectedGroupID,
	)
	st.providers["provider-unselected"] = storedTestProviderWithGroup(
		"provider-unselected",
		"Ignored Provider",
		"codex",
		"https://ignored.old.example",
		unselectedGroupID,
	)
	st.config[configStickyModeKey] = "off"

	importReq := ImportConfigRequest{
		Version:            ConfigExportVersion,
		ImportScope:        selectionConfigImportScope([]string{selectedGroupID, selectedGroupID}, nil),
		CredentialSessions: []ExportedCredentialSession{importedTestSession("provider-selected-session", "selected"), importedTestSession("provider-sibling-session", "sibling"), importedTestSession("provider-unselected-session", "unselected")},
		Groups: []ExportedGroup{
			{
				ID:       selectedGroupID,
				Name:     "New Group",
				Strategy: DefaultStrategy,
				Weight:   DefaultWeight,
				Enabled:  true,
			},
			{
				ID:       unselectedGroupID,
				Name:     "Ignored Group",
				Strategy: DefaultStrategy,
				Weight:   DefaultWeight,
				Enabled:  true,
			},
		},
		Providers: []ExportedProvider{
			scopeTestProvider("provider-selected", "Selected Provider", "codex", "https://selected.new.example", &selectedGroupID),
			scopeTestProvider("provider-sibling", "Sibling Provider", "claude", "https://sibling.new.example", &selectedGroupID),
			scopeTestProvider("provider-unselected", "Ignored Provider", "codex", "https://ignored.new.example", &unselectedGroupID),
		},
		Settings: map[string]string{
			configStickyModeKey: "api_type",
		},
	}

	preview := previewConfigImportRequest(t, h, importReq)
	if len(preview.Warnings) != 0 {
		t.Fatalf("preview warnings = %v, want none", preview.Warnings)
	}
	if preview.Changes.Groups.Update != 1 {
		t.Fatalf("preview groups.update = %d, want 1", preview.Changes.Groups.Update)
	}
	if preview.Changes.Providers.Update != 2 {
		t.Fatalf("preview providers.update = %d, want 2", preview.Changes.Providers.Update)
	}

	result, statusCode, body := applyConfigImportRequest(t, h, importReq)
	if statusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", statusCode, http.StatusOK, body)
	}
	if result.Applied.Groups.Updated != 1 {
		t.Fatalf("applied groups.updated = %d, want 1", result.Applied.Groups.Updated)
	}
	if result.Applied.Providers.Updated != 2 {
		t.Fatalf("applied providers.updated = %d, want 2", result.Applied.Providers.Updated)
	}
	if got := st.groups[selectedGroupID].Name; got != "New Group" {
		t.Fatalf("group name = %q, want New Group", got)
	}
	if got := st.providers["provider-selected"].APITypes[0].BaseURL; got != "https://selected.new.example" {
		t.Fatalf("selected provider base_url = %q, want new URL", got)
	}
	if got := st.providers["provider-sibling"].APITypes[0].BaseURL; got != "https://sibling.new.example" {
		t.Fatalf("sibling provider base_url = %q, want new URL", got)
	}
	if got := st.providers["provider-unselected"].APITypes[0].BaseURL; got != "https://ignored.old.example" {
		t.Fatalf("unselected provider base_url = %q, want unchanged old URL", got)
	}
	if got := st.config[configStickyModeKey]; got != "off" {
		t.Fatalf("sticky_mode = %q, want off", got)
	}
}

func TestImportConfig_SelectionScopeProviderMissingParentGroupReportsSingleDependencyWarning(t *testing.T) {
	h, st, _ := testHandler()
	st.config[configStickyModeKey] = "off"

	missingGroupID := "group-missing"
	importReq := ImportConfigRequest{
		Version:            ConfigExportVersion,
		ImportScope:        selectionConfigImportScope(nil, []string{"provider-selected"}),
		CredentialSessions: []ExportedCredentialSession{importedTestSession("provider-selected-session", "selected")},
		Providers: []ExportedProvider{
			scopeTestProvider(
				"provider-selected",
				"Selected Provider",
				"codex",
				"https://selected.example",
				&missingGroupID,
			),
		},
	}

	body, err := json.Marshal(importReq)
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}

	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	previewReq.Header.Set("Content-Type", "application/json")
	previewW := httptest.NewRecorder()
	h.ImportConfig(previewW, previewReq)

	if previewW.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d, want %d; body: %s", previewW.Code, http.StatusBadRequest, previewW.Body.String())
	}
	previewBody := decodeImportErrorMessage(t, previewW.Body.String())
	if !strings.Contains(
		previewBody,
		`Selected provider "provider-selected" references group "group-missing", but that group is missing from the import file`,
	) {
		t.Fatalf("preview body = %q, want provider dependency warning", previewBody)
	}
	if strings.Contains(previewBody, `Selected group "group-missing" was not found in the import file`) {
		t.Fatalf("preview body = %q, want no derived group warning", previewBody)
	}
	if strings.Contains(previewBody, `Provider 'provider-selected' references non-existent group 'group-missing'`) {
		t.Fatalf("preview body = %q, want no duplicate provider-group warning", previewBody)
	}

	_, statusCode, responseBody := applyConfigImportRequest(t, h, importReq)
	if statusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", statusCode, http.StatusBadRequest, responseBody)
	}
	responseBody = decodeImportErrorMessage(t, responseBody)
	if !strings.Contains(
		responseBody,
		`Selected provider "provider-selected" references group "group-missing", but that group is missing from the import file`,
	) {
		t.Fatalf("response body = %q, want provider dependency warning", responseBody)
	}
	if strings.Contains(responseBody, `Selected group "group-missing" was not found in the import file`) {
		t.Fatalf("response body = %q, want no derived group warning", responseBody)
	}
	if strings.Contains(responseBody, `Provider 'provider-selected' references non-existent group 'group-missing'`) {
		t.Fatalf("response body = %q, want no duplicate provider-group warning", responseBody)
	}
}

func TestImportConfig_SettingsOnlyScopeAppliesOnlySettings(t *testing.T) {
	h, st, _ := testHandler()

	groupID := "group-keep"
	providerID := "provider-keep"
	st.groups[groupID] = &model.Group{
		ID:       groupID,
		Name:     "Existing Group",
		Strategy: DefaultStrategy,
		Weight:   DefaultWeight,
		Enabled:  true,
	}
	st.providers[providerID] = storedTestProviderWithGroup(
		providerID,
		"Existing Provider",
		"codex",
		"https://provider.old.example",
		groupID,
	)
	st.routingPolicies[1] = &model.RoutingPolicy{
		ID:      1,
		APIType: "codex",
		Enabled: true,
	}
	st.config[configStickyModeKey] = "off"

	importReq := ImportConfigRequest{
		Version: ConfigExportVersion,
		ImportScope: &ConfigImportScope{
			Mode: ConfigImportModeSettingsOnly,
		},
		Groups: []ExportedGroup{
			{
				ID:       groupID,
				Name:     "Updated Group",
				Strategy: DefaultStrategy,
				Weight:   DefaultWeight,
				Enabled:  true,
			},
		},
		Providers: []ExportedProvider{
			scopeTestProvider(providerID, "Updated Provider", "claude", "https://provider.new.example", &groupID),
		},
		RoutingPolicies: []ExportedRoutingPolicy{
			{
				APIType:        "claude",
				Enabled:        false,
				AllowedVendors: []string{"anthropic"},
			},
		},
		Settings: map[string]string{
			configStickyModeKey:        "api_type",
			configGlobalMaxAttemptsKey: "4",
		},
	}

	preview := previewConfigImportRequest(t, h, importReq)
	if len(preview.Warnings) != 0 {
		t.Fatalf("preview warnings = %v, want none", preview.Warnings)
	}
	if preview.Changes.Settings.Update != 1 || preview.Changes.Settings.Add != 1 {
		t.Fatalf("preview settings = %+v, want add=1 update=1", preview.Changes.Settings)
	}
	if preview.Changes.Groups != (ChangeCount{}) {
		t.Fatalf("preview groups = %+v, want zero", preview.Changes.Groups)
	}
	if preview.Changes.Providers != (ChangeCount{}) {
		t.Fatalf("preview providers = %+v, want zero", preview.Changes.Providers)
	}
	if preview.Changes.RoutingPolicies != (ChangeCount{}) {
		t.Fatalf("preview routing_policies = %+v, want zero", preview.Changes.RoutingPolicies)
	}

	result, statusCode, body := applyConfigImportRequest(t, h, importReq)
	if statusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", statusCode, http.StatusOK, body)
	}
	if result.Applied.Settings.Updated != 1 || result.Applied.Settings.Added != 1 {
		t.Fatalf("applied settings = %+v, want add=1 update=1", result.Applied.Settings)
	}
	if result.Applied.Groups != (AppliedCount{}) {
		t.Fatalf("applied groups = %+v, want zero", result.Applied.Groups)
	}
	if result.Applied.Providers != (AppliedCount{}) {
		t.Fatalf("applied providers = %+v, want zero", result.Applied.Providers)
	}
	if result.Applied.RoutingPolicies != (AppliedCount{}) {
		t.Fatalf("applied routing_policies = %+v, want zero", result.Applied.RoutingPolicies)
	}

	if got := st.groups[groupID].Name; got != "Existing Group" {
		t.Fatalf("group name = %q, want unchanged", got)
	}
	if got := st.providers[providerID].APITypes[0].BaseURL; got != "https://provider.old.example" {
		t.Fatalf("provider base_url = %q, want unchanged", got)
	}
	if len(st.routingPolicies) != 1 || st.routingPolicies[1].APIType != "codex" {
		t.Fatalf("routingPolicies = %#v, want unchanged codex rule", st.routingPolicies)
	}
	if got := st.config[configStickyModeKey]; got != "api_type" {
		t.Fatalf("sticky_mode = %q, want api_type", got)
	}
	if got := st.config[configGlobalMaxAttemptsKey]; got != "4" {
		t.Fatalf("global_max_attempts = %q, want 4", got)
	}
}

func TestImportConfig_SelectionScopeRejectsMissingSelections(t *testing.T) {
	h, st, _ := testHandler()
	st.config[configStickyModeKey] = "off"

	importReq := ImportConfigRequest{
		Version:     ConfigExportVersion,
		ImportScope: selectionConfigImportScope([]string{"group-missing"}, []string{"provider-missing"}),
	}

	body, err := json.Marshal(importReq)
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}

	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	previewReq.Header.Set("Content-Type", "application/json")
	previewW := httptest.NewRecorder()
	h.ImportConfig(previewW, previewReq)

	if previewW.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d, want %d; body: %s", previewW.Code, http.StatusBadRequest, previewW.Body.String())
	}
	joinedWarnings := previewW.Body.String()
	if !strings.Contains(joinedWarnings, "group-missing") {
		t.Fatalf("preview body = %q, want missing group warning", joinedWarnings)
	}
	if !strings.Contains(joinedWarnings, "provider-missing") {
		t.Fatalf("preview body = %q, want missing provider warning", joinedWarnings)
	}

	_, statusCode, responseBody := applyConfigImportRequest(t, h, importReq)
	if statusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", statusCode, http.StatusBadRequest, responseBody)
	}
	if len(st.groups) != 0 || len(st.providers) != 0 || len(st.routingPolicies) != 0 {
		t.Fatalf("store mutated on rejected import: groups=%d providers=%d policies=%d", len(st.groups), len(st.providers), len(st.routingPolicies))
	}
	if got := st.config[configStickyModeKey]; got != "off" {
		t.Fatalf("sticky_mode = %q, want unchanged off", got)
	}
}

func TestImportConfig_RejectsMissingScopeModeDuringPreviewAndApply(t *testing.T) {
	h, _, _ := testHandler()

	body := []byte(`{"version":"5.0","providers":[],"credential_sessions":[],"groups":[],"routing_policies":[],"settings":{},"internal_error_rules":[]}`)

	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	previewReq.Header.Set("Content-Type", "application/json")
	previewW := httptest.NewRecorder()
	h.ImportConfig(previewW, previewReq)

	if previewW.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d, want %d; body: %s", previewW.Code, http.StatusBadRequest, previewW.Body.String())
	}
	if !strings.Contains(previewW.Body.String(), `Import scope mode is required`) {
		t.Fatalf("preview body = %q, want missing-mode validation", previewW.Body.String())
	}

	importReq := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	importReq.Header.Set("Content-Type", "application/json")
	importW := httptest.NewRecorder()
	h.ImportConfig(importW, importReq)

	if importW.Code != http.StatusBadRequest {
		t.Fatalf("import status = %d, want %d; body: %s", importW.Code, http.StatusBadRequest, importW.Body.String())
	}
	if !strings.Contains(importW.Body.String(), `Import scope mode is required`) {
		t.Fatalf("import body = %q, want missing-mode validation", importW.Body.String())
	}
}

func previewConfigImportRequest(
	t *testing.T,
	h *Handler,
	importReq ImportConfigRequest,
) ImportPreviewResponse {
	t.Helper()

	body, err := json.Marshal(importReq)
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ImportConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var preview ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	return preview
}

func applyConfigImportRequest(
	t *testing.T,
	h *Handler,
	importReq ImportConfigRequest,
) (ImportResult, int, string) {
	t.Helper()

	body, err := json.Marshal(importReq)
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ImportConfig(w, req)

	if w.Code != http.StatusOK {
		return ImportResult{}, w.Code, w.Body.String()
	}

	var result ImportResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	return result, w.Code, w.Body.String()
}

func storedTestProviderWithGroup(
	id string,
	name string,
	apiType string,
	baseURL string,
	groupID string,
) *model.Provider {
	provider := storedTestProvider(id, name, apiType, baseURL)
	provider.GroupID = &groupID
	return provider
}

func decodeImportErrorMessage(t *testing.T, body string) string {
	t.Helper()

	var resp struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return resp.Message
}
