package store

import (
	"context"
	"errors"
	"fmt"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

func providerAPITypeSet(apiTypes []model.ProviderAPIType) map[string]struct{} {
	supported := make(map[string]struct{}, len(apiTypes))
	for _, apiType := range apiTypes {
		supported[apiType.APIType] = struct{}{}
	}
	return supported
}

func (s *SQLiteStore) ListProviders(ctx context.Context) ([]model.Provider, error) {
	var providers []model.Provider
	if err := providerQueryWithState(s.db.WithContext(ctx)).Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	hydrateProviderStates(providers)
	return providers, nil
}

func (s *SQLiteStore) ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error) {
	var providers []model.Provider
	err := providerQueryWithState(s.db.WithContext(ctx)).
		Distinct().
		Joins("JOIN provider_api_types ON provider_api_types.provider_id = providers.id").
		Where("provider_api_types.api_type = ? AND providers.enabled = ?", apiType, true).
		Find(&providers).Error
	if err != nil {
		return nil, fmt.Errorf("list providers by api type %q: %w", apiType, err)
	}
	hydrateProviderStates(providers)
	return providers, nil
}

func (s *SQLiteStore) GetProvider(ctx context.Context, id string) (*model.Provider, error) {
	var provider model.Provider
	err := providerQueryWithState(s.db.WithContext(ctx)).First(&provider, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", id, err)
	}
	hydrateProviderState(&provider)
	return &provider, nil
}

