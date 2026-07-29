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
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var provider model.Provider
		if err := tx.Select("id, credential_type").
			First(&provider, "id = ?", providerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		current, err := loadPersistedProviderState(tx, providerID)
		if err != nil {
			return err
		}

		expectedVersion := credential.Version
		currentVersion := int64(0)
		if current.credential != nil {
			currentVersion = current.credential.Version
		}
		if current.credential == nil {
			if expectedVersion > 0 {
				return &CredentialVersionConflictError{
					ProviderID:      providerID,
					ExpectedVersion: expectedVersion,
					CurrentVersion:  currentVersion,
				}
			}
		} else if expectedVersion != currentVersion {
			return &CredentialVersionConflictError{
				ProviderID:      providerID,
				ExpectedVersion: expectedVersion,
				CurrentVersion:  currentVersion,
			}
		}

		normalizedCredential := model.NormalizeProviderCredentialRecord(providerID, credential)
		if current.credential != nil {
			normalizedCredential.Version = currentVersion + 1
		} else {
			normalizedCredential.Version = 1
		}
		if err := validateExclusiveCredentialBinding(tx, providerID, normalizedCredential.BindingAccountID); err != nil {
			return err
		}

		normalizedAuthState := model.NormalizeProviderAuthStateRecord(
			providerID,
			provider.CredentialType,
			authState,
		)

		if err := tx.Save(normalizedCredential).Error; err != nil {
			return err
		}
		if err := tx.Save(normalizedAuthState).Error; err != nil {
			return err
		}

		credential.Version = normalizedCredential.Version
		return nil
	})
	if err != nil {
		return fmt.Errorf("update provider credential state %q: %w", providerID, err)
	}
	return nil
}
