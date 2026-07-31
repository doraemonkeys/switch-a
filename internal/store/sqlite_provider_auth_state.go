package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

// UpdateProviderAuthState persists the non-sensitive auth lifecycle snapshot
// independently from the secret credential record so admin usage syncs stay
// pure-read on the credential blob.
func (s *SQLiteStore) UpdateProviderAuthState(
	ctx context.Context,
	providerID string,
	authState *model.ProviderAuthState,
) error {
	ownedCtx, release, err := s.WithProviderCredentialMutations(ctx, []string{providerID})
	if err != nil {
		return fmt.Errorf("update provider auth state %q: %w", providerID, err)
	}
	defer release()

	err = s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
		var provider model.Provider
		if err := tx.Select("id, credential_type").
			First(&provider, "id = ?", providerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		normalized := model.NormalizeProviderAuthStateRecord(
			providerID,
			provider.CredentialType,
			authState,
		)
		return tx.Save(normalized).Error
	})
	if err != nil {
		return fmt.Errorf("update provider auth state %q: %w", providerID, err)
	}
	return nil
}

// UpdateProviderCredentialState atomically persists the refreshed secret and auth
// snapshot using the credential version as a CAS guard so refresh races cannot
// blindly overwrite a newer token rotation.
func (s *SQLiteStore) UpdateProviderCredentialState(
	ctx context.Context,
	providerID string,
	credential *model.ProviderCredential,
	authState *model.ProviderAuthState,
) error {
	if credential == nil {
		return fmt.Errorf("provider credential is required")
	}
	ownedCtx, release, err := s.WithProviderCredentialMutations(ctx, []string{providerID})
	if err != nil {
		return fmt.Errorf("update provider credential state %q: %w", providerID, err)
	}
	defer release()

	var nextVersion int64
	err = s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
		var err error
		nextVersion, err = s.updateProviderCredentialStateInTransaction(
			tx,
			providerID,
			credential,
			authState,
			credential.Version,
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("update provider credential state %q: %w", providerID, err)
	}
	credential.Version = nextVersion
	return nil
}

func (s *SQLiteStore) updateProviderCredentialStateInTransaction(
	tx *gorm.DB,
	providerID string,
	credential *model.ProviderCredential,
	authState *model.ProviderAuthState,
	expectedVersion int64,
) (int64, error) {
	var provider model.Provider
	if err := tx.Select("id, credential_type").
		First(&provider, "id = ?", providerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrNotFound
		}
		return 0, err
	}

	current, err := loadPersistedProviderState(tx, providerID)
	if err != nil {
		return 0, err
	}
	currentVersion := int64(0)
	if current.credential != nil {
		currentVersion = current.credential.Version
	}
	if expectedVersion != currentVersion {
		return 0, &CredentialVersionConflictError{
			ProviderID:      providerID,
			ExpectedVersion: expectedVersion,
			CurrentVersion:  currentVersion,
		}
	}

	normalizedCredential := model.NormalizeProviderCredentialRecord(providerID, credential)
	normalizedCredential.Version = currentVersion + 1
	if current.credential != nil {
		// CreatedAt identifies the credential generation. Preserving it across token
		// rotation lets imports distinguish a normal version change from delete/recreate ABA.
		normalizedCredential.CreatedAt = current.credential.CreatedAt
	}
	if err := validateExclusiveCredentialBinding(tx, providerID, normalizedCredential.BindingAccountID); err != nil {
		return 0, err
	}
	normalizedAuthState := model.NormalizeProviderAuthStateRecord(
		providerID,
		provider.CredentialType,
		authState,
	)
	preserveNewerProviderUsageSnapshot(normalizedAuthState, current.authState)

	if err := tx.Save(normalizedCredential).Error; err != nil {
		return 0, err
	}
	if err := tx.Save(normalizedAuthState).Error; err != nil {
		return 0, err
	}
	return normalizedCredential.Version, nil
}

func preserveNewerProviderUsageSnapshot(
	incoming *model.ProviderAuthState,
	current *model.ProviderAuthState,
) {
	if incoming == nil || current == nil || current.UsageSnapshot == nil {
		return
	}
	currentFetchedAt := current.UsageSnapshot.FetchedAt
	incomingSnapshot := incoming.UsageSnapshot
	// Once the durable snapshot has a fetch time, an absent/equal import timestamp
	// cannot prove freshness. Keeping the durable value prevents a credential-only
	// import from erasing a usage refresh that committed after preview.
	if currentFetchedAt == nil ||
		(incomingSnapshot != nil && incomingSnapshot.FetchedAt != nil &&
			incomingSnapshot.FetchedAt.After(*currentFetchedAt)) {
		return
	}
	incoming.UsageSnapshot = model.CloneProviderUsageSnapshot(current.UsageSnapshot)
}
