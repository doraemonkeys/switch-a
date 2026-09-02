package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type sequenceIDs struct {
	mu     sync.Mutex
	values []string
}

func (g *sequenceIDs) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.values) == 0 {
		panic("test ID sequence exhausted")
	}
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

func openTestRepository(t *testing.T, ids ...string) (*gorm.DB, *Repository, *testClock) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rules.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE providers (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)}
	repository, err := Open(context.Background(), Config{
		DB: db, Clock: clock, IDGenerator: &sequenceIDs{values: append([]string(nil), ids...)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, repository, clock
}

func passthroughSpec(name string, target errorrule.Target) errorrule.RuleSpec {
	return errorrule.RuleSpec{
		Name: name, Enabled: true, Target: target, Keywords: []string{"overloaded"},
		MatchMode: errorrule.MatchAny, Action: errorrule.NewPassthroughAction(),
	}
}

func TestRepositoryCRUDNoOpRestartAndInvalidStartup(t *testing.T) {
	db, repository, clock := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	initial := repository.CurrentRuleSet()
	if initial == nil || initial.Revision() != 0 || len(initial.Rules()) != 0 {
		t.Fatalf("initial snapshot = %#v", initial)
	}

	created, err := repository.CreateRule(context.Background(), 0, passthroughSpec("Capacity", errorrule.NewGlobalTarget()))
	if err != nil {
		t.Fatal(err)
	}
	if !created.Changed || created.Revision != 1 || len(created.Rules) != 1 {
		t.Fatalf("created = %#v", created)
	}
	published := repository.CurrentRuleSet()
	createdRule := created.Rules[0]
	if _, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{
		Handle: statistics.HandleFor(createdRule), HitCount: 5, LastHitAt: clock.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	noOp, err := repository.UpdateRule(context.Background(), 1, createdRule.ID, createdRule.RuleSpec)
	if err != nil {
		t.Fatal(err)
	}
	if noOp.Changed || noOp.Revision != 1 || repository.CurrentRuleSet() != published {
		t.Fatalf("no-op mutation changed snapshot: %#v", noOp)
	}
	clock.Advance(time.Minute)
	changedSpec := createdRule.RuleSpec
	changedSpec.Name = "Capacity updated"
	updated, err := repository.UpdateRule(context.Background(), 1, createdRule.ID, changedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || !updated.Rules[0].UpdatedAt.Equal(clock.Now()) ||
		updated.Rules[0].Generation() != createdRule.Generation() ||
		!updated.Rules[0].CreatedAt.Equal(createdRule.CreatedAt) {
		t.Fatalf("updated = %#v", updated)
	}
	stats, err := repository.ListStats(context.Background())
	if err != nil || len(stats) != 1 || stats[0].HitCount != 5 {
		t.Fatalf("stats after update = %#v, error %v", stats, err)
	}

	reopened, err := Open(context.Background(), Config{DB: db, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	revision, rules := reopened.ListRules()
	if revision != 2 || len(rules) != 1 || rules[0].Name != "Capacity updated" {
		t.Fatalf("reopened revision=%d rules=%#v", revision, rules)
	}

	if err := db.Exec("UPDATE internal_error_rules SET keywords_json = 'not-json'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Config{DB: db, Clock: clock}); err == nil {
		t.Fatal("invalid persisted rule did not fail startup")
	}
}

func TestRepositorySnapshotReadsPerformNoDatabaseQueries(t *testing.T) {
	db, repository, _ := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	created, err := repository.CreateRule(
		context.Background(),
		0,
		passthroughSpec("cached", errorrule.NewGlobalTarget()),
	)
	if err != nil {
		t.Fatal(err)
	}
	ruleID := created.Rules[0].ID

	queryCount := 0
	countQuery := func(*gorm.DB) { queryCount++ }
	if err := db.Callback().Query().Before("gorm:query").Register("p2:count_snapshot_query", countQuery); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Raw().Before("gorm:raw").Register("p2:count_snapshot_raw", countQuery); err != nil {
		t.Fatal(err)
	}

	pinned := repository.CurrentRuleSet()
	for range 100 {
		if repository.CurrentRuleSet() != pinned {
			t.Fatal("snapshot pointer changed without a mutation")
		}
		if revision, rules := repository.ListRules(); revision != 1 || len(rules) != 1 {
			t.Fatalf("ListRules() = (%s, %d rules)", revision, len(rules))
		}
		if revision, _, err := repository.GetRule(ruleID); err != nil || revision != 1 {
			t.Fatalf("GetRule() = revision %s, error %v", revision, err)
		}
	}
	if queryCount != 0 {
		t.Fatalf("snapshot reads executed %d database queries", queryCount)
	}
}

func TestRepositoryProviderCascadeAndLateGenerationCannotResurrect(t *testing.T) {
	db, repository, _ := openTestRepository(t,
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
	)
	if err := db.Exec("INSERT INTO providers (id) VALUES ('provider-a')").Error; err != nil {
		t.Fatal(err)
	}
	target, _ := errorrule.NewProviderTarget("provider-a")
	publicID := errorrule.RuleID("11111111-1111-4111-8111-111111111111")
	request := ImportRequest{Mode: ImportModeFull, Rules: []ImportedRule{{ID: publicID, RuleSpec: passthroughSpec("A", target)}}}
	created, err := repository.Coordinate(context.Background(), nil, func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		candidate, _, err := BuildImportCandidate(current, request)
		return candidate, err
	})
	if err != nil {
		t.Fatal(err)
	}
	oldHandle := statistics.HandleFor(created.Rules[0])
	if err := repository.BindStatsGenerationRetirer(nil); err == nil {
		t.Fatal("nil stats generation retirer was accepted")
	}
	accumulator, err := statistics.New(repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); err != nil {
		t.Fatal(err)
	}
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); !errors.Is(err, ErrStatsRetirerBound) {
		t.Fatalf("second stats generation retirer binding error = %v", err)
	}
	if err := accumulator.Hit(oldHandle, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{
		Handle: oldHandle, HitCount: 3, LastHitAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	_, err = repository.Coordinate(context.Background(), nil, func(tx *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		if err := tx.Exec("DELETE FROM providers WHERE id = 'provider-a'").Error; err != nil {
			return nil, err
		}
		return current, nil
	})
	if err == nil {
		t.Fatal("provider deletion with retained scoped rule committed")
	}
	var missingProvider *ProviderNotFoundError
	if !errors.Is(err, ErrProviderNotFound) || !errors.As(err, &missingProvider) || missingProvider.ProviderID != "provider-a" {
		t.Fatalf("provider validation error = %v, want typed provider-a not-found error", err)
	}
	var providerCount int64
	if err := db.Table("providers").Count(&providerCount).Error; err != nil || providerCount != 1 {
		t.Fatalf("provider rollback count=%d err=%v", providerCount, err)
	}

	deleted, err := repository.Coordinate(context.Background(), nil, func(tx *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		if err := tx.Exec("DELETE FROM providers WHERE id = 'provider-a'").Error; err != nil {
			return nil, err
		}
		return RemoveProviderRules(current, "provider-a"), nil
	})
	if err != nil || deleted.Revision != 2 || len(deleted.Retired) != 1 {
		t.Fatalf("deleted=%#v err=%v", deleted, err)
	}
	if pending := accumulator.Pending(oldHandle); pending.HitCount != 0 {
		t.Fatalf("retired generation remained pending after publication: %#v", pending)
	}
	late, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{
		Handle: oldHandle, HitCount: 1, LastHitAt: time.Now(),
	}})
	if err != nil || len(late.Missing) != 1 {
		t.Fatalf("late result=%#v err=%v", late, err)
	}

	if err := db.Exec("INSERT INTO providers (id) VALUES ('provider-a')").Error; err != nil {
		t.Fatal(err)
	}
	recreated, err := repository.Coordinate(context.Background(), nil, func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		candidate, _, err := BuildImportCandidate(current, request)
		return candidate, err
	})
	if err != nil {
		t.Fatal(err)
	}
	newHandle := statistics.HandleFor(recreated.Rules[0])
	if newHandle.Generation == oldHandle.Generation {
		t.Fatal("recreated rule reused lifecycle generation")
	}
	if _, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{
		Handle: oldHandle, HitCount: 5, LastHitAt: time.Now(),
	}, {
		Handle: newHandle, HitCount: 1, LastHitAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	stats, err := repository.ListStats(context.Background())
	if err != nil || len(stats) != 1 || stats[0].HitCount != 1 {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
}

func TestRepositoryConcurrentExpectedRevisionHasOneWinner(t *testing.T) {
	_, repository, _ := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	created, err := repository.CreateRule(context.Background(), 0, passthroughSpec("base", errorrule.NewGlobalTarget()))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Rules[0].ID
	start := make(chan struct{})
	errorsByWriter := make(chan error, 2)
	for _, name := range []string{"left", "right"} {
		go func() {
			<-start
			spec := created.Rules[0].RuleSpec
			spec.Name = name
			_, err := repository.UpdateRule(context.Background(), 1, id, spec)
			errorsByWriter <- err
		}()
	}
	close(start)
	var successes, mismatches int
	for range 2 {
		err := <-errorsByWriter
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRevisionMismatch):
			mismatches++
		default:
			t.Fatalf("unexpected mutation error: %v", err)
		}
	}
	if successes != 1 || mismatches != 1 || repository.CurrentRuleSet().Revision() != 2 {
		t.Fatalf("successes=%d mismatches=%d revision=%d", successes, mismatches, repository.CurrentRuleSet().Revision())
	}
	stale, err := errorrule.CompileRuleSet(1, repository.CurrentRuleSet().Rules())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PublishRuleSet(stale); !errors.Is(err, ErrRevisionNotGreater) {
		t.Fatalf("PublishRuleSet(stale) error = %v", err)
	}
}

func TestBuildImportCandidateSelectionOrderCountsAndCollision(t *testing.T) {
	globalID := errorrule.RuleID("11111111-1111-4111-8111-111111111111")
	selectedID := errorrule.RuleID("22222222-2222-4222-8222-222222222222")
	preservedID := errorrule.RuleID("33333333-3333-4333-8333-333333333333")
	newID := errorrule.RuleID("44444444-4444-4444-8444-444444444444")
	providerA, _ := errorrule.NewProviderTarget("provider-a")
	providerB, _ := errorrule.NewProviderTarget("provider-b")
	metadata := func(id errorrule.RuleID, generation string, position int64) errorrule.RuleMetadata {
		parsed, _ := errorrule.ParseRuleGeneration(generation)
		now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
		return errorrule.RuleMetadata{ID: id, Generation: parsed, Position: position, CreatedAt: now, UpdatedAt: now}
	}
	current := []errorrule.Rule{
		errorrule.NewRule(passthroughSpec("global", errorrule.NewGlobalTarget()), metadata(globalID, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", 0)),
		errorrule.NewRule(passthroughSpec("selected", providerA), metadata(selectedID, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", 1)),
		errorrule.NewRule(passthroughSpec("preserved", providerB), metadata(preservedID, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", 2)),
	}
	changed := passthroughSpec("selected changed", providerA)
	imported := []ImportedRule{
		{ID: globalID, RuleSpec: passthroughSpec("imported global", errorrule.NewGlobalTarget())},
		{ID: selectedID, RuleSpec: changed},
		{ID: newID, RuleSpec: passthroughSpec("new", providerA)},
	}
	full, fullCounts, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModeFull, Rules: imported})
	if err != nil {
		t.Fatal(err)
	}
	if got := []errorrule.RuleID{full[0].ID, full[1].ID, full[2].ID}; !reflect.DeepEqual(got, []errorrule.RuleID{globalID, selectedID, newID}) {
		t.Fatalf("full order = %v", got)
	}
	if fullCounts != (ImportCounts{Add: 1, Update: 2, Delete: 1}) {
		t.Fatalf("full counts = %#v", fullCounts)
	}
	preserved, preservedCounts, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModePreserve, Rules: imported})
	if err != nil {
		t.Fatal(err)
	}
	if !semanticRuleSetsEqual(current, preserved) || preservedCounts != (ImportCounts{}) {
		t.Fatalf("settings-only preservation = %#v, counts %#v", preserved, preservedCounts)
	}
	request := ImportRequest{
		Mode: ImportModeSelection, SelectedProviderIDs: []string{"provider-a"},
		Rules: imported,
	}
	candidate, counts, err := BuildImportCandidate(current, request)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []errorrule.RuleID{globalID, preservedID, selectedID, newID}
	for index, id := range wantOrder {
		if candidate[index].ID != id {
			t.Fatalf("candidate order[%d]=%s want %s", index, candidate[index].ID, id)
		}
	}
	if counts.Add != 1 || counts.Update != 2 || counts.Delete != 0 || counts.Unchanged != 1 {
		t.Fatalf("counts = %#v", counts)
	}
	request.Rules = []ImportedRule{{ID: globalID, RuleSpec: passthroughSpec("collision", providerA)}}
	if _, _, err := BuildImportCandidate(current, request); !errors.Is(err, ErrImportIDCollision) {
		t.Fatalf("collision error = %v", err)
	}

	capacityCurrent := make([]errorrule.Rule, errorrule.MaxRuleCount)
	for index := range capacityCurrent {
		id := errorrule.RuleID(fmt.Sprintf("00000000-0000-4000-8000-%012d", index))
		generation := fmt.Sprintf("10000000-0000-4000-8000-%012d", index)
		capacityCurrent[index] = errorrule.NewRule(
			passthroughSpec("preserved", providerB),
			metadata(id, generation, int64(index)),
		)
	}
	request.Rules = []ImportedRule{{ID: newID, RuleSpec: passthroughSpec("new", providerA)}}
	if _, _, err := BuildImportCandidate(capacityCurrent, request); !errors.Is(err, ErrRuleCapacity) {
		t.Fatalf("selection capacity error = %v", err)
	}
}
