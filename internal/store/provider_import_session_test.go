package store

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestApplyProviderImport_AllowsIndependentSessionsForSameAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := setupTestStore(t)

	first := importTestProvider(t, "provider-first", "shared-account", nil)
	second := importTestProvider(t, "provider-second", "shared-account", nil)
	bundle := &ProviderImportBundle{Creates: []ProviderImportCreate{
		providerImportCreateFromFixture(t, "candidate-first", first),
		providerImportCreateFromFixture(t, "candidate-second", second),
	}}
	if err := store.ApplyProviderImport(ctx, bundle); err != nil {
		t.Fatalf("ApplyProviderImport() error = %v", err)
	}

	firstSnapshot, ok := first.CredentialSessionForRoute("codex", "http")
	if !ok {
		t.Fatal("first provider has no codex session")
	}
	secondSnapshot, ok := second.CredentialSessionForRoute("codex", "http")
	if !ok {
		t.Fatal("second provider has no codex session")
	}
	if firstSnapshot.SessionID == secondSnapshot.SessionID {
		t.Fatal("same account unexpectedly collapsed into one credential session")
	}
	for _, sessionID := range []string{firstSnapshot.SessionID, secondSnapshot.SessionID} {
		session, err := store.GetCredentialSession(ctx, sessionID)
		if err != nil {
			t.Fatalf("GetCredentialSession(%q) error = %v", sessionID, err)
		}
		if session.Subject().Kind != credentialsession.SubjectAccount || string(session.Subject().Value) != "shared-account" {
			t.Fatalf("session %q subject = %#v, want shared account subject", sessionID, session.Subject())
		}
	}
}

func TestApplyProviderImport_UpdatesOnlyExplicitSessionWithCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := setupTestStore(t)

	first := importTestProvider(t, "provider-update-first", "shared-account", nil)
	second := importTestProvider(t, "provider-update-second", "shared-account", nil)
	create := &ProviderImportBundle{Creates: []ProviderImportCreate{
		providerImportCreateFromFixture(t, "candidate-create-first", first),
		providerImportCreateFromFixture(t, "candidate-create-second", second),
	}}
	if err := store.ApplyProviderImport(ctx, create); err != nil {
		t.Fatalf("create import error = %v", err)
	}

	firstSnapshot, _ := first.CredentialSessionForRoute("codex", "http")
	secondSnapshot, _ := second.CredentialSessionForRoute("codex", "http")
	updatedAuth := firstSnapshot.AuthState.Clone()
	updatedAuth.Email = "updated@example.com"
	update := &ProviderImportBundle{CredentialUpdates: []ProviderImportCredentialUpdate{{
		CandidateID:     "candidate-update-first",
		SessionID:       firstSnapshot.SessionID,
		ExpectedVersion: firstSnapshot.Version,
		SecretData:      `{"access_token":"rotated"}`,
		Subject:         firstSnapshot.Subject,
		AuthState:       updatedAuth,
	}}}
	if err := store.ApplyProviderImport(ctx, update); err != nil {
		t.Fatalf("update import error = %v", err)
	}

	firstAfter, err := store.GetCredentialSession(ctx, firstSnapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	secondAfter, err := store.GetCredentialSession(ctx, secondSnapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAfter.Version != firstSnapshot.Version+1 || firstAfter.AuthState.Email != updatedAuth.Email {
		t.Fatalf("updated session = version %d email %q", firstAfter.Version, firstAfter.AuthState.Email)
	}
	if secondAfter.Version != secondSnapshot.Version || secondAfter.SecretData != secondSnapshot.SecretData {
		t.Fatal("explicit session update mutated a sibling session with the same account subject")
	}

	stale := *update
	stale.CredentialUpdates = append([]ProviderImportCredentialUpdate(nil), update.CredentialUpdates...)
	stale.CredentialUpdates[0].CandidateID = "candidate-stale"
	if err := store.ApplyProviderImport(ctx, &stale); !errors.Is(err, ErrProviderImportConflict) {
		t.Fatalf("stale update error = %v, want ErrProviderImportConflict", err)
	}
	firstAfterStale, err := store.GetCredentialSession(ctx, firstSnapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAfterStale.Version != firstAfter.Version || firstAfterStale.SecretData != firstAfter.SecretData {
		t.Fatal("stale CAS changed the credential session")
	}
}

func providerImportCreateFromFixture(t *testing.T, candidateID string, provider model.Provider) ProviderImportCreate {
	t.Helper()
	snapshot, ok := provider.CredentialSessionForRoute("codex", "http")
	if !ok {
		t.Fatalf("provider %q has no codex credential session", provider.ID)
	}
	session, err := sessionFromSnapshot(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return ProviderImportCreate{CandidateID: candidateID, Provider: provider, Sessions: []credentialsession.Session{session}}
}

func TestProviderImportCredentialUpdateOwnsDisguiseGeneration(t *testing.T) {
	ctx := context.Background()
	persistence := newCredentialSessionStore(t)
	provider := importTestProvider(t, "provider", "original-account", nil)
	if err := persistence.ApplyProviderImport(ctx, &ProviderImportBundle{Creates: []ProviderImportCreate{
		providerImportCreateFromFixture(t, "create", provider),
	}}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := provider.CredentialSessionForRoute("codex", "http")
	repo := persistence.ClientDisguiseRepository()
	original, err := repo.GetLogin(ctx, snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SelectProfile(ctx, snapshot.SessionID, clientdisguise.BuiltinProfiles()[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"original-account", "replacement-account"} {
		session, err := persistence.GetCredentialSession(ctx, snapshot.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		subject, err := credentialsession.AccountSubject(account)
		if err != nil {
			t.Fatal(err)
		}
		auth := session.AuthState.Clone()
		auth.AccountID = account
		if err := persistence.ApplyProviderImport(ctx, &ProviderImportBundle{CredentialUpdates: []ProviderImportCredentialUpdate{{
			CandidateID: "update", SessionID: session.ID, ExpectedVersion: session.Version,
			SecretData: `{"access_token":"rotated"}`, Subject: subject, AuthState: auth,
		}}}); err != nil {
			t.Fatal(err)
		}
		current, err := repo.GetLogin(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		bindings, err := repo.ListBindings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if account == "original-account" {
			if current.DeviceID != original.DeviceID || current.GenerationID != original.GenerationID || len(bindings) != 1 {
				t.Fatal("same-account import changed device or profile")
			}
		} else if current.DeviceID == original.DeviceID || current.GenerationID == original.GenerationID || len(bindings) != 0 {
			t.Fatal("account replacement retained the previous device or profile")
		}
	}
	history, err := repo.Export(ctx)
	if err != nil || len(history.LoginHistory) != 1 || history.LoginHistory[0].GenerationID != original.GenerationID {
		t.Fatalf("replaced generation was not archived: %+v, %v", history.LoginHistory, err)
	}
}
