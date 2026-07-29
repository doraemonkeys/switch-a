package store

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"gorm.io/gorm"
)

func TestPersistProviderSupplementalState_LoadsAndDeletesRecords(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "p-state",
		Name:           "State Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: "p-state", APIType: "codex", BaseURL: "https://codex.example"},
		},
	}); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	accountID := "acct-state"
	transitionAt := time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC)
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return persistProviderSupplementalState(tx, "p-state", providerSupplementalState{
			credential: &model.ProviderCredential{
				ProviderID:       "p-state",
				SecretData:       "secret-state",
				BindingAccountID: &accountID,
				Version:          3,
			},
			authState: &model.ProviderAuthState{
				ProviderID:       "p-state",
				Status:           model.ProviderAuthStatusActive,
				StatusReason:     "ready",
				LastTransitionAt: &transitionAt,
			},
		})
	})
	if err != nil {
		t.Fatalf("persistProviderSupplementalState(save) error = %v", err)
	}

	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := loadPersistedProviderState(tx, "p-state")
		if err != nil {
			return err
		}
		if state.credentialType != model.ProviderCredentialTypeChatGPT {
			t.Fatalf("credentialType = %q, want %q", state.credentialType, model.ProviderCredentialTypeChatGPT)
		}
		if state.credential == nil {
			t.Fatal("credential = nil, want persisted credential")
		}
		if state.credential.SecretData != "secret-state" || state.credential.Version != 3 {
			t.Fatalf("credential = %+v, want persisted secret/version", state.credential)
		}
		if state.authState == nil {
			t.Fatal("authState = nil, want persisted auth state")
		}
		if state.authState.Status != model.ProviderAuthStatusActive {
			t.Fatalf("authState.Status = %q, want %q", state.authState.Status, model.ProviderAuthStatusActive)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("loadPersistedProviderState(saved) error = %v", err)
	}

	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return persistProviderSupplementalState(tx, "p-state", providerSupplementalState{})
	})
	if err != nil {
		t.Fatalf("persistProviderSupplementalState(delete) error = %v", err)
	}

	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := loadPersistedProviderState(tx, "p-state")
		if err != nil {
			return err
		}
		if state.credentialType != model.ProviderCredentialTypeChatGPT {
			t.Fatalf("credentialType after delete = %q, want %q", state.credentialType, model.ProviderCredentialTypeChatGPT)
		}
		if state.credential != nil {
			t.Fatalf("credential after delete = %+v, want nil", state.credential)
		}
		if state.authState != nil {
			t.Fatalf("authState after delete = %+v, want nil", state.authState)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("loadPersistedProviderState(deleted) error = %v", err)
	}
}

func TestProviderCredentialsEqual_NormalizesOptionalBinding(t *testing.T) {
	if !providerCredentialsEqual(nil, nil) {
		t.Fatal("providerCredentialsEqual(nil, nil) = false, want true")
	}

	leftOnly := &model.ProviderCredential{ProviderID: "p", SecretData: "secret"}
	if providerCredentialsEqual(leftOnly, nil) {
		t.Fatal("providerCredentialsEqual(non-nil, nil) = true, want false")
	}

	leftBinding := " acct-1 "
	rightBinding := "acct-1"
	if !providerCredentialsEqual(
		&model.ProviderCredential{
			ProviderID:       "p",
			SecretData:       "secret",
			BindingAccountID: &leftBinding,
		},
		&model.ProviderCredential{
			ProviderID:       "p",
			SecretData:       "secret",
			BindingAccountID: &rightBinding,
		},
	) {
		t.Fatal("providerCredentialsEqual(trimmed bindings) = false, want true")
	}
}
