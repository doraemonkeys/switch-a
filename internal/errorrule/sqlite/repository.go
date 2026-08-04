package sqlite

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrRuleNotFound       = errors.New("internal-error rule not found")
	ErrProviderNotFound   = errors.New("internal-error rule provider not found")
	ErrRevisionMismatch   = errors.New("internal-error rule revision mismatch")
	ErrRevisionOverflow   = errors.New("internal-error rule revision overflow")
	ErrRuleCapacity       = errors.New("internal-error rule capacity reached")
	ErrImportIDCollision  = errors.New("internal-error import ID collides with a preserved rule")
	ErrRevisionNotGreater = errors.New("published rule revision must be greater than current")
	ErrStatsRetirerBound  = errors.New("internal-error stats generation retirer is already bound")
)

type ProviderNotFoundError struct {
	ProviderID string
}

func (e *ProviderNotFoundError) Error() string {
	return fmt.Sprintf("%v: %q", ErrProviderNotFound, e.ProviderID)
}

func (e *ProviderNotFoundError) Unwrap() error { return ErrProviderNotFound }

type RevisionMismatchError struct {
	Expected errorrule.Revision
	Current  errorrule.Revision
}

func (e *RevisionMismatchError) Error() string {
	return fmt.Sprintf("%v: expected %s, current %s", ErrRevisionMismatch, e.Expected, e.Current)
}

func (e *RevisionMismatchError) Unwrap() error { return ErrRevisionMismatch }

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Config struct {
	DB          *gorm.DB
	Clock       Clock
	IDGenerator errorrule.IDGenerator
}

// Repository is the sole mutation serialization and publication domain. The
// database transaction and snapshot pointer therefore cannot observe different
// rule-set orders or revisions.
type Repository struct {
	db           *gorm.DB
	clock        Clock
	ids          errorrule.IDGenerator
	mutation     sync.Mutex
	snapshot     atomic.Pointer[errorrule.CompiledRuleSet]
	statsRetirer atomic.Pointer[statsRetirementBinding]
}

type statsRetirementBinding struct {
	retire func(statistics.Handle)
}

func Open(ctx context.Context, config Config) (*Repository, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("rule repository database is required")
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	if config.IDGenerator == nil {
		config.IDGenerator = errorrule.UUIDGenerator{}
	}
	if err := Migrate(ctx, config.DB); err != nil {
		return nil, err
	}
	repository := &Repository{db: config.DB, clock: config.Clock, ids: config.IDGenerator}
	var snapshot *errorrule.CompiledRuleSet
	if err := config.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		revision, rules, err := loadRuleSet(tx)
		if err != nil {
			return err
		}
		if err := validateProviderReferences(tx, rules); err != nil {
			return err
		}
		snapshot, err = errorrule.CompileRuleSet(revision, rules)
		return err
	}); err != nil {
		return nil, fmt.Errorf("load internal-error rule set: %w", err)
	}
	repository.snapshot.Store(snapshot)
	return repository, nil
}

func (r *Repository) CurrentRuleSet() *errorrule.CompiledRuleSet {
	return r.snapshot.Load()
}

// BindStatsGenerationRetirer completes the intentional construction cycle:
// persistence is the accumulator sink, while committed deletion publication
// retires the accumulator handle. Composition binds it once before serving.
func (r *Repository) BindStatsGenerationRetirer(retire func(statistics.Handle)) error {
	if retire == nil {
		return fmt.Errorf("stats generation retirer is required")
	}
	binding := &statsRetirementBinding{retire: retire}
	if !r.statsRetirer.CompareAndSwap(nil, binding) {
		return ErrStatsRetirerBound
	}
	return nil
}

func (r *Repository) PublishRuleSet(candidate *errorrule.CompiledRuleSet) error {
	if candidate == nil {
		return fmt.Errorf("published rule set is required")
	}
	for {
		current := r.snapshot.Load()
		if current != nil && candidate.Revision() <= current.Revision() {
			return ErrRevisionNotGreater
		}
		if r.snapshot.CompareAndSwap(current, candidate) {
			return nil
		}
	}
}

func (r *Repository) ListRules() (errorrule.Revision, []errorrule.Rule) {
	snapshot := r.snapshot.Load()
	if snapshot == nil {
		return 0, nil
	}
	return snapshot.Revision(), snapshot.Rules()
}

func (r *Repository) GetRule(id errorrule.RuleID) (errorrule.Revision, errorrule.Rule, error) {
	snapshot := r.snapshot.Load()
	if snapshot == nil {
		return 0, errorrule.Rule{}, ErrRuleNotFound
	}
	rule, exists := snapshot.Rule(id)
	if !exists {
		return snapshot.Revision(), errorrule.Rule{}, ErrRuleNotFound
	}
	return snapshot.Revision(), rule, nil
}

type RetiredGeneration struct {
	RuleID     errorrule.RuleID
	Generation errorrule.RuleGeneration
}

type MutationResult struct {
	Revision errorrule.Revision
	Rules    []errorrule.Rule
	Changed  bool
	Retired  []RetiredGeneration
}

