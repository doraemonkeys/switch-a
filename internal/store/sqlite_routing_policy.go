package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

func routingPolicyQuery(db *gorm.DB) *gorm.DB {
	return db.
		Preload("Groups", func(db *gorm.DB) *gorm.DB {
			return db.Order("group_id ASC")
		}).
		Preload("Vendors", func(db *gorm.DB) *gorm.DB {
			return db.Order("vendor ASC")
		})
}

func normalizeRoutingPolicyRecord(policy *model.RoutingPolicy) model.RoutingPolicy {
	record := model.RoutingPolicy{
		ID:               policy.ID,
		APIType:          strings.TrimSpace(policy.APIType),
		ModelMatchType:   model.RoutingPolicyModelMatchType(strings.TrimSpace(string(policy.ModelMatchType))),
		ModelMatchValue:  strings.TrimSpace(policy.ModelMatchValue),
		Enabled:          policy.Enabled,
		TargetProviderID: normalizeRoutingPolicyTargetProviderID(policy.TargetProviderID),
		CreatedAt:        policy.CreatedAt,
		UpdatedAt:        policy.UpdatedAt,
	}
	if record.ModelMatchType == model.RoutingPolicyModelMatchTypeNone {
		record.ModelMatchValue = ""
	}
	if record.TargetProviderID == nil {
		record.Groups = normalizeRoutingPolicyGroups(policy.Groups)
		record.Vendors = normalizeRoutingPolicyVendors(policy.Vendors)
	}
	return record
}

