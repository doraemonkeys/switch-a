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
		if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "cannot import ChatGPT credential material") {
			t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
		}
		if len(storage.credentialSessions) != 0 || len(storage.groups) != 0 {
			t.Fatal("dry-run mutated storage")
		}

		applied := performConfigImport(t, handler, request, false)
		if applied.Code != http.StatusBadRequest || !strings.Contains(applied.Body.String(), "verified login/provider-import") {
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

func TestConfigExportChatGPTRoundTripUsesReauthenticationDescriptor(t *testing.T) {
	handler, storage, _ := testHandler()
	session := verifiedChatGPTSession(t, "login-session", "opaque-token-a", "acct-a")
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
	if restorePreview.Code != http.StatusOK || !strings.Contains(restorePreview.Body.String(), "create it with the same ID through login/provider-import and retry") {
		t.Fatalf("new-store preview = %d %s", restorePreview.Code, restorePreview.Body.String())
	}
	restoreApply := performConfigImport(t, restoreHandler, roundTrip, false)
	if restoreApply.Code != http.StatusBadRequest || len(restoreStorage.credentialSessions) != 0 || len(restoreStorage.providers) != 0 {
		t.Fatalf("new-store apply = %d %s storage=%#v/%#v", restoreApply.Code, restoreApply.Body.String(), restoreStorage.credentialSessions, restoreStorage.providers)
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
