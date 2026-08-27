package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestApplyConfigImportRejectsChatGPTCredentialMutationAndRollsBack(t *testing.T) {
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
			if !errors.Is(err, ErrConfigImportChatGPTCredentialMutation) || !strings.Contains(err.Error(), "login-session") {
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

func configImportChatGPTSession(t *testing.T, id, secret, accountID string) *credentialsession.Session {
	t.Helper()
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	session := &credentialsession.Session{
		ID: id, Vendor: "openai", Kind: credentialsession.KindChatGPT,
		SecretData: secret, Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: accountID},
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	return session
}
