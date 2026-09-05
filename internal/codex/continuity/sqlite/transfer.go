package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

// TransferBinding preserves the binary digests hidden by the public diagnostic
// encoders; redacted JSON is never a persistence representation.
type TransferBinding = bindingRow

func Export(ctx context.Context, db *gorm.DB) ([]TransferBinding, error) {
	rows := []TransferBinding{}
	err := db.WithContext(ctx).Order("kind, opaque_key_version, opaque_digest").Find(&rows).Error
	return rows, err
}
func Import(ctx context.Context, db *gorm.DB, rows []TransferBinding) error {
	tx := db.WithContext(ctx)
	for _, row := range rows {
		incoming, err := decodeBinding(row)
		if err != nil {
			return err
		}
		var current bindingRow
		err = tx.Where("kind = ? AND opaque_key_version = ? AND opaque_digest = ?", row.Kind, row.OpaqueKeyVersion, row.OpaqueDigest).Take(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		existing, err := decodeBinding(current)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(existing.Owner, incoming.Owner) {
			return fmt.Errorf("continuity ownership conflict for %s/%s", row.Kind, row.OpaqueKeyVersion)
		}
		// Refresh timestamps are mutable. An older backup must not rewind a live
		// record or resurrect a later tombstone.
		if row.UpdatedAtNS > current.UpdatedAtNS {
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
