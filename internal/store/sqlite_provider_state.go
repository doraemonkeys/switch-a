package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type persistedProviderState struct {
	credentialType model.ProviderCredentialType
	credential     *model.ProviderCredential
	authState      *model.ProviderAuthState
}

type providerSupplementalState struct {
	credential *model.ProviderCredential
	authState  *model.ProviderAuthState
}

func providerQueryWithState(db *gorm.DB) *gorm.DB {
	return db.Preload("APITypes").Preload("Credential").Preload("AuthState")
}

func hydrateProviderState(provider *model.Provider) {
	if provider == nil {
		return
	}
	provider.CredentialType = model.NormalizeProviderCredentialType(provider.CredentialType)
	if provider.Credential != nil {
		provider.Credential = model.NormalizeProviderCredentialRecord(provider.ID, provider.Credential)
	}
	if provider.AuthState != nil {
		provider.AuthState = model.NormalizeProviderAuthStateRecord(
			provider.ID,
			provider.CredentialType,
			provider.AuthState,
		)
	} else {
		provider.AuthState = model.ProviderAuthStateFromCredential(
			provider.ID,
			provider.CredentialType,
			provider.Credential,
		)
	}
}

func hydrateProviderStates(providers []model.Provider) {
	for i := range providers {
		hydrateProviderState(&providers[i])
	}
}

func loadPersistedProviderState(tx *gorm.DB, providerID string) (*persistedProviderState, error) {
	state := &persistedProviderState{}

	var providerRecord struct {
		CredentialType model.ProviderCredentialType
	}
	if err := tx.Model(&model.Provider{}).
		Select("credential_type").
		Where("id = ?", providerID).
		First(&providerRecord).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load provider credential type for %q: %w", providerID, err)
		}
	} else {
		state.credentialType = model.NormalizeProviderCredentialType(providerRecord.CredentialType)
	}

	var credential model.ProviderCredential
	if err := tx.Where("provider_id = ?", providerID).First(&credential).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load provider credential for %q: %w", providerID, err)
		}
	} else {
		state.credential = model.NormalizeProviderCredentialRecord(providerID, &credential)
	}

	var authState model.ProviderAuthState
	if err := tx.Where("provider_id = ?", providerID).First(&authState).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load provider auth state for %q: %w", providerID, err)
		}
	} else {
		state.authState = model.NormalizeProviderAuthStateRecord(
			providerID,
			state.credentialType,
			&authState,
		)
	}

	return state, nil
}

func resolveProviderSupplementalState(
	provider *model.Provider,
	current *persistedProviderState,
) providerSupplementalState {
	credentialType := model.NormalizeProviderCredentialType(provider.CredentialType)
	sourceCredentialData := ""
	if provider.Credential != nil {
		sourceCredentialData = provider.Credential.SecretData
	}
	if credentialType != model.ProviderCredentialTypeChatGPT {
		sourceCredentialData = ""
	}

	credential := resolveProviderCredentialRecord(
		provider.ID,
		credentialType,
		sourceCredentialData,
		provider.Credential,
		current.credential,
	)
	authState := resolveProviderAuthStateRecord(
		provider.ID,
		credentialType,
		provider.AuthState,
		current.authState,
		credential,
		current.credential,
	)

	return providerSupplementalState{
		credential: credential,
		authState:  authState,
	}
}

func resolveProviderCredentialRecord(
	providerID string,
	credentialType model.ProviderCredentialType,
	legacyCredentialData string,
	explicit *model.ProviderCredential,
	current *model.ProviderCredential,
) *model.ProviderCredential {
	if credentialType != model.ProviderCredentialTypeChatGPT {
		return nil
	}

	if explicit != nil {
		normalized := model.NormalizeProviderCredentialRecord(providerID, explicit)
		if strings.TrimSpace(normalized.SecretData) == "" {
			return nil
		}
		if explicit.Version > 0 {
			return normalized
		}
		if current != nil && providerCredentialsEqual(current, normalized) {
			normalized.Version = current.Version
			return normalized
		}
		if current != nil {
			normalized.Version = current.Version + 1
		}
		return normalized
	}

	derived := model.ProviderCredentialFromLegacy(providerID, credentialType, legacyCredentialData)
	if derived == nil {
		return nil
	}
	if current != nil {
		if providerCredentialsEqual(current, derived) {
			return current.Clone()
		}
		derived.Version = current.Version + 1
	}
	return derived
}

