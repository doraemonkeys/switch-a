package store

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func credentialBackedTestProvider(t *testing.T, store *SQLiteStore, provider *model.Provider) *model.Provider {
	t.Helper()
	provider.CredentialSessions = make([]credentialsession.RouteSnapshot, 0, len(provider.APITypes))
	for index := range provider.APITypes {
		apiType := &provider.APITypes[index]
		secret := "test-secret-" + provider.ID + "-" + apiType.APIType
		sessionID := provider.ID + "-" + apiType.APIType + "-session"
		session := &credentialsession.Session{
			ID: sessionID, Kind: credentialsession.KindAPIKey,
			SecretData: secret, Version: 1, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
		}
		if err := session.SetSubject(credentialsession.PendingSubject()); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateCredentialSession(context.Background(), session); err != nil {
			t.Fatalf("CreateCredentialSession(%q) error = %v", sessionID, err)
		}
		provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{
			RouteTargetID: provider.ID, APIType: apiType.APIType, VendorScope: provider.Vendor,
			Credential: credentialsession.Snapshot{SessionID: sessionID},
		})
	}
	return provider
}

func testStaticCredentialSession(id, _ string, secret string) credentialsession.Session {
	session := credentialsession.Session{
		ID: id, Kind: credentialsession.KindAPIKey,
		SecretData: secret, Version: 1, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	_ = session.SetSubject(credentialsession.PendingSubject())
	return session
}

type credentialMutationUnsupportedStore struct{ internal.Store }

func importTestProvider(t *testing.T, providerID, accountID string, groupID *string) model.Provider {
	t.Helper()
	secret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderSecret() error = %v", err)
	}
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatalf("AccountSubject() error = %v", err)
	}
	return model.Provider{
		ID:          providerID,
		Name:        "Provider " + providerID,
		AuthMode:    "bearer",
		GroupID:     groupID,
		Weight:      1,
		Priority:    1,
		Concurrency: 10,
		Enabled:     true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: providerID,
			APIType:    "codex",
			BaseURL:    "https://chatgpt.com/backend-api/codex",
		}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: providerID,
			APIType:       "codex",
			VendorScope:   "openai",
			Credential: credentialsession.Snapshot{
				SessionID:  providerID + "-session",
				Kind:       credentialsession.KindChatGPT,
				SecretData: secret,
				Version:    1,
				Subject:    subject,
				AuthState: credentialsession.AuthState{
					Status:    credentialsession.AuthStatusActive,
					AccountID: accountID,
				},
			},
		}},
	}
}

func assertProviderMissing(t *testing.T, store *SQLiteStore, providerID string) {
	t.Helper()
	if _, err := store.GetProvider(context.Background(), providerID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProvider(%q) error = %v, want ErrNotFound", providerID, err)
	}
}
