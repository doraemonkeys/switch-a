package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

func TestSQLiteCredentialSessionCapabilitiesTrackSubjectsAndLiveRoutes(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()

	if err := store.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{version: "h-old"}); err != nil {
		t.Fatal(err)
	}
	oldSession := mustCreateStaticSession(t, store, "session-old", "openai", "secret-old")
	if err := store.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{version: "h-current"}); err != nil {
		t.Fatal(err)
	}
	currentSession := mustCreateStaticSession(t, store, "session-current", "openai", "secret-current")
	pendingSession := mustCreateStaticSession(t, store, "session-pending", "openai", "secret-pending")
	if err := store.db.Model(&credentialsession.Session{}).Where("id = ?", pendingSession.ID).Updates(map[string]any{
		"subject_kind": credentialsession.SubjectPending, "subject_value": nil, "subject_key_version": "",
	}).Error; err != nil {
		t.Fatal(err)
	}

	inventory, err := store.InspectCodexPersistence(ctx)
	if err != nil {
		t.Fatalf("InspectCodexPersistence() error = %v", err)
	}
	if want := []string{"h-current", "h-old"}; !reflect.DeepEqual(inventory.CredentialHMACVersions, want) {
		t.Fatalf("CredentialHMACVersions = %#v, want %#v", inventory.CredentialHMACVersions, want)
	}
	if got := inventory.PendingStaticCredentialSessionIDs; !reflect.DeepEqual(got, []string{pendingSession.ID}) {
		t.Fatalf("unbound pending static IDs = %#v", got)
	}

	provider := providerWithSessionRefs("route-disabled", "openai", map[string]string{
		"codex": oldSession.ID,
	})
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	provider.Enabled = false
	if err := store.UpdateProvider(ctx, provider); err != nil {
		t.Fatalf("UpdateProvider(disabled) error = %v", err)
	}
	enabledRouteTargetIDs, err := store.CredentialSessionEnabledRouteTargetIDs(ctx, " "+oldSession.ID+" ")
	if err != nil || len(enabledRouteTargetIDs) != 0 {
		t.Fatalf("CredentialSessionEnabledRouteTargetIDs(disabled) = (%#v, %v)", enabledRouteTargetIDs, err)
	}
	if err := store.BindCredentialSession(ctx, credentialsession.RouteBinding{
		RouteTargetID: provider.ID, APIType: "codex", SessionID: pendingSession.ID,
	}); err != nil {
		t.Fatal(err)
	}
	inventory, err = store.InspectCodexPersistence(ctx)
	if err != nil || inventory.PendingStaticCredentialSubjectCount() != 1 {
		t.Fatalf("disabled pending inventory = (%+v, %v)", inventory, err)
	}
	if err := store.BindCredentialSession(ctx, credentialsession.RouteBinding{
		RouteTargetID: provider.ID, APIType: "codex", SessionID: oldSession.ID,
	}); err != nil {
		t.Fatal(err)
	}

	provider.Enabled = true
	if err := store.UpdateProvider(ctx, provider); err != nil {
		t.Fatalf("UpdateProvider(enabled) error = %v", err)
	}
	enabledRouteTargetIDs, err = store.CredentialSessionEnabledRouteTargetIDs(ctx, oldSession.ID)
	if err != nil || !reflect.DeepEqual(enabledRouteTargetIDs, []string{provider.ID}) {
		t.Fatalf("CredentialSessionEnabledRouteTargetIDs(enabled) = (%#v, %v)", enabledRouteTargetIDs, err)
	}
	if err := store.BindCredentialSession(ctx, credentialsession.RouteBinding{
		RouteTargetID: provider.ID,
		APIType:       "codex",
		SessionID:     pendingSession.ID,
	}); err != nil {
		t.Fatalf("BindCredentialSession(pending) error = %v", err)
	}
	inventory, err = store.InspectCodexPersistence(ctx)
	if err != nil || inventory.PendingStaticCredentialSubjectCount() != 1 {
		t.Fatalf("enabled pending inventory = (%+v, %v)", inventory, err)
	}
	routeIDs, err := store.CredentialSessionRouteTargetIDs(ctx, pendingSession.ID)
	if err != nil || !reflect.DeepEqual(routeIDs, []string{provider.ID}) {
		t.Fatalf("CredentialSessionRouteTargetIDs() = (%#v, %v)", routeIDs, err)
	}

	if err := store.BindCredentialSession(ctx, credentialsession.RouteBinding{
		RouteTargetID: provider.ID,
		APIType:       "codex",
		SessionID:     currentSession.ID,
	}); err != nil {
		t.Fatalf("BindCredentialSession() error = %v", err)
	}
	oldRouteIDs, err := store.CredentialSessionRouteTargetIDs(ctx, oldSession.ID)
	if err != nil || len(oldRouteIDs) != 0 {
		t.Fatalf("old session routes = (%#v, %v), want none", oldRouteIDs, err)
	}
	currentRouteIDs, err := store.CredentialSessionRouteTargetIDs(ctx, currentSession.ID)
	if err != nil || !reflect.DeepEqual(currentRouteIDs, []string{provider.ID}) {
		t.Fatalf("current session routes = (%#v, %v)", currentRouteIDs, err)
	}

	if err := store.DeleteCredentialSession(ctx, pendingSession.ID); err != nil {
		t.Fatalf("DeleteCredentialSession(pending) error = %v", err)
	}
	inventory, err = store.InspectCodexPersistence(ctx)
	if err != nil || inventory.PendingStaticCredentialSubjectCount() != 0 {
		t.Fatalf("inventory after pending deletion = (%+v, %v)", inventory, err)
	}
	if err := store.DeleteCredentialSession(ctx, "missing-session"); !errors.Is(err, credentialsession.ErrNotFound) {
		t.Fatalf("DeleteCredentialSession(missing) error = %v", err)
	}
}

