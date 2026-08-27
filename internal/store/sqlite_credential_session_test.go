package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func newCredentialSessionStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(
		filepath.Join(t.TempDir(), "credential-sessions.db"),
		&mockClock{now: time.Date(2026, 8, 27, 3, 4, 5, 0, time.UTC)},
		nil,
		migrationSubjectSigner{version: "h-current"},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustCreateStaticSession(t *testing.T, store *SQLiteStore, id, vendor, secret string) *credentialsession.Session {
	t.Helper()
	session := &credentialsession.Session{
		ID: id, Vendor: vendor, Kind: credentialsession.KindAPIKey,
		SecretData: secret, Version: 1, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if err := session.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateCredentialSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func providerWithSessionRefs(id, vendor string, refs map[string]string) *model.Provider {
	provider := &model.Provider{ID: id, Name: id, Vendor: vendor, Enabled: true}
	for apiType, sessionID := range refs {
		provider.APITypes = append(provider.APITypes, model.ProviderAPIType{
			ProviderID: id, APIType: apiType, BaseURL: "https://example.com/" + apiType,
		})
		provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{
			RouteTargetID: id, APIType: apiType,
			Credential: credentialsession.Snapshot{SessionID: sessionID},
		})
	}
	return provider
}

func TestSQLiteCredentialSessionSharedReferencesAndProviderDeletion(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	created := mustCreateStaticSession(t, store, "shared", "openai", "secret")
	if created.SubjectKind != credentialsession.SubjectKeyedDigest || created.SubjectKeyVersion != "h-current" || len(created.SubjectValue) != 32 {
		t.Fatalf("static subject = %#v", created.Subject())
	}
	for _, provider := range []*model.Provider{
		providerWithSessionRefs("p1", "openai", map[string]string{"codex": "shared", "responses": "shared"}),
		providerWithSessionRefs("p2", "openai", map[string]string{"codex": "shared"}),
	} {
		if err := store.CreateProvider(ctx, provider); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := store.ResolveCredentialSession(ctx, "p1", "responses")
	if err != nil || resolved.Credential.SessionID != "shared" || resolved.Credential.SecretData != "secret" {
		t.Fatalf("ResolveCredentialSession() = (%#v, %v)", resolved, err)
	}
	if err := store.DeleteCredentialSession(ctx, "shared"); !errors.Is(err, credentialsession.ErrSessionReferenced) {
		t.Fatalf("DeleteCredentialSession(referenced) error = %v", err)
	}
	if err := store.DeleteProvider(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveCredentialSession(ctx, "p2", "codex"); err != nil {
		t.Fatalf("shared session was removed with first provider: %v", err)
	}
	if err := store.DeleteProvider(ctx, "p2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCredentialSession(ctx, "shared"); err != nil {
		t.Fatalf("unreferenced session should survive route deletion: %v", err)
	}
	if err := store.DeleteCredentialSession(ctx, "shared"); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteCredentialSessionMutationLeaseCASAndSubjectRotation(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	created := mustCreateStaticSession(t, store, "rotating", "openai", "secret-1")
	firstDigest := append([]byte(nil), created.SubjectValue...)
	if _, err := store.UpdateCredentialSessionCAS(ctx, created.ID, 1, "secret-2", created.Subject(), created.AuthState); err == nil {
		t.Fatal("UpdateCredentialSessionCAS without lease succeeded")
	}
	owned, release, err := store.WithCredentialSessionMutations(ctx, []string{created.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	next, err := store.UpdateCredentialSessionCAS(owned, created.ID, 1, "secret-2", created.Subject(), created.AuthState)
	if err != nil || next != 2 {
		t.Fatalf("UpdateCredentialSessionCAS() = (%d, %v)", next, err)
	}
	rotated, err := store.GetCredentialSession(owned, created.ID)
	if err != nil || bytes.Equal(firstDigest, rotated.SubjectValue) {
		t.Fatalf("rotated subject = (%#v, %v)", rotated, err)
	}
	if _, err := store.UpdateCredentialSessionCAS(owned, created.ID, 1, "stale", rotated.Subject(), rotated.AuthState); !errors.Is(err, credentialsession.ErrVersionConflict) {
		t.Fatalf("stale CAS error = %v", err)
	}
	if err := store.UpdateCredentialSessionAuthState(owned, created.ID, credentialsession.AuthState{
		Status: credentialsession.AuthStatusReauthRequired, StatusReason: "invalid_grant",
	}); err != nil {
		t.Fatal(err)
	}
	afterAuth, err := store.GetCredentialSession(owned, created.ID)
	if err != nil || afterAuth.Version != 3 || afterAuth.AuthState.Status != credentialsession.AuthStatusReauthRequired {
		t.Fatalf("auth CAS result = (%#v, %v)", afterAuth, err)
	}
}

func TestSQLiteCredentialSessionAllowsSameAccountAcrossIndependentSessions(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	subject, err := credentialsession.AccountSubject("account-shared")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"login-a", "login-b"} {
		session := &credentialsession.Session{
			ID: id, Vendor: "openai", Kind: credentialsession.KindChatGPT,
			SecretData: `{"access_token":"token","refresh_token":"refresh"}`,
			Version:    1, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "account-shared"},
		}
		if err := session.SetSubject(subject); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateCredentialSession(ctx, session); err != nil {
			t.Fatalf("CreateCredentialSession(%q) error = %v", id, err)
		}
	}
	sessions, err := store.ListCredentialSessions(ctx)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("ListCredentialSessions() = (%#v, %v)", sessions, err)
	}
	if err := store.CreateProvider(ctx, providerWithSessionRefs("wrong-vendor", "anthropic", map[string]string{"codex": "login-a"})); !errors.Is(err, credentialsession.ErrInvalidRouteBinding) {
		t.Fatalf("CreateProvider(vendor mismatch) error = %v", err)
	}
}

func TestSQLiteCredentialSessionForeignKeysRejectOrphansAndReferencedDelete(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	mustCreateStaticSession(t, store, "bound", "openai", "secret")
	if err := store.CreateProvider(ctx, providerWithSessionRefs(
		"route", "openai", map[string]string{"codex": "bound"},
	)); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DELETE FROM route_target_credentials WHERE route_target_id = ?", "route").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 7, 0, 0, 0, time.UTC)
	if err := store.db.Exec(`INSERT INTO route_target_credentials
		(route_target_id, api_type, session_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"route", "codex", "missing", now, now).Error; err == nil {
		t.Fatal("foreign key accepted an orphan credential-session binding")
	}

	if err := store.BindCredentialSession(ctx, credentialsession.RouteBinding{
		RouteTargetID: "route", APIType: "codex", SessionID: "bound",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DELETE FROM credential_sessions WHERE id = ?", "bound").Error; err == nil {
		t.Fatal("foreign key allowed deletion of a referenced credential session")
	}
}

func TestSQLiteCredentialSessionConcurrentBindDeletePreservesReferentialIntegrity(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	mustCreateStaticSession(t, store, "seed-session", "openai", "seed-secret")
	mustCreateStaticSession(t, store, "race-session", "openai", "secret")
	if err := store.CreateProvider(ctx, providerWithSessionRefs(
		"race-route", "openai", map[string]string{"codex": "seed-session"},
	)); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByOperation := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		errorsByOperation <- store.BindCredentialSession(ctx, credentialsession.RouteBinding{
			RouteTargetID: "race-route", APIType: "codex", SessionID: "race-session",
		})
	}()
	go func() {
		defer workers.Done()
		<-start
		errorsByOperation <- store.DeleteCredentialSession(ctx, "race-session")
	}()
	close(start)
	workers.Wait()
	close(errorsByOperation)

	successes := 0
	for err := range errorsByOperation {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, credentialsession.ErrNotFound) && !errors.Is(err, credentialsession.ErrSessionReferenced) {
			t.Fatalf("concurrent Bind/Delete returned an unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent Bind/Delete successes = %d, want exactly one", successes)
	}
	var orphans int64
	if err := store.db.Raw(`SELECT COUNT(*) FROM route_target_credentials AS bindings
		LEFT JOIN credential_sessions AS sessions ON sessions.id = bindings.session_id
		WHERE sessions.id IS NULL`).Scan(&orphans).Error; err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("concurrent Bind/Delete left %d orphan bindings", orphans)
	}
}
