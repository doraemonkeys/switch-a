package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestConfigImportRejectsUnprovedChatGPTCredentialCreateAndUpdate(t *testing.T) {
	attacker := exportedUnprovedChatGPTSession(t, "login-session", "opaque-token-a", "acct-b")

	t.Run("create and dry-run", func(t *testing.T) {
		handler, storage, _ := testHandler()
		request := ImportConfigRequest{
			Version:            ConfigExportVersion,
			CredentialSessions: []ExportedCredentialSession{attacker},
			Groups: []ExportedGroup{{
				ID: "must-rollback", Name: "Must Roll Back", Strategy: DefaultStrategy, Weight: DefaultWeight, Enabled: true,
			}},
		}

		preview := performConfigImport(t, handler, request, true)
		if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "cannot import a ChatGPT reauthentication descriptor") {
			t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
		}
		if len(storage.credentialSessions) != 0 || len(storage.groups) != 0 {
			t.Fatal("dry-run mutated storage")
		}

		applied := performConfigImport(t, handler, request, false)
		if applied.Code != http.StatusBadRequest || !strings.Contains(applied.Body.String(), "cannot import a ChatGPT reauthentication descriptor") {
			t.Fatalf("apply = %d %s", applied.Code, applied.Body.String())
		}
		if len(storage.credentialSessions) != 0 || len(storage.groups) != 0 {
			t.Fatal("rejected ChatGPT create partially applied the config bundle")
		}
	})

	t.Run("update", func(t *testing.T) {
		handler, storage, _ := testHandler()
		original := verifiedChatGPTSession(t, "login-session", "opaque-token-original", "acct-a")
		storage.credentialSessions[original.ID] = original.Clone()

		response := performConfigImport(t, handler, ImportConfigRequest{
			Version:            ConfigExportVersion,
			CredentialSessions: []ExportedCredentialSession{attacker},
		}, false)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
		}
		current := storage.credentialSessions[original.ID]
		if current.SecretData != original.SecretData ||
			string(current.SubjectValue) != "acct-a" ||
			current.AuthState.AccountID != "acct-a" ||
			current.Version != original.Version {
			t.Fatalf("rejected update mutated credential authority: %#v", current)
		}
	})
}

func TestConfigExportChatGPTRoundTripRestoresReauthenticationPlaceholder(t *testing.T) {
	handler, storage, _ := testHandler()
	session := verifiedChatGPTSession(t, "login-session", "opaque-token-a", "acct-a")
	session.Name = "Account A"
	snapshot, err := session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	storage.credentialSessions[session.ID] = session.Clone()
	storage.providers["chat-provider"] = &model.Provider{
		ID: "chat-provider", Name: "ChatGPT", Vendor: "openai", AuthMode: DefaultAuthMode, Weight: DefaultWeight, Enabled: true,
		APITypes: []model.ProviderAPIType{{ProviderID: "chat-provider", APIType: "codex", BaseURL: "https://chat.example"}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: "chat-provider", APIType: "codex", Credential: snapshot,
		}},
	}

	exportResponse := httptest.NewRecorder()
	handler.ExportConfig(exportResponse, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export = %d %s", exportResponse.Code, exportResponse.Body.String())
	}
	if strings.Contains(exportResponse.Body.String(), "opaque-token-a") || strings.Contains(exportResponse.Body.String(), "acct-a") {
		t.Fatalf("ChatGPT authority leaked into config export: %s", exportResponse.Body.String())
	}
	var exported ExportedConfig
	if err := json.NewDecoder(exportResponse.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.CredentialSessions) != 1 {
		t.Fatalf("credential descriptors = %#v", exported.CredentialSessions)
	}
	descriptor := exported.CredentialSessions[0]
	if descriptor.TransferMode != CredentialSessionTransferReauthenticate ||
		descriptor.SecretData != "" ||
		descriptor.Subject.Kind != credentialsession.SubjectPending ||
		descriptor.AuthState.Status != credentialsession.AuthStatusReauthRequired {
		t.Fatalf("ChatGPT export descriptor = %#v", descriptor)
	}

	roundTrip := importRequestFromExport(exported)
	preview := performConfigImport(t, handler, roundTrip, true)
	if preview.Code != http.StatusOK || strings.Contains(preview.Body.String(), "requires verified ChatGPT reauthentication") {
		t.Fatalf("same-store preview = %d %s", preview.Code, preview.Body.String())
	}
	applied := performConfigImport(t, handler, roundTrip, false)
	if applied.Code != http.StatusOK {
		t.Fatalf("same-store apply = %d %s", applied.Code, applied.Body.String())
	}
	current := storage.credentialSessions[session.ID]
	if current.SecretData != session.SecretData || string(current.SubjectValue) != "acct-a" || current.Version != session.Version {
		t.Fatalf("descriptor round-trip mutated verified session: %#v", current)
	}

	restoreHandler, restoreStorage, _ := testHandler()
	restorePreview := performConfigImport(t, restoreHandler, roundTrip, true)
	var restorePlan ImportPreviewResponse
	if restorePreview.Code != http.StatusOK || json.Unmarshal(restorePreview.Body.Bytes(), &restorePlan) != nil ||
		len(restorePlan.Warnings) != 0 || restorePlan.Changes.CredentialSessions.Add != 1 || restorePlan.Changes.Providers.Add != 1 ||
		len(restorePlan.ReauthenticationRequirements) != 1 || restorePlan.ReauthenticationRequirements[0].CredentialSessionID != session.ID ||
		!strings.Contains(restorePreview.Body.String(), `"credential_reauthentication_requirements":[`) {
		t.Fatalf("new-store preview = %d %s", restorePreview.Code, restorePreview.Body.String())
	}
	restoreApply := performConfigImport(t, restoreHandler, roundTrip, false)
	var restoreResult ImportResult
	if restoreApply.Code != http.StatusOK || json.Unmarshal(restoreApply.Body.Bytes(), &restoreResult) != nil ||
		len(restoreResult.ReauthenticationRequirements) != 1 || len(restoreStorage.credentialSessions) != 1 || len(restoreStorage.providers) != 1 {
		t.Fatalf("new-store apply = %d %s storage=%#v/%#v", restoreApply.Code, restoreApply.Body.String(), restoreStorage.credentialSessions, restoreStorage.providers)
	}
	restored := restoreStorage.credentialSessions[session.ID]
	if restored == nil || !restored.IsReauthenticationPlaceholder() || restored.Name != session.Name {
		t.Fatalf("restored ChatGPT placeholder = %#v", restored)
	}
	restoredProvider := restoreStorage.providers["chat-provider"]
	if restoredProvider == nil || len(restoredProvider.CredentialSessions) != 1 ||
		restoredProvider.CredentialSessions[0].Credential.SessionID != session.ID {
		t.Fatalf("restored provider binding = %#v", restoredProvider)
	}
}

