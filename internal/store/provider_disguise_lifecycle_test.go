package store

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

func materializeProviderCredential(t *testing.T, persistence *SQLiteStore, operation string) func() error {
	t.Helper()
	ctx := context.Background()
	session := testStaticCredentialSession("new-session", "openai", "new-key")
	provider := providerWithSessionRefs("provider", "openai", map[string]string{"codex": session.ID})
	switch operation {
	case "update":
		old := mustCreateStaticSession(t, persistence, "old-session", "openai", "old-key")
		if err := persistence.CreateProvider(ctx, providerWithSessionRefs(provider.ID, provider.Vendor, map[string]string{"codex": old.ID})); err != nil {
			t.Fatal(err)
		}
		return func() error {
			return persistence.UpdateProviderWithCredentialSessions(ctx, provider, []*credentialsession.Session{&session})
		}
	case "import":
		return func() error {
			return persistence.ApplyProviderImport(ctx, &ProviderImportBundle{Creates: []ProviderImportCreate{{
				CandidateID: "candidate", Provider: *provider, Sessions: []credentialsession.Session{session},
			}}})
		}
	default:
		return func() error {
			return persistence.CreateProviderWithCredentialSessions(ctx, provider, []*credentialsession.Session{&session})
		}
	}
}

func TestProviderCredentialMaterializationCreatesLoginIdentity(t *testing.T) {
	for _, operation := range []string{"create", "update", "import"} {
		t.Run(operation, func(t *testing.T) {
			persistence := newCredentialSessionStore(t)
			ctx := context.Background()
			if err := materializeProviderCredential(t, persistence, operation)(); err != nil {
				t.Fatal(err)
			}
			session, err := persistence.GetCredentialSession(ctx, "new-session")
			if err != nil {
				t.Fatal(err)
			}
			login, err := persistence.ClientDisguiseRepository().GetLogin(ctx, session.ID)
			if err != nil || login.DeviceID == "" || login.GenerationID == "" || !login.AccountBasis.Equal(clientdisguise.AccountBasis{
				Kind: string(session.SubjectKind), Value: session.SubjectValue, KeyVersion: session.SubjectKeyVersion,
			}) {
				t.Fatalf("credential committed without its resolved login identity: %+v, %v", login, err)
			}
			bindings, err := persistence.ClientDisguiseRepository().ListBindings(ctx)
			if err != nil || len(bindings) != 0 {
				t.Fatalf("creating a credential must leave profile selection to the operator or first request: %+v, %v", bindings, err)
			}
		})
	}
}

func TestProviderCredentialAndLoginIdentityRollBackTogether(t *testing.T) {
	for _, operation := range []string{"create", "update", "import"} {
		for _, failure := range []string{"identity", "provider"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				persistence := newCredentialSessionStore(t)
				materialize := materializeProviderCredential(t, persistence, operation)
				trigger := "CREATE TRIGGER fail_identity BEFORE INSERT ON client_disguise_login_identities BEGIN SELECT RAISE(ABORT, 'identity failure'); END"
				if failure == "provider" {
					trigger = "CREATE TRIGGER fail_provider BEFORE INSERT ON providers BEGIN SELECT RAISE(ABORT, 'provider failure'); END"
					if operation == "update" {
						trigger = "CREATE TRIGGER fail_provider BEFORE UPDATE ON providers BEGIN SELECT RAISE(ABORT, 'provider failure'); END"
					}
				}
				if err := persistence.db.Exec(trigger).Error; err != nil {
					t.Fatal(err)
				}
				if err := materialize(); err == nil {
					t.Fatal("injected failure was ignored")
				}
				ctx := context.Background()
				if _, err := persistence.GetCredentialSession(ctx, "new-session"); !errors.Is(err, credentialsession.ErrNotFound) {
					t.Fatalf("partial credential persisted: %v", err)
				}
				if _, err := persistence.ClientDisguiseRepository().GetLogin(ctx, "new-session"); !errors.Is(err, clientdisguise.ErrNotFound) {
					t.Fatalf("partial identity persisted: %v", err)
				}
				if operation == "update" {
					resolved, err := persistence.ResolveCredentialSession(ctx, "provider", "codex", "http")
					if err != nil || resolved.Credential.SessionID != "old-session" {
						t.Fatalf("failed update changed the existing route: %+v, %v", resolved, err)
					}
				} else if _, err := persistence.GetProvider(ctx, "provider"); !errors.Is(err, ErrNotFound) {
					t.Fatalf("partial provider persisted: %v", err)
				}
			})
		}
	}
}

func TestStartupRepairsMissingMaterializedLoginWithoutChangingExistingDevices(t *testing.T) {
	persistence := newCredentialSessionStore(t)
	ctx := context.Background()
	old := mustCreateStaticSession(t, persistence, "existing", "openai", "existing-key")
	repo := persistence.ClientDisguiseRepository()
	existing, err := repo.GetLogin(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	session := testStaticCredentialSession("missing-login", "openai", "key")
	if err := resolveStaticCredentialSubject(&session, persistence.credentialSigning.signer); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.credentialSessions.Create(ctx, &session); err != nil {
		t.Fatal(err)
	}
	if err := initializeDisguiseLogins(ctx, persistence.db); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SelectProfile(ctx, session.ID, clientdisguise.BuiltinProfiles()[0].ID); err != nil {
		t.Fatalf("repaired login cannot save profile: %v", err)
	}
	after, err := repo.GetLogin(ctx, old.ID)
	if err != nil || after.DeviceID != existing.DeviceID || after.GenerationID != existing.GenerationID {
		t.Fatalf("startup changed existing identity: %+v, %v", after, err)
	}
}
