package store

import (
	"context"
	"errors"
	"fmt"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

func (s *SQLiteStore) ListGroups(ctx context.Context) ([]model.Group, error) {
	var groups []model.Group
	if err := s.db.WithContext(ctx).Preload("Providers").Preload("Providers.APITypes").Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return groups, nil
}

func (s *SQLiteStore) GetGroup(ctx context.Context, id string) (*model.Group, error) {
	var group model.Group
	err := s.db.WithContext(ctx).Preload("Providers").Preload("Providers.APITypes").First(&group, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get group %q: %w", id, err)
	}
	return &group, nil
}

func (s *SQLiteStore) CreateGroup(ctx context.Context, g *model.Group) error {
	if err := s.db.WithContext(ctx).Create(g).Error; err != nil {
		return fmt.Errorf("create group %q: %w", g.ID, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateGroup(ctx context.Context, g *model.Group) error {
	// Preserve providers to avoid GORM trying to update the association.
	// This is an ORM implementation detail that should be handled here.
	providers := g.Providers
	g.Providers = nil
	if err := s.db.WithContext(ctx).Save(g).Error; err != nil {
		return fmt.Errorf("update group %q: %w", g.ID, err)
	}
	// Restore providers so the returned Group object has the association
	g.Providers = providers
	return nil
}

func (s *SQLiteStore) DeleteGroup(ctx context.Context, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		referencedBy, err := findRoutingPolicyReferencingGroup(tx, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if referencedBy != nil {
			return &RoutingPolicyGroupReferenceConflictError{
				GroupID:  id,
				PolicyID: referencedBy.ID,
				Key:      referencedBy.NaturalKey(),
			}
		}
		// First update providers to remove group reference
		if err := tx.Model(&model.Provider{}).
			Where("group_id = ?", id).
			Update("group_id", nil).Error; err != nil { // coverage-ignore -- UPDATE rarely fails within transaction
			return fmt.Errorf("unlink providers: %w", err)
		}
		// Then delete the group
		if err := tx.Delete(&model.Group{}, "id = ?", id).Error; err != nil { // coverage-ignore -- DELETE rarely fails within transaction
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete group %q: %w", id, err)
	}
	return nil
}
