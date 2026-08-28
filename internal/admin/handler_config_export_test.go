package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

type groupListErrorStore struct{ mockStore }

func (s *groupListErrorStore) ListGroups(context.Context) ([]model.Group, error) {
	return nil, errors.New("database error")
}

type credentialSessionListErrorStore struct{ mockStore }

func (s *credentialSessionListErrorStore) ListCredentialSessions(context.Context) ([]credentialsession.Session, error) {
	return nil, errors.New("database error")
}

func TestExportConfig_ExportsProvidersAndEveryCredentialSession(t *testing.T) {
	h, st, _ := testHandler()
	groupID := "g1"
	st.providers["p1"] = &model.Provider{
		ID:                 "p1",
		Name:               "Provider 1",
		APITypes:           []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.p1.com"}},
		CredentialSessions: []credentialsession.RouteSnapshot{testConfigCredentialRoute("p1", "claude", "p1-session", "claude-key")},
		AuthMode:           "bearer",
		GroupID:            &groupID,
		Weight:             1,
		Enabled:            true,
	}
	unreferenced := testConfigCredentialSession(t, "unreferenced-session", "openai", "unreferenced-key")
	st.credentialSessions[unreferenced.ID] = &unreferenced
	st.groups[groupID] = &model.Group{ID: groupID, Name: "Group 1", Strategy: "priority", Weight: 1, Enabled: true}
	st.config["sticky_mode"] = "model"
	st.config[defaults.ConfigKeyWebSocketProbeClientModel] = "false"

	w := httptest.NewRecorder()
	h.ExportConfig(w, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var exported ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	if exported.Version != ConfigExportVersion || len(exported.Providers) != 1 || len(exported.CredentialSessions) != 2 {
		t.Fatalf("export summary = version %q providers %d sessions %d", exported.Version, len(exported.Providers), len(exported.CredentialSessions))
	}
	provider := exported.Providers[0]
	if len(provider.APITypes) != 1 || provider.APITypes[0].CredentialSessionID != "p1-session" {
		t.Fatalf("exported provider API types = %#v", provider.APITypes)
	}
	if !containsExportedSession(exported.CredentialSessions, "unreferenced-session", "unreferenced-key") {
		t.Fatal("unreferenced credential session was omitted from config export")
	}
	if exported.Settings["sticky_mode"] != "model" || exported.Settings[defaults.ConfigKeyWebSocketProbeClientModel] != "false" {
		t.Fatalf("settings = %#v", exported.Settings)
	}
}

func TestExportConfig_IncludesRoutingPolicies(t *testing.T) {
	h, st, _ := testHandler()
	targetProviderID := "p-exact"
	st.routingPolicies[1] = &model.RoutingPolicy{
		ID: 1, APIType: "codex", ModelMatchType: model.RoutingPolicyModelMatchTypeExact,
		ModelMatchValue: "gpt-5", TargetProviderID: &targetProviderID,
	}
	st.routingPolicies[2] = &model.RoutingPolicy{
		ID: 2, APIType: "claude", Enabled: true,
		Groups:  []model.RoutingPolicyGroup{{GroupID: "g-filter"}},
		Vendors: []model.RoutingPolicyVendor{{Vendor: "anthropic"}},
	}

	w := httptest.NewRecorder()
	h.ExportConfig(w, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var exported ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.RoutingPolicies) != 2 {
		t.Fatalf("routing policies = %#v", exported.RoutingPolicies)
	}
	if exported.RoutingPolicies[0].TargetProviderID == nil || *exported.RoutingPolicies[0].TargetProviderID != targetProviderID {
		t.Fatalf("exact policy = %#v", exported.RoutingPolicies[0])
	}
}

func TestExportConfig_NormalizesAndFiltersSettings(t *testing.T) {
	h, st, _ := testHandler()
	st.config["sticky_enabled"] = "false"
	st.config["max_retries"] = "7"
	st.config["invalid_key"] = "ignored"
	st.config["global_max_attempts"] = "-1"

	w := httptest.NewRecorder()
	h.ExportConfig(w, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var exported ExportedConfig
	if err := json.NewDecoder(w.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	if exported.Settings["sticky_mode"] != "off" {
		t.Fatalf("sticky_mode = %q, want off", exported.Settings["sticky_mode"])
	}
	for _, obsolete := range []string{"sticky_enabled", "max_retries", "invalid_key", "global_max_attempts"} {
		if _, exists := exported.Settings[obsolete]; exists {
			t.Fatalf("setting %q should be omitted", obsolete)
		}
	}
}

func TestExportConfig_ReportsStoreFailures(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	testCases := []struct {
		name  string
		store Store
	}{
		{name: "provider list", store: func() Store { value := newMockStore(); value.listErr = errors.New("database error"); return value }()},
		{name: "group list", store: &groupListErrorStore{mockStore: *newMockStore()}},
		{name: "config", store: func() Store { value := newMockStore(); value.configErr = errors.New("database error"); return value }()},
		{name: "credential sessions", store: &credentialSessionListErrorStore{mockStore: *newMockStore()}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := NewHandler(Config{Store: testCase.store, Concurrency: &mockConcurrencyTracker{}, Logger: logger})
			w := httptest.NewRecorder()
			handler.ExportConfig(w, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
			}
		})
	}
}

func TestBuildExportedProvider_BackoffAndSessionReferenceRoundTrip(t *testing.T) {
	provider := &model.Provider{
		ID:                 "p1",
		Name:               "Backoff Provider",
		APITypes:           []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.example.com"}},
		CredentialSessions: []credentialsession.RouteSnapshot{testConfigCredentialRoute("p1", "claude", "p1-session", "claude-key")},
		AuthMode:           "bearer",
		Weight:             1,
		Enabled:            true,
		Backoff: model.BackoffPolicy{
			InitialDelay: model.Duration(500 * time.Millisecond), MaxDelay: model.Duration(5 * time.Second),
			Multiplier: 2.5, Jitter: true,
		},
	}
	exported := buildExportedProvider(provider)
	if len(exported.APITypes) != 1 || exported.APITypes[0].CredentialSessionID != "p1-session" {
		t.Fatalf("exported API types = %#v", exported.APITypes)
	}
	imported, ok := buildProviderFromExport(&exported, nil)
	if !ok {
		t.Fatal("buildProviderFromExport() rejected current export")
	}
	snapshot, ok := imported.CredentialSessionForAPIType("claude")
	if !ok || snapshot.SessionID != "p1-session" {
		t.Fatalf("imported session = %#v", snapshot)
	}
	if imported.Backoff != provider.Backoff {
		t.Fatalf("backoff = %#v, want %#v", imported.Backoff, provider.Backoff)
	}
}

func testConfigCredentialRoute(providerID, apiType, sessionID, secret string) credentialsession.RouteSnapshot {
	return credentialsession.RouteSnapshot{
		RouteTargetID: providerID,
		APIType:       apiType,
		Credential: credentialsession.Snapshot{
			SessionID: sessionID, Kind: credentialsession.KindAPIKey,
			SecretData: secret, Version: 1, Subject: credentialsession.PendingSubject(),
			AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
		},
	}
}

func testConfigCredentialSession(t *testing.T, sessionID, _ string, secret string) credentialsession.Session {
	t.Helper()
	session := credentialsession.Session{
		ID: sessionID, Kind: credentialsession.KindAPIKey,
		SecretData: secret, Version: 1, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if err := session.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	return session
}

func containsExportedSession(sessions []ExportedCredentialSession, sessionID, secret string) bool {
	for _, session := range sessions {
		if session.ID == sessionID && session.SecretData == secret {
			return true
		}
	}
	return false
}
