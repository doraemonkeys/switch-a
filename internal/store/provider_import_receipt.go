package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxProviderImportReceiptResponsePayloadBytes bounds the BLOB stored for an
// idempotent commit response. The store owns this invariant so a regression in
// any HTTP handler or parser cannot create unbounded durable receipts.
const MaxProviderImportReceiptResponsePayloadBytes = 1 << 20

// ProviderImportReceipt is the durable, non-secret commit proof used to replay
// an exact response when a client or process loses the first successful reply.
type ProviderImportReceipt struct {
	ImportID        string    `gorm:"column:import_id;primaryKey"`
	Fingerprint     string    `gorm:"column:fingerprint;not null"`
	ResponsePayload []byte    `gorm:"column:response_payload;not null"`
	ExpiresAt       time.Time `gorm:"column:expires_at;not null;index"`
}

func (ProviderImportReceipt) TableName() string {
	return "provider_import_receipts"
}

// ProviderImportReceiptReplayError carries exact bytes from a previously
// committed request with the same fingerprint.
type ProviderImportReceiptReplayError struct {
	Receipt ProviderImportReceipt
}

func (e *ProviderImportReceiptReplayError) Error() string {
	if e == nil {
		return ErrProviderImportReceiptReplay.Error()
	}
	return fmt.Sprintf("%s for import %q", ErrProviderImportReceiptReplay, e.Receipt.ImportID)
}

func (e *ProviderImportReceiptReplayError) Is(target error) bool {
	return target == ErrProviderImportReceiptReplay
}

// ProviderImportReceiptConflictError rejects reuse of an import ID by a
// different normalized request without exposing the stored response payload.
type ProviderImportReceiptConflictError struct {
	ImportID string
}

func (e *ProviderImportReceiptConflictError) Error() string {
	if e == nil {
		return ErrProviderImportReceiptConflict.Error()
	}
	return fmt.Sprintf("%s for import %q", ErrProviderImportReceiptConflict, e.ImportID)
}

func (e *ProviderImportReceiptConflictError) Is(target error) bool {
	return target == ErrProviderImportReceiptConflict
}

// ProviderImportReceiptResponsePayloadTooLargeError describes the rejected
// payload size without retaining or exposing the response bytes themselves.
type ProviderImportReceiptResponsePayloadTooLargeError struct {
	SizeBytes  int
	LimitBytes int
}

func (e *ProviderImportReceiptResponsePayloadTooLargeError) Error() string {
	if e == nil {
		return ErrProviderImportReceiptResponsePayloadTooLarge.Error()
	}
	return fmt.Sprintf(
		"%s: %d bytes exceeds the %d-byte limit",
		ErrProviderImportReceiptResponsePayloadTooLarge,
		e.SizeBytes,
		e.LimitBytes,
	)
}

func (e *ProviderImportReceiptResponsePayloadTooLargeError) Is(target error) bool {
	return target == ErrProviderImportReceiptResponsePayloadTooLarge
}

// GetProviderImportReceipt returns only an unexpired receipt. Expired rows are
// deleted on observation so an abandoned import ID cannot accumulate forever.
func (s *SQLiteStore) GetProviderImportReceipt(
	ctx context.Context,
	importID string,
) (*ProviderImportReceipt, error) {
	importID = strings.TrimSpace(importID)
	if importID == "" {
		return nil, fmt.Errorf("get provider import receipt: import ID must not be blank")
	}

	var receipt ProviderImportReceipt
	err := s.db.WithContext(ctx).First(&receipt, "import_id = ?", importID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProviderImportReceiptNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get provider import receipt %q: %w", importID, err)
	}
	if !receipt.ExpiresAt.After(s.clock.Now()) {
		if err := s.db.WithContext(ctx).
			Where("import_id = ? AND expires_at <= ?", importID, s.clock.Now()).
			Delete(&ProviderImportReceipt{}).Error; err != nil {
			return nil, fmt.Errorf("delete expired provider import receipt %q: %w", importID, err)
		}
		return nil, ErrProviderImportReceiptNotFound
	}
	return cloneProviderImportReceipt(&receipt), nil
}

func normalizeProviderImportReceipt(
	receipt *ProviderImportReceipt,
	now time.Time,
) (*ProviderImportReceipt, error) {
	if receipt == nil {
		return nil, nil
	}
	importID := strings.TrimSpace(receipt.ImportID)
	fingerprint := strings.TrimSpace(receipt.Fingerprint)
	if importID == "" {
		return nil, fmt.Errorf("provider import receipt import ID must not be blank")
	}
	if fingerprint == "" {
		return nil, fmt.Errorf("provider import receipt fingerprint must not be blank")
	}
	if err := validateProviderImportReceiptResponsePayload(receipt.ResponsePayload); err != nil {
		return nil, err
	}
	if !receipt.ExpiresAt.After(now) {
		return nil, fmt.Errorf("provider import receipt must expire in the future")
	}
	normalized := cloneProviderImportReceipt(receipt)
	normalized.ImportID = importID
	normalized.Fingerprint = fingerprint
	return normalized, nil
}

func reserveProviderImportReceipt(
	tx *gorm.DB,
	receipt *ProviderImportReceipt,
	now time.Time,
) error {
	if receipt == nil {
		return nil
	}
	if err := validateProviderImportReceiptResponsePayload(receipt.ResponsePayload); err != nil {
		return err
	}
	// Deleting first obtains the SQLite write lock before the conflict check. A
	// second process therefore observes the committed receipt instead of applying
	// mutations against a stale read snapshot.
	if err := tx.Where("expires_at <= ?", now).
		Delete(&ProviderImportReceipt{}).Error; err != nil {
		return fmt.Errorf("prune expired provider import receipts: %w", err)
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "import_id"}},
		DoNothing: true,
	}).Create(receipt)
	if result.Error != nil {
		return fmt.Errorf("reserve provider import receipt %q: %w", receipt.ImportID, result.Error)
	}
	if result.RowsAffected == 1 {
		return nil
	}

	var existing ProviderImportReceipt
	if err := tx.First(&existing, "import_id = ?", receipt.ImportID).Error; err != nil {
		return fmt.Errorf("load existing provider import receipt %q: %w", receipt.ImportID, err)
	}
	if existing.Fingerprint == receipt.Fingerprint {
		return &ProviderImportReceiptReplayError{Receipt: *cloneProviderImportReceipt(&existing)}
	}
	return &ProviderImportReceiptConflictError{ImportID: receipt.ImportID}
}

func validateProviderImportReceiptResponsePayload(payload []byte) error {
	size := len(payload)
	if size == 0 {
		return fmt.Errorf("provider import receipt response payload must not be empty")
	}
	if size > MaxProviderImportReceiptResponsePayloadBytes {
		return &ProviderImportReceiptResponsePayloadTooLargeError{
			SizeBytes:  size,
			LimitBytes: MaxProviderImportReceiptResponsePayloadBytes,
		}
	}
	return nil
}

func cloneProviderImportReceipt(receipt *ProviderImportReceipt) *ProviderImportReceipt {
	if receipt == nil {
		return nil
	}
	clone := *receipt
	clone.ResponsePayload = append([]byte(nil), receipt.ResponsePayload...)
	return &clone
}
