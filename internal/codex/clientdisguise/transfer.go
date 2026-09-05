package clientdisguise

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Identity and historical feature records are immutable across environments.
// Merging a backup must never silently change an already-issued mapping.
func mergeImmutable[T any](db *gorm.DB, record *T, key string, value any) error {
	var existing T
	err := db.Where(key+" = ?", value).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(record).Error; err != nil {
			return err
		}
		if err := db.Where(key+" = ?", value).First(&existing).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if !reflect.DeepEqual(existing, *record) {
		return fmt.Errorf("%w: %s %v", ErrConflict, key, value)
	}
	return nil
}
func (r *Repository) Export(ctx context.Context) (Snapshot, error) {
	var result Snapshot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, target := range []any{&result.Logins, &result.LoginHistory, &result.Bindings, &result.Profiles, &result.Samples, &result.References, &result.Mappings, &result.TransportSamples, &result.Tracks} {
			if err := tx.Find(target).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}
func (r *Repository) Import(ctx context.Context, snapshot Snapshot) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.importProfiles(ctx, tx, snapshot.Profiles); err != nil {
			return err
		}
		if err := r.importReferences(ctx, tx, snapshot.References); err != nil {
			return err
		}
		if err := r.importTransportSamples(ctx, tx, snapshot.TransportSamples); err != nil {
			return err
		}
		if err := r.importSamples(ctx, tx, snapshot.Samples); err != nil {
			return err
		}
		if err := r.importLoginHistory(ctx, tx, snapshot.LoginHistory); err != nil {
			return err
		}
		if err := r.importLogins(ctx, tx, snapshot.Logins); err != nil {
			return err
		}
		if err := r.importBindings(ctx, tx, snapshot.Bindings); err != nil {
			return err
		}
		if err := r.importTracks(ctx, tx, snapshot.Tracks); err != nil {
			return err
		}
		if err := r.importMappings(ctx, tx, snapshot.Mappings); err != nil {
			return err
		}
		return r.reconcileAutoBindings(tx)
	})
}

func (r *Repository) importProfiles(ctx context.Context, tx *gorm.DB, records []ProfileRevision) error {
	for _, record := range records {
		if err := validateRevision(record); err != nil {
			return err
		}
		if err := mergeImmutable(tx, &record, "id", record.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importReferences(ctx context.Context, tx *gorm.DB, records []ReferenceSource) error {
	for _, record := range records {
		if record.ID == "" || record.Name == "" {
			return invalid("reference ID and name required")
		}
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importTransportSamples(ctx context.Context, tx *gorm.DB, records []TransportSample) error {
	for _, record := range records {
		if err := r.WithDB(tx).SaveTransportSample(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importSamples(ctx context.Context, tx *gorm.DB, records []Sample) error {
	for _, record := range records {
		if record.ID == "" || record.SourceID == "" || record.CapturedAt.IsZero() {
			return invalid("invalid imported sample")
		}
		if err := mergeImmutable(tx, &record, "id", record.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importLoginHistory(ctx context.Context, tx *gorm.DB, records []LoginHistory) error {
	for _, record := range records {
		if record.GenerationID == "" || record.GenerationID != record.Identity.GenerationID {
			return invalid("invalid login history")
		}
		if err := mergeImmutable(tx, &record, "generation_id", record.GenerationID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importLogins(ctx context.Context, tx *gorm.DB, records []LoginIdentity) error {
	for _, record := range records {
		if record.CredentialSessionID == "" || record.GenerationID == "" || record.DeviceID == "" || !record.AccountBasis.Resolved() {
			return invalid("invalid login identity")
		}
		if err := mergeImmutable(tx, &record, "credential_session_id", record.CredentialSessionID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importBindings(ctx context.Context, tx *gorm.DB, records []ProfileBinding) error {
	for _, record := range records {
		if record.CredentialSessionID == "" || !record.Tuple.Valid() || (record.Mode != ModeAuto && record.Mode != ModePinned) {
			return invalid("invalid profile binding")
		}
		var login LoginIdentity
		if err := tx.First(&login, "credential_session_id = ?", record.CredentialSessionID).Error; err != nil {
			return err
		}
		var profile ProfileRevision
		if err := tx.First(&profile, "id = ?", record.RevisionID).Error; err != nil {
			return err
		}
		if profile.Tuple != record.Tuple {
			return invalid("profile binding tuple conflict")
		}
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importTracks(ctx context.Context, tx *gorm.DB, records []ProfileTrack) error {
	for _, record := range records {
		if record.SourceID == "" || !record.Tuple().Valid() || record.ClientVersion == "" || record.RevisionID == "" || record.CapturedAt.IsZero() {
			return invalid("invalid profile track")
		}
		var profile ProfileRevision
		if err := tx.First(&profile, "id = ?", record.RevisionID).Error; err != nil {
			return err
		}
		if profile.Tuple != record.Tuple() || profile.SourceID != record.SourceID || profile.ClientVersion != record.ClientVersion {
			return invalid("profile track revision mismatch")
		}
		current, err := loadProfileTrack(tx, record.SourceID, record.Tuple())
		if err == nil {
			if CompareVersions(current.ClientVersion, record.ClientVersion) > 0 || current.ClientVersion == record.ClientVersion && !record.CapturedAt.After(current.CapturedAt) {
				continue
			}
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) importMappings(ctx context.Context, tx *gorm.DB, records []Mapping) error {
	for _, record := range records {
		if record.GenerationID == "" || record.ClientIdentityID == "" || record.Namespace == "" || record.Original == "" || record.Mapped == "" {
			return invalid("invalid imported mapping")
		}
		var current Mapping
		query := tx.Where("generation_id = ? AND client_identity_id = ? AND namespace = ? AND original = ?", record.GenerationID, record.ClientIdentityID, record.Namespace, record.Original)
		err := query.First(&current).Error
		if err == nil {
			if current != record {
				return fmt.Errorf("%w: identity mapping", ErrConflict)
			}
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
	}
	return nil
}
