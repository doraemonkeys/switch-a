package clientdisguise

import (
	"context"
	"errors"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"gorm.io/gorm"
)

const officialVersionStateID = 1

func (r *Repository) officialVersionForBinding(ctx context.Context, binding ProfileBinding) (officialversion.Release, error) {
	if binding.VersionSource != officialversion.Source {
		return officialversion.Release{}, nil
	}
	state, err := r.OfficialVersion(ctx)
	return state.Release, err
}

func (s TargetSnapshot) ClientVersion() string {
	if s.OfficialVersion.Version != "" {
		return s.OfficialVersion.Version
	}
	return s.Profile.ClientVersion
}

func validateVersionSource(source string) error {
	if source != "" && source != officialversion.Source {
		return invalid("version source must be profile (empty) or official_stable")
	}
	return nil
}
func (r *Repository) OfficialVersion(ctx context.Context) (officialversion.State, error) {
	var state officialversion.State
	err := r.db.WithContext(ctx).First(&state, officialVersionStateID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return officialversion.State{}, nil
	}
	return state, err
}
func (r *Repository) SaveOfficialVersion(ctx context.Context, state officialversion.State) error {
	if state.Release.Version != "" && !officialversion.ValidVersion(state.Release.Version) {
		return invalid("invalid official stable version")
	}
	state.ID = officialVersionStateID
	return r.db.WithContext(ctx).Save(&state).Error
}
func (r *Repository) HasOfficialVersionFollowers(ctx context.Context) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&ProfileBinding{}).Where("version_source = ?", officialversion.Source).Count(&count).Error
	return count > 0, err
}
