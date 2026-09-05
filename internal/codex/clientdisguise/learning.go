package clientdisguise

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CompareVersions orders numeric releases and prerelease identifiers without
// mistaking a later capture of an old client for an upgrade.
func CompareVersions(a, b string) int {
	av, ap := versionParts(a)
	bv, bp := versionParts(b)
	n := len(av)
	if len(bv) > n {
		n = len(bv)
	}
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	if ap == bp {
		return 0
	}
	if ap == "" {
		return 1
	}
	if bp == "" {
		return -1
	}
	ax, bx := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(ax) && i < len(bx); i++ {
		if ax[i] == bx[i] {
			continue
		}
		an, ae := strconv.Atoi(ax[i])
		bn, be := strconv.Atoi(bx[i])
		if ae == nil && be == nil {
			if an < bn {
				return -1
			}
			return 1
		}
		if ae == nil {
			return -1
		}
		if be == nil {
			return 1
		}
		return strings.Compare(ax[i], bx[i])
	}
	if len(ax) < len(bx) {
		return -1
	}
	return 1
}
func validateFeatures(f Features) error {
	for name := range f.Headers {
		switch strings.ToLower(name) {
		case "user-agent", "originator", "version", "x-client-version", "x-stainless-os", "x-stainless-arch":
		default:
			return invalid("field %q is not a client profile feature", name)
		}
	}
	return nil
}
func validateRevision(p ProfileRevision) error {
	if p.ID == "" || !p.Tuple.Valid() || p.ClientVersion == "" || p.SourceID == "" {
		return invalid("profile ID, supported tuple, version and source required")
	}
	if p.Features.ClientVersion != "" && p.Features.ClientVersion != p.ClientVersion {
		return invalid("profile feature version differs from client version")
	}
	return validateFeatures(p.Features)
}
func (r *Repository) LearnSample(ctx context.Context, sample Sample) (LearnResult, error) {
	var result LearnResult
	if sample.ID == "" || sample.SourceID == "" || sample.CapturedAt.IsZero() || !sample.Tuple.Valid() || sample.ClientVersion == "" {
		return result, invalid("sample ID, source, capture time, supported tuple and version required")
	}
	if err := validateRevision(ProfileRevision{ID: sample.ID, Tuple: sample.Tuple, ClientVersion: sample.ClientVersion, SourceID: sample.SourceID, Features: sample.Features}); err != nil {
		return result, err
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source ReferenceSource
		if err := tx.First(&source, "id = ?", sample.SourceID).Error; err != nil {
			return recordError(err)
		}
		if err := mergeImmutable(tx, &sample, "id", sample.ID); err != nil {
			return err
		}
		track, trackErr := loadProfileTrack(tx, sample.SourceID, sample.Tuple)
		if trackErr != nil && !errors.Is(trackErr, gorm.ErrRecordNotFound) {
			return trackErr
		}
		hasTrack := trackErr == nil
		eligible := !hasTrack || CompareVersions(sample.ClientVersion, track.ClientVersion) > 0 ||
			CompareVersions(sample.ClientVersion, track.ClientVersion) == 0 && sample.CapturedAt.After(track.CapturedAt)
		var err error
		result, err = r.learnRevision(tx, sample, track, hasTrack, eligible)
		if err != nil {
			return err
		}
		if !eligible {
			return nil
		}
		// Duplicate observations still advance collection time. Otherwise a late
		// historical sample could replace the head after a newer duplicate.
		if !result.Created && hasTrack && track.ClientVersion == sample.ClientVersion {
			track.CapturedAt = sample.CapturedAt
			return tx.Save(&track).Error
		}
		track = newProfileTrack(sample)
		track.RevisionID = result.Revision.ID
		if err := tx.Save(&track).Error; err != nil {
			return err
		}
		result.AdvancedSessions, err = r.advanceBindings(tx, sample, result.Revision)
		return err
	})
	return result, err
}

