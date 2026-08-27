package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

func providerAPITypeSet(apiTypes []model.ProviderAPIType) map[string]struct{} {
	supported := make(map[string]struct{}, len(apiTypes))
	for _, apiType := range apiTypes {
		supported[apiType.APIType] = struct{}{}
	}
	return supported
}

func providerQuery(db *gorm.DB) *gorm.DB {
	return db.Preload("APITypes")
}

func (s *SQLiteStore) ListProviders(ctx context.Context) ([]model.Provider, error) {
	var providers []model.Provider
	if err := providerQuery(s.db.WithContext(ctx)).Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	if err := s.hydrateProviderCredentialSessions(ctx, providers); err != nil {
		return nil, err
	}
	return providers, nil
}

func (s *SQLiteStore) ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error) {
	var providers []model.Provider
	err := providerQuery(s.db.WithContext(ctx)).Distinct().
		Joins("JOIN provider_api_types ON provider_api_types.provider_id = providers.id").
		Where("provider_api_types.api_type = ? AND providers.enabled = ?", apiType, true).
		Find(&providers).Error
	if err != nil {
		return nil, fmt.Errorf("list providers by api type %q: %w", apiType, err)
	}
	if err := s.hydrateProviderCredentialSessions(ctx, providers); err != nil {
		return nil, err
	}
	return providers, nil
}

func (s *SQLiteStore) GetProvider(ctx context.Context, id string) (*model.Provider, error) {
	var provider model.Provider
	err := providerQuery(s.db.WithContext(ctx)).First(&provider, "id = ?", strings.TrimSpace(id)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", id, err)
	}
	providers := []model.Provider{provider}
	if err := s.hydrateProviderCredentialSessions(ctx, providers); err != nil {
		return nil, err
	}
	return &providers[0], nil
}

func (s *SQLiteStore) hydrateProviderCredentialSessions(ctx context.Context, providers []model.Provider) error {
	ids := make([]string, 0, len(providers))
	for index := range providers {
		ids = append(ids, providers[index].ID)
	}
	snapshots, err := s.credentialSessions.ListRouteSnapshots(ctx, ids)
	if err != nil {
		return fmt.Errorf("hydrate provider credential sessions: %w", err)
	}
	for index := range providers {
		providers[index].CredentialSessions = snapshots[providers[index].ID]
	}
	return nil
}

func (s *SQLiteStore) CreateProvider(ctx context.Context, provider *model.Provider) error {
	if provider == nil {
		return fmt.Errorf("create provider: provider is nil")
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.createProviderInTransaction(ctx, tx, provider)
	}); err != nil {
		return fmt.Errorf("create provider %q: %w", provider.ID, err)
	}
	return nil
}

func (s *SQLiteStore) createProviderInTransaction(ctx context.Context, tx *gorm.DB, provider *model.Provider) error {
	bindings, err := credentialBindingsForProvider(provider)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = now
	}
	if provider.UpdatedAt.IsZero() {
		provider.UpdatedAt = now
	}
	provider.FailoverScope = providerScopeOrAny(provider.FailoverScope)
	provider.AcceptFailover = providerScopeOrAny(provider.AcceptFailover)
	if err := tx.Omit("APITypes", "CredentialSessions", "Health", "Group").Create(provider).Error; err != nil {
		return err
	}
	for index := range provider.APITypes {
		provider.APITypes[index].ProviderID = provider.ID
		if err := tx.Create(&provider.APITypes[index]).Error; err != nil {
			return err
		}
	}
	repository, err := s.credentialSessions.WithDB(tx)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := repository.Bind(ctx, binding); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) UpdateProvider(ctx context.Context, provider *model.Provider) error {
	if provider == nil {
		return fmt.Errorf("update provider: provider is nil")
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.updateProviderInTransaction(ctx, tx, provider)
	}); err != nil {
		return fmt.Errorf("update provider %q: %w", provider.ID, err)
	}
	return nil
}

func (s *SQLiteStore) updateProviderInTransaction(ctx context.Context, tx *gorm.DB, provider *model.Provider) error {
	bindings, err := credentialBindingsForProvider(provider)
	if err != nil {
		return err
	}
	if err := validateProviderAPITypeUpdate(tx, provider); err != nil {
		return err
	}
	repository, err := s.credentialSessions.WithDB(tx)
	if err != nil {
		return err
	}
	// References must disappear before their API-type parents, independent of
	// whether SQLite foreign-key enforcement is enabled for this connection.
	if err := repository.DeleteRouteBindings(ctx, provider.ID); err != nil {
		return err
	}
	if err := tx.Where("provider_id = ?", provider.ID).Delete(&model.ProviderAPIType{}).Error; err != nil {
		return err
	}
	if err := saveProviderWithoutAssociations(tx, provider); err != nil {
		return err
	}
	for index := range provider.APITypes {
		provider.APITypes[index].ProviderID = provider.ID
		if err := tx.Create(&provider.APITypes[index]).Error; err != nil {
			return err
		}
	}
	for _, binding := range bindings {
		if err := repository.Bind(ctx, binding); err != nil {
			return err
		}
	}
	return nil
}

