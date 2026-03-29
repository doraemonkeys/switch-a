package store

import (
	"context"
	"errors"
	"fmt"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

func (s *SQLiteStore) GetProviderAuthState(ctx context.Context, providerID string) (*model.ProviderAuthState, error) {
	var providerRecord struct {
		CredentialType model.ProviderCredentialType
	}
	if err := s.db.WithContext(ctx).
		Model(&model.Provider{}).
		Select("credential_type").
		Where("id = ?", providerID).
		First(&providerRecord).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get provider credential type %q: %w", providerID, err)
	}

	var authState model.ProviderAuthState
	err := s.db.WithContext(ctx).Where("provider_id = ?", providerID).First(&authState).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider auth state %q: %w", providerID, err)
	}
	return model.NormalizeProviderAuthStateRecord(
		providerID,
		model.NormalizeProviderCredentialType(providerRecord.CredentialType),
		&authState,
	), nil
}

func (s *SQLiteStore) ListRoutingPoliciesByAPIType(ctx context.Context, apiType string) ([]model.RoutingPolicy, error) {
	var policies []model.RoutingPolicy
	if err := routingPolicyQuery(s.db.WithContext(ctx)).
		Where("api_type = ? AND enabled = ?", apiType, true).
		Order("id ASC").
		Find(&policies).Error; err != nil {
		return nil, fmt.Errorf("list routing policies by api type %q: %w", apiType, err)
	}
	return policies, nil
}