func resolveProviderAuthStateRecord(
	providerID string,
	credentialType model.ProviderCredentialType,
	explicit *model.ProviderAuthState,
	current *model.ProviderAuthState,
	credential *model.ProviderCredential,
	currentCredential *model.ProviderCredential,
) *model.ProviderAuthState {
	if explicit != nil {
		return model.NormalizeProviderAuthStateRecord(providerID, credentialType, explicit)
	}

	derived := model.ProviderAuthStateFromCredential(providerID, credentialType, credential)
	if derived == nil {
		derived = model.NormalizeProviderAuthStateRecord(providerID, credentialType, nil)
	}

	if current == nil {
		return derived
	}
	if credentialType != model.ProviderCredentialTypeChatGPT {
		return model.NormalizeProviderAuthStateRecord(providerID, credentialType, derived)
	}
	if providerCredentialsEqual(currentCredential, credential) {
		return model.NormalizeProviderAuthStateRecord(providerID, credentialType, current)
	}

	merged := derived.Clone()
	preserved := current.Clone()
	if preserved != nil &&
		preserved.Status == model.ProviderAuthStatusReauthRequired &&
		merged.Status == model.ProviderAuthStatusActive {
		merged.Status = preserved.Status
		if merged.StatusReason == "" {
			merged.StatusReason = preserved.StatusReason
		}
		if merged.LastError == "" {
			merged.LastError = preserved.LastError
		}
		if merged.LastTransitionAt == nil {
			merged.LastTransitionAt = preserved.LastTransitionAt
		}
	}
	if preserved != nil {
		if merged.RefreshFailCount == 0 {
			merged.RefreshFailCount = preserved.RefreshFailCount
		}
		if merged.LastRefreshFailureAt == nil {
			merged.LastRefreshFailureAt = preserved.LastRefreshFailureAt
		}
	}
	return model.NormalizeProviderAuthStateRecord(providerID, credentialType, merged)
}

func persistProviderSupplementalState(
	tx *gorm.DB,
	providerID string,
	supplemental providerSupplementalState,
) error {
	if supplemental.credential == nil {
		if err := tx.Where("provider_id = ?", providerID).Delete(&model.ProviderCredential{}).Error; err != nil {
			return fmt.Errorf("delete provider credential for %q: %w", providerID, err)
		}
	} else if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"secret_data",
			"binding_account_id",
			"version",
			"updated_at",
		}),
	}).Create(supplemental.credential).Error; err != nil {
		return fmt.Errorf("save provider credential for %q: %w", providerID, err)
	}

	if supplemental.authState == nil {
		if err := tx.Where("provider_id = ?", providerID).Delete(&model.ProviderAuthState{}).Error; err != nil {
			return fmt.Errorf("delete provider auth state for %q: %w", providerID, err)
		}
		return nil
	}
	if err := tx.Save(supplemental.authState).Error; err != nil {
		return fmt.Errorf("save provider auth state for %q: %w", providerID, err)
	}
	return nil
}

func validateExclusiveCredentialBinding(
	tx *gorm.DB,
	providerID string,
	bindingAccountID *string,
) error {
	accountID := normalizeOptionalString(bindingAccountID)
	if accountID == "" {
		return nil
	}

	var existing model.ProviderCredential
	err := tx.Where("provider_id <> ? AND binding_account_id = ?", providerID, accountID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list provider credentials for credential binding validation: %w", err)
	}
	return &CredentialBindingConflictError{
		AccountID:  accountID,
		ProviderID: existing.ProviderID,
	}
}

// resolveCredentialBinding preserves the one-account/one-provider invariant
// while making an explicit replacement atomic with the provider write.
func resolveCredentialBinding(
	tx *gorm.DB,
	providerID string,
	bindingAccountID *string,
	resolution model.CredentialBindingResolution,
	canReplaceProvider func(string) bool,
) error {
	err := validateExclusiveCredentialBinding(tx, providerID, bindingAccountID)
	if err == nil || resolution != model.CredentialBindingResolutionReplace {
		return err
	}

	var conflict *CredentialBindingConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	if canReplaceProvider == nil || !canReplaceProvider(conflict.ProviderID) {
		return &providerCredentialMutationLeaseExpansionError{providerID: conflict.ProviderID}
	}
	// The old provider remains available, but its credential is intentionally
	// cleared so it cannot continue using an account now owned by the new one.
	if clearErr := clearProviderCredentialBinding(tx, conflict.ProviderID); clearErr != nil {
		return clearErr
	}
	return nil
}

func clearProviderCredentialBinding(tx *gorm.DB, providerID string) error {
	if err := tx.Where("provider_id = ?", providerID).Delete(&model.ProviderCredential{}).Error; err != nil {
		return fmt.Errorf("clear provider credential for %q: %w", providerID, err)
	}
	if err := tx.Where("provider_id = ?", providerID).Delete(&model.ProviderAuthState{}).Error; err != nil {
		return fmt.Errorf("clear provider auth state for %q: %w", providerID, err)
	}
	return nil
}

func providerCredentialsEqual(left, right *model.ProviderCredential) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.SecretData == right.SecretData &&
		normalizeOptionalString(left.BindingAccountID) == normalizeOptionalString(right.BindingAccountID)
}

func normalizeOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