func (s *SQLiteStore) CreateProvider(ctx context.Context, p *model.Provider, options ...ProviderWriteOptions) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		writeOptions := resolveProviderWriteOptions(options)
		supplemental := resolveProviderSupplementalState(p, &persistedProviderState{})
		bindingAccountID := (*string)(nil)
		if supplemental.credential != nil {
			bindingAccountID = supplemental.credential.BindingAccountID
		}
		if err := resolveCredentialBinding(tx, p.ID, bindingAccountID, writeOptions.CredentialBindingResolution); err != nil {
			return err
		}

		// Use raw SQL to properly handle boolean false values
		now := s.clock.Now()
		if p.CreatedAt.IsZero() {
			p.CreatedAt = now
		}
		if p.UpdatedAt.IsZero() {
			p.UpdatedAt = now
		}

		// Set default scope values if empty
		failoverScope := p.FailoverScope
		if failoverScope == "" {
			failoverScope = model.ScopeAny
		}
		acceptFailover := p.AcceptFailover
		if acceptFailover == "" {
			acceptFailover = model.ScopeAny
		}
		if err := tx.Exec(`
			INSERT INTO providers (
				id, name, api_key, auth_mode, credential_type, usage_limit_policy, group_id,
				weight, priority, concurrency, max_retries,
				backoff_initial_delay, backoff_max_delay, backoff_multiplier, backoff_jitter,
				vendor, failover_scope, accept_failover,
				enabled, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.Name, p.APIKey, p.AuthMode, model.NormalizeProviderCredentialType(p.CredentialType), p.UsageLimitPolicy, p.GroupID,
			p.Weight, p.Priority, p.Concurrency, p.MaxRetries,
			p.Backoff.InitialDelay, p.Backoff.MaxDelay, p.Backoff.Multiplier, p.Backoff.Jitter,
			p.Vendor, failoverScope, acceptFailover,
			p.Enabled, p.CreatedAt, p.UpdatedAt,
		).Error; err != nil { // coverage-ignore -- INSERT rarely fails with valid data
			return err
		}
		// Create API types separately
		for i := range p.APITypes {
			p.APITypes[i].ProviderID = p.ID
			if err := tx.Create(&p.APITypes[i]).Error; err != nil { // coverage-ignore -- transaction error after successful provider insert is rare
				return err
			}
		}
		return persistProviderSupplementalState(tx, p.ID, supplemental)
	})
	if err != nil {
		return fmt.Errorf("create provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateProvider(ctx context.Context, p *model.Provider, options ...ProviderWriteOptions) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		writeOptions := resolveProviderWriteOptions(options)
		current, err := loadPersistedProviderState(tx, p.ID)
		if err != nil {
			return err
		}
		supplemental := resolveProviderSupplementalState(p, current)
		bindingAccountID := (*string)(nil)
		if supplemental.credential != nil {
			bindingAccountID = supplemental.credential.BindingAccountID
		}
		if err := resolveCredentialBinding(tx, p.ID, bindingAccountID, writeOptions.CredentialBindingResolution); err != nil {
			return err
		}
		missingPolicy, err := findExactProviderRoutingPolicyMissingAPIType(tx, p.ID, providerAPITypeSet(p.APITypes))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if missingPolicy != nil {
			return &RoutingPolicyProviderAPITypeConflictError{
				ProviderID: p.ID,
				APIType:    missingPolicy.APIType,
				PolicyID:   missingPolicy.ID,
				Key:        missingPolicy.NaturalKey(),
			}
		}

		// Delete existing API types
		if err := tx.Where("provider_id = ?", p.ID).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}

		// Set default scope values if empty (same as CreateProvider).
		// Use local variables to avoid mutating the caller's pointer.
		failoverScope := p.FailoverScope
		if failoverScope == "" {
			failoverScope = model.ScopeAny
		}
		acceptFailover := p.AcceptFailover
		if acceptFailover == "" {
			acceptFailover = model.ScopeAny
		}
		credentialType := model.NormalizeProviderCredentialType(p.CredentialType)

		// Temporarily clear APITypes to avoid GORM trying to update them via Save
		apiTypes := p.APITypes
		p.APITypes = nil

		// Apply scopes for the Save call, then restore originals
		origFailover := p.FailoverScope
		origAccept := p.AcceptFailover
		origCredentialType := p.CredentialType
		p.FailoverScope = failoverScope
		p.AcceptFailover = acceptFailover
		p.CredentialType = credentialType

		// Save provider (without APITypes)
		if err := tx.Save(p).Error; err != nil {
			return err
		}

		// Restore original values so the caller's struct is not mutated
		p.FailoverScope = origFailover
		p.AcceptFailover = origAccept
		p.CredentialType = origCredentialType

		// Restore and create new API types explicitly
		p.APITypes = apiTypes
		for i := range p.APITypes {
			p.APITypes[i].ProviderID = p.ID
			if err := tx.Create(&p.APITypes[i]).Error; err != nil { // coverage-ignore -- transaction error after successful provider save is rare
				return err
			}
		}
		return persistProviderSupplementalState(tx, p.ID, supplemental)
	})
	if err != nil {
		return fmt.Errorf("update provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateProviderCredential(ctx context.Context, id string, credentialType model.ProviderCredentialType, credentialData string) error {
	now := s.clock.Now()
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := loadPersistedProviderState(tx, id)
		if err != nil {
			return err
		}

		provider := &model.Provider{
			ID:             id,
			CredentialType: credentialType,
			Credential: model.ProviderCredentialFromLegacy(
				id,
				credentialType,
				credentialData,
			),
		}
		if provider.Credential != nil {
			// Refresh callers provide a raw secret payload, not a versioned record.
			// Reset the version so resolveProviderCredentialRecord can apply the
			// current persisted version and bump it when the secret changes.
			provider.Credential.Version = 0
		}
		supplemental := resolveProviderSupplementalState(provider, current)
		bindingAccountID := (*string)(nil)
		if supplemental.credential != nil {
			bindingAccountID = supplemental.credential.BindingAccountID
		}
		if err := validateExclusiveCredentialBinding(tx, id, bindingAccountID); err != nil {
			return err
		}
		if err := tx.Exec(
			`UPDATE providers
			 SET credential_type = ?, updated_at = ?
			 WHERE id = ?`,
			model.NormalizeProviderCredentialType(credentialType),
			now,
			id,
		).Error; err != nil {
			return err
		}
		return persistProviderSupplementalState(tx, id, supplemental)
	}); err != nil {
		return fmt.Errorf("update provider credential %q: %w", id, err)
	}
	return nil
}

func (s *SQLiteStore) DeleteProvider(ctx context.Context, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		referencedBy, err := findRoutingPolicyTargetingProvider(tx, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if referencedBy != nil {
			return &RoutingPolicyProviderReferenceConflictError{
				ProviderID: id,
				PolicyID:   referencedBy.ID,
				Key:        referencedBy.NaturalKey(),
			}
		}
		// Delete API types first
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}
		// Delete provider credential state first so the migration shadow never leaves
		// orphaned secret/auth rows behind even if SQLite foreign-key pragmas differ.
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderAuthState{}).Error; err != nil {
			return err
		}
		// Delete health state
		if err := tx.Where("provider_id = ?", id).Delete(&model.HealthState{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}
		// Delete provider
		return tx.Delete(&model.Provider{}, "id = ?", id).Error
	})
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	return nil
}
