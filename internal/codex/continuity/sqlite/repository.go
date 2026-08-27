package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func Open(ctx context.Context, db *gorm.DB) (*Repository, error) {
	if err := ValidateSchema(ctx, db); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Claim(
	ctx context.Context,
	command codexcontinuity.StoreClaim,
) (codexcontinuity.StoreResult, error) {
	var result codexcontinuity.StoreResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := findCandidateRows(tx, command.Kind, command.DigestCandidates)
		if err != nil {
			return err
		}
		if len(rows) > 1 {
			return fmt.Errorf("claim continuity binding: multiple HMAC generations address one opaque value")
		}
		if len(rows) == 1 {
			result, err = resolveExisting(tx, rows[0], command.Now, command.Limits, command.ClientScopeCandidates, &command.Owner.ProtocolScope)
			if err != nil || result.Decision != codexcontinuity.StoreUnknown {
				return err
			}
		}
		if err := purgeElapsedRows(tx, command.Kind, command.Now, command.Limits); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&bindingRow{}).Where("kind = ?", command.Kind).Count(&count).Error; err != nil {
			return fmt.Errorf("count continuity bindings: %w", err)
		}
		if count >= command.Limits.MaxBindings {
			result.Decision = codexcontinuity.StoreCapacity
			return nil
		}
		binding := codexcontinuity.Binding{
			Kind:             command.Kind,
			Digest:           command.CurrentDigest,
			Owner:            command.Owner,
			Lifecycle:        codexcontinuity.LifecyclePending,
			ClaimOperationID: command.OperationID,
			CreatedAt:        command.Now,
			UpdatedAt:        command.Now,
			ExpiresAt:        command.Now.Add(command.Limits.PendingTTL),
		}
		row, err := encodeBinding(binding)
		if err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("insert continuity binding: %w", err)
		}
		result = codexcontinuity.StoreResult{Decision: codexcontinuity.StoreClaimed, Binding: binding}
		return nil
	})
	return result, err
}

func (r *Repository) Lookup(
	ctx context.Context,
	command codexcontinuity.StoreLookup,
) (codexcontinuity.StoreResult, error) {
	var result codexcontinuity.StoreResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := findCandidateRows(tx, command.Kind, command.DigestCandidates)
		if err != nil {
			return err
		}
		if len(rows) > 1 {
			return fmt.Errorf("lookup continuity binding: multiple HMAC generations address one opaque value")
		}
		if len(rows) == 0 {
			result.Decision = codexcontinuity.StoreUnknown
			return nil
		}
		result, err = resolveExisting(
			tx,
			rows[0],
			command.Now,
			command.Limits,
			command.ClientScopeCandidates,
			command.ProtocolScope,
		)
		return err
	})
	return result, err
}

func (r *Repository) Commit(
	ctx context.Context,
	command codexcontinuity.StoreCommit,
) (codexcontinuity.StoreResult, error) {
	var result codexcontinuity.StoreResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, found, err := findExactRow(tx, command.Binding)
		if err != nil {
			return err
		}
		if !found {
			result.Decision = codexcontinuity.StoreUnknown
			return nil
		}
		binding, decision, err := reconcileLifecycle(tx, row, command.Now, command.Limits)
		if err != nil {
			return err
		}
		result = codexcontinuity.StoreResult{Decision: decision, Binding: binding}
		if decision != codexcontinuity.StoreOwned {
			return nil
		}
		if !sameClaim(binding, command.Binding) {
			result.Decision = codexcontinuity.StoreConflict
			return nil
		}
		if binding.Lifecycle == codexcontinuity.LifecycleCommitted {
			result.Decision = codexcontinuity.StoreCommitted
			return nil
		}
		committedAt := command.Now
		binding.Lifecycle = codexcontinuity.LifecycleCommitted
		binding.UpdatedAt = command.Now
		binding.CommittedAt = &committedAt
		binding.ExpiresAt = command.Now.Add(command.Limits.CommittedTTL)
		if err := updateLifecycle(tx, binding); err != nil {
			return err
		}
		result = codexcontinuity.StoreResult{Decision: codexcontinuity.StoreCommitted, Binding: binding}
		return nil
	})
	return result, err
}

func (r *Repository) Abandon(
	ctx context.Context,
	command codexcontinuity.StoreAbandon,
) (codexcontinuity.StoreResult, error) {
	var result codexcontinuity.StoreResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, found, err := findExactRow(tx, command.Binding)
		if err != nil {
			return err
		}
		if !found {
			result.Decision = codexcontinuity.StoreUnknown
			return nil
		}
		binding, decision, err := reconcileLifecycle(tx, row, command.Now, command.Limits)
		if err != nil {
			return err
		}
		result = codexcontinuity.StoreResult{Decision: decision, Binding: binding}
		if decision != codexcontinuity.StoreOwned {
			return nil
		}
		if binding.Lifecycle != codexcontinuity.LifecyclePending || !sameClaim(binding, command.Binding) {
			result.Decision = codexcontinuity.StoreConflict
			return nil
		}
		if err := deleteBinding(tx, binding); err != nil {
			return err
		}
		result.Decision = codexcontinuity.StoreAbandoned
		return nil
	})
	return result, err
}