type MutationFunc func(*gorm.DB, []errorrule.Rule) ([]errorrule.Rule, error)

type coordinatedMutation struct {
	result    MutationResult
	committed *errorrule.CompiledRuleSet
}

// Coordinate holds the rule mutation lock around the encompassing transaction.
// Store-level provider/config callbacks can therefore mutate their own tables
// and return a complete candidate rule set without creating a publication gap.
func (r *Repository) Coordinate(
	ctx context.Context,
	expected *errorrule.Revision,
	mutate MutationFunc,
) (MutationResult, error) {
	if mutate == nil {
		return MutationResult{}, fmt.Errorf("rule mutation callback is required")
	}
	r.mutation.Lock()
	defer r.mutation.Unlock()

	var coordinated coordinatedMutation
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transactionErr error
		coordinated, transactionErr = r.coordinateMutation(tx, expected, mutate)
		return transactionErr
	})
	if err != nil {
		return MutationResult{}, err
	}
	if coordinated.committed != nil {
		// The mutex excludes every legitimate publisher, so failure here means a
		// caller violated the repository's single-owner contract.
		if err := r.PublishRuleSet(coordinated.committed); err != nil {
			return MutationResult{}, fmt.Errorf("publish committed rule set: %w", err)
		}
		if binding := r.statsRetirer.Load(); binding != nil {
			for _, retired := range coordinated.result.Retired {
				binding.retire(statistics.Handle{RuleID: retired.RuleID, Generation: retired.Generation})
			}
		}
	}
	return coordinated.result, nil
}

func (r *Repository) coordinateMutation(
	tx *gorm.DB,
	expected *errorrule.Revision,
	mutate MutationFunc,
) (coordinatedMutation, error) {
	currentRevision, current, err := loadRuleSet(tx)
	if err != nil {
		return coordinatedMutation{}, err
	}
	if expected != nil && *expected != currentRevision {
		return coordinatedMutation{}, &RevisionMismatchError{Expected: *expected, Current: currentRevision}
	}
	if _, err := errorrule.CompileRuleSet(currentRevision, current); err != nil {
		return coordinatedMutation{}, fmt.Errorf("validate current rule set: %w", err)
	}

	candidate, err := mutate(tx, cloneRules(current))
	if err != nil {
		return coordinatedMutation{}, err
	}
	candidate, err = r.prepareCandidate(current, candidate)
	if err != nil {
		return coordinatedMutation{}, err
	}
	if err := validateProviderReferences(tx, candidate); err != nil {
		return coordinatedMutation{}, err
	}
	if semanticRuleSetsEqual(current, candidate) {
		return coordinatedMutation{result: MutationResult{
			Revision: currentRevision,
			Rules:    cloneRules(current),
		}}, nil
	}
	if currentRevision == errorrule.Revision(math.MaxInt64) {
		return coordinatedMutation{}, ErrRevisionOverflow
	}

	nextRevision := currentRevision + 1
	committed, err := errorrule.CompileRuleSet(nextRevision, candidate)
	if err != nil {
		return coordinatedMutation{}, fmt.Errorf("compile candidate rule set: %w", err)
	}
	retired := retiredGenerations(current, candidate)
	if err := persistRuleSet(tx, currentRevision, nextRevision, current, candidate); err != nil {
		return coordinatedMutation{}, err
	}
	return coordinatedMutation{
		committed: committed,
		result: MutationResult{
			Revision: nextRevision,
			Rules:    committed.Rules(),
			Changed:  true,
			Retired:  retired,
		},
	}, nil
}

func (r *Repository) prepareCandidate(current, candidate []errorrule.Rule) ([]errorrule.Rule, error) {
	if len(candidate) > errorrule.MaxRuleCount {
		return nil, ErrRuleCapacity
	}
	now := utcNow(r.clock)
	existing := make(map[errorrule.RuleID]errorrule.Rule, len(current))
	for _, rule := range current {
		existing[rule.ID] = rule
	}
	prepared := make([]errorrule.Rule, len(candidate))
	for index, supplied := range candidate {
		normalized, err := errorrule.NormalizeRuleSpec(supplied.RuleSpec)
		if err != nil {
			return nil, fmt.Errorf("normalize rule %q: %w", supplied.ID, err)
		}
		metadata := errorrule.RuleMetadata{ID: supplied.ID, Position: int64(index)}
		if old, found := existing[supplied.ID]; found {
			metadata.Generation = old.Generation()
			metadata.CreatedAt = old.CreatedAt
			metadata.UpdatedAt = old.UpdatedAt
			if old.Position != int64(index) || !reflect.DeepEqual(old.RuleSpec, normalized) {
				metadata.UpdatedAt = now
			}
		} else {
			if err := supplied.ID.Validate(); err != nil {
				return nil, err
			}
			generation, err := errorrule.ParseRuleGeneration(r.ids.NewID())
			if err != nil {
				return nil, fmt.Errorf("generate rule lifecycle: %w", err)
			}
			metadata.Generation = generation
			metadata.CreatedAt = now
			metadata.UpdatedAt = now
		}
		prepared[index] = errorrule.NewRule(normalized, metadata)
	}
	return prepared, nil
}

