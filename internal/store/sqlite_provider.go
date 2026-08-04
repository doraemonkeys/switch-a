package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

// ProviderWriteOptions is kept in the store package as a consumer-friendly alias;
// the command belongs to the domain model so store interfaces avoid import cycles.
type ProviderWriteOptions = model.ProviderWriteOptions

func resolveProviderWriteOptions(options []ProviderWriteOptions) ProviderWriteOptions {
	if len(options) == 0 {
		return ProviderWriteOptions{
			CredentialBindingResolution: model.CredentialBindingResolutionReject,
		}
	}
	return options[0]
}

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
	writeOptions := resolveProviderWriteOptions(options)
	err := s.runWithProviderCredentialMutations(ctx, []string{p.ID}, func(ownedCtx context.Context) error {
		return s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
			return s.createProviderInTransaction(
				tx,
				p,
				writeOptions,
				func(providerID string) bool {
					return s.credentialMutations.contextOwns(ownedCtx, providerID)
				},
			)
		})
	})
	if err != nil {
		return fmt.Errorf("create provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) createProviderInTransaction(
	tx *gorm.DB,
	p *model.Provider,
	writeOptions ProviderWriteOptions,
	canReplaceCredentialBinding func(string) bool,
) error {
	supplemental := resolveProviderSupplementalState(p, &persistedProviderState{})
	if err := resolveCredentialBinding(
		tx,
		p.ID,
		providerCredentialBindingAccountID(supplemental.credential),
		writeOptions.CredentialBindingResolution,
		canReplaceCredentialBinding,
	); err != nil {
		return err
	}

	// Raw SQL is intentional: GORM's zero-value filtering would turn an explicit
	// disabled import into the schema default and make preview differ from commit.
	now := s.clock.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}

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
	).Error; err != nil { // coverage-ignore -- caller validation and import preflight normally make this unreachable
		return err
	}
	for i := range p.APITypes {
		p.APITypes[i].ProviderID = p.ID
		if err := tx.Create(&p.APITypes[i]).Error; err != nil { // coverage-ignore -- transaction error after successful provider insert is rare
			return err
		}
	}
	return persistProviderSupplementalState(tx, p.ID, supplemental)
}

func (s *SQLiteStore) UpdateProvider(ctx context.Context, p *model.Provider, options ...ProviderWriteOptions) error {
	writeOptions := resolveProviderWriteOptions(options)
	err := s.runWithProviderCredentialMutations(ctx, []string{p.ID}, func(ownedCtx context.Context) error {
		return s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
			return s.updateProviderInTransaction(tx, p, writeOptions, func(providerID string) bool {
				return s.credentialMutations.contextOwns(ownedCtx, providerID)
			})
		})
	})
	if err != nil {
		return fmt.Errorf("update provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) updateProviderInTransaction(
	tx *gorm.DB,
	p *model.Provider,
	writeOptions ProviderWriteOptions,
	canReplaceCredentialBinding func(string) bool,
) error {
	current, err := loadPersistedProviderState(tx, p.ID)
	if err != nil {
		return err
	}
	supplemental := resolveProviderSupplementalState(p, current)
	if err := resolveCredentialBinding(
		tx,
		p.ID,
		providerCredentialBindingAccountID(supplemental.credential),
		writeOptions.CredentialBindingResolution,
		canReplaceCredentialBinding,
	); err != nil {
		return err
	}
	if err := validateProviderAPITypeUpdate(tx, p); err != nil {
		return err
	}
	if err := replaceProviderAPITypeRecords(tx, p); err != nil {
		return err
	}
	return persistProviderSupplementalState(tx, p.ID, supplemental)
}

func validateProviderAPITypeUpdate(tx *gorm.DB, p *model.Provider) error {
	missingPolicy, err := findExactProviderRoutingPolicyMissingAPIType(tx, p.ID, providerAPITypeSet(p.APITypes))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if missingPolicy == nil {
		return nil
	}
	return &RoutingPolicyProviderAPITypeConflictError{
		ProviderID: p.ID,
		APIType:    missingPolicy.APIType,
		PolicyID:   missingPolicy.ID,
		Key:        missingPolicy.NaturalKey(),
	}
}

func replaceProviderAPITypeRecords(tx *gorm.DB, p *model.Provider) error {
	if err := tx.Where("provider_id = ?", p.ID).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
		return err
	}
	if err := saveProviderWithoutAPITypeAssociations(tx, p); err != nil {
		return err
	}
	for i := range p.APITypes {
		p.APITypes[i].ProviderID = p.ID
		if err := tx.Create(&p.APITypes[i]).Error; err != nil { // coverage-ignore -- transaction error after successful provider save is rare
			return err
		}
	}
	return nil
}

