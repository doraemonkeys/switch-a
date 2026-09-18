package store

import (
	"context"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"gorm.io/gorm"
)

func (s *SQLiteStore) CreateCredentialSession(ctx context.Context, session *credentialsession.Session) (*credentialsession.Session, error) {
	s.credentialSigning.mu.RLock()
	defer s.credentialSigning.mu.RUnlock()
	var created *credentialsession.Session
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = s.createCredentialSessionInTransaction(ctx, tx, session)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create credential session: %w", err)
	}
	return created, nil
}

// Every creation path must commit the credential and its device together, before
// profile administration or account maintenance can observe the new session.
// The caller owns the transaction and credential-signing read lock.
func (s *SQLiteStore) createCredentialSessionInTransaction(ctx context.Context, tx *gorm.DB, session *credentialsession.Session) (*credentialsession.Session, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: session is nil", credentialsession.ErrInvalidSession)
	}
	if err := resolveStaticCredentialSubject(session, s.credentialSigning.signer); err != nil {
		return nil, err
	}
	if err := preserveRestoredStaticSubject(ctx, tx, session, s.credentialSigning.signer); err != nil {
		return nil, err
	}
	repository, err := s.credentialSessions.WithDB(tx)
	if err != nil {
		return nil, err
	}
	created, err := repository.Create(ctx, session)
	if err != nil {
		return nil, err
	}
	if err := syncDisguiseLogin(ctx, tx, created.ID, created.Subject()); err != nil {
		return nil, fmt.Errorf("initialize login identity for credential session %q: %w", created.ID, err)
	}
	return created, nil
}

func (s *SQLiteStore) GetCredentialSession(ctx context.Context, sessionID string) (*credentialsession.Session, error) {
	return s.credentialSessions.Get(ctx, sessionID)
}

func (s *SQLiteStore) ListCredentialSessions(ctx context.Context) ([]credentialsession.Session, error) {
	return s.credentialSessions.List(ctx)
}

func (s *SQLiteStore) ResolveCredentialSession(ctx context.Context, routeTargetID, apiType, transport string) (credentialsession.RouteSnapshot, error) {
	return s.credentialSessions.Resolve(ctx, routeTargetID, apiType, transport)
}

func (s *SQLiteStore) BindCredentialSession(ctx context.Context, binding credentialsession.RouteBinding) error {
	return s.credentialSessions.Bind(ctx, binding)
}

func (s *SQLiteStore) CredentialSessionRouteTargetIDs(ctx context.Context, sessionID string) ([]string, error) {
	return s.credentialSessions.ListRouteTargetIDs(ctx, sessionID)
}

func (s *SQLiteStore) CredentialSessionRouteReferences(ctx context.Context, sessionID string) ([]credentialsession.RouteReference, error) {
	return s.credentialSessions.ListRouteReferences(ctx, sessionID)
}

func (s *SQLiteStore) DeleteCredentialSession(ctx context.Context, sessionID string) error {
	ownedCtx, release, err := s.credentialMutations.With(ctx, []string{sessionID})
	if err != nil {
		return err
	}
	defer release()
	s.credentialSigning.mu.RLock()
	defer s.credentialSigning.mu.RUnlock()
	return s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
		repository, err := s.credentialSessions.WithDB(tx)
		if err != nil {
			return err
		}
		if err := repository.DeleteIfUnreferenced(ownedCtx, sessionID); err != nil {
			return err
		}
		return s.ClientDisguiseRepository().WithDB(tx).RetireLogin(ownedCtx, sessionID)
	})
}

func (s *SQLiteStore) RenameCredentialSessionCAS(ctx context.Context, sessionID string, expectedVersion int64, name string) (int64, error) {
	ownedCtx, release, err := s.credentialMutations.With(ctx, []string{sessionID})
	if err != nil {
		return 0, err
	}
	defer release()
	return s.credentialSessions.RenameCAS(ownedCtx, sessionID, expectedVersion, name)
}

func (s *SQLiteStore) WithCredentialSessionMutations(
	ctx context.Context,
	sessionIDs []string,
) (context.Context, func(), error) {
	return s.credentialMutations.With(ctx, sessionIDs)
}

func (s *SQLiteStore) UpdateCredentialSessionCAS(
	ctx context.Context,
	sessionID string,
	expectedVersion int64,
	secretData string,
	subject credentialsession.Subject,
	authState credentialsession.AuthState,
) (int64, error) {
	if !s.credentialMutations.Owns(ctx, sessionID) {
		return 0, fmt.Errorf("update credential session %q: mutation lease is required", sessionID)
	}
	s.credentialSigning.mu.RLock()
	defer s.credentialSigning.mu.RUnlock()
	var version int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		version, err = s.updateCredentialSessionInTransaction(ctx, tx, sessionID, expectedVersion, secretData, subject, authState)
		return err
	})
	return version, err
}

// Imports and direct credential edits share account-change semantics: preserve
// the device on refresh, and retire the old generation on account replacement.
// The caller owns the mutation lease, transaction and signing read lock.
func (s *SQLiteStore) updateCredentialSessionInTransaction(
	ctx context.Context,
	tx *gorm.DB,
	sessionID string,
	expectedVersion int64,
	secretData string,
	subject credentialsession.Subject,
	authState credentialsession.AuthState,
) (int64, error) {
	repository, err := s.credentialSessions.WithDB(tx)
	if err != nil {
		return 0, err
	}
	current, err := repository.Get(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if current.Kind == credentialsession.KindAPIKey {
		if secretData == current.SecretData {
			subject = current.Subject()
		} else {
			candidate := current.Clone()
			candidate.SecretData = secretData
			if err := resolveStaticCredentialSubject(candidate, s.credentialSigning.signer); err != nil {
				return 0, err
			}
			subject = candidate.Subject()
		}
	}
	version, err := repository.UpdateCredentialCAS(ctx, sessionID, expectedVersion, secretData, subject, authState)
	if err != nil {
		return 0, err
	}
	if err := syncDisguiseLogin(ctx, tx, sessionID, subject); err != nil {
		return 0, fmt.Errorf("synchronize login identity for credential session %q: %w", sessionID, err)
	}
	return version, nil
}

func resolveStaticCredentialSubject(session *credentialsession.Session, signer StaticCredentialSubjectSigner) error {
	if session == nil || session.Kind != credentialsession.KindAPIKey {
		return nil
	}
	if signer == nil {
		return session.SetSubject(credentialsession.PendingSubject())
	}
	subject, err := staticSubject(session.SecretData, signer)
	if err != nil {
		return err
	}
	return session.SetSubject(subject)
}

func (s *SQLiteStore) UpdateCredentialSessionAuthState(
	ctx context.Context,
	sessionID string,
	authState credentialsession.AuthState,
) error {
	if !s.credentialMutations.Owns(ctx, sessionID) {
		return fmt.Errorf("update credential session auth state %q: mutation lease is required", sessionID)
	}
	s.credentialSigning.mu.RLock()
	defer s.credentialSigning.mu.RUnlock()
	current, err := s.credentialSessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = s.credentialSessions.UpdateAuthStateCAS(ctx, sessionID, current.Version, authState)
	return err
}

// CredentialSessionEnabledRouteTargetIDs identifies the live owners that must
// pause before a refresh-capable credential can leave switch-a.
func (s *SQLiteStore) CredentialSessionEnabledRouteTargetIDs(ctx context.Context, sessionID string) ([]string, error) {
	return s.credentialSessions.ListEnabledRouteTargetIDs(ctx, sessionID)
}