func (r *Repository) Cleanup(
	ctx context.Context,
	command codexcontinuity.StoreCleanup,
) (codexcontinuity.CleanupResult, error) {
	var result codexcontinuity.CleanupResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []bindingRow
		if err := tx.Order("kind, opaque_key_version").Find(&rows).Error; err != nil {
			return fmt.Errorf("list continuity bindings for cleanup: %w", err)
		}
		for _, row := range rows {
			kind := codexcontinuity.Kind(row.Kind)
			limits, exists := command.Policy[kind]
			if !exists {
				return fmt.Errorf("cleanup continuity binding: kind %q has no policy", row.Kind)
			}
			transition, err := reconcileForCleanup(tx, row, command.Now, limits)
			if err != nil {
				return err
			}
			result.Expired += transition.Expired
			result.Tombstoned += transition.Tombstoned
			result.Deleted += transition.Deleted
		}
		return nil
	})
	return result, err
}

func (r *Repository) RequiredHMACVersions(ctx context.Context) ([]string, error) {
	var versions []string
	err := r.db.WithContext(ctx).Raw(`
		SELECT version FROM (
			SELECT opaque_key_version AS version FROM codex_continuity_bindings
			UNION
			SELECT client_key_version AS version FROM codex_continuity_bindings
			UNION
			SELECT protocol_subject_key_version AS version FROM codex_continuity_bindings
			WHERE protocol_subject_key_version IS NOT NULL
		) ORDER BY version
	`).Scan(&versions).Error
	if err != nil {
		return nil, fmt.Errorf("list continuity HMAC versions: %w", err)
	}
	return versions, nil
}

func findCandidateRows(
	tx *gorm.DB,
	kind codexcontinuity.Kind,
	digests []codexidentity.OpaqueDigest,
) ([]bindingRow, error) {
	if len(digests) == 0 {
		return nil, fmt.Errorf("find continuity binding: digest candidates are required")
	}
	conditions := make([]string, 0, len(digests))
	arguments := make([]any, 0, 1+len(digests)*2)
	arguments = append(arguments, kind)
	for _, digest := range digests {
		sum := digest.Digest()
		conditions = append(conditions, "(opaque_key_version = ? AND opaque_digest = ?)")
		arguments = append(arguments, digest.KeyVersion(), sum[:])
	}
	var rows []bindingRow
	query := "kind = ? AND (" + strings.Join(conditions, " OR ") + ")"
	if err := tx.Where(query, arguments...).Limit(2).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("find continuity binding: %w", err)
	}
	return rows, nil
}

func findExactRow(tx *gorm.DB, binding codexcontinuity.Binding) (bindingRow, bool, error) {
	sum := binding.Digest.Digest()
	var row bindingRow
	result := tx.Where(
		"kind = ? AND opaque_key_version = ? AND opaque_digest = ?",
		binding.Kind,
		binding.Digest.KeyVersion(),
		sum[:],
	).Take(&row)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return bindingRow{}, false, nil
	}
	if result.Error != nil {
		return bindingRow{}, false, fmt.Errorf("find exact continuity binding: %w", result.Error)
	}
	return row, true, nil
}

func resolveExisting(
	tx *gorm.DB,
	row bindingRow,
	now time.Time,
	limits codexcontinuity.Limits,
	clientScopes []codexidentity.ClientScope,
	protocolScope *codexidentity.ProtocolScope,
) (codexcontinuity.StoreResult, error) {
	binding, decision, err := reconcileLifecycle(tx, row, now, limits)
	if err != nil {
		return codexcontinuity.StoreResult{}, err
	}
	result := codexcontinuity.StoreResult{Decision: decision, Binding: binding}
	if decision != codexcontinuity.StoreOwned {
		return result, nil
	}
	if !clientOwned(binding.Owner.ClientScope, clientScopes) {
		result.Decision = codexcontinuity.StoreConflict
		return result, nil
	}
	if protocolScope != nil && !binding.Owner.ProtocolScope.Equal(*protocolScope) {
		result.Decision = codexcontinuity.StoreConflict
	}
	return result, nil
}

func reconcileLifecycle(
	tx *gorm.DB,
	row bindingRow,
	now time.Time,
	limits codexcontinuity.Limits,
) (codexcontinuity.Binding, codexcontinuity.StoreDecision, error) {
	binding, err := decodeBinding(row)
	if err != nil {
		return codexcontinuity.Binding{}, "", err
	}
	if binding.Lifecycle == codexcontinuity.LifecycleTombstone {
		if binding.TombstoneUntil == nil {
			return codexcontinuity.Binding{}, "", fmt.Errorf("continuity tombstone has no expiry")
		}
		if !now.Before(*binding.TombstoneUntil) {
			if err := deleteBinding(tx, binding); err != nil {
				return codexcontinuity.Binding{}, "", err
			}
			return codexcontinuity.Binding{}, codexcontinuity.StoreUnknown, nil
		}
		return binding, codexcontinuity.StoreExpired, nil
	}
	if now.Before(binding.ExpiresAt) {
		return binding, codexcontinuity.StoreOwned, nil
	}
	tombstoneUntil := binding.ExpiresAt.Add(limits.TombstoneTTL)
	if !now.Before(tombstoneUntil) {
		if err := deleteBinding(tx, binding); err != nil {
			return codexcontinuity.Binding{}, "", err
		}
		return codexcontinuity.Binding{}, codexcontinuity.StoreUnknown, nil
	}
	binding.Lifecycle = codexcontinuity.LifecycleTombstone
	binding.UpdatedAt = now
	binding.TombstoneUntil = &tombstoneUntil
	if err := updateLifecycle(tx, binding); err != nil {
		return codexcontinuity.Binding{}, "", err
	}
	return binding, codexcontinuity.StoreExpired, nil
}

