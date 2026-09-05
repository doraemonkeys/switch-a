package clientdisguise

import (
	"context"
	"errors"
	"fmt"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db    *gorm.DB
	now   func() time.Time
	newID func() string
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db, now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString}
}
func (r *Repository) WithDB(db *gorm.DB) *Repository {
	return &Repository{db: db, now: r.now, newID: r.newID}
}

func Migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(&LoginIdentity{}, &LoginHistory{}, &ProfileBinding{}, &ProfileRevision{}, &Sample{}, &ReferenceSource{}, &Mapping{}, &TransportSample{}, &ProfileTrack{}); err != nil {
			return err
		}
		for _, profile := range BuiltinProfiles() {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&profile).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func recordError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
func (r *Repository) GetLogin(ctx context.Context, sessionID string) (LoginIdentity, error) {
	var login LoginIdentity
	err := r.db.WithContext(ctx).First(&login, "credential_session_id = ?", sessionID).Error
	return login, recordError(err)
}

func (r *Repository) SyncLoginAccount(ctx context.Context, sessionID string, basis AccountBasis) (LoginIdentity, error) {
	var result LoginIdentity
	if sessionID == "" {
		return result, invalid("credential session ID required")
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		repo := r.WithDB(tx)
		current, err := repo.GetLogin(ctx, sessionID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && (!basis.Resolved() || current.AccountBasis.Equal(basis)) {
			result = current
			return nil
		}
		if !basis.Resolved() {
			return ErrNotFound
		}
		if err == nil {
			history := LoginHistory{GenerationID: current.GenerationID, Identity: current}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&history).Error; err != nil {
				return err
			}
			// A replaced account must make a fresh first-profile decision. Old
			// generations and mappings remain available for ownership diagnostics.
			if err := tx.Delete(&ProfileBinding{}, "credential_session_id = ?", sessionID).Error; err != nil {
				return err
			}
		}
		result = LoginIdentity{CredentialSessionID: sessionID, GenerationID: r.newID(), DeviceID: r.newID(), AccountBasis: basis, CreatedAt: r.now()}
		return tx.Save(&result).Error
	})
	return result, err
}

func (r *Repository) EvaluateCandidate(ctx context.Context, sessionID string, basis AccountBasis, policy Policy, facts PlatformFacts) (Candidate, error) {
	candidate := Candidate{CredentialSessionID: sessionID, AccountBasis: basis, Policy: policy, Facts: facts}
	if err := policy.Validate(); err != nil {
		return candidate, err
	}
	if !policy.Enabled {
		candidate.Decision = EvaluatePlatform(policy, facts, Tuple{})
		return candidate, nil
	}
	var binding ProfileBinding
	err := r.db.WithContext(ctx).First(&binding, "credential_session_id = ?", sessionID).Error
	if err == nil {
		candidate.Binding = &binding
		err = r.db.WithContext(ctx).First(&candidate.Profile, "id = ?", binding.RevisionID).Error

	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		if facts.Tuple.Valid() {
			for _, profile := range BuiltinProfiles() {
				if profile.Tuple == facts.Tuple {
					candidate.Profile = profile
					break
				}
			}
		}
		err = nil
	}
	if err != nil {
		return candidate, err
	}
	if binding.TransportSampleID != "" {
		var transport TransportSample
		if err := r.db.WithContext(ctx).First(&transport, "id = ?", binding.TransportSampleID).Error; err != nil {
			return candidate, err
		}
		candidate.Transport = &transport
	}
	candidate.Decision = EvaluatePlatform(policy, facts, candidate.Profile.Tuple)
	if candidate.Profile.ID == "" {
		candidate.Decision.Allowed = false
		candidate.Decision.Reason = "profile_unavailable"
	}
	return candidate, nil
}

func (r *Repository) CommitTarget(ctx context.Context, candidate Candidate) (TargetSnapshot, error) {
	result := TargetSnapshot{Policy: candidate.Policy}
	if !candidate.Policy.Enabled {
		return result, nil
	}
	if !candidate.Decision.Allowed {
		return result, fmt.Errorf("%w: %s", ErrCandidateExcluded, candidate.Decision.Reason)
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		repo := r.WithDB(tx)
		login, err := repo.GetLogin(ctx, candidate.CredentialSessionID)
		if errors.Is(err, ErrNotFound) {
			login, err = repo.SyncLoginAccount(ctx, candidate.CredentialSessionID, candidate.AccountBasis)
		}
		if err != nil {
			return err
		}
		if !login.AccountBasis.Equal(candidate.AccountBasis) {
			return ErrAccountChanged
		}
		// An existing binding was already snapshotted at the logical ingress boundary.
		// Later profile edits cannot change the first transmission of this operation.
		if candidate.Binding != nil {
			result.Login, result.Binding, result.Profile, result.Transport = login, candidate.Binding.Clone(), candidate.Profile.Clone(), candidate.Transport
			return nil
		}
		binding := ProfileBinding{CredentialSessionID: candidate.CredentialSessionID, Tuple: candidate.Profile.Tuple, Mode: ModeAuto, RevisionID: candidate.Profile.ID, UpdatedAt: r.now()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
			return err
		}
		if err := tx.First(&binding, "credential_session_id = ?", candidate.CredentialSessionID).Error; err != nil {
			return err
		}
		var profile ProfileRevision
		if err := tx.First(&profile, "id = ?", binding.RevisionID).Error; err != nil {
			return err
		}
		decision := EvaluatePlatform(candidate.Policy, candidate.Facts, profile.Tuple)
		if !decision.Allowed {
			return fmt.Errorf("%w: %s", ErrCandidateExcluded, decision.Reason)
		}
		result.Login, result.Binding, result.Profile = login, binding, profile
		if binding.TransportSampleID != "" {
			var transport TransportSample
			if err := tx.First(&transport, "id = ?", binding.TransportSampleID).Error; err != nil {
				return err
			}
			result.Transport = &transport
		}
		return nil
	})
	return result, err
}