func saveProviderWithoutAPITypeAssociations(tx *gorm.DB, p *model.Provider) error {
	apiTypes := p.APITypes
	originalFailoverScope := p.FailoverScope
	originalAcceptFailover := p.AcceptFailover
	originalCredentialType := p.CredentialType
	// GORM must see the normalized scalar record without managing API-type
	// associations; defer keeps the caller stable even when the database rejects Save.
	defer func() {
		p.APITypes = apiTypes
		p.FailoverScope = originalFailoverScope
		p.AcceptFailover = originalAcceptFailover
		p.CredentialType = originalCredentialType
	}()
	p.APITypes = nil
	p.FailoverScope = providerScopeOrAny(p.FailoverScope)
	p.AcceptFailover = providerScopeOrAny(p.AcceptFailover)
	p.CredentialType = model.NormalizeProviderCredentialType(p.CredentialType)
	return tx.Save(p).Error
}

func providerScopeOrAny(scope model.Scope) model.Scope {
	if scope == "" {
		return model.ScopeAny
	}
	return scope
}

func providerCredentialBindingAccountID(credential *model.ProviderCredential) *string {
	if credential == nil {
		return nil
	}
	return credential.BindingAccountID
}

func (s *SQLiteStore) UpdateProviderCredential(ctx context.Context, id string, credentialType model.ProviderCredentialType, credentialData string) error {
	ownedCtx, release, err := s.WithProviderCredentialMutations(ctx, []string{id})
	if err != nil {
		return fmt.Errorf("update provider credential %q: %w", id, err)
	}
	defer release()

	now := s.clock.Now()
	if err := s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
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
		if err := validateExclusiveCredentialBinding(
			tx,
			id,
			providerCredentialBindingAccountID(supplemental.credential),
		); err != nil {
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
	ownedCtx, release, err := s.WithProviderCredentialMutations(ctx, []string{id})
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	defer release()

	_, err = s.ruleRepository.Coordinate(ownedCtx, nil, func(tx *gorm.DB, currentRules []errorrule.Rule) ([]errorrule.Rule, error) {
		referencedBy, err := findRoutingPolicyTargetingProvider(tx, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if referencedBy != nil {
			return nil, &RoutingPolicyProviderReferenceConflictError{
				ProviderID: id,
				PolicyID:   referencedBy.ID,
				Key:        referencedBy.NaturalKey(),
			}
		}
		// Delete API types first
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return nil, err
		}
		// Delete provider credential state first so the migration shadow never leaves
		// orphaned secret/auth rows behind even if SQLite foreign-key pragmas differ.
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderCredential{}).Error; err != nil {
			return nil, err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderAuthState{}).Error; err != nil {
			return nil, err
		}
		// Delete health state
		if err := tx.Where("provider_id = ?", id).Delete(&model.HealthState{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return nil, err
		}
		// Delete provider
		if err := tx.Delete(&model.Provider{}, "id = ?", id).Error; err != nil {
			return nil, err
		}
		return errorrulesqlite.RemoveProviderRules(currentRules, id), nil
	})
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	return nil
}
