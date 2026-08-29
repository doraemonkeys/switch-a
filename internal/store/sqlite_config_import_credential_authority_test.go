package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestApplyConfigImportRejectsChatGPTCredentialMaterialMutationAndRollsBack(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		existing *credentialsession.Session
	}{
		{name: "create"},
		{name: "update", existing: configImportChatGPTSession(t, "login-session", "token-original", "acct-a")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			storage := newCredentialSessionStore(t)
			ctx := context.Background()
			if testCase.existing != nil {
				if _, err := storage.CreateCredentialSession(ctx, testCase.existing); err != nil {
					t.Fatal(err)
				}
			}
			candidate := configImportChatGPTSession(t, "login-session", "token-a", "acct-b")
			err := storage.ApplyConfigImport(ctx, &ConfigImportBundle{
				RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
				Groups:             []model.Group{{ID: "must-rollback", Name: "Must Roll Back", Strategy: "priority", Weight: 1, Enabled: true}},
				CredentialSessions: []credentialsession.Session{*candidate},
			})
			if !errors.Is(err, ErrConfigImportChatGPTCredentialMaterialMutation) || !strings.Contains(err.Error(), "login-session") {
				t.Fatalf("ApplyConfigImport() error = %v", err)
			}
			if _, err := storage.GetGroup(ctx, "must-rollback"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rolled-back group lookup error = %v", err)
			}
			current, err := storage.GetCredentialSession(ctx, "login-session")
			if testCase.existing == nil {
				if !errors.Is(err, credentialsession.ErrNotFound) {
					t.Fatalf("rejected create lookup = (%#v, %v)", current, err)
				}
				return
			}
			if err != nil || current.SecretData != testCase.existing.SecretData ||
				string(current.SubjectValue) != "acct-a" || current.Version != testCase.existing.Version {
				t.Fatalf("rejected update mutated session = (%#v, %v)", current, err)
			}
		})
	}
}

func TestApplyConfigImportCreatesChatGPTReauthenticationPlaceholder(t *testing.T) {
	storage := newCredentialSessionStore(t)
	ctx := context.Background()
	placeholder := credentialsession.Session{
		ID: "restored-session", Name: "Restored GPT", Kind: credentialsession.KindChatGPT, Version: 7,
		SubjectKind: credentialsession.SubjectPending,
		AuthState: credentialsession.AuthState{
			Status:       credentialsession.AuthStatusReauthRequired,
			StatusReason: "config_restore_requires_verified_reauthentication",
		},
	}
	provider := model.Provider{
		ID: "restored-provider", Name: "Restored Provider", Vendor: "openai", Enabled: true,
		APITypes: []model.ProviderAPIType{{ProviderID: "restored-provider", APIType: "codex", BaseURL: "https://chatgpt.com/backend-api/codex"}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: "restored-provider", APIType: "codex", VendorScope: "openai",
			Credential: credentialsession.Snapshot{SessionID: placeholder.ID},
		}},
	}

	if err := storage.ApplyConfigImport(ctx, &ConfigImportBundle{
		RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
		CredentialSessions: []credentialsession.Session{placeholder},
		Providers:          []model.Provider{provider},
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := storage.GetCredentialSession(ctx, placeholder.ID)
	if err != nil || !restored.IsReauthenticationPlaceholder() || restored.Version != placeholder.Version {
		t.Fatalf("restored placeholder = (%#v, %v)", restored, err)
	}
	restoredProvider, err := storage.GetProvider(ctx, provider.ID)
	if err != nil || len(restoredProvider.CredentialSessions) != 1 ||
		restoredProvider.CredentialSessions[0].Credential.SessionID != placeholder.ID ||
		restoredProvider.CredentialSessions[0].Credential.HasCredentialMaterial() {
		t.Fatalf("restored provider = (%#v, %v)", restoredProvider, err)
	}
}

func TestApplyConfigImportRenamesChatGPTSessionWithoutMutatingCredentialAuthority(t *testing.T) {
	storage := newCredentialSessionStore(t)
	ctx := context.Background()
	existing := configImportChatGPTSession(t, "login-session", "token-original", "acct-a")
	existing.Name = "Current Name"
	if _, err := storage.CreateCredentialSession(ctx, existing); err != nil {
		t.Fatal(err)
	}
	placeholder := credentialsession.Session{
		ID: existing.ID, Name: "Imported Name", Kind: credentialsession.KindChatGPT, Version: 99,
		SubjectKind: credentialsession.SubjectPending,
		AuthState:   credentialsession.AuthState{Status: credentialsession.AuthStatusReauthRequired},
	}

	if err := storage.ApplyConfigImport(ctx, &ConfigImportBundle{
		RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
		CredentialSessions: []credentialsession.Session{placeholder},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := storage.GetCredentialSession(ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != placeholder.Name || current.SecretData != existing.SecretData ||
		string(current.SubjectValue) != "acct-a" || current.AuthState.Status != credentialsession.AuthStatusActive ||
		current.AuthState.AccountID != "acct-a" || current.Version <= existing.Version {
		t.Fatalf("renamed ChatGPT session = %#v", current)
	}
}

func configImportChatGPTSession(t *testing.T, id, secret, accountID string) *credentialsession.Session {
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