// An absent observation is not evidence that a previous feature disappeared.
func overlayFeatures(previous, observed Features) Features {
	result := previous.Clone()
	if observed.UserAgent != "" {
		result.UserAgent = observed.UserAgent
	}
	if observed.Originator != "" {
		result.Originator = observed.Originator
	}
	if observed.ClientVersion != "" {
		result.ClientVersion = observed.ClientVersion
	}
	if observed.DesktopBuild != "" {
		result.DesktopBuild = observed.DesktopBuild
	}
	if observed.OSVersion != "" {
		result.OSVersion = observed.OSVersion
	}
	if len(observed.Headers) > 0 && result.Headers == nil {
		result.Headers = make(map[string]string)
	}
	for name, value := range observed.Headers {
		result.Headers[name] = value
	}
	return result
}

// ObserveReference learns only from an explicitly chosen persistent client.
func (r *Repository) ObserveReference(ctx context.Context, clientID string, sample Sample) ([]LearnResult, error) {
	var sources []ReferenceSource
	if err := r.db.WithContext(ctx).Where("client_identity_id = ?", clientID).Find(&sources).Error; err != nil {
		return nil, err
	}
	results := make([]LearnResult, 0, len(sources))
	for _, source := range sources {
		input := sample
		input.ID = uuid.NewString()
		input.SourceID = source.ID
		result, err := r.LearnSample(ctx, input)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}
func versionParts(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v = strings.SplitN(v, "+", 2)[0]
	pair := strings.SplitN(v, "-", 2)
	nums := strings.Split(pair[0], ".")
	result := make([]int, len(nums))
	for i, n := range nums {
		result[i], _ = strconv.Atoi(n)
	}
	pre := ""
	if len(pair) > 1 {
		pre = pair[1]
	}
	return result, pre
}

func (r *Repository) learnRevision(tx *gorm.DB, sample Sample, track ProfileTrack, hasTrack, eligible bool) (LearnResult, error) {
	var result LearnResult
	var profiles []ProfileRevision
	if err := tx.Where("source_id = ?", sample.SourceID).Find(&profiles).Error; err != nil {
		return result, err
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].CapturedAt.After(profiles[j].CapturedAt) })
	features := sample.Features.Clone()
	for _, previous := range profiles {
		use := previous.Tuple == sample.Tuple && !previous.CapturedAt.After(sample.CapturedAt) && CompareVersions(previous.ClientVersion, sample.ClientVersion) <= 0
		if eligible && hasTrack {
			use = previous.ID == track.RevisionID
		}
		if use {
			features = overlayFeatures(previous.Features, features)
			break
		}
	}
	if features.ClientVersion != "" {
		features.ClientVersion = sample.ClientVersion
	}
	for _, profile := range profiles {
		if profile.Tuple == sample.Tuple && profile.ClientVersion == sample.ClientVersion && reflect.DeepEqual(profile.Features, features) {
			result.Revision = profile
			break
		}
	}
	if result.Revision.ID == "" {
		result.Revision = ProfileRevision{EvidenceKind: "reference", ID: r.newID(), Tuple: sample.Tuple, ClientVersion: sample.ClientVersion, Features: features, SourceID: sample.SourceID, CapturedAt: sample.CapturedAt, CreatedAt: r.now()}
		if err := tx.Create(&result.Revision).Error; err != nil {
			return result, err
		}
		result.Created = true
	}
	return result, nil
}

func (r *Repository) advanceBindings(tx *gorm.DB, sample Sample, revision ProfileRevision) ([]string, error) {
	var advanced []string
	var bindings []ProfileBinding
	if err := tx.Where("mode = ? AND reference_source_id = ?", ModeAuto, sample.SourceID).Find(&bindings).Error; err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.Tuple != sample.Tuple {
			continue
		}
		var current ProfileRevision
		if err := tx.First(&current, "id = ?", binding.RevisionID).Error; err != nil {
			return nil, err
		}
		if CompareVersions(revision.ClientVersion, current.ClientVersion) < 0 || current.ID == revision.ID {
			continue
		}
		binding.RevisionID, binding.UpdatedAt = revision.ID, r.now()
		if err := tx.Save(&binding).Error; err != nil {
			return nil, err
		}
		advanced = append(advanced, binding.CredentialSessionID)
	}
	return advanced, nil
}