func normalizeRoutingPolicyTargetProviderID(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func routingPolicyEnabledValue(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}

func findRoutingPolicyByNaturalKey(
	tx *gorm.DB,
	key model.RoutingPolicyNaturalKey,
) (*model.RoutingPolicy, error) {
	var policy model.RoutingPolicy
	err := routingPolicyQuery(tx).
		Where(
			"api_type = ? AND model_match_type = ? AND model_match_value = ?",
			key.APIType,
			key.ModelMatchType,
			key.ModelMatchValue,
		).
		First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func findRoutingPolicyReferencingGroup(tx *gorm.DB, groupID string) (*model.RoutingPolicy, error) {
	var policy model.RoutingPolicy
	err := routingPolicyQuery(tx).
		Joins("JOIN routing_policy_groups ON routing_policy_groups.routing_policy_id = routing_policies.id").
		Where("routing_policy_groups.group_id = ?", groupID).
		Order("routing_policies.id ASC").
		First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func findRoutingPolicyTargetingProvider(tx *gorm.DB, providerID string) (*model.RoutingPolicy, error) {
	var policy model.RoutingPolicy
	err := routingPolicyQuery(tx).
		Where("target_provider_id = ?", providerID).
		Order("id ASC").
		First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func findExactProviderRoutingPolicyMissingAPIType(
	tx *gorm.DB,
	providerID string,
	supportedAPITypes map[string]struct{},
) (*model.RoutingPolicy, error) {
	var policies []model.RoutingPolicy
	if err := routingPolicyQuery(tx).
		Where("target_provider_id = ?", providerID).
		Order("id ASC").
		Find(&policies).Error; err != nil {
		return nil, err
	}
	for i := range policies {
		if _, ok := supportedAPITypes[policies[i].APIType]; ok {
			continue
		}
		return &policies[i], nil
	}
	return nil, ErrNotFound
}

func normalizeRoutingPolicyGroups(groups []model.RoutingPolicyGroup) []model.RoutingPolicyGroup {
	if len(groups) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(groups))
	normalized := make([]model.RoutingPolicyGroup, 0, len(groups))
	for _, group := range groups {
		groupID := strings.TrimSpace(group.GroupID)
		if groupID == "" {
			continue
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		seen[groupID] = struct{}{}
		normalized = append(normalized, model.RoutingPolicyGroup{
			RoutingPolicyID: group.RoutingPolicyID,
			GroupID:         groupID,
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].GroupID < normalized[j].GroupID
	})
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeRoutingPolicyVendors(vendors []model.RoutingPolicyVendor) []model.RoutingPolicyVendor {
	if len(vendors) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(vendors))
	normalized := make([]model.RoutingPolicyVendor, 0, len(vendors))
	for _, vendor := range vendors {
		name := strings.TrimSpace(vendor.Vendor)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, model.RoutingPolicyVendor{
			RoutingPolicyID: vendor.RoutingPolicyID,
			Vendor:          name,
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Vendor < normalized[j].Vendor
	})
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func ensureRoutingPolicyUnique(tx *gorm.DB, excludedID uint, policy *model.RoutingPolicy) error {
	var existing model.RoutingPolicy
	err := tx.
		Select("id").
		Where("api_type = ? AND model_match_type = ? AND model_match_value = ?", policy.APIType, policy.ModelMatchType, policy.ModelMatchValue).
		Where("id <> ?", excludedID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return &RoutingPolicyConflictError{
		ExistingID:      existing.ID,
		APIType:         policy.APIType,
		ModelMatchType:  policy.ModelMatchType,
		ModelMatchValue: policy.ModelMatchValue,
	}
}

func replaceRoutingPolicyScopes(
	tx *gorm.DB,
	policyID uint,
	groups []model.RoutingPolicyGroup,
	vendors []model.RoutingPolicyVendor,
) error {
	if err := tx.Where("routing_policy_id = ?", policyID).Delete(&model.RoutingPolicyGroup{}).Error; err != nil {
		return err
	}
	if err := tx.Where("routing_policy_id = ?", policyID).Delete(&model.RoutingPolicyVendor{}).Error; err != nil {
		return err
	}

	for _, group := range groups {
		row := model.RoutingPolicyGroup{
			RoutingPolicyID: policyID,
			GroupID:         group.GroupID,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	for _, vendor := range vendors {
		row := model.RoutingPolicyVendor{
			RoutingPolicyID: policyID,
			Vendor:          vendor.Vendor,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func loadRoutingPolicy(tx *gorm.DB, id uint) (*model.RoutingPolicy, error) {
	var policy model.RoutingPolicy
	if err := routingPolicyQuery(tx).First(&policy, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &policy, nil
}

func (s *SQLiteStore) ListRoutingPolicies(ctx context.Context) ([]model.RoutingPolicy, error) {
	var policies []model.RoutingPolicy
	if err := routingPolicyQuery(s.db.WithContext(ctx)).
		Order("id ASC").
		Find(&policies).Error; err != nil {
		return nil, fmt.Errorf("list routing policies: %w", err)
	}
	return policies, nil
}

func (s *SQLiteStore) GetRoutingPolicy(ctx context.Context, id uint) (*model.RoutingPolicy, error) {
	policy, err := loadRoutingPolicy(s.db.WithContext(ctx), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("get routing policy %d: %w", id, err)
	}
	return policy, nil
}

func (s *SQLiteStore) CreateRoutingPolicy(ctx context.Context, policy *model.RoutingPolicy) error {
	record := normalizeRoutingPolicyRecord(policy)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureRoutingPolicyUnique(tx, 0, &record); err != nil {
			return err
		}
		now := s.clock.Now()
		if record.CreatedAt.IsZero() {
			record.CreatedAt = now
		}
		if record.UpdatedAt.IsZero() {
			record.UpdatedAt = now
		}
		if record.ID == 0 {
			if err := tx.Exec(
				`INSERT INTO routing_policies
					(api_type, model_match_type, model_match_value, enabled, target_provider_id, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				record.APIType,
				record.ModelMatchType,
				record.ModelMatchValue,
				routingPolicyEnabledValue(record.Enabled),
				record.TargetProviderID,
				record.CreatedAt,
				record.UpdatedAt,
			).Error; err != nil {
				return err
			}
			if err := tx.Raw(`SELECT last_insert_rowid()`).Scan(&record.ID).Error; err != nil {
				return err
			}
		} else if err := tx.Exec(
			`INSERT INTO routing_policies
				(id, api_type, model_match_type, model_match_value, enabled, target_provider_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID,
			record.APIType,
			record.ModelMatchType,
			record.ModelMatchValue,
			routingPolicyEnabledValue(record.Enabled),
			record.TargetProviderID,
			record.CreatedAt,
			record.UpdatedAt,
		).Error; err != nil {
			return err
		}
		if err := replaceRoutingPolicyScopes(tx, record.ID, record.Groups, record.Vendors); err != nil {
			return err
		}
		loaded, err := loadRoutingPolicy(tx, record.ID)
		if err != nil {
			return err
		}
		record = *loaded
		return nil
	})
	if err != nil {
		return fmt.Errorf("create routing policy: %w", err)
	}
	*policy = record
	return nil
}

func (s *SQLiteStore) UpdateRoutingPolicy(ctx context.Context, policy *model.RoutingPolicy) error {
	record := normalizeRoutingPolicyRecord(policy)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := loadRoutingPolicy(tx, record.ID)
		if err != nil {
			return err
		}
		if err := ensureRoutingPolicyUnique(tx, record.ID, &record); err != nil {
			return err
		}

		record.CreatedAt = current.CreatedAt
		record.UpdatedAt = s.clock.Now()
		if err := tx.Model(&model.RoutingPolicy{}).
			Where("id = ?", record.ID).
			Updates(map[string]any{
				"api_type":           record.APIType,
				"model_match_type":   record.ModelMatchType,
				"model_match_value":  record.ModelMatchValue,
				"enabled":            routingPolicyEnabledValue(record.Enabled),
				"target_provider_id": record.TargetProviderID,
				"updated_at":         record.UpdatedAt,
			}).Error; err != nil {
			return err
		}
		if err := replaceRoutingPolicyScopes(tx, record.ID, record.Groups, record.Vendors); err != nil {
			return err
		}
		loaded, err := loadRoutingPolicy(tx, record.ID)
		if err != nil {
			return err
		}
		record = *loaded
		return nil
	})
	if err != nil {
		return fmt.Errorf("update routing policy %d: %w", policy.ID, err)
	}
	*policy = record
	return nil
}

func (s *SQLiteStore) DeleteRoutingPolicy(ctx context.Context, id uint) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := loadRoutingPolicy(tx, id); err != nil {
			return err
		}
		if err := tx.Where("routing_policy_id = ?", id).Delete(&model.RoutingPolicyGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("routing_policy_id = ?", id).Delete(&model.RoutingPolicyVendor{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.RoutingPolicy{}, "id = ?", id).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete routing policy %d: %w", id, err)
	}
	return nil
}
