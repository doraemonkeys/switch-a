package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

func (s *SQLiteStore) CreateCredentialSession(ctx context.Context, session *credentialsession.Session) (*credentialsession.Session, error) {
	if err := s.resolveStaticCredentialSubject(session); err != nil {
		return nil, fmt.Errorf("create credential session: %w", err)
	}
	created, err := s.credentialSessions.Create(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("create credential session: %w", err)
	}
	return created, nil
}

func (s *SQLiteStore) GetCredentialSession(ctx context.Context, sessionID string) (*credentialsession.Session, error) {
	return s.credentialSessions.Get(ctx, sessionID)
}

func (s *SQLiteStore) ListCredentialSessions(ctx context.Context) ([]credentialsession.Session, error) {
	return s.credentialSessions.List(ctx)
}

func (s *SQLiteStore) ResolveCredentialSession(ctx context.Context, routeTargetID, apiType string) (credentialsession.RouteSnapshot, error) {
	return s.credentialSessions.Resolve(ctx, routeTargetID, apiType)
}

func (s *SQLiteStore) BindCredentialSession(ctx context.Context, binding credentialsession.RouteBinding) error {
	return s.credentialSessions.Bind(ctx, binding)
}

func (s *SQLiteStore) CredentialSessionRouteTargetIDs(ctx context.Context, sessionID string) ([]string, error) {
	return s.credentialSessions.ListRouteTargetIDs(ctx, sessionID)
}

func (s *SQLiteStore) DeleteCredentialSession(ctx context.Context, sessionID string) error {
	ownedCtx, release, err := s.credentialMutations.With(ctx, []string{sessionID})
	if err != nil {
		return err
	}
	defer release()
	return s.credentialSessions.DeleteIfUnreferenced(ownedCtx, sessionID)
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
	current, err := s.credentialSessions.Get(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if current.Kind == credentialsession.KindAPIKey {
		if secretData == current.SecretData {
			subject = current.Subject()
		} else {
			candidate := current.Clone()
			candidate.SecretData = secretData
			if err := s.resolveStaticCredentialSubject(candidate); err != nil {
				return 0, err
			}
			subject = candidate.Subject()
		}
	}
	return s.credentialSessions.UpdateCredentialCAS(ctx, sessionID, expectedVersion, secretData, subject, authState)
}

func (s *SQLiteStore) resolveStaticCredentialSubject(session *credentialsession.Session) error {
	if session == nil || session.Kind != credentialsession.KindAPIKey {
		return nil
	}
	if s.credentialSigner == nil {
		return session.SetSubject(credentialsession.PendingSubject())
	}
	input, err := credentialsession.StaticSubjectInput(session.Vendor, session.Kind, session.SecretData)
	if err != nil {
		return err
	}
	digest, err := s.credentialSigner.Sign(codexkeyring.HMACCredentialSubject, input)
	if err != nil {
		return fmt.Errorf("sign static credential subject: %w", err)
	}
	subject, err := credentialsession.KeyedDigestSubject(digest.Version, digest.Sum[:])
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
	current, err := s.credentialSessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = s.credentialSessions.UpdateAuthStateCAS(ctx, sessionID, current.Version, authState)
	return err
}

func (s *SQLiteStore) RequiredCredentialSubjectKeyVersions(ctx context.Context) ([]string, error) {
	var versions []string
	err := s.db.WithContext(ctx).Table("credential_sessions").
		Distinct("subject_key_version").
		Where("subject_kind = ? AND subject_key_version <> ''", credentialsession.SubjectKeyedDigest).
		Order("subject_key_version ASC").
		Pluck("subject_key_version", &versions).Error
	if err != nil {
		return nil, fmt.Errorf("list credential subject key versions: %w", err)
	}
	return versions, nil
}

func (s *SQLiteStore) CredentialSubjectsResolved(ctx context.Context) (bool, error) {
	var pending int64
	err := s.db.WithContext(ctx).Table("credential_sessions AS sessions").
		Joins("JOIN route_target_credentials AS bindings ON bindings.session_id = sessions.id").
		Joins("JOIN providers ON providers.id = bindings.route_target_id").
		Where("providers.enabled = ?", true).
		Where("sessions.subject_kind = ?", credentialsession.SubjectPending).
		Where("NOT (sessions.kind = ? AND sessions.auth_status = ?)",
			credentialsession.KindChatGPT, credentialsession.AuthStatusReauthRequired).
		Distinct("sessions.id").
		Count(&pending).Error
	if err != nil {
		return false, fmt.Errorf("count unresolved subjects on enabled credential routes: %w", err)
	}
	return pending == 0, nil
}

// CredentialSessionHasEnabledRoute reports whether exporting or refreshing a
// shared login session would affect at least one live route target.
func (s *SQLiteStore) CredentialSessionHasEnabledRoute(ctx context.Context, sessionID string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Table("route_target_credentials AS bindings").
		Joins("JOIN providers ON providers.id = bindings.route_target_id").
		Where("bindings.session_id = ? AND providers.enabled = ?", strings.TrimSpace(sessionID), true).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("count enabled routes for credential session %q: %w", sessionID, err)
	}
	return count != 0, nil
}
