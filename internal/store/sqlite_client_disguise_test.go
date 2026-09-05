package store

import (
	"context"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"testing"
)

func TestCredentialLifecycleOwnsDisguiseGeneration(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	create := func(id, account string) *credentialsession.Session {
		t.Helper()
		subject, err := credentialsession.AccountSubject(account)
		if err != nil {
			t.Fatal(err)
		}
		session := &credentialsession.Session{ID: id, Kind: credentialsession.KindChatGPT, SecretData: `{"access_token":"access","refresh_token":"refresh"}`, Version: 1, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: account}}
		if err := session.SetSubject(subject); err != nil {
			t.Fatal(err)
		}
		result, err := store.CreateCredentialSession(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	a := create("a", "same")
	create("b", "same")
	repo := store.ClientDisguiseRepository()
	first, err := repo.GetLogin(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.GetLogin(ctx, "b")
	if err != nil || other.DeviceID == first.DeviceID {
		t.Fatal("independent login devices", err)
	}
	owned, release, err := store.WithCredentialSessionMutations(ctx, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := store.UpdateCredentialSessionCAS(owned, a.ID, a.Version, a.SecretData, a.Subject(), a.AuthState); err != nil {
		t.Fatal(err)
	}
	refreshed, err := repo.GetLogin(owned, "a")
	if err != nil || refreshed.GenerationID != first.GenerationID {
		t.Fatal("same account refresh changed generation", err)
	}
	subject, _ := credentialsession.AccountSubject("different")
	a.AuthState.AccountID = "different"
	if _, err := store.UpdateCredentialSessionCAS(owned, a.ID, 2, a.SecretData, subject, a.AuthState); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.GetLogin(owned, "a")
	if err != nil || changed.GenerationID == first.GenerationID {
		t.Fatal("account change retained generation", err)
	}
	snapshot, err := repo.Export(owned)
	if err != nil || len(snapshot.LoginHistory) != 1 {
		t.Fatal(snapshot, err)
	}
	if err := initializeDisguiseLogins(owned, store.db); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := repo.GetLogin(owned, "a")
	if reloaded.GenerationID != changed.GenerationID {
		t.Fatal("startup changed generation")
	}
	if err := store.DeleteCredentialSession(owned, "a"); err != nil {
		t.Fatal(err)
	}
	retired, err := repo.Export(owned)
	if err != nil || len(retired.Logins) != 1 || len(retired.LoginHistory) != 2 {
		t.Fatal(retired, err)
	}
}
