package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"gorm.io/gorm"
)

// ProviderImportBundle is one reviewed, atomic set of session and route writes.
// A session update never rewrites routing configuration, and importing the same
// account twice intentionally creates two independently rotating sessions.
type ProviderImportBundle struct {
	Creates           []ProviderImportCreate
	CredentialUpdates []ProviderImportCredentialUpdate
	Receipt           *ProviderImportReceipt
}

type ProviderImportCreate struct {
	CandidateID string
	Provider    model.Provider
	Sessions    []credentialsession.Session
}

type ProviderImportCredentialUpdate struct {
	CandidateID     string
	SessionID       string
	ExpectedVersion int64
	SecretData      string
	Subject         credentialsession.Subject
	AuthState       credentialsession.AuthState
}

func (s *SQLiteStore) ApplyProviderImport(ctx context.Context, bundle *ProviderImportBundle) error {
	if bundle == nil || (len(bundle.Creates) == 0 && len(bundle.CredentialUpdates) == 0 && bundle.Receipt == nil) {
		return nil
	}
	if err := validateProviderImportBundle(bundle); err != nil {
		return fmt.Errorf("apply provider import: %w", err)
	}
	receipt, err := normalizeProviderImportReceipt(bundle.Receipt, s.clock.Now())
	if err != nil {
		return fmt.Errorf("apply provider import: normalize receipt: %w", err)
	}
	sessionIDs := providerImportMutationSessionIDs(bundle)
	ownedCtx, release, err := s.credentialMutations.With(ctx, sessionIDs)
	if err != nil {
		return fmt.Errorf("apply provider import: %w", err)
	}
	defer release()

	err = s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
		return s.applyProviderImportTransaction(ownedCtx, tx, bundle, receipt)
	})
	if err != nil {
		return fmt.Errorf("apply provider import: %w", err)
	}
	return nil
}

func (s *SQLiteStore) applyProviderImportTransaction(
	ctx context.Context,
	tx *gorm.DB,
	bundle *ProviderImportBundle,
	receipt *ProviderImportReceipt,
) error {
	if err := reserveProviderImportReceipt(tx, receipt, s.clock.Now()); err != nil {
		return err
	}
	repository, err := s.credentialSessions.WithDB(tx)
	if err != nil {
		return err
	}
	if err := s.createImportedProviders(ctx, tx, repository, bundle.Creates); err != nil {
		return err
	}
	return updateImportedCredentialSessions(ctx, repository, bundle.CredentialUpdates)
}

func (s *SQLiteStore) createImportedProviders(
	ctx context.Context,
	tx *gorm.DB,
	repository *credentialsession.Repository,
	creates []ProviderImportCreate,
) error {
	for index := range creates {
		entry := &creates[index]
		var count int64
		if err := tx.Model(&model.Provider{}).Where("id = ?", entry.Provider.ID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return importConflict(entry.CandidateID, ProviderImportConflictProviderAlreadyExists, entry.Provider.ID, 0, 0)
		}
		for sessionIndex := range entry.Sessions {
			if _, err := repository.Create(ctx, &entry.Sessions[sessionIndex]); err != nil {
				return fmt.Errorf("create imported credential session for candidate %q: %w", entry.CandidateID, err)
			}
		}
		if err := s.createProviderInTransaction(ctx, tx, &entry.Provider); err != nil {
			return fmt.Errorf("create imported provider for candidate %q: %w", entry.CandidateID, err)
		}
	}
	return nil
}

func updateImportedCredentialSessions(
	ctx context.Context,
	repository *credentialsession.Repository,
	updates []ProviderImportCredentialUpdate,
) error {
	for index := range updates {
		update := &updates[index]
		current, err := repository.Get(ctx, update.SessionID)
		if errors.Is(err, credentialsession.ErrNotFound) {
			return importConflict(update.CandidateID, ProviderImportConflictSessionNotFound, "", update.ExpectedVersion, 0)
		}
		if err != nil {
			return err
		}
		if current.Version != update.ExpectedVersion {
			return importConflict(update.CandidateID, ProviderImportConflictCredentialVersionMismatch, "", update.ExpectedVersion, current.Version)
		}
		if err := updateImportedCredentialSession(ctx, repository, update); err != nil {
			return err
		}
	}
	return nil
}

func updateImportedCredentialSession(
	ctx context.Context,
	repository *credentialsession.Repository,
	update *ProviderImportCredentialUpdate,
) error {
	_, err := repository.UpdateCredentialCAS(
		ctx,
		update.SessionID,
		update.ExpectedVersion,
		update.SecretData,
		update.Subject,
		update.AuthState,
	)
	if errors.Is(err, credentialsession.ErrVersionConflict) {
		return importConflict(update.CandidateID, ProviderImportConflictCredentialVersionMismatch, "", update.ExpectedVersion, update.ExpectedVersion+1)
	}
	return err
}

func validateProviderImportBundle(bundle *ProviderImportBundle) error {
	candidates := make(map[string]struct{}, len(bundle.Creates)+len(bundle.CredentialUpdates))
	sessions := make(map[string]struct{})
	providers := make(map[string]struct{})
	for index := range bundle.Creates {
		if err := validateProviderImportCreate(&bundle.Creates[index], candidates, providers, sessions); err != nil {
			return err
		}
	}
	for index := range bundle.CredentialUpdates {
		if err := validateProviderImportUpdate(&bundle.CredentialUpdates[index], candidates); err != nil {
			return err
		}
	}
	return nil
}

