package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestCodexMaintenanceCatalogTracksRetargetSubjectOriginAndDelete(t *testing.T) {
	ctx := context.Background()
	storage, err := NewSQLiteStore(filepath.Join(t.TempDir(), "maintenance-catalog.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	createMaintenanceSession(t, storage, "session-a", "account-a")
	createMaintenanceSession(t, storage, "session-b", "account-b")
	provider := maintenanceProvider("route-a", "session-a", "https://OLD.example:443/v1/responses")
	if err := storage.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	// Reachability follows route existence rather than health/enablement. A
	// temporarily disabled target must not orphan authentication state.
	if err := storage.db.Model(&model.Provider{}).Where("id = ?", provider.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	assertMaintenanceAuthority(t, storage, "account-a", "https://old.example")

	current, err := storage.GetProvider(ctx, provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.APITypes[0].BaseURL = "wss://new.example/socket"
	current.CredentialSessions[0].Credential.SessionID = "session-b"
	if err := storage.UpdateProvider(ctx, current); err != nil {
		t.Fatal(err)
	}
	assertMaintenanceAuthority(t, storage, "account-b", "https://new.example")

	accountC, _ := credentialsession.AccountSubject("account-c")
	ownedCtx, release, err := storage.WithCredentialSessionMutations(ctx, []string{"session-b"})
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := storage.UpdateCredentialSessionCAS(
		ownedCtx, "session-b", 1, `{"access_token":"rotated"}`, accountC,
		credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "account-c"},
	)
	release()
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	assertMaintenanceAuthority(t, storage, "account-c", "https://new.example")

	if err := storage.DeleteProvider(ctx, provider.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := storage.LoadCodexMaintenanceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if routes := snapshot.Routes(); len(routes) != 0 {
		t.Fatalf("catalog after provider delete = %+v", routes)
	}
}

func TestCodexMaintenanceCatalogIsAtomicAndRejectsIncompleteRows(t *testing.T) {
	ctx := context.Background()
	storage, err := NewSQLiteStore(filepath.Join(t.TempDir(), "maintenance-incomplete.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	createMaintenanceSession(t, storage, "session-a", "account-a")
	provider := maintenanceProvider("route-a", "session-a", "https://example.test")
	if err := storage.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	if err := storage.db.Where("route_target_id = ?", provider.ID).Delete(&credentialsession.RouteBinding{}).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot, err := storage.LoadCodexMaintenanceCatalog(ctx); err == nil || len(snapshot.Routes()) != 0 {
		t.Fatalf("incomplete catalog = %+v, %v", snapshot.Routes(), err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := storage.LoadCodexMaintenanceCatalog(canceled); err == nil {
		t.Fatal("catalog accepted a canceled list operation")
	}
	if _, err := storage.LoadCodexMaintenanceCatalog(nil); err == nil {
		t.Fatal("catalog accepted nil context")
	}
	if _, err := (*SQLiteStore)(nil).LoadCodexMaintenanceCatalog(ctx); err == nil {
		t.Fatal("nil store loaded catalog")
	}
}

func TestCodexMaintenanceCatalogIncludesOnlyCodexFinalOriginsAndOwnsBytes(t *testing.T) {
	ctx := context.Background()
	storage, err := NewSQLiteStore(filepath.Join(t.TempDir(), "maintenance-filter.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	createMaintenanceSession(t, storage, "session-a", "account-a")
	provider := maintenanceProvider("route-a", "session-a", "https://codex.example/v1")
	provider.APITypes = append(provider.APITypes, model.ProviderAPIType{APIType: "claude", BaseURL: "https://claude.example"})
	provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{
		APIType: "claude", Credential: credentialsession.Snapshot{SessionID: "session-a"},
	})
	if err := storage.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	snapshot, err := storage.LoadCodexMaintenanceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	routes := snapshot.Routes()
	if len(routes) != 1 || routes[0].FinalURL != "https://codex.example/v1" {
		t.Fatalf("catalog routes = %+v", routes)
	}
	routes[0].Subject.Value[0] = 'X'
	fresh := snapshot.Routes()
	if string(fresh[0].Subject.Value) != "account-a" {
		t.Fatalf("catalog subject was mutated through returned bytes: %q", fresh[0].Subject.Value)
	}
}

func TestCodexMaintenanceCatalogSkipsReauthenticationPlaceholdersUntilResolved(t *testing.T) {
	ctx := context.Background()
	storage, err := NewSQLiteStore(filepath.Join(t.TempDir(), "maintenance-recovery.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	placeholder := &credentialsession.Session{
		ID: "session-recovery", Name: "Recovery", Kind: credentialsession.KindChatGPT, Version: 1,
		SubjectKind: credentialsession.SubjectPending,
		AuthState:   credentialsession.AuthState{Status: credentialsession.AuthStatusReauthRequired},
	}
	if _, err := storage.CreateCredentialSession(ctx, placeholder); err != nil {
		t.Fatal(err)
	}
	provider := maintenanceProvider("route-recovery", placeholder.ID, "https://codex.example")
	if err := storage.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}

	snapshot, err := storage.LoadCodexMaintenanceCatalog(ctx)
	if err != nil || len(snapshot.Routes()) != 0 {
		t.Fatalf("placeholder catalog = (%+v, %v)", snapshot.Routes(), err)
	}
	account, err := credentialsession.AccountSubject("account-restored")
	if err != nil {
		t.Fatal(err)
	}
	ownedCtx, release, err := storage.WithCredentialSessionMutations(ctx, []string{placeholder.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := storage.UpdateCredentialSessionCAS(
		ownedCtx, placeholder.ID, placeholder.Version, `{"access_token":"restored"}`, account,
		credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "account-restored"},
	)
	release()
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	assertMaintenanceAuthority(t, storage, "account-restored", "https://codex.example")
}

func TestCodexMaintenanceCatalogRejectsCredentialKindCorruption(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  string
	}{
		{name: "unknown kind", column: "kind", value: "unknown"},
		{name: "incompatible kind", column: "kind", value: string(credentialsession.KindAPIKey)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storage, err := NewSQLiteStore(filepath.Join(t.TempDir(), "maintenance-corrupt.db"), internal.RealClock{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer storage.Close()
			createMaintenanceSession(t, storage, "session-a", "account-a")
			provider := maintenanceProvider("route-a", "session-a", "https://codex.example")
			if err := storage.CreateProvider(ctx, &provider); err != nil {
				t.Fatal(err)
			}
			if err := storage.db.Model(&credentialsession.Session{}).Where("id = ?", "session-a").UpdateColumn(test.column, test.value).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := storage.LoadCodexMaintenanceCatalog(ctx); err == nil {
				t.Fatal("catalog accepted a corrupt credential session")
			}
		})
	}
}

func maintenanceProvider(id, sessionID, finalURL string) model.Provider {
	return model.Provider{
		ID: id, Name: id, Vendor: "openai", Enabled: true,
		APITypes: []model.ProviderAPIType{{APIType: codexMaintenanceAPIType, BaseURL: finalURL}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			APIType: codexMaintenanceAPIType, Credential: credentialsession.Snapshot{SessionID: sessionID},
		}},
	}
}

func createMaintenanceSession(t *testing.T, storage *SQLiteStore, id, accountID string) {
	t.Helper()
	now := time.Now().UTC()
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	session := &credentialsession.Session{
		ID: id, Kind: credentialsession.KindChatGPT,
		SecretData: `{"access_token":"test"}`, Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: accountID},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.CreateCredentialSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
}

func assertMaintenanceAuthority(t *testing.T, storage *SQLiteStore, accountID, origin string) {
	t.Helper()
	snapshot, err := storage.LoadCodexMaintenanceCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reachable, err := snapshot.ReachableCookieAuthorities()
	if err != nil {
		t.Fatal(err)
	}
	if len(reachable) != 1 {
		t.Fatalf("reachable authorities = %d", len(reachable))
	}
	gotAccount, ok := reachable[0].Authority().Subject().AccountID()
	if !ok || gotAccount != accountID || reachable[0].Authority().Origin().String() != origin {
		t.Fatalf("authority account/origin = %q/%q", gotAccount, reachable[0].Authority().Origin())
	}
}
