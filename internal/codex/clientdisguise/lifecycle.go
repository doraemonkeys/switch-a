package clientdisguise

import (
	"context"
	"errors"
	"gorm.io/gorm"
)

// RetireLogin retains historical mappings while removing the live credential
// reference, so deleting a login cannot leave a backup that cannot be restored.
func (r *Repository) RetireLogin(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		login, err := r.WithDB(tx).GetLogin(ctx, sessionID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		history := LoginHistory{GenerationID: login.GenerationID, Identity: login}
		if err := mergeImmutable(tx, &history, "generation_id", history.GenerationID); err != nil {
			return err
		}
		if err := tx.Delete(&ProfileBinding{}, "credential_session_id = ?", sessionID).Error; err != nil {
			return err
		}
		return tx.Delete(&LoginIdentity{}, "credential_session_id = ?", sessionID).Error
	})
}
