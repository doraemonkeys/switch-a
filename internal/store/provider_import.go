package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

// ProviderImportBundle is the durable command produced from a reviewed import
// preview. Creates and credential refreshes are separate because refreshing an
// existing account must never rewrite provider routing configuration.
type ProviderImportBundle struct {
	Creates           []ProviderImportCreate
	CredentialUpdates []ProviderImportCredentialUpdate
	Receipt           *ProviderImportReceipt
}

// ProviderImportCreate creates one new provider from a staged credential.
type ProviderImportCreate struct {
	CandidateID string
	Provider    model.Provider
}

// ProviderImportCredentialUpdate refreshes only the credential and auth snapshot
// of the provider that already owns the imported account.
type ProviderImportCredentialUpdate struct {
	CandidateID                 string
	ProviderID                  string
	ExpectedCredentialVersion   int64
	ExpectedCredentialCreatedAt time.Time
	Credential                  model.ProviderCredential
	AuthState                   model.ProviderAuthState
}

type normalizedProviderImport struct {
	items   []normalizedProviderImportItem
	creates []ProviderImportCreate
	updates []ProviderImportCredentialUpdate
}

type normalizedProviderImportItem struct {
	candidateID       string
	providerID        string
	accountID         string
	groupID           string
	create            bool
	expectedVer       int64
	expectedCreatedAt time.Time
}