func loadRuleSet(tx *gorm.DB) (errorrule.Revision, []errorrule.Rule, error) {
	var revision int64
	if err := tx.Raw(
		"SELECT revision FROM internal_error_rule_set_meta WHERE id = ?",
		metaRowID,
	).Scan(&revision).Error; err != nil {
		return 0, nil, fmt.Errorf("load rule revision: %w", err)
	}
	var rows []ruleRow
	if err := tx.Order("position ASC").Find(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("load rules: %w", err)
	}
	rules := make([]errorrule.Rule, len(rows))
	for index, row := range rows {
		rule, err := decodeRule(row)
		if err != nil {
			return 0, nil, err
		}
		rules[index] = rule
	}
	return errorrule.Revision(revision), rules, nil
}

func validateProviderReferences(tx *gorm.DB, rules []errorrule.Rule) error {
	required := make(map[string]struct{})
	for _, rule := range rules {
		providerID, scoped := rule.Target.ProviderID()
		if scoped {
			required[string(providerID)] = struct{}{}
		}
	}
	if len(required) == 0 {
		return nil
	}
	ids := make([]string, 0, len(required))
	for id := range required {
		ids = append(ids, id)
	}
	var found []string
	if err := tx.Table("providers").Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return fmt.Errorf("load provider catalog for rules: %w", err)
	}
	for _, id := range found {
		delete(required, id)
	}
	if len(required) != 0 {
		missing := make([]string, 0, len(required))
		for id := range required {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return &ProviderNotFoundError{ProviderID: missing[0]}
	}
	return nil
}

func persistRuleSet(
	tx *gorm.DB,
	currentRevision, nextRevision errorrule.Revision,
	current, candidate []errorrule.Rule,
) error {
	keep := make(map[errorrule.RuleID]struct{}, len(candidate))
	for _, rule := range candidate {
		keep[rule.ID] = struct{}{}
	}
	for _, rule := range current {
		if _, retained := keep[rule.ID]; retained {
			continue
		}
		if err := tx.Where("rule_id = ?", rule.ID).Delete(&statsRow{}).Error; err != nil {
			return fmt.Errorf("delete stats for rule %q: %w", rule.ID, err)
		}
		if err := tx.Where("id = ?", rule.ID).Delete(&ruleRow{}).Error; err != nil {
			return fmt.Errorf("delete rule %q: %w", rule.ID, err)
		}
	}
	// Dense positions are unique. Moving the retained rows out of the target
	// range first makes arbitrary reorder permutations valid within one atomic
	// transaction instead of depending on update order.
	if len(current) > 0 {
		if err := tx.Exec(
			"UPDATE internal_error_rules SET position = position + ?",
			errorrule.MaxRuleCount+1,
		).Error; err != nil {
			return fmt.Errorf("stage rule reorder: %w", err)
		}
	}
	for _, rule := range candidate {
		row, err := encodeRule(rule)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
			return fmt.Errorf("persist rule %q: %w", rule.ID, err)
		}
		stats := statsRow{RuleID: string(rule.ID), Generation: rule.Generation().String(), HitCount: "0"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stats).Error; err != nil {
			return fmt.Errorf("initialize stats for rule %q: %w", rule.ID, err)
		}
	}
	updated := tx.Exec(
		"UPDATE internal_error_rule_set_meta SET revision = ? WHERE id = ? AND revision = ?",
		nextRevision, metaRowID, currentRevision,
	)
	if updated.Error != nil {
		return fmt.Errorf("persist rule revision: %w", updated.Error)
	}
	if updated.RowsAffected != 1 {
		return &RevisionMismatchError{Expected: currentRevision, Current: currentRevision}
	}
	return nil
}

func semanticRuleSetsEqual(left, right []errorrule.Rule) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || !reflect.DeepEqual(left[index].RuleSpec, right[index].RuleSpec) {
			return false
		}
	}
	return true
}

func retiredGenerations(current, candidate []errorrule.Rule) []RetiredGeneration {
	retained := make(map[errorrule.RuleID]struct{}, len(candidate))
	for _, rule := range candidate {
		retained[rule.ID] = struct{}{}
	}
	retired := make([]RetiredGeneration, 0)
	for _, rule := range current {
		if _, exists := retained[rule.ID]; !exists {
			retired = append(retired, RetiredGeneration{RuleID: rule.ID, Generation: rule.Generation()})
		}
	}
	return retired
}

func cloneRules(source []errorrule.Rule) []errorrule.Rule {
	if len(source) == 0 {
		return nil
	}
	// Compiling at the already validated revision is the domain-owned deep-copy
	// boundary; callers never receive mutable keyword/API pointers from storage.
	compiled, err := errorrule.CompileRuleSet(0, source)
	if err != nil {
		panic(fmt.Sprintf("clone validated rule set: %v", err))
	}
	return compiled.Rules()
}
