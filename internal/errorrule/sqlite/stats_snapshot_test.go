package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"gorm.io/gorm"
)

func TestListStatsSnapshotReturnsRevisionAndRowsFromOneDatabaseSnapshot(t *testing.T) {
	_, repository, clock := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	revision, stats, err := repository.ListStatsSnapshot(context.Background())
	if err != nil || revision != 0 || len(stats) != 0 {
		t.Fatalf("empty snapshot revision=%s stats=%#v err=%v", revision, stats, err)
	}
	created, err := repository.CreateRule(context.Background(), 0, passthroughSpec("stats", errorrule.NewGlobalTarget()))
	if err != nil {
		t.Fatal(err)
	}
	rule := created.Rules[0]
	lastHit := clock.Now().Add(time.Minute)
	if _, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{
		Handle: statistics.HandleFor(rule), HitCount: 7, LastHitAt: lastHit,
	}}); err != nil {
		t.Fatal(err)
	}
	revision, stats, err = repository.ListStatsSnapshot(context.Background())
	if err != nil || revision != 1 || len(stats) != 1 || stats[0].RuleID != rule.ID ||
		stats[0].HitCount != 7 || stats[0].LastHitAt == nil || !stats[0].LastHitAt.Equal(lastHit.UTC()) {
		t.Fatalf("snapshot revision=%s stats=%#v err=%v", revision, stats, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := repository.ListStatsSnapshot(canceled); err == nil {
		t.Fatal("canceled stats snapshot succeeded")
	}
}

func TestListStatsSnapshotExposesCommittedRevisionDuringPublicationGap(t *testing.T) {
	_, repository, _ := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	before := repository.CurrentRuleSet()
	created, err := repository.CreateRule(context.Background(), 0, passthroughSpec("gap", errorrule.NewGlobalTarget()))
	if err != nil {
		t.Fatal(err)
	}
	after := repository.CurrentRuleSet()

	// Reinstalling the old immutable pointer deterministically models the small
	// interval after the database commit and before Coordinate publishes `after`.
	repository.snapshot.Store(before)
	t.Cleanup(func() { repository.snapshot.Store(after) })
	revision, stats, err := repository.ListStatsSnapshot(context.Background())
	if err != nil || revision != created.Revision || len(stats) != 1 || stats[0].RuleID != created.Rules[0].ID {
		t.Fatalf("publication-gap snapshot revision=%s stats=%#v err=%v", revision, stats, err)
	}
	if repository.CurrentRuleSet().Revision() != 0 {
		t.Fatal("test did not preserve the old in-memory side of the publication gap")
	}
}

func TestCreateResultRemainsAuthoritativeWhenProviderDeletionQueuesBehindIt(t *testing.T) {
	db, repository, _ := openTestRepository(t)
	if err := db.Exec("INSERT INTO providers (id) VALUES (?)", "provider-race").Error; err != nil {
		t.Fatal(err)
	}
	ids := &createDeleteBarrierIDs{
		values: []string{
			"11111111-1111-4111-8111-111111111111",
			"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		},
		insideCreate:  make(chan struct{}),
		releaseCreate: make(chan struct{}),
	}
	repository.ids = ids
	target, err := errorrule.NewProviderTarget("provider-race")
	if err != nil {
		t.Fatal(err)
	}

	type createOutcome struct {
		result MutationResult
		err    error
	}
	created := make(chan createOutcome, 1)
	go func() {
		result, createErr := repository.CreateRule(context.Background(), 0, passthroughSpec("race", target))
		created <- createOutcome{result: result, err: createErr}
	}()
	<-ids.insideCreate

	type deleteOutcome struct {
		result MutationResult
		err    error
	}
	deleteStarted := make(chan struct{})
	deleted := make(chan deleteOutcome, 1)
	go func() {
		close(deleteStarted)
		result, deleteErr := repository.Coordinate(context.Background(), nil, func(tx *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
			if err := tx.Exec("DELETE FROM providers WHERE id = ?", "provider-race").Error; err != nil {
				return nil, err
			}
			return RemoveProviderRules(current, "provider-race"), nil
		})
		deleted <- deleteOutcome{result: result, err: deleteErr}
	}()
	<-deleteStarted
	close(ids.releaseCreate)

	createResult := <-created
	deleteResult := <-deleted
	if createResult.err != nil || !createResult.result.Changed || createResult.result.Revision != 1 ||
		len(createResult.result.Rules) != 1 || createResult.result.Rules[0].ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("create result=%#v err=%v", createResult.result, createResult.err)
	}
	if deleteResult.err != nil || !deleteResult.result.Changed || deleteResult.result.Revision != 2 || len(deleteResult.result.Rules) != 0 {
		t.Fatalf("delete result=%#v err=%v", deleteResult.result, deleteResult.err)
	}
}

type createDeleteBarrierIDs struct {
	mu            sync.Mutex
	values        []string
	insideCreate  chan struct{}
	releaseCreate chan struct{}
}

func (g *createDeleteBarrierIDs) NewID() string {
	g.mu.Lock()
	value := g.values[0]
	g.values = g.values[1:]
	isGeneration := len(g.values) == 0
	g.mu.Unlock()
	if isGeneration {
		close(g.insideCreate)
		<-g.releaseCreate
	}
	return value
}
