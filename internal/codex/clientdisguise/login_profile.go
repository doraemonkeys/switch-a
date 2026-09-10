package clientdisguise

import (
	"context"
	"errors"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"gorm.io/gorm"
)

// LoginProfile is independent of route policy: shared credential maintenance has
// no downstream platform to match and must not create a first-request binding.
type LoginProfile struct {
	Login           LoginIdentity
	Binding         ProfileBinding
	Profile         ProfileRevision
	OfficialVersion officialversion.Release
}

func (p LoginProfile) UserAgent() string {
	return p.Profile.UserAgent(p.OfficialVersion.Version)
}

// ResolveLoginProfile freezes an existing binding and its version selection in
// one read transaction. An unbound login has no observed client to impersonate.
func (r *Repository) ResolveLoginProfile(ctx context.Context, sessionID string) (LoginProfile, error) {
	var result LoginProfile
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&result.Binding, "credential_session_id = ?", sessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.First(&result.Login, "credential_session_id = ?", sessionID).Error; err != nil {
			return recordError(err)
		}
		if err := tx.First(&result.Profile, "id = ?", result.Binding.RevisionID).Error; err != nil {
			return recordError(err)
		}
		var err error
		result.OfficialVersion, err = r.WithDB(tx).officialVersionForBinding(ctx, result.Binding)
		return err
	})
	return result, err
}
