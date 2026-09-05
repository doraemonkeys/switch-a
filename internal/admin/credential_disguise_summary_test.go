package admin

import (
	"context"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"testing"
)

func TestCredentialDisguiseSummaryReflectsPersistentLogin(t *testing.T) {
	_, store := newCredentialSessionHandler(t)
	ctx := context.Background()
	if summary, err := credentialDisguiseSummary(ctx, store, "login"); err != nil || summary != nil {
		t.Fatal(summary, err)
	}
	repo := store.ClientDisguiseRepository()
	identity, err := repo.SyncLoginAccount(ctx, "login", clientdisguise.AccountBasis{Kind: "account", Value: []byte("account")})
	if err != nil {
		t.Fatal(err)
	}
	profile := clientdisguise.BuiltinProfiles()[0]
	_, err = repo.SetBinding(ctx, clientdisguise.ProfileBinding{CredentialSessionID: "login", Tuple: profile.Tuple, RevisionID: profile.ID, Mode: clientdisguise.ModePinned})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := credentialDisguiseSummary(ctx, store, "login")
	if err != nil {
		t.Fatal(err)
	}
	if summary.DeviceID != identity.DeviceID || summary.RevisionID != profile.ID || summary.Mode != clientdisguise.ModePinned {
		t.Fatal(summary)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := credentialDisguiseSummary(ctx, store, "login"); err == nil {
		t.Fatal("closed repository failure suppressed")
	}
}
