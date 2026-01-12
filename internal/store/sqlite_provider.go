package store

import (
	"context"
	"errors"
	"fmt"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

// Provider operations

func (s *SQLiteStore) ListProviders(ctx context.Context) ([]model.Provider, error) {
	var providers []model.Provider
	if err := s.db.WithContext(ctx).Preload("APITypes").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	return providers, nil
}

func (s *SQLiteStore) ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error) {
	var providers []model.Provider
	err := s.db.WithContext(ctx).
		Preload("APITypes").
		Distinct().
		Joins("JOIN provider_api_types ON provider_api_types.provider_id = providers.id").
		Where("provider_api_types.api_type = ? AND providers.enabled = ?", apiType, true).
		Find(&providers).Error
	if err != nil {
		return nil, fmt.Errorf("list providers by api type %q: %w", apiType, err)
	}
	return providers, nil
}

func (s *SQLiteStore) GetProvider(ctx context.Context, id string) (*model.Provider, error) {
	var provider model.Provider
	err := s.db.WithContext(ctx).Preload("APITypes").First(&provider, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", id, err)
	}
	return &provider, nil
}

func (s *SQLiteStore) CreateProvider(ctx context.Context, p *model.Provider) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Use raw SQL to properly handle boolean false values
		now := s.clock.Now()
		if p.CreatedAt.IsZero() {
			p.CreatedAt = now
		}
		if p.UpdatedAt.IsZero() {
			p.UpdatedAt = now
		}

		if err := tx.Exec(`
			INSERT INTO providers (id, name, base_url, api_key, auth_mode, group_id, weight, priority, concurrency, max_retries, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, p.ID, p.Name, p.BaseURL, p.APIKey, p.AuthMode, p.GroupID, p.Weight, p.Priority, p.Concurrency, p.MaxRetries, p.Enabled, p.CreatedAt, p.UpdatedAt).Error; err != nil { // coverage-ignore -- INSERT rarely fails with valid data
			return err
		}
		// Create API types separately
		for i := range p.APITypes {
			p.APITypes[i].ProviderID = p.ID
			if err := tx.Create(&p.APITypes[i]).Error; err != nil { // coverage-ignore -- transaction error after successful provider insert is rare
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateProvider(ctx context.Context, p *model.Provider) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete existing API types
		if err := tx.Where("provider_id = ?", p.ID).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}

		// Temporarily clear APITypes to avoid GORM trying to update them via Save
		apiTypes := p.APITypes
		p.APITypes = nil

		// Save provider (without APITypes)
		if err := tx.Save(p).Error; err != nil {
			return err
		}

		// Restore and create new API types explicitly
		p.APITypes = apiTypes
		for i := range p.APITypes {
			p.APITypes[i].ProviderID = p.ID
			if err := tx.Create(&p.APITypes[i]).Error; err != nil { // coverage-ignore -- transaction error after successful provider save is rare
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("update provider %q: %w", p.ID, err)
	}
	return nil
}

func (s *SQLiteStore) DeleteProvider(ctx context.Context, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete API types first
		if err := tx.Where("provider_id = ?", id).Delete(&model.ProviderAPIType{}).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
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