func validateProviderImportCreate(
	entry *ProviderImportCreate,
	candidates map[string]struct{},
	providers map[string]struct{},
	sessions map[string]struct{},
) error {
	if err := validateImportCandidateID(entry.CandidateID, candidates); err != nil {
		return err
	}
	entry.Provider.ID = strings.TrimSpace(entry.Provider.ID)
	if entry.Provider.ID == "" {
		return fmt.Errorf("candidate %q provider ID is required", entry.CandidateID)
	}
	if _, duplicate := providers[entry.Provider.ID]; duplicate {
		return fmt.Errorf("candidate %q duplicates provider ID %q", entry.CandidateID, entry.Provider.ID)
	}
	providers[entry.Provider.ID] = struct{}{}
	if err := validateExplicitImportSessions(entry, sessions); err != nil {
		return err
	}
	if err := appendEmbeddedImportSessions(entry, sessions); err != nil {
		return err
	}
	if _, err := credentialBindingsForProvider(&entry.Provider); err != nil {
		return fmt.Errorf("candidate %q: %w", entry.CandidateID, err)
	}
	return nil
}

func validateExplicitImportSessions(entry *ProviderImportCreate, sessions map[string]struct{}) error {
	for sessionIndex := range entry.Sessions {
		session := &entry.Sessions[sessionIndex]
		session.ID = strings.TrimSpace(session.ID)
		if err := session.Validate(); err != nil {
			return fmt.Errorf("candidate %q session %d: %w", entry.CandidateID, sessionIndex, err)
		}
		if _, duplicate := sessions[session.ID]; duplicate {
			return fmt.Errorf("candidate %q duplicates session ID %q", entry.CandidateID, session.ID)
		}
		sessions[session.ID] = struct{}{}
	}
	return nil
}

func appendEmbeddedImportSessions(entry *ProviderImportCreate, sessions map[string]struct{}) error {
	explicit := make(map[string]struct{}, len(entry.Sessions))
	for sessionIndex := range entry.Sessions {
		explicit[entry.Sessions[sessionIndex].ID] = struct{}{}
	}
	for _, route := range entry.Provider.CredentialSessions {
		if _, exists := explicit[route.Credential.SessionID]; exists {
			continue
		}
		session, err := sessionFromSnapshot(route.Credential)
		if err != nil {
			return fmt.Errorf("candidate %q embedded session %q: %w", entry.CandidateID, route.Credential.SessionID, err)
		}
		if _, duplicate := sessions[session.ID]; duplicate {
			return fmt.Errorf("candidate %q duplicates session ID %q", entry.CandidateID, session.ID)
		}
		sessions[session.ID] = struct{}{}
		explicit[session.ID] = struct{}{}
		entry.Sessions = append(entry.Sessions, session)
	}
	return nil
}

func validateProviderImportUpdate(update *ProviderImportCredentialUpdate, candidates map[string]struct{}) error {
	if err := validateImportCandidateID(update.CandidateID, candidates); err != nil {
		return err
	}
	update.SessionID = strings.TrimSpace(update.SessionID)
	if update.SessionID == "" || update.ExpectedVersion < 1 || strings.TrimSpace(update.SecretData) == "" {
		return fmt.Errorf("candidate %q requires session ID, version, and secret", update.CandidateID)
	}
	if err := update.Subject.Validate(); err != nil {
		return fmt.Errorf("candidate %q: %w", update.CandidateID, err)
	}
	return nil
}

func sessionFromSnapshot(snapshot credentialsession.Snapshot) (credentialsession.Session, error) {
	session := credentialsession.Session{
		ID:         snapshot.SessionID,
		Vendor:     snapshot.Vendor,
		Kind:       snapshot.Kind,
		SecretData: snapshot.SecretData,
		Version:    snapshot.Version,
		AuthState:  snapshot.AuthState.Clone(),
	}
	if err := session.SetSubject(snapshot.Subject); err != nil {
		return credentialsession.Session{}, err
	}
	if err := session.Validate(); err != nil {
		return credentialsession.Session{}, err
	}
	return session, nil
}

func validateImportCandidateID(candidateID string, seen map[string]struct{}) error {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return fmt.Errorf("candidate ID is required")
	}
	if _, duplicate := seen[candidateID]; duplicate {
		return fmt.Errorf("duplicate candidate ID %q", candidateID)
	}
	seen[candidateID] = struct{}{}
	return nil
}

func providerImportMutationSessionIDs(bundle *ProviderImportBundle) []string {
	ids := make([]string, 0, len(bundle.CredentialUpdates))
	for index := range bundle.CredentialUpdates {
		ids = append(ids, bundle.CredentialUpdates[index].SessionID)
	}
	return ids
}

func importConflict(candidateID string, kind ProviderImportConflictKind, providerID string, expected, current int64) error {
	return &ProviderImportConflictError{Conflicts: []ProviderImportConflict{{
		CandidateID:               candidateID,
		Kind:                      kind,
		ProviderID:                providerID,
		ExpectedCredentialVersion: expected,
		CurrentCredentialVersion:  current,
	}}}
}