// ApplyProviderImport applies every reviewed mutation in one transaction. The
// preflight runs against the same snapshot as the writes, closing the preview/apply
// race without accepting config-import upsert semantics.
func (s *SQLiteStore) ApplyProviderImport(ctx context.Context, bundle *ProviderImportBundle) error {
	if bundle == nil ||
		(len(bundle.Creates) == 0 && len(bundle.CredentialUpdates) == 0 && bundle.Receipt == nil) {
		return nil
	}

	plan, err := normalizeProviderImport(bundle)
	if err != nil {
		return err
	}
	receipt, err := normalizeProviderImportReceipt(bundle.Receipt, s.clock.Now())
	if err != nil {
		return fmt.Errorf("normalize provider import receipt: %w", err)
	}
	providerIDs := uniqueProviderImportValues(plan.items, func(item normalizedProviderImportItem) string {
		return item.providerID
	})
	ownedCtx, release, err := s.WithProviderCredentialMutations(ctx, providerIDs)
	if err != nil {
		return fmt.Errorf("apply provider import: %w", err)
	}
	defer release()

	err = s.db.WithContext(ownedCtx).Transaction(func(tx *gorm.DB) error {
		if err := reserveProviderImportReceipt(tx, receipt, s.clock.Now()); err != nil {
			return err
		}
		conflicts, err := preflightProviderImport(tx, plan.items)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return &ProviderImportConflictError{Conflicts: conflicts}
		}

		writeOptions := resolveProviderWriteOptions(nil)
		for i := range plan.creates {
			if err := s.createProviderInTransaction(tx, &plan.creates[i].Provider, writeOptions, nil); err != nil {
				return mapProviderImportWriteError(plan.creates[i].CandidateID, plan.creates[i].Provider.ID, err)
			}
		}
		for i := range plan.updates {
			update := &plan.updates[i]
			if _, err := s.updateProviderCredentialStateInTransaction(
				tx,
				update.ProviderID,
				&update.Credential,
				&update.AuthState,
				update.ExpectedCredentialVersion,
			); err != nil {
				return mapProviderImportWriteError(update.CandidateID, update.ProviderID, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("apply provider import: %w", err)
	}
	return nil
}

func normalizeProviderImport(bundle *ProviderImportBundle) (*normalizedProviderImport, error) {
	plan := &normalizedProviderImport{
		items:   make([]normalizedProviderImportItem, 0, len(bundle.Creates)+len(bundle.CredentialUpdates)),
		creates: make([]ProviderImportCreate, 0, len(bundle.Creates)),
		updates: make([]ProviderImportCredentialUpdate, 0, len(bundle.CredentialUpdates)),
	}

	for i := range bundle.Creates {
		entry, item, err := normalizeProviderImportCreate(bundle.Creates[i])
		if err != nil {
			return nil, fmt.Errorf("normalize provider import create %d: %w", i, err)
		}
		plan.creates = append(plan.creates, entry)
		plan.items = append(plan.items, item)
	}
	for i := range bundle.CredentialUpdates {
		entry, item, err := normalizeProviderImportCredentialUpdate(bundle.CredentialUpdates[i])
		if err != nil {
			return nil, fmt.Errorf("normalize provider import credential update %d: %w", i, err)
		}
		plan.updates = append(plan.updates, entry)
		plan.items = append(plan.items, item)
	}
	return plan, nil
}

func normalizeProviderImportCreate(entry ProviderImportCreate) (ProviderImportCreate, normalizedProviderImportItem, error) {
	candidateID := strings.TrimSpace(entry.CandidateID)
	if candidateID == "" {
		return ProviderImportCreate{}, normalizedProviderImportItem{}, fmt.Errorf("candidate_id is required")
	}
	provider := cloneProviderForImport(&entry.Provider)
	provider.ID = strings.TrimSpace(provider.ID)
	if provider.ID == "" {
		return ProviderImportCreate{}, normalizedProviderImportItem{}, fmt.Errorf("provider_id is required for candidate %q", candidateID)
	}
	provider.CredentialType = model.NormalizeProviderCredentialType(provider.CredentialType)
	if provider.CredentialType != model.ProviderCredentialTypeChatGPT {
		return ProviderImportCreate{}, normalizedProviderImportItem{}, fmt.Errorf(
			"candidate %q provider %q must use chatgpt credentials",
			candidateID,
			provider.ID,
		)
	}
	if provider.Credential == nil {
		return ProviderImportCreate{}, normalizedProviderImportItem{}, fmt.Errorf(
			"candidate %q provider %q is missing a credential",
			candidateID,
			provider.ID,
		)
	}

	credential, accountID, err := normalizeProviderImportCredential(provider.ID, provider.Credential)
	if err != nil {
		return ProviderImportCreate{}, normalizedProviderImportItem{}, fmt.Errorf("candidate %q: %w", candidateID, err)
	}
	provider.Credential = credential
	provider.AuthState, err = normalizeProviderImportAuthState(
		provider.ID,
		provider.CredentialType,
		provider.AuthState,
		credential,
		accountID,
	)
	if err != nil {
		return ProviderImportCreate{}, normalizedProviderImportItem{}, fmt.Errorf("candidate %q: %w", candidateID, err)
	}

	groupID := ""
	if provider.GroupID != nil {
		groupID = strings.TrimSpace(*provider.GroupID)
		if groupID == "" {
			provider.GroupID = nil
		} else {
			provider.GroupID = stringPointer(groupID)
		}
	}

	entry.CandidateID = candidateID
	entry.Provider = *provider
	return entry, normalizedProviderImportItem{
		candidateID: candidateID,
		providerID:  provider.ID,
		accountID:   accountID,
		groupID:     groupID,
		create:      true,
	}, nil
}

func normalizeProviderImportCredentialUpdate(
	entry ProviderImportCredentialUpdate,
) (ProviderImportCredentialUpdate, normalizedProviderImportItem, error) {
	candidateID := strings.TrimSpace(entry.CandidateID)
	providerID := strings.TrimSpace(entry.ProviderID)
	switch {
	case candidateID == "":
		return ProviderImportCredentialUpdate{}, normalizedProviderImportItem{}, fmt.Errorf("candidate_id is required")
	case providerID == "":
		return ProviderImportCredentialUpdate{}, normalizedProviderImportItem{}, fmt.Errorf("provider_id is required for candidate %q", candidateID)
	case entry.ExpectedCredentialVersion < 0:
		return ProviderImportCredentialUpdate{}, normalizedProviderImportItem{}, fmt.Errorf(
			"candidate %q expected credential version must be non-negative",
			candidateID,
		)
	case entry.ExpectedCredentialCreatedAt.IsZero():
		return ProviderImportCredentialUpdate{}, normalizedProviderImportItem{}, fmt.Errorf(
			"candidate %q expected credential creation time is required",
			candidateID,
		)
	}

	credential, accountID, err := normalizeProviderImportCredential(providerID, &entry.Credential)
	if err != nil {
		return ProviderImportCredentialUpdate{}, normalizedProviderImportItem{}, fmt.Errorf("candidate %q: %w", candidateID, err)
	}
	authState, err := normalizeProviderImportAuthState(
		providerID,
		model.ProviderCredentialTypeChatGPT,
		&entry.AuthState,
		credential,
		accountID,
	)
	if err != nil {
		return ProviderImportCredentialUpdate{}, normalizedProviderImportItem{}, fmt.Errorf("candidate %q: %w", candidateID, err)
	}

	entry.CandidateID = candidateID
	entry.ProviderID = providerID
	entry.Credential = *credential
	entry.AuthState = *authState
	return entry, normalizedProviderImportItem{
		candidateID:       candidateID,
		providerID:        providerID,
		accountID:         accountID,
		expectedVer:       entry.ExpectedCredentialVersion,
		expectedCreatedAt: entry.ExpectedCredentialCreatedAt,
	}, nil
}

func normalizeProviderImportCredential(
	providerID string,
	credential *model.ProviderCredential,
) (*model.ProviderCredential, string, error) {
	if credential == nil || strings.TrimSpace(credential.SecretData) == "" {
		return nil, "", fmt.Errorf("provider %q is missing credential secret data", providerID)
	}
	secret, err := model.DecodeChatGPTProviderSecret(credential.SecretData)
	if err != nil {
		return nil, "", fmt.Errorf("decode provider %q credential: %w", providerID, err)
	}
	if secret == nil || !secret.Ready() {
		return nil, "", fmt.Errorf("provider %q credential is incomplete", providerID)
	}

	normalized := model.NormalizeProviderCredentialRecord(providerID, credential)
	accountID := normalizeOptionalString(normalized.BindingAccountID)
	if accountID == "" {
		return nil, "", fmt.Errorf("provider %q credential binding account is required", providerID)
	}
	return normalized, accountID, nil
}

func normalizeProviderImportAuthState(
	providerID string,
	credentialType model.ProviderCredentialType,
	authState *model.ProviderAuthState,
	credential *model.ProviderCredential,
	accountID string,
) (*model.ProviderAuthState, error) {
	if authState == nil {
		return model.ProviderAuthStateFromCredential(providerID, credentialType, credential), nil
	}
	normalized := model.NormalizeProviderAuthStateRecord(providerID, credentialType, authState)
	if normalized.AccountID != "" && normalized.AccountID != accountID {
		return nil, fmt.Errorf(
			"provider %q auth account %q does not match credential account %q",
			providerID,
			normalized.AccountID,
			accountID,
		)
	}
	normalized.AccountID = accountID
	return normalized, nil
}

func cloneProviderForImport(provider *model.Provider) *model.Provider {
	clone := *provider
	clone.APITypes = append([]model.ProviderAPIType(nil), provider.APITypes...)
	clone.Credential = provider.Credential.Clone()
	clone.AuthState = provider.AuthState.Clone()
	if provider.GroupID != nil {
		clone.GroupID = stringPointer(*provider.GroupID)
	}
	return &clone
}

func preflightProviderImport(
	tx *gorm.DB,
	items []normalizedProviderImportItem,
) ([]ProviderImportConflict, error) {
	conflicts := duplicateProviderImportConflicts(items)
	snapshot, err := loadProviderImportPreflightSnapshot(tx, items)
	if err != nil {
		return nil, err
	}
	for i := range items {
		conflicts = append(conflicts, snapshot.conflictsFor(items[i])...)
	}
	return conflicts, nil
}

type providerImportPreflightSnapshot struct {
	providers map[string]model.Provider
	bindings  map[string]string
	groups    map[string]struct{}
}

func loadProviderImportPreflightSnapshot(
	tx *gorm.DB,
	items []normalizedProviderImportItem,
) (*providerImportPreflightSnapshot, error) {
	providerIDs := uniqueProviderImportValues(items, func(item normalizedProviderImportItem) string {
		return item.providerID
	})
	providers, err := loadProviderImportProviders(tx, providerIDs)
	if err != nil {
		return nil, err
	}
	accountIDs := uniqueProviderImportValues(items, func(item normalizedProviderImportItem) string {
		return item.accountID
	})
	bindings, err := loadProviderImportBindings(tx, accountIDs)
	if err != nil {
		return nil, err
	}
	groupIDs := uniqueProviderImportValues(items, func(item normalizedProviderImportItem) string {
		return item.groupID
	})
	groups, err := loadProviderImportGroups(tx, groupIDs)
	if err != nil {
		return nil, err
	}
	return &providerImportPreflightSnapshot{
		providers: providers,
		bindings:  bindings,
		groups:    groups,
	}, nil
}

func (s *providerImportPreflightSnapshot) conflictsFor(
	item normalizedProviderImportItem,
) []ProviderImportConflict {
	if item.create {
		return s.createConflicts(item)
	}
	provider, exists := s.providers[item.providerID]
	if !exists {
		return []ProviderImportConflict{{
			CandidateID: item.candidateID,
			Kind:        ProviderImportConflictProviderNotFound,
			ProviderID:  item.providerID,
			AccountID:   item.accountID,
		}}
	}
	return s.updateConflicts(item, provider)
}

func (s *providerImportPreflightSnapshot) createConflicts(
	item normalizedProviderImportItem,
) []ProviderImportConflict {
	conflicts := make([]ProviderImportConflict, 0, 3)
	if provider, exists := s.providers[item.providerID]; exists {
		conflicts = append(conflicts, ProviderImportConflict{
			CandidateID:           item.candidateID,
			Kind:                  ProviderImportConflictProviderAlreadyExists,
			ProviderID:            item.providerID,
			ConflictingProviderID: provider.ID,
		})
	}
	if _, exists := s.groups[item.groupID]; item.groupID != "" && !exists {
		conflicts = append(conflicts, ProviderImportConflict{
			CandidateID: item.candidateID,
			Kind:        ProviderImportConflictGroupNotFound,
			ProviderID:  item.providerID,
			GroupID:     item.groupID,
		})
	}
	if bindingProviderID, exists := s.bindings[item.accountID]; exists {
		conflicts = append(conflicts, ProviderImportConflict{
			CandidateID:           item.candidateID,
			Kind:                  ProviderImportConflictAccountAlreadyBound,
			ProviderID:            item.providerID,
			ConflictingProviderID: bindingProviderID,
			AccountID:             item.accountID,
		})
	}
	return conflicts
}

func (s *providerImportPreflightSnapshot) updateConflicts(
	item normalizedProviderImportItem,
	provider model.Provider,
) []ProviderImportConflict {
	conflicts := make([]ProviderImportConflict, 0, 3)
	currentVersion, currentCreatedAt, currentAccountID := providerImportCredentialState(provider)
	if item.expectedVer != currentVersion || !item.expectedCreatedAt.Equal(currentCreatedAt) {
		conflicts = append(conflicts, ProviderImportConflict{
			CandidateID:               item.candidateID,
			Kind:                      ProviderImportConflictCredentialVersionMismatch,
			ProviderID:                item.providerID,
			AccountID:                 item.accountID,
			ExpectedCredentialVersion: item.expectedVer,
			CurrentCredentialVersion:  currentVersion,
		})
	}
	if model.NormalizeProviderCredentialType(provider.CredentialType) != model.ProviderCredentialTypeChatGPT ||
		currentAccountID != item.accountID {
		conflicts = append(conflicts, ProviderImportConflict{
			CandidateID:           item.candidateID,
			Kind:                  ProviderImportConflictAccountBindingMismatch,
			ProviderID:            item.providerID,
			ConflictingProviderID: item.providerID,
			AccountID:             item.accountID,
		})
	}
	if bindingProviderID, exists := s.bindings[item.accountID]; exists && bindingProviderID != item.providerID {
		conflicts = append(conflicts, ProviderImportConflict{
			CandidateID:           item.candidateID,
			Kind:                  ProviderImportConflictAccountAlreadyBound,
			ProviderID:            item.providerID,
			ConflictingProviderID: bindingProviderID,
			AccountID:             item.accountID,
		})
	}
	return conflicts
}

func providerImportCredentialState(provider model.Provider) (int64, time.Time, string) {
	if provider.Credential == nil {
		return 0, time.Time{}, ""
	}
	return provider.Credential.Version,
		provider.Credential.CreatedAt,
		normalizeOptionalString(provider.Credential.BindingAccountID)
}

func duplicateProviderImportConflicts(items []normalizedProviderImportItem) []ProviderImportConflict {
	providerIndexes := make(map[string][]int, len(items))
	accountIndexes := make(map[string][]int, len(items))
	for i := range items {
		providerIndexes[items[i].providerID] = append(providerIndexes[items[i].providerID], i)
		accountIndexes[items[i].accountID] = append(accountIndexes[items[i].accountID], i)
	}

	conflicts := make([]ProviderImportConflict, 0)
	for i := range items {
		item := items[i]
		if indexes := providerIndexes[item.providerID]; len(indexes) > 1 {
			conflicts = append(conflicts, ProviderImportConflict{
				CandidateID:            item.candidateID,
				ConflictingCandidateID: conflictingCandidateID(items, indexes, i),
				Kind:                   ProviderImportConflictDuplicateProviderID,
				ProviderID:             item.providerID,
				AccountID:              item.accountID,
			})
		}
		if indexes := accountIndexes[item.accountID]; len(indexes) > 1 {
			conflicts = append(conflicts, ProviderImportConflict{
				CandidateID:            item.candidateID,
				ConflictingCandidateID: conflictingCandidateID(items, indexes, i),
				Kind:                   ProviderImportConflictDuplicateAccountBinding,
				ProviderID:             item.providerID,
				AccountID:              item.accountID,
			})
		}
	}
	return conflicts
}

func conflictingCandidateID(items []normalizedProviderImportItem, indexes []int, current int) string {
	for _, index := range indexes {
		if index != current {
			return items[index].candidateID
		}
	}
	return ""
}

func uniqueProviderImportValues(
	items []normalizedProviderImportItem,
	value func(normalizedProviderImportItem) string,
) []string {
	seen := make(map[string]struct{}, len(items))
	values := make([]string, 0, len(items))
	for i := range items {
		candidate := value(items[i])
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		values = append(values, candidate)
	}
	return values
}

func loadProviderImportProviders(tx *gorm.DB, providerIDs []string) (map[string]model.Provider, error) {
	providers := make([]model.Provider, 0, len(providerIDs))
	if len(providerIDs) > 0 {
		if err := providerQueryWithState(tx).Where("id IN ?", providerIDs).Find(&providers).Error; err != nil {
			return nil, fmt.Errorf("load providers for import preflight: %w", err)
		}
	}
	hydrateProviderStates(providers)
	byID := make(map[string]model.Provider, len(providers))
	for i := range providers {
		byID[providers[i].ID] = providers[i]
	}
	return byID, nil
}

func loadProviderImportBindings(tx *gorm.DB, accountIDs []string) (map[string]string, error) {
	credentials := make([]model.ProviderCredential, 0, len(accountIDs))
	if len(accountIDs) > 0 {
		if err := tx.Where("binding_account_id IN ?", accountIDs).Find(&credentials).Error; err != nil {
			return nil, fmt.Errorf("load credential bindings for import preflight: %w", err)
		}
	}
	bindings := make(map[string]string, len(credentials))
	for i := range credentials {
		accountID := normalizeOptionalString(credentials[i].BindingAccountID)
		if accountID != "" {
			bindings[accountID] = credentials[i].ProviderID
		}
	}
	return bindings, nil
}

func loadProviderImportGroups(tx *gorm.DB, groupIDs []string) (map[string]struct{}, error) {
	groups := make([]model.Group, 0, len(groupIDs))
	if len(groupIDs) > 0 {
		if err := tx.Select("id").Where("id IN ?", groupIDs).Find(&groups).Error; err != nil {
			return nil, fmt.Errorf("load groups for import preflight: %w", err)
		}
	}
	byID := make(map[string]struct{}, len(groups))
	for i := range groups {
		byID[groups[i].ID] = struct{}{}
	}
	return byID, nil
}

func mapProviderImportWriteError(candidateID, providerID string, err error) error {
	var bindingConflict *CredentialBindingConflictError
	if errors.As(err, &bindingConflict) {
		return &ProviderImportConflictError{Conflicts: []ProviderImportConflict{{
			CandidateID:           candidateID,
			Kind:                  ProviderImportConflictAccountAlreadyBound,
			ProviderID:            providerID,
			ConflictingProviderID: bindingConflict.ProviderID,
			AccountID:             bindingConflict.AccountID,
		}}}
	}
	var versionConflict *CredentialVersionConflictError
	if errors.As(err, &versionConflict) {
		return &ProviderImportConflictError{Conflicts: []ProviderImportConflict{{
			CandidateID:               candidateID,
			Kind:                      ProviderImportConflictCredentialVersionMismatch,
			ProviderID:                providerID,
			ExpectedCredentialVersion: versionConflict.ExpectedVersion,
			CurrentCredentialVersion:  versionConflict.CurrentVersion,
		}}}
	}
	return err
}

func stringPointer(value string) *string {
	return &value
}
