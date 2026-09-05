package clientdisguise

import (
	"errors"
	"gorm.io/gorm"
)

func loadProfileTrack(db *gorm.DB, sourceID string, tuple Tuple) (ProfileTrack, error) {
	var result ProfileTrack
	err := db.Where("source_id = ? AND client_type = ? AND platform = ? AND arch = ?", sourceID, tuple.ClientType, tuple.Platform, tuple.Arch).First(&result).Error
	return result, err
}
func newProfileTrack(sample Sample) ProfileTrack {
	return ProfileTrack{SourceID: sample.SourceID, ClientType: sample.Tuple.ClientType, Platform: sample.Tuple.Platform, Arch: sample.Tuple.Arch, ClientVersion: sample.ClientVersion, CapturedAt: sample.CapturedAt}
}

// Auto mode derives the revision from the configured source's winning track;
// restoring configuration cannot override that track's monotonic history.
func resolveAutoRevision(db *gorm.DB, binding *ProfileBinding, current ProfileRevision) error {
	if binding.Mode != ModeAuto || binding.ReferenceSourceID == "" {
		return nil
	}
	track, err := loadProfileTrack(db, binding.ReferenceSourceID, binding.Tuple)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if CompareVersions(track.ClientVersion, current.ClientVersion) >= 0 {
		binding.RevisionID = track.RevisionID
	}
	return nil
}

func (r *Repository) reconcileAutoBindings(db *gorm.DB) error {
	var bindings []ProfileBinding
	if err := db.Where("mode = ? AND reference_source_id <> ?", ModeAuto, "").Find(&bindings).Error; err != nil {
		return err
	}
	for _, binding := range bindings {
		var current ProfileRevision
		if err := db.First(&current, "id = ?", binding.RevisionID).Error; err != nil {
			return err
		}
		if err := resolveAutoRevision(db, &binding, current); err != nil {
			return err
		}
		if binding.RevisionID == current.ID {
			continue
		}
		binding.UpdatedAt = r.now()
		if err := db.Save(&binding).Error; err != nil {
			return err
		}
	}
	return nil
}
