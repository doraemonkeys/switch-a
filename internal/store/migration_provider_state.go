package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

const (
	// Legacy provider rows predate the one-account/one-provider invariant. A
	// deterministic loser remains visible for repair but cannot refresh or route
	// with an account another provider already owns.
	providerAuthReasonLegacyDuplicateBinding = "legacy_duplicate_account_binding"
	providerAuthErrorLegacyDuplicateBinding  = "legacy credential binding is already owned by another provider; reauthentication required"
)

// migrateProviderStateTables backfills the new credential/auth tables without
// overwriting rows that were already created by a newer binary, then drops the
// legacy providers.credential_data shadow once split storage is populated.
func migrateProviderStateTables(db *gorm.DB) error {
	hasLegacyCredentialData, err := tableColumnExists(db, providersTableName, providerCredentialDataColumn)
	if err != nil {
		return fmt.Errorf("check providers credential shadow column: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if hasLegacyCredentialData {
			var providers []legacyProviderCredentialShadow
			if err := tx.Table(providersTableName).
				Select("id, credential_type, credential_data").
				Order("id ASC").
				Scan(&providers).Error; err != nil {
				return fmt.Errorf("list providers for provider state migration: %w", err)
			}
			claimedBindings, err := loadProviderCredentialBindingOwners(tx)
			if err != nil {
				return err
			}
			for i := range providers {
				if err := backfillProviderStateWithBindings(tx, &providers[i], claimedBindings); err != nil {
					return err
				}
			}
			if err := tx.Exec(
				fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, providersTableName, providerCredentialDataColumn),
			).Error; err != nil {
				return fmt.Errorf("drop providers credential shadow column: %w", err)
			}
		}
		return nil
	})
}

type legacyProviderCredentialShadow struct {
	ID             string
	CredentialType model.ProviderCredentialType
	CredentialData string
}

type providerCredentialBindingOwner struct {
	ProviderID       string
	BindingAccountID *string
}

type providerCredentialBindingOwners map[string]string

func backfillProviderState(tx *gorm.DB, provider *legacyProviderCredentialShadow) error {
	if provider == nil {
		return nil
	}
	claimedBindings, err := loadProviderCredentialBindingOwners(tx)
	if err != nil {
		return err
	}
	return backfillProviderStateWithBindings(tx, provider, claimedBindings)
}

func loadProviderCredentialBindingOwners(tx *gorm.DB) (providerCredentialBindingOwners, error) {
	var rows []providerCredentialBindingOwner
	if err := tx.Model(&model.ProviderCredential{}).
		Select("provider_id, binding_account_id").
		Where("binding_account_id IS NOT NULL").
		Order("provider_id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list provider credential binding owners: %w", err)
	}

	owners := make(providerCredentialBindingOwners, len(rows))
	for i := range rows {
		if rows[i].BindingAccountID == nil {
			continue
		}
		accountID := strings.TrimSpace(*rows[i].BindingAccountID)
		if accountID == "" {
			continue
		}
		// Existing split rows are authoritative. Ordering makes the fallback
		// deterministic even for a hand-edited database whose values only differ
		// by surrounding whitespace.
		if _, claimed := owners[accountID]; !claimed {
			owners[accountID] = rows[i].ProviderID
		}
	}
	return owners, nil
}

func backfillProviderStateWithBindings(
	tx *gorm.DB,
	provider *legacyProviderCredentialShadow,
	claimedBindings providerCredentialBindingOwners,
) error {
	if provider == nil {
		return nil
	}
	if claimedBindings == nil {
		claimedBindings = make(providerCredentialBindingOwners)
	}

	credential, derivedAuthState, duplicateBinding, err := backfillProviderCredential(tx, provider, claimedBindings)
	if err != nil {
		return err
	}
	return backfillProviderAuthState(tx, provider, credential, derivedAuthState, duplicateBinding)
}

