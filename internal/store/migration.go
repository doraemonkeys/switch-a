package store

import "gorm.io/gorm"

// migrateBaseURLToAPIType moves base_url from the providers table to provider_api_types.
// Idempotent: skips if providers.base_url column no longer exists.
func migrateBaseURLToAPIType(db *gorm.DB) error {
	// Check whether the old column still exists.
	var count int64
	err := db.Raw(`SELECT COUNT(*) FROM pragma_table_info('providers') WHERE name = 'base_url'`).Scan(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return nil // Already migrated.
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// Copy each provider's base_url into its associated api_type rows.
		// Only propagate non-empty base_url values to prevent creating rows
		// with empty base_url that would cause proxy routing failures at runtime.
		if err := tx.Exec(`
			UPDATE provider_api_types
			SET base_url = (
				SELECT providers.base_url
				FROM providers
				WHERE providers.id = provider_api_types.provider_id
				  AND providers.base_url != ''
			)
			WHERE (base_url = '' OR base_url IS NULL)
			  AND EXISTS (
				SELECT 1 FROM providers
				WHERE providers.id = provider_api_types.provider_id
				  AND providers.base_url != ''
			)
		`).Error; err != nil {
			return err
		}

		// Drop the old column from providers.
		if err := tx.Exec(`ALTER TABLE providers DROP COLUMN base_url`).Error; err != nil {
			return err
		}

		return nil
	})
}