func TestConfigImportSelectionRestoresOnlySelectedChatGPTProvider(t *testing.T) {
	handler, storage, _ := testHandler()
	request := ImportConfigRequest{
		Version:     ConfigExportVersion,
		ImportScope: selectionConfigImportScope(nil, []string{"provider-a"}),
		CredentialSessions: []ExportedCredentialSession{
			chatGPTReauthenticationDescriptor("session-a", "Account A"),
			chatGPTReauthenticationDescriptor("session-b", "Account B"),
		},
		Providers: []ExportedProvider{
			{
				ID: "provider-a", Name: "Provider A", Vendor: "openai", AuthMode: DefaultAuthMode, Weight: DefaultWeight, Enabled: true,
				APITypes: []ExportedAPIType{{APIType: "codex", BaseURL: "https://chatgpt.com/backend-api/codex", CredentialSessionID: "session-a"}},
			},
			{
				ID: "provider-b", Name: "Provider B", Vendor: "openai", AuthMode: DefaultAuthMode, Weight: DefaultWeight, Enabled: true,
				APITypes: []ExportedAPIType{{APIType: "codex", BaseURL: "https://chatgpt.com/backend-api/codex", CredentialSessionID: "session-b"}},
			},
		},
	}

	preview := performConfigImport(t, handler, request, true)
	var plan ImportPreviewResponse
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &plan) != nil || len(plan.Warnings) != 0 ||
		plan.Changes.CredentialSessions.Add != 1 || plan.Changes.Providers.Add != 1 ||
		len(plan.ReauthenticationRequirements) != 1 || plan.ReauthenticationRequirements[0].CredentialSessionID != "session-a" {
		t.Fatalf("selection preview = %d %s", preview.Code, preview.Body.String())
	}
	applied := performConfigImport(t, handler, request, false)
	if applied.Code != http.StatusOK {
		t.Fatalf("selection apply = %d %s", applied.Code, applied.Body.String())
	}
	if len(storage.providers) != 1 || storage.providers["provider-a"] == nil || storage.providers["provider-b"] != nil {
		t.Fatalf("selected providers = %#v", storage.providers)
	}
	if len(storage.credentialSessions) != 1 || storage.credentialSessions["session-a"] == nil ||
		!storage.credentialSessions["session-a"].IsReauthenticationPlaceholder() || storage.credentialSessions["session-b"] != nil {
		t.Fatalf("selected credential sessions = %#v", storage.credentialSessions)
	}
}

func chatGPTReauthenticationDescriptor(id, name string) ExportedCredentialSession {
	return ExportedCredentialSession{
		ID: id, Name: name, Kind: credentialsession.KindChatGPT,
		TransferMode: CredentialSessionTransferReauthenticate,
		Version:      1,
		Subject:      credentialsession.PendingSubject(),
		AuthState: credentialsession.AuthState{
			Status:       credentialsession.AuthStatusReauthRequired,
			StatusReason: configRestoreReauthenticationReason,
		},
	}
}

func exportedUnprovedChatGPTSession(t *testing.T, id, secret, accountID string) ExportedCredentialSession {
	t.Helper()
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	return ExportedCredentialSession{
		ID: id, Kind: credentialsession.KindChatGPT,
		TransferMode: CredentialSessionTransferStaticSecret,
		SecretData:   secret, Version: 1, Subject: subject,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: accountID},
	}
}

func verifiedChatGPTSession(t *testing.T, id, secret, accountID string) *credentialsession.Session {
	t.Helper()
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	session := &credentialsession.Session{
		ID: id, Kind: credentialsession.KindChatGPT,
		SecretData: secret, Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: accountID},
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	return session
}