func backfillProviderCredential(
	tx *gorm.DB,
	provider *legacyProviderCredentialShadow,
	claimedBindings providerCredentialBindingOwners,
) (*model.ProviderCredential, *model.ProviderAuthState, bool, error) {
	var existing model.ProviderCredential
	err := tx.First(&existing, "provider_id = ?", provider.ID).Error
	if err == nil {
		return &existing, model.ProviderAuthStateFromCredential(
			provider.ID,
			provider.CredentialType,
			&existing,
		), false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, fmt.Errorf("read provider credential for %q: %w", provider.ID, err)
	}

	credential := model.ProviderCredentialFromLegacy(
		provider.ID,
		provider.CredentialType,
		provider.CredentialData,
	)
	if credential == nil {
		return nil, model.ProviderAuthStateFromCredential(
			provider.ID,
			provider.CredentialType,
			nil,
		), false, nil
	}
	// Derive the public summary before removing it from the monolithic blob.
	// This keeps migration lossless while making the persisted secret obey the
	// split credential contract used by every post-migration write path.
	derivedAuthState := model.ProviderAuthStateFromCredential(
		provider.ID,
		provider.CredentialType,
		credential,
	)
	secretData, err := canonicalizeLegacyChatGPTSecret(credential.SecretData)
	if err != nil {
		return nil, nil, false, fmt.Errorf("canonicalize provider credential for %q: %w", provider.ID, err)
	}
	credential.SecretData = secretData

	accountID := ""
	if credential.BindingAccountID != nil {
		accountID = strings.TrimSpace(*credential.BindingAccountID)
	}
	ownerID, claimed := claimedBindings[accountID]
	duplicateBinding := accountID != "" && claimed && ownerID != provider.ID
	if duplicateBinding {
		// NULL intentionally preserves the secret for recovery while satisfying the
		// database uniqueness invariant. An empty string would still be a claimed key.
		credential.BindingAccountID = nil
	}
	if err := tx.Create(credential).Error; err != nil {
		return nil, nil, false, fmt.Errorf("backfill provider credential for %q: %w", provider.ID, err)
	}
	if accountID != "" && !duplicateBinding {
		claimedBindings[accountID] = provider.ID
	}
	return credential, derivedAuthState, duplicateBinding, nil
}

func canonicalizeLegacyChatGPTSecret(raw string) (string, error) {
	legacy, err := model.DecodeChatGPTProviderCredential(raw)
	if err != nil || legacy == nil || !hasRecognizedLegacyChatGPTFields(legacy) {
		// Unknown payloads remain recoverable instead of being destructively
		// rewritten into an empty, apparently valid secret object.
		return raw, nil
	}
	return model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:   legacy.AccessToken,
		RefreshToken:  legacy.RefreshToken,
		IDToken:       legacy.IDToken,
		OAuthIssuer:   legacy.OAuthIssuer,
		OAuthClientID: legacy.OAuthClientID,
	})
}

func hasRecognizedLegacyChatGPTFields(credential *model.ChatGPTProviderCredential) bool {
	return credential != nil && (strings.TrimSpace(credential.AccessToken) != "" ||
		strings.TrimSpace(credential.RefreshToken) != "" ||
		strings.TrimSpace(credential.IDToken) != "" ||
		strings.TrimSpace(credential.OAuthIssuer) != "" ||
		strings.TrimSpace(credential.OAuthClientID) != "" ||
		strings.TrimSpace(credential.AccountID) != "" ||
		strings.TrimSpace(credential.Email) != "" ||
		strings.TrimSpace(credential.PlanType) != "" ||
		credential.Usage != nil ||
		!credential.LastRefresh.IsZero() ||
		!credential.ExpiresAt.IsZero())
}

func backfillProviderAuthState(
	tx *gorm.DB,
	provider *legacyProviderCredentialShadow,
	credential *model.ProviderCredential,
	derived *model.ProviderAuthState,
	duplicateBinding bool,
) error {
	var existing model.ProviderAuthState
	err := tx.First(&existing, "provider_id = ?", provider.ID).Error
	hasExisting := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("read provider auth state for %q: %w", provider.ID, err)
	}
	if hasExisting && !duplicateBinding {
		return nil
	}

	if derived == nil {
		derived = model.ProviderAuthStateFromCredential(provider.ID, provider.CredentialType, credential)
		if derived == nil {
			derived = model.NormalizeProviderAuthStateRecord(
				provider.ID,
				provider.CredentialType,
				nil,
			)
		}
	}

	authState := derived
	if hasExisting {
		authState = existing.Clone()
		preserveMissingProviderIdentity(authState, derived)
	}
	if duplicateBinding {
		authState.Status = model.ProviderAuthStatusReauthRequired
		authState.StatusReason = providerAuthReasonLegacyDuplicateBinding
		authState.LastError = providerAuthErrorLegacyDuplicateBinding
	}
	authState = model.NormalizeProviderAuthStateRecord(
		provider.ID,
		provider.CredentialType,
		authState,
	)

	if hasExisting {
		if err := tx.Model(&model.ProviderAuthState{}).
			Where("provider_id = ?", provider.ID).
			Updates(map[string]any{
				"status":        authState.Status,
				"status_reason": authState.StatusReason,
				"last_error":    authState.LastError,
				"email":         authState.Email,
				"account_id":    authState.AccountID,
			}).Error; err != nil {
			return fmt.Errorf("demote duplicate provider auth state for %q: %w", provider.ID, err)
		}
		return nil
	}
	if err := tx.Create(authState).Error; err != nil {
		return fmt.Errorf("backfill provider auth state for %q: %w", provider.ID, err)
	}
	return nil
}

func preserveMissingProviderIdentity(current, derived *model.ProviderAuthState) {
	if current == nil || derived == nil {
		return
	}
	if strings.TrimSpace(current.Email) == "" {
		current.Email = derived.Email
	}
	if strings.TrimSpace(current.AccountID) == "" {
		current.AccountID = derived.AccountID
	}
}
