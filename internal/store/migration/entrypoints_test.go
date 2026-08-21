package migration

import "gorm.io/gorm"

func migrateRequestLogLifecycleFields(db *gorm.DB) error {
	return MigrateRequestLogLifecycleFields(db)
}