func (r *Repository) SetBinding(ctx context.Context, binding ProfileBinding) (ProfileBinding, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var login LoginIdentity
		if err := tx.First(&login, "credential_session_id = ?", binding.CredentialSessionID).Error; err != nil {
			return recordError(err)
		}
		if binding.Mode != ModeAuto && binding.Mode != ModePinned {
			return invalid("profile mode must be auto or pinned")
		}
		var revision ProfileRevision
		if err := tx.First(&revision, "id = ?", binding.RevisionID).Error; err != nil {
			return recordError(err)
		}
		binding.Tuple = revision.Tuple
		if binding.ReferenceSourceID != "" {
			var source ReferenceSource
			if err := tx.First(&source, "id = ?", binding.ReferenceSourceID).Error; err != nil {
				return recordError(err)
			}
		}
		if binding.TransportSampleID != "" {
			var transport TransportSample
			if err := tx.First(&transport, "id = ?", binding.TransportSampleID).Error; err != nil {
				return recordError(err)
			}
		}
		if err := resolveAutoRevision(tx, &binding, revision); err != nil {
			return err
		}
		binding.UpdatedAt = r.now()
		return tx.Save(&binding).Error
	})
	return binding, err
}

func (r *Repository) SelectProfile(ctx context.Context, sessionID, revisionID string) (ProfileBinding, error) {
	var binding ProfileBinding
	if err := r.db.WithContext(ctx).First(&binding, "credential_session_id = ?", sessionID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return binding, err
	}
	binding.CredentialSessionID, binding.RevisionID, binding.Mode = sessionID, revisionID, ModePinned
	return r.SetBinding(ctx, binding)
}

func listRecords[T any](ctx context.Context, db *gorm.DB) ([]T, error) {
	result := make([]T, 0)
	err := db.WithContext(ctx).Find(&result).Error
	return result, err
}
func (r *Repository) ListLogins(ctx context.Context) ([]LoginIdentity, error) {
	return listRecords[LoginIdentity](ctx, r.db)
}
func (r *Repository) ListBindings(ctx context.Context) ([]ProfileBinding, error) {
	return listRecords[ProfileBinding](ctx, r.db)
}
func (r *Repository) ListProfiles(ctx context.Context) ([]ProfileRevision, error) {
	return listRecords[ProfileRevision](ctx, r.db)
}
func (r *Repository) ListReferences(ctx context.Context) ([]ReferenceSource, error) {
	return listRecords[ReferenceSource](ctx, r.db)
}
func (r *Repository) ListTransportSamples(ctx context.Context) ([]TransportSample, error) {
	return listRecords[TransportSample](ctx, r.db)
}

func (r *Repository) SaveReference(ctx context.Context, source ReferenceSource) error {
	if source.ID == "" || source.Name == "" || source.ClientIdentityID == "" {
		return invalid("reference ID, name and client identity required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var client clientidentity.Client
		if err := tx.First(&client, "id = ?", source.ClientIdentityID).Error; err != nil {
			return recordError(err)
		}
		return tx.Save(&source).Error
	})
}
func (r *Repository) SaveTransportSample(ctx context.Context, sample TransportSample) error {
	if err := validateTransportSample(sample); err != nil {
		return err
	}
	return mergeImmutable(r.db.WithContext(ctx), &sample, "id", sample.ID)
}

func (r *Repository) MapIdentity(ctx context.Context, key MappingKey) (string, error) {
	if key.Original == "" {
		return "", nil
	}
	if key.GenerationID == "" || key.ClientIdentityID == "" || key.Namespace == "" {
		return "", invalid("mapping generation, client and namespace required")
	}
	mapping := Mapping{MappingKey: key, Mapped: uuid.NewString()}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&mapping).Error; err != nil {
			return err
		}
		return tx.First(&mapping, "generation_id = ? AND client_identity_id = ? AND namespace = ? AND original = ?", key.GenerationID, key.ClientIdentityID, key.Namespace, key.Original).Error
	})
	return mapping.Mapped, err
}
func (r *Repository) RestoreIdentity(ctx context.Context, generationID, clientIdentityID, namespace, mapped string) (string, bool, error) {
	var mapping Mapping
	err := r.db.WithContext(ctx).First(&mapping, "generation_id = ? AND client_identity_id = ? AND namespace = ? AND mapped = ?", generationID, clientIdentityID, namespace, mapped).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return mapped, false, nil
	}
	return mapping.Original, err == nil, err
}