func TestSQLiteCredentialSessionCapabilitiesAllowExplicitReauthRecovery(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	recovery := &credentialsession.Session{
		ID: "login-recovery", Kind: credentialsession.KindChatGPT,
		SecretData: `{"access_token":"expired","refresh_token":"invalid"}`,
		Version:    1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusReauthRequired, StatusReason: "legacy_identity_unresolved",
		},
	}
	if err := recovery.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCredentialSession(ctx, recovery); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProvider(ctx, providerWithSessionRefs(
		"recovery-route", "openai", map[string]string{"codex": recovery.ID},
	)); err != nil {
		t.Fatal(err)
	}
	inventory, err := store.InspectCodexPersistence(ctx)
	if err != nil || inventory.PendingStaticCredentialSubjectCount() != 0 ||
		!reflect.DeepEqual(inventory.PendingChatGPTReauthSessionIDs, []string{recovery.ID}) {
		t.Fatalf("recovery inventory = (%+v, %v)", inventory, err)
	}
	if _, err := store.ResolveCredentialSession(ctx, "recovery-route", "codex"); err != nil {
		t.Fatalf("durable recovery binding should remain inspectable: %v", err)
	}
}

func TestSQLiteCredentialSessionMutationLeaseSerializesSameSession(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	mustCreateStaticSession(t, store, "serialized", "openai", "secret")

	owned, release, err := store.WithCredentialSessionMutations(ctx, []string{"serialized"})
	if err != nil {
		t.Fatalf("first mutation lease error = %v", err)
	}
	defer release()
	if _, err := store.GetCredentialSession(owned, "serialized"); err != nil {
		t.Fatalf("owned context cannot read session: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	if _, blockedRelease, err := store.WithCredentialSessionMutations(waitCtx, []string{"serialized"}); !errors.Is(err, context.DeadlineExceeded) {
		if blockedRelease != nil {
			blockedRelease()
		}
		t.Fatalf("contending mutation lease error = %v, want deadline", err)
	}

	release()
	reacquired, reacquiredRelease, err := store.WithCredentialSessionMutations(ctx, []string{"serialized"})
	if err != nil {
		t.Fatalf("reacquired mutation lease error = %v", err)
	}
	defer reacquiredRelease()
	if _, err := store.GetCredentialSession(reacquired, "serialized"); err != nil {
		t.Fatalf("reacquired context cannot read session: %v", err)
	}
}

func TestSQLiteCredentialSessionUpdateFailureKeepsDurableVersion(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	created := mustCreateStaticSession(t, store, "stable", "openai", "secret")

	owned, release, err := store.WithCredentialSessionMutations(ctx, []string{created.ID, "missing"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	nextVersion, err := store.UpdateCredentialSessionCAS(
		owned,
		created.ID,
		created.Version,
		created.SecretData,
		credentialsession.PendingSubject(),
		created.AuthState,
	)
	if err != nil || nextVersion != created.Version+1 {
		t.Fatalf("same-secret CAS = (%d, %v)", nextVersion, err)
	}
	unchangedSubject, err := store.GetCredentialSession(owned, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchangedSubject.Subject(), created.Subject()) {
		t.Fatalf("same-secret CAS replaced derived subject: got %#v want %#v", unchangedSubject.Subject(), created.Subject())
	}

	signErr := errors.New("subject signer unavailable")
	if err := store.FinalizeStaticCredentialSubjects(ctx, migrationSubjectSigner{err: signErr}); err != nil {
		t.Fatalf("install failing signer: %v", err)
	}
	if _, err := store.UpdateCredentialSessionCAS(
		owned,
		created.ID,
		nextVersion,
		"rotated-secret",
		unchangedSubject.Subject(),
		unchangedSubject.AuthState,
	); !errors.Is(err, signErr) {
		t.Fatalf("signer failure = %v, want %v", err, signErr)
	}
	afterFailure, err := store.GetCredentialSession(owned, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Version != nextVersion || afterFailure.SecretData != created.SecretData {
		t.Fatalf("failed rotation mutated durable state: %#v", afterFailure)
	}

	if _, err := store.UpdateCredentialSessionCAS(
		owned,
		"missing",
		1,
		"secret",
		credentialsession.PendingSubject(),
		credentialsession.AuthState{},
	); !errors.Is(err, credentialsession.ErrNotFound) {
		t.Fatalf("missing session CAS error = %v", err)
	}
	if err := store.UpdateCredentialSessionAuthState(
		owned,
		"missing",
		credentialsession.AuthState{},
	); !errors.Is(err, credentialsession.ErrNotFound) {
		t.Fatalf("missing session auth update error = %v", err)
	}
}

func TestSQLiteCredentialSessionCapabilitiesPropagateContextCancellation(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.InspectCodexPersistence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("InspectCodexPersistence(canceled) error = %v", err)
	}
	if _, err := store.CredentialSessionEnabledRouteTargetIDs(ctx, "session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("CredentialSessionEnabledRouteTargetIDs(canceled) error = %v", err)
	}
	if _, err := store.CreateCredentialSession(ctx, &credentialsession.Session{
		ID:         "canceled",
		Kind:       credentialsession.KindAPIKey,
		SecretData: "secret",
		Version:    1,
		AuthState:  credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateCredentialSession(canceled) error = %v", err)
	}
	if err := store.DeleteCredentialSession(ctx, "session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteCredentialSession(canceled) error = %v", err)
	}
}

func TestApplyConfigImportCredentialSessionCASAndRollback(t *testing.T) {
	t.Run("rotate and idempotently replay", func(t *testing.T) {
		store := newCredentialSessionStore(t)
		ctx := context.Background()
		created := mustCreateStaticSession(t, store, "imported", "openai", "secret-1")

		rotated := created.Clone()
		rotated.SecretData = "secret-2"
		if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
			RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
			CredentialSessions: []credentialsession.Session{*rotated},
		}); err != nil {
			t.Fatalf("ApplyConfigImport(rotation) error = %v", err)
		}
		current, err := store.GetCredentialSession(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Version != 2 || current.SecretData != "secret-2" ||
			reflect.DeepEqual(current.Subject(), created.Subject()) {
			t.Fatalf("rotated session = %#v", current)
		}

		if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
			RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
			CredentialSessions: []credentialsession.Session{*current.Clone()},
		}); err != nil {
			t.Fatalf("ApplyConfigImport(idempotent replay) error = %v", err)
		}
		replayed, err := store.GetCredentialSession(ctx, created.ID)
		if err != nil || replayed.Version != current.Version {
			t.Fatalf("idempotent replay = (%#v, %v)", replayed, err)
		}
	})

	for _, testCase := range []struct {
		name   string
		mutate func(*credentialsession.Session)
		match  string
	}{
		{
			name: "immutable kind",
			mutate: func(candidate *credentialsession.Session) {
				candidate.Kind = credentialsession.KindChatGPT
			},
			match: "kind is immutable",
		},
		{
			name: "version mismatch",
			mutate: func(candidate *credentialsession.Session) {
				candidate.Version++
			},
			match: "version mismatch",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := newCredentialSessionStore(t)
			ctx := context.Background()
			created := mustCreateStaticSession(t, store, "imported", "openai", "secret-1")
			candidate := created.Clone()
			testCase.mutate(candidate)

			err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
				RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
				CredentialSessions: []credentialsession.Session{*candidate},
			})
			if err == nil || !strings.Contains(err.Error(), testCase.match) {
				t.Fatalf("ApplyConfigImport() error = %v, want containing %q", err, testCase.match)
			}
			current, getErr := store.GetCredentialSession(ctx, created.ID)
			if getErr != nil || current.Version != created.Version ||
				current.SecretData != created.SecretData || current.Kind != created.Kind {
				t.Fatalf("failed import mutated session: current=%#v err=%v", current, getErr)
			}
		})
	}
}