func credentialBindingsForProvider(provider *model.Provider) ([]credentialsession.RouteBinding, error) {
	if provider == nil || strings.TrimSpace(provider.ID) == "" {
		return nil, fmt.Errorf("provider ID is required")
	}
	supported := providerAPITypeSet(provider.APITypes)
	bindings := make([]credentialsession.RouteBinding, 0, len(provider.CredentialSessions))
	seen := make(map[string]struct{}, len(provider.CredentialSessions))
	for _, route := range provider.CredentialSessions {
		apiType := strings.TrimSpace(route.APIType)
		if _, exists := supported[apiType]; !exists {
			return nil, fmt.Errorf("credential session reference targets unsupported API type %q", apiType)
		}
		if _, duplicate := seen[apiType]; duplicate {
			return nil, fmt.Errorf("duplicate credential session reference for API type %q", apiType)
		}
		seen[apiType] = struct{}{}
		bindings = append(bindings, credentialsession.RouteBinding{
			RouteTargetID: provider.ID,
			APIType:       apiType,
			SessionID:     strings.TrimSpace(route.Credential.SessionID),
		})
	}
	for apiType := range supported {
		if _, exists := seen[apiType]; !exists {
			return nil, fmt.Errorf("provider API type %q requires a credential session reference", apiType)
		}
	}
	return bindings, nil
}

func validateProviderAPITypeUpdate(tx *gorm.DB, provider *model.Provider) error {
	missingPolicy, err := findExactProviderRoutingPolicyMissingAPIType(tx, provider.ID, providerAPITypeSet(provider.APITypes))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if missingPolicy == nil {
		return nil
	}
	return &RoutingPolicyProviderAPITypeConflictError{
		ProviderID: provider.ID,
		APIType:    missingPolicy.APIType,
		PolicyID:   missingPolicy.ID,
		Key:        missingPolicy.NaturalKey(),
	}
}

func saveProviderWithoutAssociations(tx *gorm.DB, provider *model.Provider) error {
	provider.FailoverScope = providerScopeOrAny(provider.FailoverScope)
	provider.AcceptFailover = providerScopeOrAny(provider.AcceptFailover)
	return tx.Omit("APITypes", "CredentialSessions", "Health", "Group").Save(provider).Error
}

func providerScopeOrAny(scope model.Scope) model.Scope {
	if scope == "" {
		return model.ScopeAny
	}
	return scope
}

func (s *SQLiteStore) DeleteProvider(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	provider, err := s.GetProvider(ctx, id)
	if err != nil {
		return err
	}
	ownedCtx, release, err := s.credentialMutations.With(ctx, provider.CredentialSessionIDs())
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	defer release()

	_, err = s.ruleRepository.Coordinate(ownedCtx, nil, func(tx *gorm.DB, currentRules []errorrule.Rule) ([]errorrule.Rule, error) {
		referencedBy, lookupErr := findRoutingPolicyTargetingProvider(tx, id)
		if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
			return nil, lookupErr
		}
		if referencedBy != nil {
			return nil, &RoutingPolicyProviderReferenceConflictError{
				ProviderID: id,
				PolicyID:   referencedBy.ID,
				Key:        referencedBy.NaturalKey(),
			}
		}
		repository, repoErr := s.credentialSessions.WithDB(tx)
		if repoErr != nil {
			return nil, repoErr
		}
		if repoErr = repository.DeleteRouteBindings(ownedCtx, id); repoErr != nil {
			return nil, repoErr
		}
		if repoErr = tx.Where("provider_id = ?", id).Delete(&model.ProviderAPIType{}).Error; repoErr != nil {
			return nil, repoErr
		}
		if repoErr = tx.Where("provider_id = ?", id).Delete(&model.HealthState{}).Error; repoErr != nil {
			return nil, repoErr
		}
		result := tx.Delete(&model.Provider{}, "id = ?", id)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, ErrNotFound
		}
		return errorrulesqlite.RemoveProviderRules(currentRules, id), nil
	})
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	return nil
}
