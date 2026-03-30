package store

import (
	"context"
	"errors"
	"fmt"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

// ConfigImportRoutingPolicyMode makes routing-policy scope explicit at the store
// boundary so callers never rely on empty slices to mean two different things.
type ConfigImportRoutingPolicyMode string

const (
	// Replace keeps the export file authoritative for full imports, so missing rules
	// are removed before provider updates are validated inside the same transaction.
	ConfigImportRoutingPolicyModeReplace ConfigImportRoutingPolicyMode = "replace"
	// Preserve lets scoped imports leave routing policies out of scope without
	// accidentally turning omission into a destructive delete-all.
	ConfigImportRoutingPolicyModePreserve ConfigImportRoutingPolicyMode = "preserve"
)

// ConfigImportBundle captures the normalized, fully validated import payload
// that the store can apply atomically without re-running admin-level staging.
type ConfigImportBundle struct {
	Groups            []model.Group
	Providers         []model.Provider
	RoutingPolicyMode ConfigImportRoutingPolicyMode
	RoutingPolicies   []model.RoutingPolicy
	Settings          map[string]string
}

func (s *SQLiteStore) ApplyConfigImport(ctx context.Context, bundle *ConfigImportBundle) error {
	if bundle == nil {
		return nil
	}

	routingPolicyMode, err := resolveConfigImportRoutingPolicyMode(bundle)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := &SQLiteStore{db: tx, clock: s.clock}
		if err := applyImportedGroups(ctx, txStore, bundle.Groups); err != nil {
			return err
		}
		if err := applyImportedRoutingPolicies(
			ctx,
			txStore,
			tx,
			routingPolicyMode,
			bundle.RoutingPolicies,
		); err != nil {
			return err
		}
		if err := applyImportedProviders(ctx, txStore, bundle.Providers); err != nil {
			return err
		}
		return applyImportedSettings(ctx, txStore, bundle.Settings)
	})
}

func applyImportedGroups(
	ctx context.Context,
	txStore *SQLiteStore,
	groups []model.Group,
) error {
	for i := range groups {
		group := groups[i]
		existing, err := txStore.GetGroup(ctx, group.ID)
		switch {
		case err == nil:
			group.CreatedAt = existing.CreatedAt
			if err := txStore.UpdateGroup(ctx, &group); err != nil {
				return err
			}
		case errors.Is(err, ErrNotFound):
			if err := txStore.CreateGroup(ctx, &group); err != nil {
				return err
			}
		default:
			return fmt.Errorf("upsert group %q: %w", group.ID, err)
		}
	}
	return nil
}

func resolveConfigImportRoutingPolicyMode(
	bundle *ConfigImportBundle,
) (ConfigImportRoutingPolicyMode, error) {
	switch bundle.RoutingPolicyMode {
	case ConfigImportRoutingPolicyModeReplace:
		return ConfigImportRoutingPolicyModeReplace, nil
	case ConfigImportRoutingPolicyModePreserve:
		if len(bundle.RoutingPolicies) != 0 {
			return "", fmt.Errorf(
				"routing policy import mode %q cannot include imported routing policies",
				ConfigImportRoutingPolicyModePreserve,
			)
		}
		return ConfigImportRoutingPolicyModePreserve, nil
	case "":
		return "", fmt.Errorf("routing policy import mode is required")
	default:
		return "", fmt.Errorf(
			"unsupported routing policy import mode %q",
			bundle.RoutingPolicyMode,
		)
	}
}

func applyImportedRoutingPolicies(
	ctx context.Context,
	txStore *SQLiteStore,
	tx *gorm.DB,
	mode ConfigImportRoutingPolicyMode,
	policies []model.RoutingPolicy,
) error {
	switch mode {
	case ConfigImportRoutingPolicyModePreserve:
		return nil
	case ConfigImportRoutingPolicyModeReplace:
		return replaceImportedRoutingPolicies(ctx, txStore, tx, policies)
	default:
		return fmt.Errorf("unsupported routing policy import mode %q", mode)
	}
}

func replaceImportedRoutingPolicies(
	ctx context.Context,
	txStore *SQLiteStore,
	tx *gorm.DB,
	policies []model.RoutingPolicy,
) error {
	existingPolicies, err := txStore.ListRoutingPolicies(ctx)
	if err != nil {
		return fmt.Errorf("list routing policies for import: %w", err)
	}
	if err := deleteRemovedRoutingPolicies(tx, existingPolicies, policies); err != nil {
		return err
	}
	for i := range policies {
		if err := upsertImportedRoutingPolicy(ctx, txStore, tx, policies[i]); err != nil {
			return err
		}
	}
	return nil
}

func deleteRemovedRoutingPolicies(
	tx *gorm.DB,
	existingPolicies []model.RoutingPolicy,
	desiredPolicies []model.RoutingPolicy,
) error {
	desiredPolicyKeys := make(map[model.RoutingPolicyNaturalKey]struct{}, len(desiredPolicies))
	for i := range desiredPolicies {
		desiredPolicyKeys[desiredPolicies[i].NaturalKey()] = struct{}{}
	}
	for i := range existingPolicies {
		key := existingPolicies[i].NaturalKey()
		if _, keep := desiredPolicyKeys[key]; keep {
			continue
		}
		if err := deleteRoutingPolicyRecord(tx, existingPolicies[i].ID); err != nil {
			return fmt.Errorf("delete routing policy for api_type %q: %w", existingPolicies[i].APIType, err)
		}
	}
	return nil
}

func upsertImportedRoutingPolicy(
	ctx context.Context,
	txStore *SQLiteStore,
	tx *gorm.DB,
	policy model.RoutingPolicy,
) error {
	existing, err := findRoutingPolicyByNaturalKey(tx, policy.NaturalKey())
	switch {
	case err == nil:
		policy.ID = existing.ID
		policy.CreatedAt = existing.CreatedAt
		if err := txStore.UpdateRoutingPolicy(ctx, &policy); err != nil {
			return err
		}
	case errors.Is(err, ErrNotFound):
		if err := txStore.CreateRoutingPolicy(ctx, &policy); err != nil {
			return err
		}
	default:
		return fmt.Errorf("upsert routing policy for api_type %q: %w", policy.APIType, err)
	}
	return nil
}

func applyImportedProviders(
	ctx context.Context,
	txStore *SQLiteStore,
	providers []model.Provider,
) error {
	for i := range providers {
		provider := providers[i]
		existing, err := txStore.GetProvider(ctx, provider.ID)
		switch {
		case err == nil:
			provider.CreatedAt = existing.CreatedAt
			if err := txStore.UpdateProvider(ctx, &provider); err != nil {
				return err
			}
		case errors.Is(err, ErrNotFound):
			if err := txStore.CreateProvider(ctx, &provider); err != nil {
				return err
			}
		default:
			return fmt.Errorf("upsert provider %q: %w", provider.ID, err)
		}
	}
	return nil
}

func applyImportedSettings(
	ctx context.Context,
	txStore *SQLiteStore,
	settings map[string]string,
) error {
	if len(settings) == 0 {
		return nil
	}
	return txStore.SetConfigs(ctx, settings)
}

func deleteRoutingPolicyRecord(tx *gorm.DB, id uint) error {
	if err := tx.Where("routing_policy_id = ?", id).Delete(&model.RoutingPolicyGroup{}).Error; err != nil {
		return err
	}
	if err := tx.Where("routing_policy_id = ?", id).Delete(&model.RoutingPolicyVendor{}).Error; err != nil {
		return err
	}
	return tx.Delete(&model.RoutingPolicy{}, "id = ?", id).Error
}