func reconcileForCleanup(
	tx *gorm.DB,
	row bindingRow,
	now time.Time,
	limits codexcontinuity.Limits,
) (codexcontinuity.CleanupResult, error) {
	binding, err := decodeBinding(row)
	if err != nil {
		return codexcontinuity.CleanupResult{}, err
	}
	if binding.Lifecycle == codexcontinuity.LifecycleTombstone {
		if binding.TombstoneUntil == nil {
			return codexcontinuity.CleanupResult{}, fmt.Errorf("continuity tombstone has no expiry")
		}
		if now.Before(*binding.TombstoneUntil) {
			return codexcontinuity.CleanupResult{}, nil
		}
		return deleteForCleanup(tx, binding)
	}
	if now.Before(binding.ExpiresAt) {
		return codexcontinuity.CleanupResult{}, nil
	}
	tombstoneUntil := binding.ExpiresAt.Add(limits.TombstoneTTL)
	if !now.Before(tombstoneUntil) {
		result, err := deleteForCleanup(tx, binding)
		result.Expired = 1
		return result, err
	}
	binding.Lifecycle = codexcontinuity.LifecycleTombstone
	binding.UpdatedAt = now
	binding.TombstoneUntil = &tombstoneUntil
	if err := updateLifecycle(tx, binding); err != nil {
		return codexcontinuity.CleanupResult{}, err
	}
	return codexcontinuity.CleanupResult{Expired: 1, Tombstoned: 1}, nil
}

func deleteForCleanup(tx *gorm.DB, binding codexcontinuity.Binding) (codexcontinuity.CleanupResult, error) {
	if err := deleteBinding(tx, binding); err != nil {
		return codexcontinuity.CleanupResult{}, err
	}
	return codexcontinuity.CleanupResult{Deleted: 1}, nil
}

func purgeElapsedRows(
	tx *gorm.DB,
	kind codexcontinuity.Kind,
	now time.Time,
	limits codexcontinuity.Limits,
) error {
	var rows []bindingRow
	if err := tx.Where("kind = ?", kind).Find(&rows).Error; err != nil {
		return fmt.Errorf("list continuity capacity rows: %w", err)
	}
	for _, row := range rows {
		if _, err := reconcileForCleanup(tx, row, now, limits); err != nil {
			return err
		}
	}
	return nil
}

func updateLifecycle(tx *gorm.DB, binding codexcontinuity.Binding) error {
	row, err := encodeBinding(binding)
	if err != nil {
		return err
	}
	result := tx.Model(&bindingRow{}).Where(
		"kind = ? AND opaque_key_version = ? AND opaque_digest = ? AND claim_operation_id = ?",
		row.Kind,
		row.OpaqueKeyVersion,
		row.OpaqueDigest,
		row.ClaimOperationID,
	).Updates(map[string]any{
		"lifecycle":          row.Lifecycle,
		"updated_at_ns":      row.UpdatedAtNS,
		"committed_at_ns":    row.CommittedAtNS,
		"expires_at_ns":      row.ExpiresAtNS,
		"tombstone_until_ns": row.TombstoneUntilNS,
	})
	if result.Error != nil {
		return fmt.Errorf("update continuity lifecycle: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update continuity lifecycle: owner changed concurrently")
	}
	return nil
}

func deleteBinding(tx *gorm.DB, binding codexcontinuity.Binding) error {
	sum := binding.Digest.Digest()
	result := tx.Where(
		"kind = ? AND opaque_key_version = ? AND opaque_digest = ? AND claim_operation_id = ?",
		binding.Kind,
		binding.Digest.KeyVersion(),
		sum[:],
		binding.ClaimOperationID,
	).Delete(&bindingRow{})
	if result.Error != nil {
		return fmt.Errorf("delete continuity binding: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("delete continuity binding: owner changed concurrently")
	}
	return nil
}

func clientOwned(owner codexidentity.ClientScope, candidates []codexidentity.ClientScope) bool {
	for _, candidate := range candidates {
		if owner.Equal(candidate) {
			return true
		}
	}
	return false
}

func sameClaim(current, expected codexcontinuity.Binding) bool {
	return current.Kind == expected.Kind &&
		current.Digest.Equal(expected.Digest) &&
		current.Owner.Equal(expected.Owner) &&
		current.ClaimOperationID == expected.ClaimOperationID
}

var _ codexcontinuity.Store = (*Repository)(nil)
