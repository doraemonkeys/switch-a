package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

var ErrConfigImportChatGPTCredentialMaterialMutation = errors.New("config import cannot write ChatGPT credential material")

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
	Groups               []model.Group
	CredentialSessions   []credentialsession.Session
	Providers            []model.Provider
	RoutingPolicyMode    ConfigImportRoutingPolicyMode
	RoutingPolicies      []model.RoutingPolicy
	Settings             map[string]string
	RuleImport           errorrulesqlite.ImportRequest
	ExpectedRuleRevision *errorrule.Revision
}

func (s *SQLiteStore) ApplyConfigImport(ctx context.Context, bundle *ConfigImportBundle) error {
	if bundle == nil {
		return nil
	}

	routingPolicyMode, err := resolveConfigImportRoutingPolicyMode(bundle)
	if err != nil {
		return err
	}
	sessionIDs := make([]string, 0, len(bundle.CredentialSessions))
	for i := range bundle.CredentialSessions {
		sessionIDs = append(sessionIDs, bundle.CredentialSessions[i].ID)
	}
	for i := range bundle.Providers {
		sessionIDs = append(sessionIDs, bundle.Providers[i].CredentialSessionIDs()...)
	}
	ownedCtx, release, err := s.WithCredentialSessionMutations(ctx, sessionIDs)
	if err != nil {
		return fmt.Errorf("apply config import: %w", err)
	}
	defer release()

	ruleImport := bundle.RuleImport
	if ruleImport.Mode == "" {
		ruleImport.Mode = errorrulesqlite.ImportModePreserve
	}
	applyRecords := func(tx *gorm.DB) error {
		txStore := &SQLiteStore{
			db:                  tx,
			clock:               s.clock,
			credentialMutations: s.credentialMutations,
			credentialSessions:  nil,
			credentialSigning:   s.credentialSigning,
			ruleRepository:      s.ruleRepository,
		}
		txStore.credentialSessions, err = s.credentialSessions.WithDB(tx)
		if err != nil {
			return err
		}
		if err := applyImportedGroups(ownedCtx, txStore, bundle.Groups); err != nil {
			return err
		}
		if err := applyImportedRoutingPolicies(
			ownedCtx,
			txStore,
			tx,
			routingPolicyMode,
			bundle.RoutingPolicies,
		); err != nil {
			return err
		}
		if err := applyImportedCredentialSessions(ownedCtx, txStore, bundle.CredentialSessions); err != nil {
			return err
		}
		if err := applyImportedProviders(ownedCtx, txStore, bundle.Providers); err != nil {
			return err
		}
		return applyImportedSettings(ownedCtx, txStore, bundle.Settings)
	}

	if ruleImport.Mode == errorrulesqlite.ImportModePreserve {
		if bundle.ExpectedRuleRevision != nil {
			return fmt.Errorf("settings-only config import cannot precondition the rule set")
		}
		// Settings-only import has no rule partition. Avoiding the coordinator here
		// preserves that semantic boundary and prevents unrelated settings writes
		// from contending with rule CRUD.
		return s.db.WithContext(ownedCtx).Transaction(applyRecords)
	}

	_, err = s.ruleRepository.Coordinate(
		ownedCtx,
		bundle.ExpectedRuleRevision,
		func(tx *gorm.DB, currentRules []errorrule.Rule) ([]errorrule.Rule, error) {
			if err := applyRecords(tx); err != nil {
				return nil, err
			}
			candidate, _, err := errorrulesqlite.BuildImportCandidate(currentRules, ruleImport)
			return candidate, err
		})
	return err
}

func applyImportedCredentialSessions(
	ctx context.Context,
	txStore *SQLiteStore,
	sessions []credentialsession.Session,
) error {
	for index := range sessions {
		candidate := sessions[index]
		current, err := txStore.GetCredentialSession(ctx, candidate.ID)
		switch {
		case err == nil:
			if err := applyExistingImportedCredentialSession(ctx, txStore, current, candidate); err != nil {
				return err
			}
		case errors.Is(err, credentialsession.ErrNotFound):
			if candidate.Kind == credentialsession.KindChatGPT && !candidate.IsReauthenticationPlaceholder() {
				return fmt.Errorf("%w: session %q", ErrConfigImportChatGPTCredentialMaterialMutation, candidate.ID)
			}
			if _, err := txStore.CreateCredentialSession(ctx, &candidate); err != nil {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

func applyExistingImportedCredentialSession(
	ctx context.Context,
	txStore *SQLiteStore,
	current *credentialsession.Session,
	candidate credentialsession.Session,
) error {
	if current.Kind != candidate.Kind {
		return fmt.Errorf("credential session %q kind is immutable", candidate.ID)
	}
	if candidate.Kind == credentialsession.KindChatGPT {
		if !candidate.IsReauthenticationPlaceholder() {
			// Config restore owns names and route topology, while verified login owns
			// every byte that can authenticate or establish account identity.
			return fmt.Errorf("%w: session %q", ErrConfigImportChatGPTCredentialMaterialMutation, candidate.ID)
		}
		if strings.TrimSpace(candidate.Name) == "" || candidate.Name == current.Name {
			return nil
		}
		_, err := txStore.RenameCredentialSessionCAS(ctx, candidate.ID, current.Version, candidate.Name)
		return err
	}
	if current.Version != candidate.Version {
		return fmt.Errorf("credential session %q version mismatch: expected %d, current %d", candidate.ID, candidate.Version, current.Version)
	}
	if strings.TrimSpace(candidate.Name) == "" {
		candidate.Name = current.Name
	}
	materialChanged := current.SecretData != candidate.SecretData ||
		!reflect.DeepEqual(current.Subject(), candidate.Subject()) ||
		!reflect.DeepEqual(current.AuthState, candidate.AuthState)
	nameChanged := current.Name != candidate.Name
	if !materialChanged && !nameChanged {
		return nil
	}
	nextVersion := current.Version
	var err error
	if materialChanged {
		nextVersion, err = txStore.UpdateCredentialSessionCAS(ctx, candidate.ID, nextVersion, candidate.SecretData, candidate.Subject(), candidate.AuthState)
		if err != nil {
			return err
		}
	}
	if !nameChanged {
		return nil
	}
	_, err = txStore.RenameCredentialSessionCAS(ctx, candidate.ID, nextVersion, candidate.Name)
	return err
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
